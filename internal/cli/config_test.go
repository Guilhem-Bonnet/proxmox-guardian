package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test.yaml")

	configContent := `
ups:
  driver: nut
  host: localhost:3493
  name: test-ups
  thresholds:
    warning: 30
    critical: 20
    emergency: 10

proxmox:
  api_url: https://127.0.0.1:8006/api2/json
  token_id: test@pve!test
  insecure_tls: true

phases:
  - name: "test-phase"
    parallel: false
    actions:
      - type: local
        command: "echo test"
        timeout: 10s
        on_error: continue

recovery:
  enabled: true
  power_stable_delay: 30s
  on_error: notify

options:
  dry_run: false
  log_level: info
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Verify UPS config
	if cfg.UPS.Host != "localhost:3493" {
		t.Errorf("Expected UPS host 'localhost:3493', got '%s'", cfg.UPS.Host)
	}
	if cfg.UPS.Name != "test-ups" {
		t.Errorf("Expected UPS name 'test-ups', got '%s'", cfg.UPS.Name)
	}
	if cfg.UPS.Thresholds.Warning != 30 {
		t.Errorf("Expected warning threshold 30, got %d", cfg.UPS.Thresholds.Warning)
	}

	// Verify Proxmox config
	if cfg.Proxmox.APIURL != "https://127.0.0.1:8006/api2/json" {
		t.Errorf("Expected Proxmox API URL, got '%s'", cfg.Proxmox.APIURL)
	}

	// Verify phases
	if len(cfg.Phases) != 1 {
		t.Errorf("Expected 1 phase, got %d", len(cfg.Phases))
	}
	if cfg.Phases[0].Name != "test-phase" {
		t.Errorf("Expected phase name 'test-phase', got '%s'", cfg.Phases[0].Name)
	}

	// Verify recovery
	if !cfg.Recovery.Enabled {
		t.Error("Expected recovery to be enabled")
	}
	if cfg.Recovery.PowerStableDelay != 30*time.Second {
		t.Errorf("Expected power stable delay 30s, got %v", cfg.Recovery.PowerStableDelay)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		config    Config
		expectErr bool
	}{
		{
			name: "valid config",
			config: Config{
				UPS: UPSConfig{
					Host: "localhost:3493",
					Name: "test-ups",
				},
				Proxmox: ProxmoxConfig{
					APIURL:  "https://127.0.0.1:8006/api2/json",
					TokenID: "test@pve!test",
				},
				Phases: []Phase{
					{
						Name: "test",
						Actions: []Action{
							{Type: "local", Command: "echo test"},
						},
					},
				},
			},
			expectErr: false,
		},
		{
			name: "missing UPS host",
			config: Config{
				UPS: UPSConfig{
					Name: "test-ups",
				},
				Proxmox: ProxmoxConfig{
					APIURL:  "https://127.0.0.1:8006/api2/json",
					TokenID: "test@pve!test",
				},
				Phases: []Phase{
					{Name: "test", Actions: []Action{{Type: "local", Command: "echo"}}},
				},
			},
			expectErr: true,
		},
		{
			name: "missing phases",
			config: Config{
				UPS: UPSConfig{
					Host: "localhost:3493",
					Name: "test-ups",
				},
				Proxmox: ProxmoxConfig{
					APIURL:  "https://127.0.0.1:8006/api2/json",
					TokenID: "test@pve!test",
				},
				Phases: []Phase{},
			},
			expectErr: true,
		},
		{
			name: "invalid action type",
			config: Config{
				UPS: UPSConfig{
					Host: "localhost:3493",
					Name: "test-ups",
				},
				Proxmox: ProxmoxConfig{
					APIURL:  "https://127.0.0.1:8006/api2/json",
					TokenID: "test@pve!test",
				},
				Phases: []Phase{
					{
						Name: "test",
						Actions: []Action{
							{Type: "invalid-type"},
						},
					},
				},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectErr && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
		})
	}
}

func TestValidateAction(t *testing.T) {
	tests := []struct {
		name      string
		action    Action
		expectErr bool
	}{
		{
			name:      "valid local action",
			action:    Action{Type: "local", Command: "echo test"},
			expectErr: false,
		},
		{
			name:      "valid ssh action",
			action:    Action{Type: "ssh", Host: "server.local", Command: "uptime"},
			expectErr: false,
		},
		{
			name:      "ssh missing host",
			action:    Action{Type: "ssh", Command: "uptime"},
			expectErr: true,
		},
		{
			name:      "ssh missing command",
			action:    Action{Type: "ssh", Host: "server.local"},
			expectErr: true,
		},
		{
			name: "valid proxmox-guest action",
			action: Action{
				Type:     "proxmox-guest",
				Action:   "shutdown",
				Selector: &GuestSelector{Type: "lxc"},
			},
			expectErr: false,
		},
		{
			name:      "proxmox-guest missing selector",
			action:    Action{Type: "proxmox-guest", Action: "shutdown"},
			expectErr: true,
		},
		{
			name:      "invalid on_error value",
			action:    Action{Type: "local", Command: "echo", OnError: "invalid"},
			expectErr: true,
		},
		{
			name:      "valid on_error continue",
			action:    Action{Type: "local", Command: "echo", OnError: "continue"},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAction(tt.action)
			if tt.expectErr && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
		})
	}
}
