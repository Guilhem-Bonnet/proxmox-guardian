package executor

import (
	"context"
	"time"
)

// ActionResult represents the result of an action execution
type ActionResult struct {
	Success  bool          `json:"success"`
	Output   string        `json:"output,omitempty"`
	Error    string        `json:"error,omitempty"`
	Duration time.Duration `json:"duration"`
	Retries  int           `json:"retries,omitempty"`
}

// Executor interface for all action types
type Executor interface {
	// Execute runs the action
	Execute(ctx context.Context) (*ActionResult, error)

	// Recover reverses the action (for recovery mode)
	Recover(ctx context.Context) (*ActionResult, error)

	// Healthcheck verifies the action completed successfully
	Healthcheck(ctx context.Context) (bool, error)

	// String returns a human-readable description
	String() string
}

// BaseAction contains common fields for all actions
type BaseAction struct {
	Type        string
	Command     string
	Recovery    string
	Timeout     time.Duration
	OnError     string
	Retry       *RetryConfig
	Healthcheck *HealthcheckConfig
}

// RetryConfig defines retry behavior
type RetryConfig struct {
	Attempts int
	Delay    time.Duration
	Backoff  string // "linear" or "exponential"
}

// HealthcheckConfig defines post-action verification
type HealthcheckConfig struct {
	Command string
	Expect  string // "success" or "failure"
}

// ExecuteWithRetry runs an executor with configured retry logic
func ExecuteWithRetry(ctx context.Context, exec Executor, retry *RetryConfig) (*ActionResult, error) {
	if retry == nil || retry.Attempts <= 1 {
		return exec.Execute(ctx)
	}

	var lastResult *ActionResult
	var lastErr error

	delay := retry.Delay

	for attempt := 1; attempt <= retry.Attempts; attempt++ {
		result, err := exec.Execute(ctx)
		if err == nil && result.Success {
			result.Retries = attempt - 1
			return result, nil
		}

		lastResult = result
		lastErr = err

		if attempt < retry.Attempts {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}

			// Increase delay for exponential backoff
			if retry.Backoff == "exponential" {
				delay = delay * 2
			}
		}
	}

	if lastResult != nil {
		lastResult.Retries = retry.Attempts
	}

	return lastResult, lastErr
}
