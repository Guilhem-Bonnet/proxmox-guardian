package executor

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ProxmoxExecExecutor executes commands inside a VM or LXC via Proxmox API
type ProxmoxExecExecutor struct {
	BaseAction
	Guest      string // Format: "lxc:name" or "vm:name" or "lxc:100"
	ProxmoxAPI ProxmoxAPI
}

// ProxmoxAPI interface for Proxmox operations (to be implemented)
type ProxmoxAPI interface {
	ExecInGuest(ctx context.Context, guestType, guestID, command string) (string, error)
	ShutdownGuest(ctx context.Context, guestType, guestID string, timeout time.Duration) error
	GetGuestsBySelector(ctx context.Context, selector GuestSelector) ([]Guest, error)
}

// Guest represents a Proxmox VM or LXC
type Guest struct {
	Type   string // "vm" or "lxc"
	VMID   int
	Name   string
	Node   string
	Status string
	Tags   []string
}

// GuestSelector for filtering guests
type GuestSelector struct {
	Type        string
	Tags        []string
	ExcludeTags []string
	NameRegex   string
	VMIDRange   []int
}

// NewProxmoxExecExecutor creates a new Proxmox exec executor
func NewProxmoxExecExecutor(guest, command string, api ProxmoxAPI) *ProxmoxExecExecutor {
	return &ProxmoxExecExecutor{
		BaseAction: BaseAction{
			Type:    "proxmox-exec",
			Command: command,
			Timeout: 60 * time.Second,
		},
		Guest:      guest,
		ProxmoxAPI: api,
	}
}

// Execute runs the command inside the guest
func (p *ProxmoxExecExecutor) Execute(ctx context.Context) (*ActionResult, error) {
	start := time.Now()
	
	guestType, guestID, err := parseGuest(p.Guest)
	if err != nil {
		return &ActionResult{
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, err
	}
	
	output, err := p.ProxmoxAPI.ExecInGuest(ctx, guestType, guestID, p.Command)
	if err != nil {
		return &ActionResult{
			Success:  false,
			Output:   output,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, err
	}
	
	return &ActionResult{
		Success:  true,
		Output:   output,
		Duration: time.Since(start),
	}, nil
}

// Recover runs the recovery command
func (p *ProxmoxExecExecutor) Recover(ctx context.Context) (*ActionResult, error) {
	if p.Recovery == "" {
		return &ActionResult{
			Success: true,
			Output:  "no recovery command defined",
		}, nil
	}
	
	recoveryExec := NewProxmoxExecExecutor(p.Guest, p.Recovery, p.ProxmoxAPI)
	recoveryExec.Timeout = p.Timeout
	
	return recoveryExec.Execute(ctx)
}

// Healthcheck verifies the action completed
func (p *ProxmoxExecExecutor) Healthcheck(ctx context.Context) (bool, error) {
	if p.BaseAction.Healthcheck == nil {
		return true, nil
	}
	
	checkExec := NewProxmoxExecExecutor(p.Guest, p.BaseAction.Healthcheck.Command, p.ProxmoxAPI)
	checkExec.Timeout = 10 * time.Second
	
	result, _ := checkExec.Execute(ctx)
	
	expectSuccess := p.BaseAction.Healthcheck.Expect == "success"
	
	if expectSuccess {
		return result.Success, nil
	}
	return !result.Success, nil
}

// String returns a human-readable description
func (p *ProxmoxExecExecutor) String() string {
	return fmt.Sprintf("ProxmoxExec[%s]: %s", p.Guest, truncateCmd(p.Command))
}

// parseGuest parses "lxc:name" or "vm:100" format
func parseGuest(guest string) (guestType, guestID string, err error) {
	parts := strings.SplitN(guest, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid guest format: %s (expected 'lxc:name' or 'vm:id')", guest)
	}
	
	guestType = strings.ToLower(parts[0])
	guestID = parts[1]
	
	if guestType != "lxc" && guestType != "vm" {
		return "", "", fmt.Errorf("invalid guest type: %s (expected 'lxc' or 'vm')", guestType)
	}
	
	return guestType, guestID, nil
}
