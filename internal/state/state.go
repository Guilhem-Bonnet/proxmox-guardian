package state

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Status represents the current state status
type Status string

const (
	StatusIdle       Status = "idle"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusRecovering Status = "recovering"
)

// State represents the persistent shutdown state
type State struct {
	SessionID        string            `json:"session_id"`
	StartedAt        time.Time         `json:"started_at"`
	Status           Status            `json:"status"`
	TriggerEvent     string            `json:"trigger_event"`
	CurrentPhase     int               `json:"current_phase"`
	CurrentAction    int               `json:"current_action"`
	CompletedActions []CompletedAction `json:"completed_actions"`
	LastUpdated      time.Time         `json:"last_updated"`
	LastError        string            `json:"last_error,omitempty"`
}

// CompletedAction represents an action that was executed
type CompletedAction struct {
	PhaseIndex  int        `json:"phase_index"`
	PhaseName   string     `json:"phase_name"`
	ActionIndex int        `json:"action_index"`
	ActionType  string     `json:"action_type"`
	ActionSpec  ActionSpec `json:"action_spec"`
	CompletedAt time.Time  `json:"completed_at"`
	Success     bool       `json:"success"`
	Output      string     `json:"output,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// ActionSpec contains all info needed to recreate an executor
type ActionSpec struct {
	Type     string        `json:"type"`
	Host     string        `json:"host,omitempty"`
	User     string        `json:"user,omitempty"`
	Guest    string        `json:"guest,omitempty"`
	Command  string        `json:"command,omitempty"`
	Recovery string        `json:"recovery,omitempty"`
	Action   string        `json:"action,omitempty"`
	Selector *SelectorSpec `json:"selector,omitempty"`
}

// SelectorSpec for proxmox-guest actions
type SelectorSpec struct {
	Type        string   `json:"type,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	ExcludeTags []string `json:"exclude_tags,omitempty"`
	NameRegex   string   `json:"name_regex,omitempty"`
	VMIDRange   []int    `json:"vmid_range,omitempty"`
}

// Manager handles state persistence
type Manager struct {
	filePath string
	state    *State
	mu       sync.RWMutex
}

// NewManager creates a new state manager
func NewManager(filePath string) *Manager {
	return &Manager{
		filePath: filePath,
		state: &State{
			Status: StatusIdle,
		},
	}
}

// Load loads state from file
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.filePath)
	if os.IsNotExist(err) {
		// No previous state, start fresh
		m.state = &State{Status: StatusIdle}
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading state file: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parsing state file: %w", err)
	}

	m.state = &state
	return nil
}

// Save persists state to file
func (m *Manager) Save() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Ensure directory exists
	dir := m.filePath[:len(m.filePath)-len("/state.json")]
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	if err := os.WriteFile(m.filePath, data, 0600); err != nil {
		return fmt.Errorf("writing state file: %w", err)
	}

	return nil
}

// StartSession starts a new shutdown session
func (m *Manager) StartSession(triggerEvent string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
	m.state = &State{
		SessionID:        sessionID,
		StartedAt:        time.Now(),
		Status:           StatusInProgress,
		TriggerEvent:     triggerEvent,
		CurrentPhase:     0,
		CurrentAction:    0,
		CompletedActions: []CompletedAction{},
		LastUpdated:      time.Now(),
	}

	return sessionID
}

// UpdateProgress updates current phase/action
func (m *Manager) UpdateProgress(phaseIndex, actionIndex int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.CurrentPhase = phaseIndex
	m.state.CurrentAction = actionIndex
	m.state.LastUpdated = time.Now()
}

// RecordAction records a completed action
func (m *Manager) RecordAction(action CompletedAction) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.CompletedActions = append(m.state.CompletedActions, action)
	m.state.LastUpdated = time.Now()
}

// SetStatus sets the current status
func (m *Manager) SetStatus(status Status) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.Status = status
	m.state.LastUpdated = time.Now()
}

// SetError sets the last error
func (m *Manager) SetError(err string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.LastError = err
	m.state.LastUpdated = time.Now()
}

// GetState returns a copy of current state
func (m *Manager) GetState() State {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return *m.state
}

// GetActionsForRecovery returns actions that need recovery (in reverse order)
func (m *Manager) GetActionsForRecovery() []CompletedAction {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Filter successful actions with recovery commands
	var recoverable []CompletedAction
	for _, action := range m.state.CompletedActions {
		if action.Success && action.ActionSpec.Recovery != "" {
			recoverable = append(recoverable, action)
		}
	}

	// Reverse order for recovery
	for i, j := 0, len(recoverable)-1; i < j; i, j = i+1, j-1 {
		recoverable[i], recoverable[j] = recoverable[j], recoverable[i]
	}

	return recoverable
}

// NeedsRecovery checks if there's an incomplete shutdown that needs recovery
func (m *Manager) NeedsRecovery() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.state.Status == StatusInProgress || m.state.Status == StatusFailed
}

// Clear clears the state (after successful recovery or completion)
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state = &State{
		Status:      StatusIdle,
		LastUpdated: time.Now(),
	}
}

// GetSessionDuration returns the duration of the current session
func (m *Manager) GetSessionDuration() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.state.StartedAt.IsZero() {
		return 0
	}
	return time.Since(m.state.StartedAt)
}
