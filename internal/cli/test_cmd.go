package cli

import (
"context"
"fmt"
"time"

"github.com/Guilhem-Bonnet/proxmox-guardian/internal/executor"
"github.com/Guilhem-Bonnet/proxmox-guardian/internal/orchestrator"
"github.com/Guilhem-Bonnet/proxmox-guardian/internal/proxmox"
"log/slog"

	"github.com/Guilhem-Bonnet/proxmox-guardian/internal/ups"
	"github.com/spf13/cobra"
)

var (
dryRun     bool
testPhase  int
testAction int
)

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Test shutdown sequence",
	Long: `Test the shutdown sequence without waiting for UPS events.
Use --dry-run to simulate without executing actions.
Use --phase and --action to test specific parts.`,
}

var testConnectionCmd = &cobra.Command{
	Use:   "connection",
	Short: "Test connections to NUT, Proxmox, and SSH hosts",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		ctx := context.Background()
		hasError := false

		// Test NUT connection
		fmt.Println("🔌 Testing NUT connection...")
		nutClient := ups.NewClient(cfg.UPS.Host+":3493", cfg.UPS.Name)
		if err := nutClient.Connect(); err != nil {
			fmt.Printf("   ❌ NUT: Failed - %v\n", err)
			hasError = true
		} else {
			status, err := nutClient.GetStatus(ctx)
			if err != nil {
				fmt.Printf("   ❌ NUT: Connected but status failed - %v\n", err)
				hasError = true
			} else {
				fmt.Printf("   ✅ NUT: OK - Battery %d%%, Status: %s\n", status.BatteryCharge, status.Status)
			}
			nutClient.Close()
		}

		// Test Proxmox connection
		fmt.Println("\n🖥️  Testing Proxmox API connection...")
		pxClient, err := proxmox.NewClient(proxmox.Config{
APIURL:      cfg.Proxmox.APIURL,
TokenID:     cfg.Proxmox.TokenID,
TokenSecret: cfg.Proxmox.TokenSecret,
InsecureTLS: cfg.Proxmox.InsecureTLS,
})
		if err != nil {
			fmt.Printf("   ❌ Proxmox: Failed to create client - %v\n", err)
			hasError = true
		} else {
			version, err := pxClient.GetVersion(ctx)
			if err != nil {
				fmt.Printf("   ❌ Proxmox: Failed to connect - %v\n", err)
				hasError = true
			} else {
				fmt.Printf("   ✅ Proxmox: OK - Version %s\n", version)

				// List guests
				guests, err := pxClient.GetAllGuests(ctx)
				if err != nil {
					fmt.Printf("   ⚠️  Proxmox: Cannot list guests - %v\n", err)
				} else {
					fmt.Printf("   📋 Found %d guests\n", len(guests))
					for _, g := range guests {
						status := "🔴"
						if g.Status == "running" {
							status = "🟢"
						}
						fmt.Printf("      %s %s:%d - %s (%s)\n", status, g.Type, g.VMID, g.Name, g.Status)
					}
				}
			}
		}

		// Test SSH connections
		fmt.Println("\n🔑 Testing SSH connections...")
		sshHosts := make(map[string]bool)
		for _, phase := range cfg.Phases {
			for _, action := range phase.Actions {
				if action.Type == "ssh" && action.Host != "" {
					sshHosts[action.Host] = true
				}
			}
		}

		for host := range sshHosts {
			exec := executor.NewSSHExecutor(host, "root", "echo 'SSH test OK'")
			exec.Timeout = 10 * time.Second
			result, err := exec.Execute(ctx)
			if err != nil || !result.Success {
				errMsg := ""
				if err != nil {
					errMsg = err.Error()
				} else {
					errMsg = result.Error
				}
				fmt.Printf("   ❌ SSH %s: Failed - %s\n", host, errMsg)
				hasError = true
			} else {
				fmt.Printf("   ✅ SSH %s: OK\n", host)
			}
		}

		fmt.Println()
		if hasError {
			return fmt.Errorf("some connection tests failed")
		}
		fmt.Println("✅ All connection tests passed!")
		return nil
	},
}

var testShutdownCmd = &cobra.Command{
	Use:   "shutdown",
	Short: "Test full shutdown sequence",
	Long: `Execute the shutdown sequence for testing.
Use --dry-run to simulate without executing.
Use --phase=N to test only phase N (1-based).
Use --action=N to test only action N within the phase.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		if dryRun {
			fmt.Println("🧪 DRY-RUN MODE - No actions will be executed")
		} else {
			fmt.Println("⚠️  LIVE MODE - Actions WILL be executed!")
			fmt.Println("    Press Ctrl+C within 5 seconds to cancel...")
			time.Sleep(5 * time.Second)
		}

		ctx := context.Background()

		// Create Proxmox client
		pxClient, err := proxmox.NewClient(proxmox.Config{
APIURL:      cfg.Proxmox.APIURL,
TokenID:     cfg.Proxmox.TokenID,
TokenSecret: cfg.Proxmox.TokenSecret,
InsecureTLS: cfg.Proxmox.InsecureTLS,
})
		if err != nil {
			return fmt.Errorf("failed to create Proxmox client: %w", err)
		}

		// Filter phases if specified
		phasesToTest := cfg.Phases
		if testPhase > 0 {
			if testPhase > len(cfg.Phases) {
				return fmt.Errorf("phase %d does not exist (max: %d)", testPhase, len(cfg.Phases))
			}
			phasesToTest = []Phase{cfg.Phases[testPhase-1]}
			fmt.Printf("📋 Testing only phase %d: %s\n", testPhase, phasesToTest[0].Name)
		}

		for i, cfgPhase := range phasesToTest {
			phaseNum := i + 1
			if testPhase > 0 {
				phaseNum = testPhase
			}

			fmt.Printf("\n━━━ Phase %d: %s ━━━\n", phaseNum, cfgPhase.Name)

			actionsToTest := cfgPhase.Actions
			if testAction > 0 {
				if testAction > len(cfgPhase.Actions) {
					return fmt.Errorf("action %d does not exist in phase (max: %d)", testAction, len(cfgPhase.Actions))
				}
				actionsToTest = []Action{cfgPhase.Actions[testAction-1]}
				fmt.Printf("    Testing only action %d\n", testAction)
			}

			for j, cfgAction := range actionsToTest {
				actionNum := j + 1
				if testAction > 0 {
					actionNum = testAction
				}

				desc := describeAction(cfgAction)
				fmt.Printf("  [%d.%d] %s\n", phaseNum, actionNum, desc)

				if dryRun {
					fmt.Printf("        ⏭️  SKIPPED (dry-run)\n")
					continue
				}

				// Execute action
				exec, err := createExecutor(cfg, cfgAction, pxClient)
				if err != nil {
					fmt.Printf("        ❌ FAILED to create executor: %v\n", err)
					continue
				}

				start := time.Now()
				result, err := exec.Execute(ctx)
				duration := time.Since(start)

				if err != nil || !result.Success {
					errMsg := ""
					if err != nil {
						errMsg = err.Error()
					} else {
						errMsg = result.Error
					}
					fmt.Printf("        ❌ FAILED (%s): %s\n", duration.Round(time.Millisecond), errMsg)
				} else {
					fmt.Printf("        ✅ SUCCESS (%s)\n", duration.Round(time.Millisecond))
					if result.Output != "" {
						fmt.Printf("        📝 Output: %s\n", truncate(result.Output, 100))
					}
				}
			}
		}

		fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		if dryRun {
			fmt.Println("✅ Dry-run completed - no actions were executed")
		} else {
			fmt.Println("✅ Test completed")
		}

		return nil
	},
}

var testRecoveryCmd = &cobra.Command{
	Use:   "recovery",
	Short: "Test recovery sequence (restart services)",
	Long: `Execute recovery commands to restart services.
Use --dry-run to simulate without executing.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		if dryRun {
			fmt.Println("🧪 DRY-RUN MODE - No actions will be executed")
		} else {
			fmt.Println("⚠️  LIVE MODE - Recovery actions WILL be executed!")
			fmt.Println("    Press Ctrl+C within 5 seconds to cancel...")
			time.Sleep(5 * time.Second)
		}

		ctx := context.Background()

		// Create Proxmox client
		pxClient, err := proxmox.NewClient(proxmox.Config{
APIURL:      cfg.Proxmox.APIURL,
TokenID:     cfg.Proxmox.TokenID,
TokenSecret: cfg.Proxmox.TokenSecret,
InsecureTLS: cfg.Proxmox.InsecureTLS,
})
		if err != nil {
			return fmt.Errorf("failed to create Proxmox client: %w", err)
		}

		// Build phases and execute recovery
		phases, err := buildPhasesFromConfig(cfg, pxClient)
		if err != nil {
			return fmt.Errorf("failed to build phases: %w", err)
		}

		logger := &slogLogger{slog.Default()}
		orch := orchestrator.NewOrchestrator(phases, cfg.Options.StateFile, logger, &noopNotifier{})

		if dryRun {
			fmt.Println("\n📋 Recovery commands that would be executed:")
			for _, phase := range cfg.Phases {
				for _, action := range phase.Actions {
					if action.Recovery != "" {
						fmt.Printf("  - [%s] %s\n", action.Type, action.Recovery)
					}
				}
			}
			fmt.Println("\n✅ Dry-run completed")
			return nil
		}

		fmt.Println("\n🔄 Executing recovery...")
		if err := orch.Recover(ctx); err != nil {
			return fmt.Errorf("recovery failed: %w", err)
		}

		fmt.Println("✅ Recovery completed")
		return nil
	},
}

func describeAction(action Action) string {
	switch action.Type {
	case "ssh":
		return fmt.Sprintf("[SSH] %s@%s: %s", action.User, action.Host, truncate(action.Command, 40))
	case "local":
		return fmt.Sprintf("[Local] %s", truncate(action.Command, 50))
	case "proxmox-guest":
		desc := fmt.Sprintf("[Proxmox] %s", action.Action)
		if action.Selector != nil {
			if len(action.Selector.VMIDRange) > 0 {
				desc += fmt.Sprintf(" vmid=%v", action.Selector.VMIDRange)
			}
			if action.Selector.Type != "" {
				desc += fmt.Sprintf(" type=%s", action.Selector.Type)
			}
		}
		return desc
	default:
		return fmt.Sprintf("[%s] %s", action.Type, action.Command)
	}
}

func init() {
	testCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "Simulate without executing actions")
	testShutdownCmd.Flags().IntVar(&testPhase, "phase", 0, "Test only this phase (1-based)")
	testShutdownCmd.Flags().IntVar(&testAction, "action", 0, "Test only this action within the phase (1-based)")

	testCmd.AddCommand(testConnectionCmd)
	testCmd.AddCommand(testShutdownCmd)
	testCmd.AddCommand(testRecoveryCmd)

	rootCmd.AddCommand(testCmd)
}
