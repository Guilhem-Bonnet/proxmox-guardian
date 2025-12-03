package executor

import (
	"context"
	"testing"
	"time"
)

func TestLocalExecutor(t *testing.T) {
	exec := NewLocalExecutor("echo 'hello world'")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := exec.Execute(ctx)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success, got failure: %s", result.Error)
	}

	if result.Output != "hello world\n" {
		t.Errorf("Expected 'hello world\\n', got '%s'", result.Output)
	}
}

func TestLocalExecutorFailure(t *testing.T) {
	exec := NewLocalExecutor("exit 1")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := exec.Execute(ctx)
	if err == nil {
		t.Error("Expected error, got nil")
	}

	if result.Success {
		t.Error("Expected failure, got success")
	}
}

func TestLocalExecutorTimeout(t *testing.T) {
	exec := NewLocalExecutor("sleep 10")
	exec.Timeout = 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result, err := exec.Execute(ctx)
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}

	if result.Success {
		t.Error("Expected failure due to timeout")
	}
}

func TestLocalExecutorRecovery(t *testing.T) {
	exec := NewLocalExecutor("echo 'shutdown'")
	exec.Recovery = "echo 'recovery'"

	ctx := context.Background()

	result, err := exec.Recover(ctx)
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success, got failure: %s", result.Error)
	}

	if result.Output != "recovery\n" {
		t.Errorf("Expected 'recovery\\n', got '%s'", result.Output)
	}
}

func TestLocalExecutorNoRecovery(t *testing.T) {
	exec := NewLocalExecutor("echo 'test'")
	// No recovery command set

	ctx := context.Background()

	result, err := exec.Recover(ctx)
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success for no-op recovery")
	}
}

func TestLocalExecutorString(t *testing.T) {
	exec := NewLocalExecutor("echo test")

	expected := "Local: echo test"
	if exec.String() != expected {
		t.Errorf("Expected '%s', got '%s'", expected, exec.String())
	}
}

func TestExecuteWithRetry(t *testing.T) {
	// Create a counter to track attempts
	attempts := 0

	// Create a mock executor that fails twice then succeeds
	exec := &mockExecutor{
		executeFunc: func(ctx context.Context) (*ActionResult, error) {
			attempts++
			if attempts < 3 {
				return &ActionResult{Success: false, Error: "simulated failure"}, nil
			}
			return &ActionResult{Success: true, Output: "success"}, nil
		},
	}

	retry := &RetryConfig{
		Attempts: 3,
		Delay:    10 * time.Millisecond,
		Backoff:  "linear",
	}

	ctx := context.Background()
	result, err := ExecuteWithRetry(ctx, exec, retry)

	if err != nil {
		t.Fatalf("ExecuteWithRetry failed: %v", err)
	}

	if !result.Success {
		t.Error("Expected success after retries")
	}

	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}

	if result.Retries != 2 {
		t.Errorf("Expected 2 retries recorded, got %d", result.Retries)
	}
}

// mockExecutor for testing
type mockExecutor struct {
	executeFunc func(ctx context.Context) (*ActionResult, error)
}

func (m *mockExecutor) Execute(ctx context.Context) (*ActionResult, error) {
	return m.executeFunc(ctx)
}

func (m *mockExecutor) Recover(ctx context.Context) (*ActionResult, error) {
	return &ActionResult{Success: true}, nil
}

func (m *mockExecutor) Healthcheck(ctx context.Context) (bool, error) {
	return true, nil
}

func (m *mockExecutor) String() string {
	return "MockExecutor"
}
