package recovery

import (
	"context"
	"fmt"
	"time"

	"github.com/Guilhem-Bonnet/proxmox-guardian/internal/executor"
	"github.com/Guilhem-Bonnet/proxmox-guardian/internal/state"
)

// Config holds recovery configuration
type Config struct {
	Enabled          bool
	PowerStableDelay time.Duration
	OnError          string // "notify", "retry", "ignore"
	MaxRetries       int
}

// Manager handles the recovery process
type Manager struct {
	config       Config
	stateManager *state.Manager
	logger       Logger
	notifier     Notifier
}

// Logger interface
type Logger interface {
	Info(msg string, keyvals ...interface{})
	Error(msg string, keyvals ...interface{})
	Debug(msg string, keyvals ...interface{})
}

// Notifier interface
type Notifier interface {
	Notify(event string, data map[string]interface{}) error
}

// NewManager creates a new recovery manager
func NewManager(cfg Config, stateMgr *state.Manager, logger Logger, notifier Notifier) *Manager {
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}

	return &Manager{
		config:       cfg,
		stateManager: stateMgr,
		logger:       logger,
		notifier:     notifier,
	}
}

// NeedsRecovery checks if recovery is needed
func (m *Manager) NeedsRecovery() bool {
	if !m.config.Enabled {
		return false
	}
	return m.stateManager.NeedsRecovery()
}

// Execute performs the recovery process
func (m *Manager) Execute(ctx context.Context) error {
	if !m.config.Enabled {
		return fmt.Errorf("recovery is disabled")
	}

	currentState := m.stateManager.GetState()
	if currentState.Status != state.StatusInProgress && currentState.Status != state.StatusFailed {
		return fmt.Errorf("nothing to recover (status: %s)", currentState.Status)
	}

	m.logger.Info("Starting recovery process",
		"session_id", currentState.SessionID,
		"trigger", currentState.TriggerEvent,
	)

	m.notify("recovery_start", map[string]interface{}{
		"session_id":         currentState.SessionID,
		"original_trigger":   currentState.TriggerEvent,
		"actions_to_recover": len(m.stateManager.GetActionsForRecovery()),
	})

	m.stateManager.SetStatus(state.StatusRecovering)
	if err := m.stateManager.Save(); err != nil {
		m.logger.Error("Failed to save recovery state", "error", err)
	}

	// Wait for power to stabilize (anti-flapping)
	if m.config.PowerStableDelay > 0 {
		m.logger.Info("Waiting for power to stabilize",
			"delay", m.config.PowerStableDelay,
		)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.config.PowerStableDelay):
		}
	}

	// Get actions that need recovery (in reverse order)
	actionsToRecover := m.stateManager.GetActionsForRecovery()

	m.logger.Info("Recovering actions",
		"count", len(actionsToRecover),
	)

	var recoveryErrors []error
	successCount := 0

	for i, action := range actionsToRecover {
		m.logger.Info("Recovering action",
			"index", i+1,
			"total", len(actionsToRecover),
			"phase", action.PhaseName,
			"type", action.ActionType,
		)

		err := m.recoverAction(ctx, action)
		if err != nil {
			m.logger.Error("Recovery failed for action",
				"phase", action.PhaseName,
				"type", action.ActionType,
				"error", err,
			)

			recoveryErrors = append(recoveryErrors, err)

			// Handle error based on config
			switch m.config.OnError {
			case "notify":
				m.notify("recovery_error", map[string]interface{}{
					"phase":  action.PhaseName,
					"action": action.ActionType,
					"error":  err.Error(),
				})
			case "ignore":
				// Continue to next action
			default:
				// Continue by default
			}
		} else {
			successCount++
			m.logger.Info("Action recovered successfully",
				"phase", action.PhaseName,
				"type", action.ActionType,
			)
		}
	}

	// Update state
	if len(recoveryErrors) == 0 {
		m.stateManager.SetStatus(state.StatusIdle)
		m.stateManager.Clear()
	} else {
		m.stateManager.SetStatus(state.StatusFailed)
		m.stateManager.SetError(fmt.Sprintf("%d recovery errors", len(recoveryErrors)))
	}

	if err := m.stateManager.Save(); err != nil {
		m.logger.Error("Failed to save state after recovery", "error", err)
	}

	m.notify("recovery_complete", map[string]interface{}{
		"session_id":    currentState.SessionID,
		"total_actions": len(actionsToRecover),
		"success_count": successCount,
		"error_count":   len(recoveryErrors),
	})

	if len(recoveryErrors) > 0 {
		return fmt.Errorf("recovery completed with %d errors", len(recoveryErrors))
	}

	m.logger.Info("Recovery completed successfully",
		"actions_recovered", successCount,
	)

	return nil
}

// recoverAction executes recovery for a single action
func (m *Manager) recoverAction(ctx context.Context, action state.CompletedAction) error {
	spec := action.ActionSpec

	if spec.Recovery == "" {
		return nil // No recovery command
	}

	// Create executor based on action type
	exec, err := m.createExecutor(spec)
	if err != nil {
		return fmt.Errorf("creating executor: %w", err)
	}

	// Execute with retry
	var lastErr error
	for attempt := 1; attempt <= m.config.MaxRetries; attempt++ {
		result, err := exec.Recover(ctx)
		if err == nil && result.Success {
			return nil
		}

		lastErr = err
		if result != nil && !result.Success {
			lastErr = fmt.Errorf("recovery failed: %s", result.Error)
		}

		if attempt < m.config.MaxRetries {
			m.logger.Debug("Recovery attempt failed, retrying",
				"attempt", attempt,
				"max", m.config.MaxRetries,
				"error", lastErr,
			)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
	}

	return lastErr
}

// createExecutor creates an executor from action spec
func (m *Manager) createExecutor(spec state.ActionSpec) (executor.Executor, error) {
	switch spec.Type {
	case "ssh":
		exec := executor.NewSSHExecutor(spec.Host, spec.User, spec.Command)
		exec.Recovery = spec.Recovery
		return exec, nil

	case "local":
		exec := executor.NewLocalExecutor(spec.Command)
		exec.Recovery = spec.Recovery
		return exec, nil

	case "proxmox-exec":
		// For proxmox-exec, we need the ProxmoxAPI which we don't have here
		// Fall back to logging a warning
		return nil, fmt.Errorf("proxmox-exec recovery requires API connection - manual recovery needed")

	case "proxmox-guest":
		// Guest recovery (restart) also needs API
		return nil, fmt.Errorf("proxmox-guest recovery requires API connection - manual recovery needed")

	default:
		return nil, fmt.Errorf("unknown action type: %s", spec.Type)
	}
}

func (m *Manager) notify(event string, data map[string]interface{}) {
	if m.notifier == nil {
		return
	}

	if err := m.notifier.Notify(event, data); err != nil {
		m.logger.Error("Notification failed", "event", event, "error", err)
	}
}
