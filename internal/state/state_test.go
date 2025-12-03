package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateManager(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	mgr := NewManager(statePath)

	// Test initial state
	state := mgr.GetState()
	if state.Status != StatusIdle {
		t.Errorf("Expected initial status 'idle', got '%s'", state.Status)
	}

	// Test starting a session
	sessionID := mgr.StartSession("test-trigger")
	if sessionID == "" {
		t.Error("Expected non-empty session ID")
	}

	state = mgr.GetState()
	if state.Status != StatusInProgress {
		t.Errorf("Expected status 'in_progress', got '%s'", state.Status)
	}
	if state.TriggerEvent != "test-trigger" {
		t.Errorf("Expected trigger 'test-trigger', got '%s'", state.TriggerEvent)
	}

	// Test recording an action
	action := CompletedAction{
		PhaseIndex:  0,
		PhaseName:   "test-phase",
		ActionIndex: 0,
		ActionType:  "local",
		ActionSpec: ActionSpec{
			Type:     "local",
			Command:  "echo test",
			Recovery: "echo recovery",
		},
		CompletedAt: time.Now(),
		Success:     true,
	}
	mgr.RecordAction(action)

	state = mgr.GetState()
	if len(state.CompletedActions) != 1 {
		t.Errorf("Expected 1 completed action, got %d", len(state.CompletedActions))
	}

	// Test saving and loading
	if err := mgr.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Create new manager and load
	mgr2 := NewManager(statePath)
	if err := mgr2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	state2 := mgr2.GetState()
	if state2.SessionID != sessionID {
		t.Errorf("Expected session ID '%s', got '%s'", sessionID, state2.SessionID)
	}
	if len(state2.CompletedActions) != 1 {
		t.Errorf("Expected 1 completed action after load, got %d", len(state2.CompletedActions))
	}
}

func TestGetActionsForRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	mgr := NewManager(statePath)
	mgr.StartSession("test")

	// Add some actions
	actions := []CompletedAction{
		{
			ActionIndex: 0,
			ActionType:  "local",
			ActionSpec:  ActionSpec{Type: "local", Recovery: "recovery1"},
			Success:     true,
		},
		{
			ActionIndex: 1,
			ActionType:  "local",
			ActionSpec:  ActionSpec{Type: "local", Recovery: ""}, // No recovery
			Success:     true,
		},
		{
			ActionIndex: 2,
			ActionType:  "local",
			ActionSpec:  ActionSpec{Type: "local", Recovery: "recovery3"},
			Success:     false, // Failed, shouldn't be recovered
		},
		{
			ActionIndex: 3,
			ActionType:  "local",
			ActionSpec:  ActionSpec{Type: "local", Recovery: "recovery4"},
			Success:     true,
		},
	}

	for _, a := range actions {
		mgr.RecordAction(a)
	}

	recoverable := mgr.GetActionsForRecovery()

	// Should only have actions 0 and 3 (successful with recovery commands), in reverse order
	if len(recoverable) != 2 {
		t.Fatalf("Expected 2 recoverable actions, got %d", len(recoverable))
	}

	// Check reverse order (action 3 should be first)
	if recoverable[0].ActionIndex != 3 {
		t.Errorf("Expected first recoverable action index 3, got %d", recoverable[0].ActionIndex)
	}
	if recoverable[1].ActionIndex != 0 {
		t.Errorf("Expected second recoverable action index 0, got %d", recoverable[1].ActionIndex)
	}
}

func TestNeedsRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	mgr := NewManager(statePath)

	// Initially no recovery needed
	if mgr.NeedsRecovery() {
		t.Error("Expected no recovery needed initially")
	}

	// Start a session
	mgr.StartSession("test")

	// Now recovery should be needed
	if !mgr.NeedsRecovery() {
		t.Error("Expected recovery needed after starting session")
	}

	// Complete successfully
	mgr.SetStatus(StatusCompleted)

	// No recovery needed after completion
	if mgr.NeedsRecovery() {
		t.Error("Expected no recovery needed after completion")
	}

	// Set to failed
	mgr.SetStatus(StatusFailed)

	// Recovery needed for failed state
	if !mgr.NeedsRecovery() {
		t.Error("Expected recovery needed for failed state")
	}
}

func TestStateClear(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	mgr := NewManager(statePath)
	mgr.StartSession("test")
	mgr.RecordAction(CompletedAction{ActionType: "test"})

	state := mgr.GetState()
	if len(state.CompletedActions) == 0 {
		t.Error("Expected actions before clear")
	}

	mgr.Clear()

	state = mgr.GetState()
	if state.Status != StatusIdle {
		t.Errorf("Expected status 'idle' after clear, got '%s'", state.Status)
	}
	if len(state.CompletedActions) != 0 {
		t.Error("Expected no actions after clear")
	}
}

func TestLoadNonExistentState(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "nonexistent", "state.json")

	mgr := NewManager(statePath)

	// Should not error for non-existent file
	if err := mgr.Load(); err != nil {
		t.Errorf("Expected no error for non-existent file, got: %v", err)
	}

	state := mgr.GetState()
	if state.Status != StatusIdle {
		t.Errorf("Expected idle status for new state, got '%s'", state.Status)
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "newdir", "state.json")

	mgr := NewManager(statePath)
	mgr.StartSession("test")

	if err := mgr.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Check file exists
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Error("State file was not created")
	}
}
