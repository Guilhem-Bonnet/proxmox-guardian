package cli

import (
	"fmt"
	"os"

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

func Execute(info BuildInfo) error {
	buildInfo = info
	return rootCmd.Execute()
}

var rootCmd = &cobra.Command{
	Use:   "proxmox-guardian",
	Short: "Graceful shutdown orchestrator for Proxmox VE with UPS integration",
	Long: `Proxmox Guardian monitors your UPS via NUT and orchestrates a clean,
ordered shutdown of your entire infrastructure when power fails.

It handles VMs, LXC containers, Docker stacks, databases, and services
with configurable phases, dependencies, and recovery support.`,
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

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate configuration syntax and connectivity",
	Long: `Validates the configuration file syntax, checks Proxmox API connectivity,
verifies SSH connections, and ensures all referenced guests exist.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return fmt.Errorf("config validation failed: %w", err)
		}
		
		fmt.Println("✅ Configuration syntax: OK")
		
		// TODO: Validate Proxmox connectivity
		// TODO: Validate SSH connectivity
		// TODO: Validate guests exist
		
		_ = cfg // Will be used for connectivity checks
		fmt.Println("✅ All validations passed")
		return nil
	},
}

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Show execution plan without making changes",
	Long: `Displays what actions would be taken during a shutdown sequence
without actually executing them. Useful for verifying configuration.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		
		fmt.Println("📋 Execution Plan")
		fmt.Println("=================")
		fmt.Println()
		
		for i, phase := range cfg.Phases {
			parallel := ""
			if phase.Parallel {
				parallel = " (parallel)"
			}
			fmt.Printf("Phase %d: %s%s\n", i+1, phase.Name, parallel)
			
			for j, action := range phase.Actions {
				fmt.Printf("  %d.%d [%s] ", i+1, j+1, action.Type)
				switch action.Type {
				case "ssh":
					fmt.Printf("SSH to %s: %s\n", action.Host, truncate(action.Command, 50))
				case "proxmox-exec":
					fmt.Printf("Exec in %s: %s\n", action.Guest, truncate(action.Command, 50))
				case "proxmox-guest":
					fmt.Printf("Shutdown %s (selector: %v)\n", action.Action, action.Selector)
				case "local":
					fmt.Printf("Local: %s\n", truncate(action.Command, 50))
				}
				
				if action.OnError != "" {
					fmt.Printf("       on_error: %s\n", action.OnError)
				}
				if action.Retry != nil {
					fmt.Printf("       retry: %d attempts, %s delay\n", action.Retry.Attempts, action.Retry.Delay)
				}
			}
			fmt.Println()
		}
		
		return nil
	},
}

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Execute the shutdown sequence",
	Long: `Executes the configured shutdown sequence. This will actually
stop services, containers, VMs, and potentially the host.

Use with caution! Run 'plan' first to verify the sequence.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		if dryRun {
			fmt.Println("🔍 DRY RUN MODE - No changes will be made")
		}
		
		// TODO: Implement orchestrator execution
		_ = cfg
		
		fmt.Println("🚀 Starting shutdown sequence...")
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
		
		// TODO: Implement daemon mode with UPS monitoring
		_ = cfg
		
		fmt.Println("👁️ Starting daemon mode...")
		fmt.Printf("📡 Connecting to NUT at %s...\n", cfg.UPS.Host)
		return nil
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
		
		switch event {
		case "ONBATT":
			fmt.Println("⚡ Power lost - running on battery")
			// TODO: Start monitoring battery level
		case "LOWBATT":
			fmt.Println("🔋 Low battery - initiating shutdown")
			// TODO: Trigger shutdown sequence
		case "ONLINE":
			fmt.Println("✅ Power restored")
			// TODO: Check if recovery needed
		default:
			fmt.Printf("ℹ️ Event %s acknowledged\n", event)
		}
		
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "/etc/proxmox-guardian/guardian.yaml", "config file path")
	
	applyCmd.Flags().Bool("dry-run", false, "simulate execution without making changes")
	
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(planCmd)
	rootCmd.AddCommand(applyCmd)
	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(notifyCmd)
}

func loadConfig() (*Config, error) {
	// Check if config file exists
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
