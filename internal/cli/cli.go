package cli

import (
"context"
"fmt"
"log/slog"
"os"
"os/signal"
"strconv"
"syscall"
"time"

"github.com/Guilhem-Bonnet/proxmox-guardian/internal/executor"
"github.com/Guilhem-Bonnet/proxmox-guardian/internal/orchestrator"
"github.com/Guilhem-Bonnet/proxmox-guardian/internal/proxmox"
"github.com/Guilhem-Bonnet/proxmox-guardian/internal/ups"
"github.com/spf13/cobra"
)

type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

var (
cfgFile   string
buildInfo BuildInfo
)

var rootCmd = &cobra.Command{
	Use:   "proxmox-guardian",
	Short: "UPS-triggered graceful shutdown orchestrator for Proxmox VE",
	Long: `Proxmox Guardian monitors your UPS via NUT and orchestrates
graceful shutdown of VMs, containers, and services when power fails.`,
}

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate configuration file",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return fmt.Errorf("config validation failed: %w", err)
		}

		fmt.Println("✅ Configuration syntax: OK")
		fmt.Printf("   UPS: %s@%s\n", cfg.UPS.Name, cfg.UPS.Host)
		fmt.Printf("   Phases: %d\n", len(cfg.Phases))

		totalActions := 0
		for _, p := range cfg.Phases {
			totalActions += len(p.Actions)
		}
		fmt.Printf("   Actions: %d\n", totalActions)

		fmt.Println("✅ All validations passed")
		return nil
	},
}

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Show execution plan without running",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		fmt.Println("📋 Shutdown Plan:")
		fmt.Println()

		for i, phase := range cfg.Phases {
			mode := "sequential"
			if phase.Parallel {
				mode = "parallel"
			}
			fmt.Printf("Phase %d: %s (%s)\n", i+1, phase.Name, mode)
			if phase.Timeout > 0 {
				fmt.Printf("  Timeout: %s\n", phase.Timeout)
			}

			for j, action := range phase.Actions {
				fmt.Printf("  %d.%d [%s] ", i+1, j+1, action.Type)
				switch action.Type {
				case "ssh":
					fmt.Printf("%s@%s: %s", action.User, action.Host, truncate(action.Command, 40))
				case "local":
					fmt.Printf("%s", truncate(action.Command, 50))
				case "proxmox-guest":
					fmt.Printf("%s ", action.Action)
					if action.Selector != nil {
						if len(action.Selector.VMIDRange) > 0 {
							fmt.Printf("vmid=%v ", action.Selector.VMIDRange)
						}
						if action.Selector.Type != "" {
							fmt.Printf("type=%s", action.Selector.Type)
						}
					}
				}
				fmt.Println()
			}
			fmt.Println()
		}

		return nil
	},
}

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run in daemon mode, monitoring UPS",
	Long: `Starts Proxmox Guardian in daemon mode, continuously monitoring
the UPS via NUT and triggering shutdown when thresholds are reached.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		fmt.Println("👁️ Starting daemon mode...")
		fmt.Printf("📡 Connecting to NUT at %s...\n", cfg.UPS.Host)

		// Create NUT client
		nutClient := ups.NewClient(cfg.UPS.Host+":3493", cfg.UPS.Name)
		if err := nutClient.Connect(); err != nil {
			return fmt.Errorf("failed to connect to NUT: %w", err)
		}
		defer nutClient.Close()

		fmt.Println("✅ Connected to NUT server")

		// Create Proxmox client for shutdown operations
		pxClient, err := proxmox.NewClient(proxmox.Config{
APIURL:      cfg.Proxmox.APIURL,
TokenID:     cfg.Proxmox.TokenID,
TokenSecret: cfg.Proxmox.TokenSecret,
InsecureTLS: cfg.Proxmox.InsecureTLS,
})
		if err != nil {
			return fmt.Errorf("failed to create Proxmox client: %w", err)
		}

		// Test Proxmox connection
		ctx := context.Background()
		version, err := pxClient.GetVersion(ctx)
		if err != nil {
			fmt.Printf("⚠️ Warning: Cannot connect to Proxmox API: %v\n", err)
		} else {
			fmt.Printf("✅ Connected to Proxmox %s\n", version)
		}

		// Setup signal handling
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		// Start monitoring loop
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		var onBatteryStart time.Time
		var shutdownTriggered bool

		fmt.Println("🔋 Starting UPS monitoring loop...")

		// Get initial status
		status, err := nutClient.GetStatus(ctx)
		if err != nil {
			fmt.Printf("⚠️ Initial status check failed: %v\n", err)
		} else {
			fmt.Printf("🔋 Initial: Battery %d%% | Runtime %ds | Status: %s\n",
status.BatteryCharge, status.Runtime, status.Status)
		}

		for {
			select {
			case <-sigChan:
				fmt.Println("\n⚠️ Received shutdown signal, stopping...")
				return nil
			case <-ticker.C:
				status, err := nutClient.GetStatus(ctx)
				if err != nil {
					fmt.Printf("❌ Error getting UPS status: %v\n", err)
					continue
				}

				fmt.Printf("🔋 Battery: %d%% | Runtime: %ds | Status: %s | Load: %d%%\n",
status.BatteryCharge, status.Runtime, status.Status, status.Load)

				if status.IsOnBattery() && !shutdownTriggered {
					if onBatteryStart.IsZero() {
						onBatteryStart = time.Now()
						fmt.Println("⚡ Power outage detected! Starting monitoring...")
					}

					shouldShutdown := false
					reason := ""

					if status.BatteryCharge <= cfg.UPS.Thresholds.Critical {
						shouldShutdown = true
						reason = fmt.Sprintf("battery at %d%% (critical threshold: %d%%)",
status.BatteryCharge, cfg.UPS.Thresholds.Critical)
					}

					if status.IsLowBattery() {
						shouldShutdown = true
						reason = "UPS reports low battery"
					}

					if shouldShutdown {
						fmt.Printf("🚨 SHUTDOWN TRIGGERED: %s\n", reason)
						shutdownTriggered = true

						// Build orchestrator phases from config
						phases, err := buildPhasesFromConfig(cfg, pxClient)
						if err != nil {
							fmt.Printf("❌ Failed to build phases: %v\n", err)
							return fmt.Errorf("building phases: %w", err)
						}

						// Create orchestrator
						logger := &slogLogger{slog.Default()}
						orch := orchestrator.NewOrchestrator(phases, cfg.Options.StateFile, logger, &noopNotifier{})

						// Execute shutdown sequence
						fmt.Println("📋 Executing shutdown phases...")
						shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 15*time.Minute)
						defer shutdownCancel()

						if err := orch.Execute(shutdownCtx, reason); err != nil {
							fmt.Printf("❌ Shutdown sequence failed: %v\n", err)
						} else {
							fmt.Println("✅ Shutdown sequence completed successfully")
						}

						// Final: shutdown the Proxmox host itself
						fmt.Println("🔴 Initiating Proxmox host shutdown...")
						if err := executeHostShutdown(); err != nil {
							fmt.Printf("❌ Host shutdown failed: %v\n", err)
						}

						return nil
					}
				} else if status.IsOnline() && !onBatteryStart.IsZero() {
					fmt.Println("✅ Power restored!")
					onBatteryStart = time.Time{}
					// Note: shutdownTriggered stays false as it was never set (shutdown didn't happen)
				}
			}
		}
	},
}

var notifyCmd = &cobra.Command{
	Use:   "notify [event]",
	Short: "Handle NUT notification event",
	Long: `Called by NUT's NOTIFYCMD when UPS events occur.
Events: ONLINE, ONBATT, LOWBATT, FSD, COMMOK, COMMBAD, SHUTDOWN, REPLBATT, NOCOMM`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		event := args[0]

		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		_ = cfg
		fmt.Printf("📨 Received NUT event: %s\n", event)
		return nil
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("proxmox-guardian %s\n", buildInfo.Version)
		fmt.Printf("  commit: %s\n", buildInfo.Commit)
		fmt.Printf("  built:  %s\n", buildInfo.Date)
	},
}

func Execute(info BuildInfo) error {
	buildInfo = info
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "/etc/proxmox-guardian/guardian.yaml", "config file path")

	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(planCmd)
	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(notifyCmd)
	rootCmd.AddCommand(versionCmd)
}

func loadConfig() (*Config, error) {
	if _, err := os.Stat(cfgFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("config file not found: %s", cfgFile)
	}

	cfg, err := LoadConfig(cfgFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return cfg, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// slogLogger adapts slog.Logger to the orchestrator.Logger interface
type slogLogger struct {
	logger *slog.Logger
}

func (l *slogLogger) Info(msg string, fields ...interface{}) {
	l.logger.Info(msg, fields...)
}

func (l *slogLogger) Error(msg string, fields ...interface{}) {
	l.logger.Error(msg, fields...)
}

func (l *slogLogger) Debug(msg string, fields ...interface{}) {
	l.logger.Debug(msg, fields...)
}

// noopNotifier is a no-op notifier
type noopNotifier struct{}

func (n *noopNotifier) Notify(event string, data map[string]interface{}) error {
	fmt.Printf("📣 Event: %s\n", event)
	return nil
}

// buildPhasesFromConfig converts config phases to orchestrator phases
func buildPhasesFromConfig(cfg *Config, pxClient *proxmox.Client) ([]orchestrator.Phase, error) {
	var phases []orchestrator.Phase

	for _, cfgPhase := range cfg.Phases {
		phase := orchestrator.Phase{
			Name:      cfgPhase.Name,
			Parallel:  cfgPhase.Parallel,
			Timeout:   cfgPhase.Timeout,
			Condition: cfgPhase.Condition,
			Actions:   []orchestrator.Action{},
		}

		for _, cfgAction := range cfgPhase.Actions {
			exec, err := createExecutor(cfg, cfgAction, pxClient)
			if err != nil {
				return nil, fmt.Errorf("creating executor for action in phase %s: %w", cfgPhase.Name, err)
			}

			action := orchestrator.Action{
				Type:     cfgAction.Type,
				Executor: exec,
				Recovery: cfgAction.Recovery,
				OnError:  cfgAction.OnError,
			}

			if cfgAction.Retry != nil {
				action.Retry = &executor.RetryConfig{
					Attempts: cfgAction.Retry.Attempts,
					Delay:    cfgAction.Retry.Delay,
				}
			}

			phase.Actions = append(phase.Actions, action)
		}

		phases = append(phases, phase)
	}

	return phases, nil
}

// createExecutor creates the appropriate executor for an action
func createExecutor(cfg *Config, action Action, pxClient *proxmox.Client) (executor.Executor, error) {
	timeout := action.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	switch action.Type {
	case "ssh":
		exec := executor.NewSSHExecutor(action.Host, action.User, action.Command)
		exec.Timeout = timeout
		return exec, nil

	case "local":
		exec := executor.NewLocalExecutor(action.Command)
		exec.Timeout = timeout
		return exec, nil

	case "proxmox-guest":
		if pxClient == nil {
			return nil, fmt.Errorf("proxmox client required for proxmox-guest action")
		}

		selector := executor.GuestSelector{}
		if action.Selector != nil {
			selector.Type = action.Selector.Type
			selector.Tags = action.Selector.Tags
			selector.ExcludeTags = action.Selector.ExcludeTags
			selector.NameRegex = action.Selector.NameRegex
			selector.VMIDRange = action.Selector.VMIDRange
		}

		adapter := &proxmoxAPIAdapter{client: pxClient}
		exec := executor.NewProxmoxGuestExecutor(selector, action.Action, adapter)
		exec.Timeout = timeout
		return exec, nil

	default:
		return nil, fmt.Errorf("unknown action type: %s", action.Type)
	}
}

// proxmoxAPIAdapter adapts proxmox.Client to executor.ProxmoxAPI interface
type proxmoxAPIAdapter struct {
	client *proxmox.Client
}

func (a *proxmoxAPIAdapter) ExecInGuest(ctx context.Context, guestType, guestID, command string) (string, error) {
	return "", fmt.Errorf("ExecInGuest not implemented")
}

func (a *proxmoxAPIAdapter) ShutdownGuest(ctx context.Context, guestType, guestID string, timeout time.Duration) error {
	vmid, err := strconv.Atoi(guestID)
	if err != nil {
		return fmt.Errorf("invalid guest ID %s: %w", guestID, err)
	}

	selector := proxmox.GuestSelector{
		Type:      guestType,
		VMIDRange: []int{vmid, vmid},
	}
	guests, err := a.client.GetGuestsBySelector(ctx, selector)
	if err != nil {
		return fmt.Errorf("finding guest %d: %w", vmid, err)
	}
	if len(guests) == 0 {
		return fmt.Errorf("guest %d not found", vmid)
	}

	return a.client.ShutdownGuest(ctx, guestType, vmid, guests[0].Node, timeout)
}

func (a *proxmoxAPIAdapter) GetGuestsBySelector(ctx context.Context, selector executor.GuestSelector) ([]executor.Guest, error) {
	pxSelector := proxmox.GuestSelector{
		Type:        selector.Type,
		Tags:        selector.Tags,
		ExcludeTags: selector.ExcludeTags,
		NameRegex:   selector.NameRegex,
		VMIDRange:   selector.VMIDRange,
	}

	pxGuests, err := a.client.GetGuestsBySelector(ctx, pxSelector)
	if err != nil {
		return nil, err
	}

	var guests []executor.Guest
	for _, g := range pxGuests {
		guests = append(guests, executor.Guest{
Type:   g.Type,
VMID:   g.VMID,
Name:   g.Name,
Node:   g.Node,
Status: g.Status,
Tags:   g.Tags,
})
	}

	return guests, nil
}

// executeHostShutdown initiates the Proxmox host shutdown
func executeHostShutdown() error {
	fmt.Println("⏳ Waiting 10 seconds before host shutdown...")
	time.Sleep(10 * time.Second)

	exec := executor.NewLocalExecutor("shutdown -h now 'UPS battery critical - emergency shutdown'")
	exec.Timeout = 30 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := exec.Execute(ctx)
	if err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("shutdown command failed: %s", result.Error)
	}

	return nil
}
