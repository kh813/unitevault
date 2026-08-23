package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fyne.io/systray"
	"github.com/kh813/unitevault/internal/bootstrap"
	"github.com/kh813/unitevault/internal/config"
	"github.com/kh813/unitevault/internal/drive"
	"github.com/kh813/unitevault/internal/engine"
)

func main() {
	if len(os.Args) < 2 {
		// If launched without args (e.g. clicking macOS .app bundle), run in tray/GUI mode
		runTrayMode()
		return
	}

	subcommand := os.Args[1]
	switch subcommand {
	case "init":
		handleInit(os.Args[2:])
	case "run":
		handleRun(os.Args[2:])
	case "status":
		handleStatus(os.Args[2:])
	case "promote":
		handlePromote(os.Args[2:])
	case "gui", "tray":
		runTrayMode()
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Printf("Unknown subcommand: %s\n\n", subcommand)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("UniteVault - Personal Obsidian Vault Sync & Mirroring System")
	fmt.Println("\nUsage:")
	fmt.Println("  unitevault <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  gui      Run in Menu Bar / System Tray GUI mode")
	fmt.Println("  init     Initialize local configuration and node role")
	fmt.Println("  run      Run background sync process (defaults to resident daemon mode)")
	fmt.Println("             Options: --once (run single cycle and exit)")
	fmt.Println("  status   Display current node ID, role, and configuration")
	fmt.Println("  promote  Promote current node to Primary node manually")
}

func runTrayMode() {
	systray.Run(onTrayReady, onTrayExit)
}

func onTrayReady() {
	systray.SetTitle("UniteVault")
	systray.SetTooltip("UniteVault - Obsidian Sync & Backup")

	mStatus := systray.AddMenuItem("Status: Idle", "Current status")
	mStatus.Disable()

	systray.AddSeparator()
	mSyncNow := systray.AddMenuItem("Sync Now", "Trigger manual sync cycle")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit UniteVault", "Quit application")

	cfgMgr, err := config.NewConfigManager()
	if err != nil {
		mStatus.SetTitle("Status: Config Error")
		return
	}

	cfg, err := cfgMgr.LoadConfig()
	if err != nil || cfg.VaultPath == "" {
		mStatus.SetTitle("Status: Not Initialized (Run 'unitevault init')")
	} else {
		role, _ := cfgMgr.LoadRole()
		if role != "" {
			mStatus.SetTitle(fmt.Sprintf("Status: Active (%s)", role))
		} else {
			mStatus.SetTitle("Status: Active")
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Start daemon loop in goroutine if configured
	if cfg != nil && cfg.VaultPath != "" {
		hostname, _ := os.Hostname()
		eng := engine.NewSyncEngine(cfgMgr, cfg.VaultPath, hostname, nil)
		go func() {
			interval := cfg.IntervalSeconds
			if interval <= 0 {
				interval = 120
			}
			ticker := time.NewTicker(time.Duration(interval) * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					mStatus.SetTitle("Status: Syncing...")
					_ = eng.RunCycle(ctx)
					role, _ := cfgMgr.LoadRole()
					mStatus.SetTitle(fmt.Sprintf("Status: Active (%s) - %s", role, time.Now().Format("15:04")))
				}
			}
		}()
	}

	go func() {
		for {
			select {
			case <-mSyncNow.ClickedCh:
				if cfg != nil && cfg.VaultPath != "" {
					mStatus.SetTitle("Status: Syncing...")
					hostname, _ := os.Hostname()
					eng := engine.NewSyncEngine(cfgMgr, cfg.VaultPath, hostname, nil)
					err := eng.RunCycle(ctx)
					role, _ := cfgMgr.LoadRole()
					if err != nil {
						mStatus.SetTitle(fmt.Sprintf("Status: Error (%v)", err))
					} else {
						mStatus.SetTitle(fmt.Sprintf("Status: Active (%s) - %s", role, time.Now().Format("15:04")))
					}
				} else {
					mStatus.SetTitle("Status: Not Initialized")
				}
			case <-mQuit.ClickedCh:
				cancel()
				systray.Quit()
				return
			}
		}
	}()
}

func onTrayExit() {
	// Cleanup on exit
}

func handleInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	vaultPath := fs.String("vault", "", "Path to Obsidian Vault directory")
	remoteName := fs.String("remote", "gdrive", "rclone remote name")
	remotePath := fs.String("remote-path", "VaultBackup", "rclone remote backup target folder path")
	label := fs.String("label", "", "Device label name (default: hostname)")
	_ = fs.Parse(args)

	if *vaultPath == "" {
		fmt.Println("Error: -vault flag is required")
		fs.Usage()
		os.Exit(1)
	}

	hostname, _ := os.Hostname()
	if *label == "" {
		*label = hostname
	}

	cfgMgr, err := config.NewConfigManager()
	if err != nil {
		fmt.Printf("Error initializing config manager: %v\n", err)
		os.Exit(1)
	}

	cfg := &config.Config{
		VaultPath:       *vaultPath,
		RcloneRemote:    *remoteName,
		RclonePath:      *remotePath,
		IntervalSeconds: 120,
	}

	if err := cfgMgr.SaveConfig(cfg); err != nil {
		fmt.Printf("Error saving config: %v\n", err)
		os.Exit(1)
	}

	devID, err := cfgMgr.GetDeviceID()
	if err != nil {
		fmt.Printf("Error getting device ID: %v\n", err)
		os.Exit(1)
	}

	client := drive.NewClient("")
	bootstrapper := bootstrap.NewBootstrapper(cfgMgr, client)
	remoteTarget := fmt.Sprintf("%s:%s", *remoteName, *remotePath)

	ctx := context.Background()
	role, err := bootstrapper.InitializeNode(ctx, *vaultPath, remoteTarget, *label)
	if err != nil {
		fmt.Printf("Initialization error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully initialized UniteVault!\n")
	fmt.Printf("  Device ID: %s\n", devID)
	fmt.Printf("  Label:     %s\n", *label)
	fmt.Printf("  Role:      %s\n", role)
	fmt.Printf("  Vault:     %s\n", *vaultPath)
	fmt.Printf("  Target:    %s\n", remoteTarget)
}

func handleRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	once := fs.Bool("once", false, "Run a single sync cycle and exit")
	_ = fs.Parse(args)

	cfgMgr, err := config.NewConfigManager()
	if err != nil {
		fmt.Printf("Config error: %v\n", err)
		os.Exit(1)
	}

	cfg, err := cfgMgr.LoadConfig()
	if err != nil || cfg.VaultPath == "" {
		fmt.Println("Error: Config not found or invalid. Please run 'unitevault init' first.")
		os.Exit(1)
	}

	hostname, _ := os.Hostname()
	eng := engine.NewSyncEngine(cfgMgr, cfg.VaultPath, hostname, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	if *once {
		if err := eng.RunCycle(ctx); err != nil {
			fmt.Printf("Sync error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Single sync cycle completed successfully.")
		return
	}

	if err := eng.RunDaemon(ctx, cfg.IntervalSeconds); err != nil && err != context.Canceled {
		fmt.Printf("Daemon error: %v\n", err)
		os.Exit(1)
	}
}

func handleStatus(args []string) {
	cfgMgr, err := config.NewConfigManager()
	if err != nil {
		fmt.Printf("Config error: %v\n", err)
		os.Exit(1)
	}

	devID, _ := cfgMgr.GetDeviceID()
	role, _ := cfgMgr.LoadRole()
	cfg, _ := cfgMgr.LoadConfig()
	dir, _ := config.GetConfigDir()

	fmt.Println("UniteVault Status:")
	fmt.Printf("  Config Dir:  %s\n", dir)
	fmt.Printf("  Device ID:   %s\n", devID)
	fmt.Printf("  Role:        %s\n", role)
	if cfg != nil {
		fmt.Printf("  Vault Path:  %s\n", cfg.VaultPath)
		fmt.Printf("  Remote:      %s:%s\n", cfg.RcloneRemote, cfg.RclonePath)
	}
}

func handlePromote(args []string) {
	cfgMgr, err := config.NewConfigManager()
	if err != nil {
		fmt.Printf("Config error: %v\n", err)
		os.Exit(1)
	}

	cfg, err := cfgMgr.LoadConfig()
	if err != nil || cfg.VaultPath == "" {
		fmt.Println("Error: Config not found. Please run 'unitevault init' first.")
		os.Exit(1)
	}

	hostname, _ := os.Hostname()
	client := drive.NewClient("")
	bootstrapper := bootstrap.NewBootstrapper(cfgMgr, client)
	remoteTarget := fmt.Sprintf("%s:%s", cfg.RcloneRemote, cfg.RclonePath)

	ctx := context.Background()
	if err := bootstrapper.PromoteToPrimary(ctx, cfg.VaultPath, remoteTarget, hostname); err != nil {
		fmt.Printf("Promote error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Successfully promoted current node to Primary!")
}
