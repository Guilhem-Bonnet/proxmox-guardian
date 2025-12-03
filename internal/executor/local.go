package executor

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// LocalExecutor executes commands locally
type LocalExecutor struct {
	BaseAction
	Shell string
}

// NewLocalExecutor creates a new local executor
func NewLocalExecutor(command string) *LocalExecutor {
	return &LocalExecutor{
		BaseAction: BaseAction{
			Type:    "local",
			Command: command,
			Timeout: 60 * time.Second,
		},
		Shell: "/bin/sh",
	}
}

// Execute runs the local command
func (l *LocalExecutor) Execute(ctx context.Context) (*ActionResult, error) {
	start := time.Now()

	// Create command with context for timeout
	ctx, cancel := context.WithTimeout(ctx, l.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, l.Shell, "-c", l.Command)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return &ActionResult{
			Success:  false,
			Output:   stdout.String(),
			Error:    "command timed out",
			Duration: time.Since(start),
		}, ctx.Err()
	}

	if err != nil {
		return &ActionResult{
			Success:  false,
			Output:   stdout.String(),
			Error:    fmt.Sprintf("%v: %s", err, stderr.String()),
			Duration: time.Since(start),
		}, err
	}

	return &ActionResult{
		Success:  true,
		Output:   stdout.String(),
		Duration: time.Since(start),
	}, nil
}

// Recover runs the recovery command
func (l *LocalExecutor) Recover(ctx context.Context) (*ActionResult, error) {
	if l.Recovery == "" {
		return &ActionResult{
			Success: true,
			Output:  "no recovery command defined",
		}, nil
	}

	recoveryExec := NewLocalExecutor(l.Recovery)
	recoveryExec.Timeout = l.Timeout

	return recoveryExec.Execute(ctx)
}

// Healthcheck verifies the action completed
func (l *LocalExecutor) Healthcheck(ctx context.Context) (bool, error) {
	if l.BaseAction.Healthcheck == nil {
		return true, nil
	}

	checkExec := NewLocalExecutor(l.BaseAction.Healthcheck.Command)
	checkExec.Timeout = 10 * time.Second

	result, _ := checkExec.Execute(ctx)

	expectSuccess := l.BaseAction.Healthcheck.Expect == "success"

	if expectSuccess {
		return result.Success, nil
	}
	return !result.Success, nil
}

// String returns a human-readable description
func (l *LocalExecutor) String() string {
	return fmt.Sprintf("Local: %s", truncateCmd(l.Command))
}
