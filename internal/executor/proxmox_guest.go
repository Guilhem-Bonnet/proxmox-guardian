package executor

import (
	"context"
	"fmt"
	"time"
)

// ProxmoxGuestExecutor shuts down VMs/LXCs via Proxmox API
type ProxmoxGuestExecutor struct {
	BaseAction
	Selector   GuestSelector
	Action     string // "shutdown" or "stop"
	ProxmoxAPI ProxmoxAPI
}

// NewProxmoxGuestExecutor creates a new Proxmox guest executor
func NewProxmoxGuestExecutor(selector GuestSelector, action string, api ProxmoxAPI) *ProxmoxGuestExecutor {
	return &ProxmoxGuestExecutor{
		BaseAction: BaseAction{
			Type:    "proxmox-guest",
			Timeout: 120 * time.Second,
		},
		Selector:   selector,
		Action:     action,
		ProxmoxAPI: api,
	}
}

// Execute shuts down matching guests
func (p *ProxmoxGuestExecutor) Execute(ctx context.Context) (*ActionResult, error) {
	start := time.Now()

	guests, err := p.ProxmoxAPI.GetGuestsBySelector(ctx, p.Selector)
	if err != nil {
		return &ActionResult{
			Success:  false,
			Error:    fmt.Sprintf("failed to get guests: %v", err),
			Duration: time.Since(start),
		}, err
	}

	if len(guests) == 0 {
		return &ActionResult{
			Success:  true,
			Output:   "no matching guests found",
			Duration: time.Since(start),
		}, nil
	}

	var shutdownErrors []string
	var shutdownSuccess []string

	for _, guest := range guests {
		guestID := fmt.Sprintf("%d", guest.VMID)

		err := p.ProxmoxAPI.ShutdownGuest(ctx, guest.Type, guestID, p.Timeout)
		if err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Sprintf("%s:%s (%v)", guest.Type, guest.Name, err))
		} else {
			shutdownSuccess = append(shutdownSuccess, fmt.Sprintf("%s:%s", guest.Type, guest.Name))
		}
	}

	output := fmt.Sprintf("shutdown %d guests: %v", len(shutdownSuccess), shutdownSuccess)

	if len(shutdownErrors) > 0 {
		return &ActionResult{
			Success:  false,
			Output:   output,
			Error:    fmt.Sprintf("failed to shutdown: %v", shutdownErrors),
			Duration: time.Since(start),
		}, fmt.Errorf("partial failure")
	}

	return &ActionResult{
		Success:  true,
		Output:   output,
		Duration: time.Since(start),
	}, nil
}

// Recover starts the guests that were stopped (for recovery mode)
func (p *ProxmoxGuestExecutor) Recover(ctx context.Context) (*ActionResult, error) {
	// TODO: Implement guest restart for recovery
	return &ActionResult{
		Success: true,
		Output:  "guest recovery not yet implemented",
	}, nil
}

// Healthcheck verifies guests are stopped
func (p *ProxmoxGuestExecutor) Healthcheck(ctx context.Context) (bool, error) {
	guests, err := p.ProxmoxAPI.GetGuestsBySelector(ctx, p.Selector)
	if err != nil {
		return false, err
	}

	for _, guest := range guests {
		if guest.Status == "running" {
			return false, nil
		}
	}

	return true, nil
}

// String returns a human-readable description
func (p *ProxmoxGuestExecutor) String() string {
	return fmt.Sprintf("ProxmoxGuest[%s type=%s tags=%v]", p.Action, p.Selector.Type, p.Selector.Tags)
}
