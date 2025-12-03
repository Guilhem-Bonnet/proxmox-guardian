package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

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

		// Show summary
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

		fmt.Println("📋 Execution Plan:")
		fmt.Println("==================")

		for i, phase := range cfg.Phases {
			parallel := "sequential"
			if phase.Parallel {
				parallel = "parallel"
			}
			fmt.Printf("\nPhase %d: %s (%s, timeout: %s)\n", i+1, phase.Name, parallel, phase.Timeout)

			for j, action := range phase.Actions {
				fmt.Printf("  %d.%d [%s] ", i+1, j+1, action.Type)
				switch action.Type {
				case "ssh":
					fmt.Printf("%s@%s: %s\n", action.User, action.Host, truncate(action.Command, 40))
				case "local":
					fmt.Printf("%s\n", truncate(action.Command, 50))
				case "proxmox-guest":
					fmt.Printf("%s on selector\n", action.Action)
				default:
					fmt.Printf("%s\n", action.Type)
				}
			}
		}

		return nil
	},
}

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Start in daemon mode (monitors UPS continuously)",
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

				// Log status periodically
				fmt.Printf("🔋 Battery: %d%% | Runtime: %ds | Status: %s | Load: %d%%\n",
					status.BatteryCharge, status.Runtime, status.Status, status.Load)

				// Check if on battery
				if status.IsOnBattery() && !shutdownTriggered {
					if onBatteryStart.IsZero() {
						onBatteryStart = time.Now()
						fmt.Println("⚡ Power outage detected! Starting monitoring...")
					}

					// Check thresholds
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

						// Execute shutdown phases
						fmt.Println("📋 Executing shutdown phases...")
						for i, phase := range cfg.Phases {
							fmt.Printf("  Phase %d: %s\n", i+1, phase.Name)
							for _, action := range phase.Actions {
								fmt.Printf("    - %s: %s\n", action.Type, truncate(action.Command, 50))
							}
						}

						fmt.Println("✅ Shutdown sequence would be executed here")
						// In production, call orchestrator here
						// return nil to stop after shutdown
					}
				} else if status.IsOnline() && !onBatteryStart.IsZero() {
					fmt.Println("✅ Power restored!")
					onBatteryStart = time.Time{}
					shutdownTriggered = false
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
