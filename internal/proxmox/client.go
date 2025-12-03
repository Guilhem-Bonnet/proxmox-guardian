package proxmox

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/luthermonson/go-proxmox"
)

// Client wraps the go-proxmox client with additional functionality
type Client struct {
	client      *proxmox.Client
	node        string // Default node if not specified
	apiURL      string
	tokenID     string
	tokenSecret string
}

// Guest represents a VM or LXC container
type Guest struct {
	Type   string   // "vm" or "lxc"
	VMID   int      // VM/LXC ID
	Name   string   // Guest name
	Node   string   // Proxmox node
	Status string   // running, stopped, etc.
	Tags   []string // Tags assigned to guest
}

// GuestSelector defines criteria for selecting guests
type GuestSelector struct {
	Type        string   // "vm", "lxc", or "" for both
	Tags        []string // Must have ALL these tags
	ExcludeTags []string // Must NOT have ANY of these tags
	NameRegex   string   // Name must match this regex
	VMIDRange   []int    // [min, max] VMID range
}

// Config holds Proxmox client configuration
type Config struct {
	APIURL       string
	TokenID      string
	TokenSecret  string
	InsecureTLS  bool
	DefaultNode  string
}

// NewClient creates a new Proxmox client
func NewClient(cfg Config) (*Client, error) {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	if cfg.InsecureTLS {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	opts := []proxmox.Option{
		proxmox.WithHTTPClient(httpClient),
		proxmox.WithAPIToken(cfg.TokenID, cfg.TokenSecret),
	}

	client := proxmox.NewClient(cfg.APIURL, opts...)

	return &Client{
		client:      client,
		node:        cfg.DefaultNode,
		apiURL:      cfg.APIURL,
		tokenID:     cfg.TokenID,
		tokenSecret: cfg.TokenSecret,
	}, nil
}

// GetVersion checks API connectivity by fetching version
func (c *Client) GetVersion(ctx context.Context) (string, error) {
	version, err := c.client.Version(ctx)
	if err != nil {
		return "", fmt.Errorf("getting Proxmox version: %w", err)
	}
	return version.Version, nil
}

// GetAllGuests returns all VMs and LXCs across all nodes
func (c *Client) GetAllGuests(ctx context.Context) ([]Guest, error) {
	var guests []Guest

	nodes, err := c.client.Nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting nodes: %w", err)
	}

	for _, nodeStatus := range nodes {
		node, err := c.client.Node(ctx, nodeStatus.Node)
		if err != nil {
			continue // Skip nodes we can't access
		}

		// Get VMs
		vms, err := node.VirtualMachines(ctx)
		if err == nil {
			for _, vm := range vms {
				guests = append(guests, Guest{
					Type:   "vm",
					VMID:   int(vm.VMID),
					Name:   vm.Name,
					Node:   nodeStatus.Node,
					Status: vm.Status,
					Tags:   parseTags(vm.Tags),
				})
			}
		}

		// Get LXCs
		containers, err := node.Containers(ctx)
		if err == nil {
			for _, ct := range containers {
				guests = append(guests, Guest{
					Type:   "lxc",
					VMID:   int(ct.VMID),
					Name:   ct.Name,
					Node:   nodeStatus.Node,
					Status: ct.Status,
					Tags:   parseTags(ct.Tags),
				})
			}
		}
	}

	return guests, nil
}

// GetGuestsBySelector returns guests matching the selector criteria
func (c *Client) GetGuestsBySelector(ctx context.Context, selector GuestSelector) ([]Guest, error) {
	allGuests, err := c.GetAllGuests(ctx)
	if err != nil {
		return nil, err
	}

	var matched []Guest

	for _, guest := range allGuests {
		if c.matchesSelector(guest, selector) {
			matched = append(matched, guest)
		}
	}

	return matched, nil
}

// matchesSelector checks if a guest matches the selector criteria
func (c *Client) matchesSelector(guest Guest, selector GuestSelector) bool {
	// Type filter
	if selector.Type != "" && selector.Type != guest.Type {
		return false
	}

	// Required tags (must have ALL)
	if len(selector.Tags) > 0 {
		for _, requiredTag := range selector.Tags {
			found := false
			for _, tag := range guest.Tags {
				if tag == requiredTag {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}

	// Excluded tags (must NOT have ANY)
	if len(selector.ExcludeTags) > 0 {
		for _, excludeTag := range selector.ExcludeTags {
			for _, tag := range guest.Tags {
				if tag == excludeTag {
					return false
				}
			}
		}
	}

	// Name regex
	if selector.NameRegex != "" {
		matched, err := regexp.MatchString(selector.NameRegex, guest.Name)
		if err != nil || !matched {
			return false
		}
	}

	// VMID range
	if len(selector.VMIDRange) == 2 {
		if guest.VMID < selector.VMIDRange[0] || guest.VMID > selector.VMIDRange[1] {
			return false
		}
	}

	return true
}

// ShutdownGuest gracefully shuts down a VM or LXC
func (c *Client) ShutdownGuest(ctx context.Context, guestType string, vmid int, node string, timeout time.Duration) error {
	nodeClient, err := c.client.Node(ctx, node)
	if err != nil {
		return fmt.Errorf("getting node %s: %w", node, err)
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if guestType == "vm" {
		vm, err := nodeClient.VirtualMachine(ctx, vmid)
		if err != nil {
			return fmt.Errorf("getting VM %d: %w", vmid, err)
		}

		task, err := vm.Shutdown(ctx)
		if err != nil {
			return fmt.Errorf("shutting down VM %d: %w", vmid, err)
		}

		// Wait for shutdown task to complete
		if err := c.waitForTask(ctx, task); err != nil {
			return fmt.Errorf("waiting for VM %d shutdown: %w", vmid, err)
		}

	} else if guestType == "lxc" {
		container, err := nodeClient.Container(ctx, vmid)
		if err != nil {
			return fmt.Errorf("getting LXC %d: %w", vmid, err)
		}

		// Shutdown(ctx, forceStop bool, timeout int)
		task, err := container.Shutdown(ctx, false, int(timeout.Seconds()))
		if err != nil {
			return fmt.Errorf("shutting down LXC %d: %w", vmid, err)
		}

		if err := c.waitForTask(ctx, task); err != nil {
			return fmt.Errorf("waiting for LXC %d shutdown: %w", vmid, err)
		}
	}

	return nil
}

// StopGuest forcefully stops a VM or LXC
func (c *Client) StopGuest(ctx context.Context, guestType string, vmid int, node string) error {
	nodeClient, err := c.client.Node(ctx, node)
	if err != nil {
		return fmt.Errorf("getting node %s: %w", node, err)
	}

	if guestType == "vm" {
		vm, err := nodeClient.VirtualMachine(ctx, vmid)
		if err != nil {
			return fmt.Errorf("getting VM %d: %w", vmid, err)
		}

		task, err := vm.Stop(ctx)
		if err != nil {
			return fmt.Errorf("stopping VM %d: %w", vmid, err)
		}

		if err := c.waitForTask(ctx, task); err != nil {
			return fmt.Errorf("waiting for VM %d stop: %w", vmid, err)
		}

	} else if guestType == "lxc" {
		container, err := nodeClient.Container(ctx, vmid)
		if err != nil {
			return fmt.Errorf("getting LXC %d: %w", vmid, err)
		}

		task, err := container.Stop(ctx)
		if err != nil {
			return fmt.Errorf("stopping LXC %d: %w", vmid, err)
		}

		if err := c.waitForTask(ctx, task); err != nil {
			return fmt.Errorf("waiting for LXC %d stop: %w", vmid, err)
		}
	}

	return nil
}

// StartGuest starts a VM or LXC
func (c *Client) StartGuest(ctx context.Context, guestType string, vmid int, node string) error {
	nodeClient, err := c.client.Node(ctx, node)
	if err != nil {
		return fmt.Errorf("getting node %s: %w", node, err)
	}

	if guestType == "vm" {
		vm, err := nodeClient.VirtualMachine(ctx, vmid)
		if err != nil {
			return fmt.Errorf("getting VM %d: %w", vmid, err)
		}

		task, err := vm.Start(ctx)
		if err != nil {
			return fmt.Errorf("starting VM %d: %w", vmid, err)
		}

		if err := c.waitForTask(ctx, task); err != nil {
			return fmt.Errorf("waiting for VM %d start: %w", vmid, err)
		}

	} else if guestType == "lxc" {
		container, err := nodeClient.Container(ctx, vmid)
		if err != nil {
			return fmt.Errorf("getting LXC %d: %w", vmid, err)
		}

		task, err := container.Start(ctx)
		if err != nil {
			return fmt.Errorf("starting LXC %d: %w", vmid, err)
		}

		if err := c.waitForTask(ctx, task); err != nil {
			return fmt.Errorf("waiting for LXC %d start: %w", vmid, err)
		}
	}

	return nil
}

// ExecInGuest executes a command inside a guest
// For VMs: uses qemu-guest-agent
// For LXCs: uses pct exec
func (c *Client) ExecInGuest(ctx context.Context, guestType string, vmid int, node string, command string) (string, error) {
	nodeClient, err := c.client.Node(ctx, node)
	if err != nil {
		return "", fmt.Errorf("getting node %s: %w", node, err)
	}

	if guestType == "vm" {
		vm, err := nodeClient.VirtualMachine(ctx, vmid)
		if err != nil {
			return "", fmt.Errorf("getting VM %d: %w", vmid, err)
		}

		// Use QEMU guest agent
		pid, err := vm.AgentExec(ctx, []string{"/bin/sh", "-c", command}, "")
		if err != nil {
			return "", fmt.Errorf("executing command in VM %d: %w", vmid, err)
		}

		// Wait for command to complete and get output
		status, err := vm.WaitForAgentExecExit(ctx, pid, 300) // 5 min timeout
		if err != nil {
			return "", fmt.Errorf("waiting for command in VM %d: %w", vmid, err)
		}

		output := ""
		if status.OutData != "" {
			output = status.OutData
		}
		if status.ErrData != "" {
			if output != "" {
				output += "\n"
			}
			output += status.ErrData
		}

		if status.ExitCode != 0 {
			return output, fmt.Errorf("command exited with code %d", status.ExitCode)
		}

		return output, nil

	} else if guestType == "lxc" {
		container, err := nodeClient.Container(ctx, vmid)
		if err != nil {
			return "", fmt.Errorf("getting LXC %d: %w", vmid, err)
		}

		// LXC exec is not directly supported by go-proxmox
		// We need to call the API directly or use SSH
		// For now, return an error suggesting SSH
		_ = container
		return "", fmt.Errorf("LXC exec not yet implemented - use SSH executor instead")
	}

	return "", fmt.Errorf("unknown guest type: %s", guestType)
}

// FindGuestByName finds a guest by name and optional type
func (c *Client) FindGuestByName(ctx context.Context, name string, guestType string) (*Guest, error) {
	guests, err := c.GetAllGuests(ctx)
	if err != nil {
		return nil, err
	}

	for _, guest := range guests {
		if guest.Name == name {
			if guestType == "" || guest.Type == guestType {
				return &guest, nil
			}
		}
	}

	return nil, fmt.Errorf("guest not found: %s", name)
}

// FindGuestByID finds a guest by VMID and type
func (c *Client) FindGuestByID(ctx context.Context, vmid int, guestType string) (*Guest, error) {
	guests, err := c.GetAllGuests(ctx)
	if err != nil {
		return nil, err
	}

	for _, guest := range guests {
		if guest.VMID == vmid {
			if guestType == "" || guest.Type == guestType {
				return &guest, nil
			}
		}
	}

	return nil, fmt.Errorf("guest not found: %d", vmid)
}

// waitForTask waits for a Proxmox task to complete
func (c *Client) waitForTask(ctx context.Context, task *proxmox.Task) error {
	if task == nil {
		return nil
	}

	// Poll task status
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			err := task.Ping(ctx)
			if err != nil {
				return err
			}

			if task.IsCompleted {
				if task.IsFailed {
					return fmt.Errorf("task failed: %s", task.ExitStatus)
				}
				return nil
			}
		}
	}
}

// parseTags parses comma-separated tags string into slice
func parseTags(tagsStr string) []string {
	if tagsStr == "" {
		return nil
	}
	
	tags := strings.Split(tagsStr, ";")
	result := make([]string, 0, len(tags))
	
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			result = append(result, tag)
		}
	}
	
	return result
}

// ParseGuestString parses "lxc:name" or "vm:100" format
func ParseGuestString(guest string) (guestType string, identifier string, isID bool, err error) {
	parts := strings.SplitN(guest, ":", 2)
	if len(parts) != 2 {
		return "", "", false, fmt.Errorf("invalid guest format: %s (expected 'lxc:name' or 'vm:id')", guest)
	}

	guestType = strings.ToLower(parts[0])
	identifier = parts[1]

	if guestType != "lxc" && guestType != "vm" {
		return "", "", false, fmt.Errorf("invalid guest type: %s (expected 'lxc' or 'vm')", guestType)
	}

	// Check if identifier is a number (VMID) or name
	if _, err := strconv.Atoi(identifier); err == nil {
		isID = true
	}

	return guestType, identifier, isID, nil
}
