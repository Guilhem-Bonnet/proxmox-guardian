package executor

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHExecutor executes commands via SSH
type SSHExecutor struct {
	BaseAction
	Host       string
	User       string
	KeyFile    string
	KnownHosts string
}

// NewSSHExecutor creates a new SSH executor
func NewSSHExecutor(host, user, command string) *SSHExecutor {
	if user == "" {
		user = "root"
	}

	return &SSHExecutor{
		BaseAction: BaseAction{
			Type:    "ssh",
			Command: command,
			Timeout: 60 * time.Second,
		},
		Host:    host,
		User:    user,
		KeyFile: os.ExpandEnv("$HOME/.ssh/id_rsa"),
	}
}

// Execute runs the SSH command
func (s *SSHExecutor) Execute(ctx context.Context) (*ActionResult, error) {
	start := time.Now()

	client, err := s.connect()
	if err != nil {
		return &ActionResult{
			Success:  false,
			Error:    fmt.Sprintf("SSH connection failed: %v", err),
			Duration: time.Since(start),
		}, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return &ActionResult{
			Success:  false,
			Error:    fmt.Sprintf("SSH session failed: %v", err),
			Duration: time.Since(start),
		}, err
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	// Create a channel to handle command completion
	done := make(chan error, 1)
	go func() {
		done <- session.Run(s.Command)
	}()

	// Wait for completion or context cancellation
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		return &ActionResult{
			Success:  false,
			Error:    "command cancelled",
			Duration: time.Since(start),
		}, ctx.Err()
	case err := <-done:
		if err != nil {
			return &ActionResult{
				Success:  false,
				Output:   stdout.String(),
				Error:    fmt.Sprintf("%v: %s", err, stderr.String()),
				Duration: time.Since(start),
			}, err
		}
	}

	return &ActionResult{
		Success:  true,
		Output:   stdout.String(),
		Duration: time.Since(start),
	}, nil
}

// Recover runs the recovery command
func (s *SSHExecutor) Recover(ctx context.Context) (*ActionResult, error) {
	if s.Recovery == "" {
		return &ActionResult{
			Success: true,
			Output:  "no recovery command defined",
		}, nil
	}

	recoveryExec := &SSHExecutor{
		BaseAction: BaseAction{
			Type:    "ssh",
			Command: s.Recovery,
			Timeout: s.Timeout,
		},
		Host:    s.Host,
		User:    s.User,
		KeyFile: s.KeyFile,
	}

	return recoveryExec.Execute(ctx)
}

// Healthcheck verifies the action completed
func (s *SSHExecutor) Healthcheck(ctx context.Context) (bool, error) {
	if s.BaseAction.Healthcheck == nil {
		return true, nil
	}

	checkExec := &SSHExecutor{
		BaseAction: BaseAction{
			Type:    "ssh",
			Command: s.BaseAction.Healthcheck.Command,
			Timeout: 10 * time.Second,
		},
		Host:    s.Host,
		User:    s.User,
		KeyFile: s.KeyFile,
	}

	result, err := checkExec.Execute(ctx)

	expectSuccess := s.BaseAction.Healthcheck.Expect == "success"

	if expectSuccess {
		return result.Success, err
	}
	// Expect failure = command should fail
	return !result.Success, nil
}

// String returns a human-readable description
func (s *SSHExecutor) String() string {
	return fmt.Sprintf("SSH[%s@%s]: %s", s.User, s.Host, truncateCmd(s.Command))
}

func (s *SSHExecutor) connect() (*ssh.Client, error) {
	key, err := os.ReadFile(s.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("reading SSH key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("parsing SSH key: %w", err)
	}

	config := &ssh.ClientConfig{
		User: s.User,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: Use known_hosts
		Timeout:         10 * time.Second,
	}

	// Add port if not present
	host := s.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "22")
	}

	return ssh.Dial("tcp", host, config)
}

func truncateCmd(cmd string) string {
	if len(cmd) > 50 {
		return cmd[:47] + "..."
	}
	return cmd
}
