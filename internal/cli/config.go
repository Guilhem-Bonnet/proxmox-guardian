package cli

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the main configuration structure
type Config struct {
	UPS           UPSConfig           `yaml:"ups"`
	Proxmox       ProxmoxConfig       `yaml:"proxmox"`
	Phases        []Phase             `yaml:"phases"`
	Recovery      RecoveryConfig      `yaml:"recovery"`
	Notifications []NotificationConfig `yaml:"notifications"`
	Options       OptionsConfig       `yaml:"options"`
}

// UPSConfig holds NUT connection settings
type UPSConfig struct {
	Driver     string         `yaml:"driver"`
	Host       string         `yaml:"host"`
	Name       string         `yaml:"name"`
	Thresholds UPSThresholds  `yaml:"thresholds"`
}

// UPSThresholds defines battery level thresholds
type UPSThresholds struct {
	Warning   int `yaml:"warning"`
	Critical  int `yaml:"critical"`
	Emergency int `yaml:"emergency"`
}

// ProxmoxConfig holds Proxmox API connection settings
type ProxmoxConfig struct {
	APIURL       string `yaml:"api_url"`
	TokenID      string `yaml:"token_id"`
	TokenSecret  string `yaml:"token_secret,omitempty"`
	SecretsFile  string `yaml:"secrets_file,omitempty"`
	InsecureTLS  bool   `yaml:"insecure_tls"`
}

// Phase represents a shutdown phase with ordered actions
type Phase struct {
	Name      string        `yaml:"name"`
	Parallel  bool          `yaml:"parallel"`
	Timeout   time.Duration `yaml:"timeout,omitempty"`
	Condition string        `yaml:"condition,omitempty"`
	Actions   []Action      `yaml:"actions"`
}

// Action represents a single executable action
type Action struct {
	Type        string            `yaml:"type"`
	Host        string            `yaml:"host,omitempty"`
	User        string            `yaml:"user,omitempty"`
	Guest       string            `yaml:"guest,omitempty"`
	Selector    *GuestSelector    `yaml:"selector,omitempty"`
	Command     string            `yaml:"command,omitempty"`
	Action      string            `yaml:"action,omitempty"`
	Recovery    string            `yaml:"recovery,omitempty"`
	Healthcheck *Healthcheck      `yaml:"healthcheck,omitempty"`
	Timeout     time.Duration     `yaml:"timeout,omitempty"`
	OnError     string            `yaml:"on_error,omitempty"`
	Retry       *RetryConfig      `yaml:"retry,omitempty"`
	Env         map[string]string `yaml:"env,omitempty"`
}

// GuestSelector defines how to select Proxmox guests
type GuestSelector struct {
	Type        string   `yaml:"type,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
	ExcludeTags []string `yaml:"exclude_tags,omitempty"`
	NameRegex   string   `yaml:"name_regex,omitempty"`
	VMIDRange   []int    `yaml:"vmid_range,omitempty"`
}

// Healthcheck defines post-action verification
type Healthcheck struct {
	Command string `yaml:"command"`
	Expect  string `yaml:"expect"` // "success" or "failure"
}

// RetryConfig defines retry behavior for failed actions
type RetryConfig struct {
	Attempts int           `yaml:"attempts"`
	Delay    time.Duration `yaml:"delay"`
	Backoff  string        `yaml:"backoff,omitempty"` // "linear" or "exponential"
}

// RecoveryConfig defines recovery behavior when power returns
type RecoveryConfig struct {
	Enabled          bool          `yaml:"enabled"`
	PowerStableDelay time.Duration `yaml:"power_stable_delay"`
	OnError          string        `yaml:"on_error"`
}

// NotificationConfig defines notification channels
type NotificationConfig struct {
	Type     string   `yaml:"type"`
	URL      string   `yaml:"url,omitempty"`
	URLEnv   string   `yaml:"url_env,omitempty"`
	Events   []string `yaml:"events"`
	Template string   `yaml:"template,omitempty"`
}

// OptionsConfig holds global options
type OptionsConfig struct {
	DryRun    bool   `yaml:"dry_run"`
	LogLevel  string `yaml:"log_level"`
	LogFormat string `yaml:"log_format"`
	LogFile   string `yaml:"log_file"`
	StateFile string `yaml:"state_file"`
	LockFile  string `yaml:"lock_file"`
}

// LoadConfig loads and parses the configuration file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Set defaults
	if cfg.Options.LogLevel == "" {
		cfg.Options.LogLevel = "info"
	}
	if cfg.Options.LogFormat == "" {
		cfg.Options.LogFormat = "json"
	}
	if cfg.Options.StateFile == "" {
		cfg.Options.StateFile = "/var/lib/proxmox-guardian/state.json"
	}
	if cfg.Options.LockFile == "" {
		cfg.Options.LockFile = "/var/run/proxmox-guardian.lock"
	}

	// Validate config
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// Validate checks the configuration for errors
func (c *Config) Validate() error {
	if c.UPS.Host == "" {
		return fmt.Errorf("ups.host is required")
	}
	if c.UPS.Name == "" {
		return fmt.Errorf("ups.name is required")
	}
	if c.Proxmox.APIURL == "" {
		return fmt.Errorf("proxmox.api_url is required")
	}
	if c.Proxmox.TokenID == "" {
		return fmt.Errorf("proxmox.token_id is required")
	}
	if len(c.Phases) == 0 {
		return fmt.Errorf("at least one phase is required")
	}

	for i, phase := range c.Phases {
		if phase.Name == "" {
			return fmt.Errorf("phase %d: name is required", i+1)
		}
		if len(phase.Actions) == 0 {
			return fmt.Errorf("phase %s: at least one action is required", phase.Name)
		}

		for j, action := range phase.Actions {
			if err := validateAction(action); err != nil {
				return fmt.Errorf("phase %s, action %d: %w", phase.Name, j+1, err)
			}
		}
	}

	return nil
}

func validateAction(a Action) error {
	validTypes := map[string]bool{
		"ssh":           true,
		"proxmox-exec":  true,
		"proxmox-guest": true,
		"local":         true,
	}

	if !validTypes[a.Type] {
		return fmt.Errorf("invalid type: %s", a.Type)
	}

	switch a.Type {
	case "ssh":
		if a.Host == "" {
			return fmt.Errorf("ssh action requires host")
		}
		if a.Command == "" {
			return fmt.Errorf("ssh action requires command")
		}
	case "proxmox-exec":
		if a.Guest == "" {
			return fmt.Errorf("proxmox-exec action requires guest")
		}
		if a.Command == "" {
			return fmt.Errorf("proxmox-exec action requires command")
		}
	case "proxmox-guest":
		if a.Selector == nil {
			return fmt.Errorf("proxmox-guest action requires selector")
		}
		if a.Action == "" {
			return fmt.Errorf("proxmox-guest action requires action (shutdown/stop)")
		}
	case "local":
		if a.Command == "" {
			return fmt.Errorf("local action requires command")
		}
	}

	// Validate on_error
	if a.OnError != "" {
		validOnError := map[string]bool{
			"continue":    true,
			"abort_phase": true,
			"abort_all":   true,
		}
		if !validOnError[a.OnError] {
			return fmt.Errorf("invalid on_error: %s", a.OnError)
		}
	}

	// Validate healthcheck expect
	if a.Healthcheck != nil && a.Healthcheck.Expect != "" {
		if a.Healthcheck.Expect != "success" && a.Healthcheck.Expect != "failure" {
			return fmt.Errorf("healthcheck.expect must be 'success' or 'failure'")
		}
	}

	return nil
}
