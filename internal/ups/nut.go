package ups

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Status represents UPS status
type Status struct {
	Name          string
	Status        string // OL (online), OB (on battery), LB (low battery)
	BatteryCharge int    // Percentage
	Runtime       int    // Seconds remaining
	Load          int    // Percentage
	Timestamp     time.Time
}

// IsOnline returns true if UPS is on line power
func (s *Status) IsOnline() bool {
	return strings.Contains(s.Status, "OL")
}

// IsOnBattery returns true if UPS is running on battery
func (s *Status) IsOnBattery() bool {
	return strings.Contains(s.Status, "OB")
}

// IsLowBattery returns true if battery is low
func (s *Status) IsLowBattery() bool {
	return strings.Contains(s.Status, "LB")
}

// Client is a NUT (Network UPS Tools) client
type Client struct {
	host    string
	upsName string
	conn    net.Conn
	mu      sync.Mutex
	timeout time.Duration
}

// NewClient creates a new NUT client
func NewClient(host, upsName string) *Client {
	return &Client{
		host:    host,
		upsName: upsName,
		timeout: 10 * time.Second,
	}
}

// Connect establishes connection to NUT server
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	conn, err := net.DialTimeout("tcp", c.host, c.timeout)
	if err != nil {
		return fmt.Errorf("connecting to NUT server: %w", err)
	}

	c.conn = conn
	return nil
}

// Close closes the connection
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// GetStatus retrieves current UPS status
func (c *Client) GetStatus(ctx context.Context) (*Status, error) {
	vars, err := c.getVariables(ctx)
	if err != nil {
		return nil, err
	}

	status := &Status{
		Name:      c.upsName,
		Timestamp: time.Now(),
	}

	if v, ok := vars["ups.status"]; ok {
		status.Status = v
	}
	if v, ok := vars["battery.charge"]; ok {
		status.BatteryCharge, _ = strconv.Atoi(v)
	}
	if v, ok := vars["battery.runtime"]; ok {
		status.Runtime, _ = strconv.Atoi(v)
	}
	if v, ok := vars["ups.load"]; ok {
		status.Load, _ = strconv.Atoi(v)
	}

	return status, nil
}

// getVariables retrieves all UPS variables
func (c *Client) getVariables(ctx context.Context) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Set deadline from context
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(deadline)
	} else {
		_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
	}

	// Request all variables
	cmd := fmt.Sprintf("LIST VAR %s\n", c.upsName)
	if _, err := c.conn.Write([]byte(cmd)); err != nil {
		return nil, fmt.Errorf("sending command: %w", err)
	}

	vars := make(map[string]string)
	scanner := bufio.NewScanner(c.conn)

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "BEGIN LIST VAR") {
			continue
		}
		if strings.HasPrefix(line, "END LIST VAR") {
			break
		}
		if strings.HasPrefix(line, "ERR") {
			return nil, fmt.Errorf("NUT error: %s", line)
		}

		// Parse: VAR upsname varname "value"
		if strings.HasPrefix(line, "VAR ") {
			parts := strings.SplitN(line, " ", 4)
			if len(parts) >= 4 {
				varName := parts[2]
				varValue := strings.Trim(parts[3], "\"")
				vars[varName] = varValue
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	return vars, nil
}

// Monitor continuously monitors UPS status and sends updates to channel
type Monitor struct {
	client     *Client
	interval   time.Duration
	thresholds Thresholds
	statusCh   chan *Status
	eventCh    chan Event
	stopCh     chan struct{}
}

// Thresholds for battery levels
type Thresholds struct {
	Warning   int // Notify at this level
	Critical  int // Start shutdown at this level
	Emergency int // Force immediate shutdown
}

// Event types
type EventType string

const (
	EventPowerLost       EventType = "POWER_LOST"
	EventPowerRestored   EventType = "POWER_RESTORED"
	EventLowBattery      EventType = "LOW_BATTERY"
	EventCriticalBattery EventType = "CRITICAL_BATTERY"
	EventEmergency       EventType = "EMERGENCY"
)

// Event represents a UPS event
type Event struct {
	Type      EventType
	Status    *Status
	Timestamp time.Time
	Message   string
}

// NewMonitor creates a new UPS monitor
func NewMonitor(client *Client, thresholds Thresholds) *Monitor {
	return &Monitor{
		client:     client,
		interval:   5 * time.Second,
		thresholds: thresholds,
		statusCh:   make(chan *Status, 10),
		eventCh:    make(chan Event, 10),
		stopCh:     make(chan struct{}),
	}
}

// Start begins monitoring
func (m *Monitor) Start(ctx context.Context) error {
	if err := m.client.Connect(); err != nil {
		return err
	}

	go m.monitorLoop(ctx)

	return nil
}

// Stop stops monitoring
func (m *Monitor) Stop() {
	close(m.stopCh)
	m.client.Close()
}

// Events returns the event channel
func (m *Monitor) Events() <-chan Event {
	return m.eventCh
}

// Status returns the status channel
func (m *Monitor) Status() <-chan *Status {
	return m.statusCh
}

func (m *Monitor) monitorLoop(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	var lastStatus *Status

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			status, err := m.client.GetStatus(ctx)
			if err != nil {
				// TODO: Log error, maybe emit event
				continue
			}

			// Send status update
			select {
			case m.statusCh <- status:
			default:
			}

			// Check for events
			m.checkEvents(status, lastStatus)
			lastStatus = status
		}
	}
}

func (m *Monitor) checkEvents(current, last *Status) {
	// Power transition events
	if last != nil {
		if last.IsOnline() && current.IsOnBattery() {
			m.emitEvent(EventPowerLost, current, "Power lost, running on battery")
		}
		if last.IsOnBattery() && current.IsOnline() {
			m.emitEvent(EventPowerRestored, current, "Power restored")
		}
	}

	// Battery level events
	if current.IsOnBattery() {
		if current.BatteryCharge <= m.thresholds.Emergency {
			m.emitEvent(EventEmergency, current, fmt.Sprintf("EMERGENCY: Battery at %d%%", current.BatteryCharge))
		} else if current.BatteryCharge <= m.thresholds.Critical {
			m.emitEvent(EventCriticalBattery, current, fmt.Sprintf("Critical battery: %d%%", current.BatteryCharge))
		} else if current.BatteryCharge <= m.thresholds.Warning {
			m.emitEvent(EventLowBattery, current, fmt.Sprintf("Low battery: %d%%", current.BatteryCharge))
		}
	}
}

func (m *Monitor) emitEvent(eventType EventType, status *Status, message string) {
	event := Event{
		Type:      eventType,
		Status:    status,
		Timestamp: time.Now(),
		Message:   message,
	}

	select {
	case m.eventCh <- event:
	default:
		// Channel full, drop event
	}
}
