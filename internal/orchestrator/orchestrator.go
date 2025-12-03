package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Guilhem-Bonnet/proxmox-guardian/internal/executor"
)

// State represents the current shutdown state
type State struct {
	SessionID        string           `json:"session_id"`
	StartedAt        time.Time        `json:"started_at"`
	Status           string           `json:"status"` // "idle", "in_progress", "completed", "failed", "recovering"
	CurrentPhase     int              `json:"current_phase"`
	CurrentAction    int              `json:"current_action"`
	CompletedActions []CompletedAction `json:"completed_actions"`
	TriggerEvent     string           `json:"trigger_event"`
	LastUpdated      time.Time        `json:"last_updated"`
}

// CompletedAction tracks an action that was executed
type CompletedAction struct {
	PhaseIndex   int       `json:"phase_index"`
	PhaseName    string    `json:"phase_name"`
	ActionIndex  int       `json:"action_index"`
	ActionType   string    `json:"action_type"`
	Description  string    `json:"description"`
	RecoveryCmd  string    `json:"recovery_cmd,omitempty"`
	CompletedAt  time.Time `json:"completed_at"`
	Success      bool      `json:"success"`
	Error        string    `json:"error,omitempty"`
}

// Phase represents a shutdown phase
type Phase struct {
	Name      string
	Parallel  bool
	Timeout   time.Duration
	Condition string
	Actions   []Action
}

// Action represents a single action to execute
type Action struct {
	Type        string
	Executor    executor.Executor
	Recovery    string
	OnError     string
	Retry       *executor.RetryConfig
	Healthcheck *executor.HealthcheckConfig
}

// Orchestrator manages the shutdown sequence
type Orchestrator struct {
	phases    []Phase
	stateFile string
	state     *State
	mu        sync.RWMutex
	logger    Logger
	notifier  Notifier
}

// Logger interface for logging
type Logger interface {
	Info(msg string, fields ...interface{})
	Error(msg string, fields ...interface{})
	Debug(msg string, fields ...interface{})
}

// Notifier interface for sending notifications
type Notifier interface {
	Notify(event string, data map[string]interface{}) error
}

// NewOrchestrator creates a new orchestrator
func NewOrchestrator(phases []Phase, stateFile string, logger Logger, notifier Notifier) *Orchestrator {
	return &Orchestrator{
		phases:    phases,
		stateFile: stateFile,
		logger:    logger,
		notifier:  notifier,
		state: &State{
			Status: "idle",
		},
	}
}

// Execute runs the shutdown sequence
func (o *Orchestrator) Execute(ctx context.Context, triggerEvent string) error {
	o.mu.Lock()
	
	// Initialize new session
	o.state = &State{
		SessionID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		StartedAt:        time.Now(),
		Status:           "in_progress",
		CurrentPhase:     0,
		CurrentAction:    0,
		TriggerEvent:     triggerEvent,
		CompletedActions: []CompletedAction{},
		LastUpdated:      time.Now(),
	}
	
	if err := o.saveState(); err != nil {
		o.mu.Unlock()
		return fmt.Errorf("saving initial state: %w", err)
	}
	o.mu.Unlock()
	
	o.notify("shutdown_start", map[string]interface{}{
		"trigger":    triggerEvent,
		"session_id": o.state.SessionID,
		"phases":     len(o.phases),
	})
	
	// Execute phases
	for i, phase := range o.phases {
		o.logger.Info("Starting phase", "phase", phase.Name, "index", i+1, "total", len(o.phases))
		
		o.mu.Lock()
		o.state.CurrentPhase = i
		o.state.CurrentAction = 0
		o.state.LastUpdated = time.Now()
		o.saveState()
		o.mu.Unlock()
		
		o.notify("phase_start", map[string]interface{}{
			"phase": phase.Name,
			"index": i + 1,
		})
		
		if err := o.executePhase(ctx, i, phase); err != nil {
			o.logger.Error("Phase failed", "phase", phase.Name, "error", err)
			
			// Check if we should continue despite error
			// For now, continue to next phase
		}
		
		o.notify("phase_complete", map[string]interface{}{
			"phase": phase.Name,
			"index": i + 1,
		})
	}
	
	o.mu.Lock()
	o.state.Status = "completed"
	o.state.LastUpdated = time.Now()
	o.saveState()
	o.mu.Unlock()
	
	o.notify("shutdown_complete", map[string]interface{}{
		"session_id": o.state.SessionID,
		"duration":   time.Since(o.state.StartedAt).String(),
	})
	
	return nil
}

func (o *Orchestrator) executePhase(ctx context.Context, phaseIndex int, phase Phase) error {
	// Apply phase timeout
	if phase.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, phase.Timeout)
		defer cancel()
	}
	
	if phase.Parallel {
		return o.executeParallel(ctx, phaseIndex, phase)
	}
	return o.executeSequential(ctx, phaseIndex, phase)
}

func (o *Orchestrator) executeSequential(ctx context.Context, phaseIndex int, phase Phase) error {
	for i, action := range phase.Actions {
		o.mu.Lock()
		o.state.CurrentAction = i
		o.state.LastUpdated = time.Now()
		o.saveState()
		o.mu.Unlock()
		
		result, err := o.executeAction(ctx, phaseIndex, phase.Name, i, action)
		
		// Track completed action
		completed := CompletedAction{
			PhaseIndex:  phaseIndex,
			PhaseName:   phase.Name,
			ActionIndex: i,
			ActionType:  action.Type,
			Description: action.Executor.String(),
			RecoveryCmd: action.Recovery,
			CompletedAt: time.Now(),
			Success:     err == nil && result.Success,
		}
		if err != nil {
			completed.Error = err.Error()
		} else if !result.Success {
			completed.Error = result.Error
		}
		
		o.mu.Lock()
		o.state.CompletedActions = append(o.state.CompletedActions, completed)
		o.saveState()
		o.mu.Unlock()
		
		// Handle error based on on_error setting
		if err != nil || !result.Success {
			switch action.OnError {
			case "continue":
				o.logger.Info("Action failed, continuing", "action", action.Executor.String())
				continue
			case "abort_phase":
				return fmt.Errorf("action failed, aborting phase: %w", err)
			case "abort_all":
				return fmt.Errorf("action failed, aborting all: %w", err)
			default:
				// Default: continue
				continue
			}
		}
	}
	
	return nil
}

func (o *Orchestrator) executeParallel(ctx context.Context, phaseIndex int, phase Phase) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(phase.Actions))
	
	for i, action := range phase.Actions {
		wg.Add(1)
		go func(idx int, act Action) {
			defer wg.Done()
			
			result, err := o.executeAction(ctx, phaseIndex, phase.Name, idx, act)
			
			// Track completed action
			completed := CompletedAction{
				PhaseIndex:  phaseIndex,
				PhaseName:   phase.Name,
				ActionIndex: idx,
				ActionType:  act.Type,
				Description: act.Executor.String(),
				RecoveryCmd: act.Recovery,
				CompletedAt: time.Now(),
				Success:     err == nil && result.Success,
			}
			if err != nil {
				completed.Error = err.Error()
			} else if !result.Success {
				completed.Error = result.Error
			}
			
			o.mu.Lock()
			o.state.CompletedActions = append(o.state.CompletedActions, completed)
			o.saveState()
			o.mu.Unlock()
			
			if err != nil && act.OnError == "abort_all" {
				errCh <- err
			}
		}(i, action)
	}
	
	wg.Wait()
	close(errCh)
	
	// Check for abort errors
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	
	return nil
}

func (o *Orchestrator) executeAction(ctx context.Context, phaseIndex int, phaseName string, actionIndex int, action Action) (*executor.ActionResult, error) {
	o.logger.Debug("Executing action", 
		"phase", phaseName,
		"action", action.Executor.String(),
	)
	
	// Execute with retry if configured
	var result *executor.ActionResult
	var err error
	
	if action.Retry != nil {
		result, err = executor.ExecuteWithRetry(ctx, action.Executor, action.Retry)
	} else {
		result, err = action.Executor.Execute(ctx)
	}
	
	if err != nil {
		o.logger.Error("Action failed",
			"phase", phaseName,
			"action", action.Executor.String(),
			"error", err,
		)
		return result, err
	}
	
	// Run healthcheck if configured
	if action.Healthcheck != nil {
		ok, hcErr := action.Executor.Healthcheck(ctx)
		if hcErr != nil || !ok {
			o.logger.Error("Healthcheck failed",
				"phase", phaseName,
				"action", action.Executor.String(),
			)
			return result, fmt.Errorf("healthcheck failed")
		}
	}
	
	o.logger.Info("Action completed",
		"phase", phaseName,
		"action", action.Executor.String(),
		"duration", result.Duration,
	)
	
	return result, nil
}

// Recover runs recovery for completed actions (in reverse order)
func (o *Orchestrator) Recover(ctx context.Context) error {
	o.mu.Lock()
	if o.state.Status != "in_progress" && o.state.Status != "completed" {
		o.mu.Unlock()
		return fmt.Errorf("nothing to recover")
	}
	o.state.Status = "recovering"
	o.saveState()
	o.mu.Unlock()
	
	o.notify("recovery_start", map[string]interface{}{
		"session_id": o.state.SessionID,
		"actions":    len(o.state.CompletedActions),
	})
	
	// Recover in reverse order
	for i := len(o.state.CompletedActions) - 1; i >= 0; i-- {
		action := o.state.CompletedActions[i]
		
		if action.RecoveryCmd == "" {
			continue
		}
		
		o.logger.Info("Recovering action",
			"phase", action.PhaseName,
			"action", action.Description,
		)
		
		// Find the executor and run recovery
		// TODO: Need to recreate executor from state
	}
	
	o.mu.Lock()
	o.state.Status = "idle"
	o.state.CompletedActions = nil
	o.saveState()
	o.mu.Unlock()
	
	o.notify("recovery_complete", map[string]interface{}{
		"session_id": o.state.SessionID,
	})
	
	return nil
}

// GetState returns current state
func (o *Orchestrator) GetState() State {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return *o.state
}

// LoadState loads state from file
func (o *Orchestrator) LoadState() error {
	data, err := os.ReadFile(o.stateFile)
	if os.IsNotExist(err) {
		return nil // No previous state
	}
	if err != nil {
		return err
	}
	
	o.mu.Lock()
	defer o.mu.Unlock()
	
	return json.Unmarshal(data, o.state)
}

func (o *Orchestrator) saveState() error {
	data, err := json.MarshalIndent(o.state, "", "  ")
	if err != nil {
		return err
	}
	
	return os.WriteFile(o.stateFile, data, 0600)
}

func (o *Orchestrator) notify(event string, data map[string]interface{}) {
	if o.notifier == nil {
		return
	}
	
	if err := o.notifier.Notify(event, data); err != nil {
		o.logger.Error("Notification failed", "event", event, "error", err)
	}
}
