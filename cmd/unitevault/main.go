package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"fyne.io/systray"
	"github.com/kh813/unitevault/internal/bootstrap"
	"github.com/kh813/unitevault/internal/config"
	"github.com/kh813/unitevault/internal/drive"
	"github.com/kh813/unitevault/internal/engine"
	"github.com/kh813/unitevault/internal/gui"
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

//go:embed assets/tray/icon.png
var trayIconPNG []byte

//go:embed assets/tray/icon.ico
var trayIconICO []byte

func onTrayReady() {
	if runtime.GOOS == "windows" && len(trayIconICO) > 0 {
		systray.SetIcon(trayIconICO)
	} else if len(trayIconPNG) > 0 {
		systray.SetIcon(trayIconPNG)
	} else {
		systray.SetTitle("UniteVault")
	}
	systray.SetTooltip("UniteVault - Obsidian Sync & Backup")

	mStatus := systray.AddMenuItem("Status: Idle", "Current status")
	mStatus.Disable()

	systray.AddSeparator()
	mSyncNow := systray.AddMenuItem("Sync Now", "Trigger manual sync cycle")
	mSettings := systray.AddMenuItem("Settings...", "Open configuration settings")
	mResetConfig := systray.AddMenuItem("Reset Configuration", "Reset settings to uninitialized state")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit UniteVault", "Quit application")

	go func() {
		if !ensurePreflightChecks() {
			mStatus.SetTitle("Status: Preflight Check Failed")
			return
		}

		cfgMgr, err := config.NewConfigManager()
		if err != nil {
			mStatus.SetTitle("Status: Config Error")
			return
		}

		ctx, cancel := context.WithCancel(context.Background())

		cfg, err := cfgMgr.LoadConfig()
		if err != nil || cfg.VaultPath == "" {
			mStatus.SetTitle("Status: Not Initialized")
			// Prompt user for settings automatically on startup if uninitialized
			openSettingsGUI(cfgMgr)
		} else {
			role, _ := cfgMgr.LoadRole()
			if role != "" {
				mStatus.SetTitle(fmt.Sprintf("Status: Active (%s)", role))
			} else {
				mStatus.SetTitle("Status: Active")
			}
			startDaemonLoop(ctx, cfgMgr, cfg, mStatus)
		}

		for {
			select {
			case <-mSyncNow.ClickedCh:
				c, err := cfgMgr.LoadConfig()
				if err == nil && c.VaultPath != "" {
					mStatus.SetTitle("Status: Syncing...")
					hostname, _ := os.Hostname()
					eng := engine.NewSyncEngine(cfgMgr, c.VaultPath, hostname, nil)
					err := eng.RunCycle(ctx)
					role, _ := cfgMgr.LoadRole()
					if err != nil {
						mStatus.SetTitle(fmt.Sprintf("Status: Error (%v)", err))
					} else {
						mStatus.SetTitle(fmt.Sprintf("Status: Active (%s) - %s", role, time.Now().Format("15:04")))
					}
				} else {
					mStatus.SetTitle("Status: Not Initialized")
					openSettingsGUI(cfgMgr)
				}
			case <-mSettings.ClickedCh:
				openSettingsGUI(cfgMgr)
			case <-mResetConfig.ClickedCh:
				if confirmDialog("Reset Configuration", "Are you sure you want to reset UniteVault configuration? This will uninitialize this device.") {
					_ = cfgMgr.ResetConfig()
					mStatus.SetTitle("Status: Not Initialized")
					openSettingsGUI(cfgMgr)
				}
			case <-mQuit.ClickedCh:
				cancel()
				systray.Quit()
				return
			}
		}
	}()
}

func startDaemonLoop(ctx context.Context, cfgMgr *config.ConfigManager, cfg *config.Config, mStatus *systray.MenuItem) {
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

func ensurePreflightChecks() bool {
	// 1. Git check & auto-install
	if !bootstrap.CheckGitInstalled() {
		msg := "Git is required for 3-way merge conflict resolution in UniteVault, but was not found on your system.\n\nWould you like UniteVault to automatically install Git now?"
		if confirmDialog("Git Install Required", msg) {
			if err := bootstrap.AutoInstallGit(); err == nil && bootstrap.CheckGitInstalled() {
				gui.PromptMessage("Git Installed", "Git was successfully installed!")
			} else {
				if runtime.GOOS == "darwin" {
					gui.PromptMessage("Git Installing", "Git installation process triggered (Command Line Developer Tools / Homebrew).\nPlease complete the installation on screen.")
				} else {
					gui.PromptMessage("Git Installing", "Git installer launched / in progress. Please complete the installation window.")
				}
			}
		} else {
			if confirmDialog("Git Download Page", "Would you like to open the official Git download page in your browser?") {
				_ = bootstrap.OpenURL(bootstrap.GetGitDownloadURL())
			}
			return false
		}
	}

	// 2. rclone check & auto-download
	if !bootstrap.CheckRcloneInstalled() {
		msg := "rclone is required for Google Drive sync/backup, but was not found on your system.\n\nWould you like UniteVault to automatically download rclone now?"
		if confirmDialog("rclone Download Required", msg) {
			targetPath, err := drive.GetDefaultRcloneTargetPath()
			if err == nil {
				if err := drive.EnsureRcloneBinary(targetPath); err == nil {
					gui.PromptMessage("rclone Installed", fmt.Sprintf("rclone was successfully downloaded and installed to:\n%s", targetPath))
					return true
				}
			}
			gui.PromptMessage("rclone Download Failed", "Failed to auto-download rclone.\n\nOpening official download page in browser...")
			_ = bootstrap.OpenURL(bootstrap.GetRcloneDownloadURL())
			return false
		} else {
			if confirmDialog("rclone Download Page", "Would you like to open the official rclone download page in your browser?") {
				_ = bootstrap.OpenURL(bootstrap.GetRcloneDownloadURL())
			}
			return false
		}
	}

	return true
}

func openSettingsGUI(cfgMgr *config.ConfigManager) {
	if !ensurePreflightChecks() {
		return
	}

	cfg, _ := cfgMgr.LoadConfig()
	role, _ := cfgMgr.LoadRole()
	if role == "" {
		role = "Not Initialized"
	}

	client := drive.NewClient("")
	gitStatus := "Not Found"
	if bootstrap.CheckGitInstalled() {
		gitStatus = "Installed"
	}

	rcloneStatus := "Not Found"
	if bootstrap.CheckRcloneInstalled() {
		rcloneStatus = "Installed"
	}

	currentData := gui.SettingsFormData{
		GitStatus:        gitStatus,
		RcloneStatus:     rcloneStatus,
		DeviceRole:       role,
		VaultPath:        "",
		RcloneRemote:     "gdrive",
		RclonePath:       "VaultBackup",
		IntervalSeconds:  120,
		RcloneExecPath:   client.GetBinaryPath(),
		RcloneRemoteInfo: "OK",
	}

	if cfg != nil {
		currentData.VaultPath = cfg.VaultPath
		if cfg.RcloneRemote != "" {
			currentData.RcloneRemote = cfg.RcloneRemote
		}
		if cfg.RclonePath != "" {
			currentData.RclonePath = cfg.RclonePath
		}
		if cfg.IntervalSeconds > 0 {
			currentData.IntervalSeconds = cfg.IntervalSeconds
		}
	}

	if !client.IsRemoteConfigured(context.Background(), currentData.RcloneRemote) {
		currentData.RcloneRemoteInfo = fmt.Sprintf("⚠️ Remote '%s' NOT configured in rclone yet", currentData.RcloneRemote)
	} else {
		currentData.RcloneRemoteInfo = fmt.Sprintf("OK (Configured: %s)", currentData.RcloneRemote)
	}

	// Windows iCloud Drive Notice for new setups
	if runtime.GOOS == "windows" && (cfg == nil || cfg.VaultPath == "") {
		icloudMsg := "Notice for Windows Users:\n\nIf you plan to sync this Vault with an iPhone (iOS), 'iCloud for Windows' must be installed and your Vault folder should be stored inside your iCloud Drive folder.\n\nWould you like to open the 'iCloud for Windows' download page?"
		if confirmDialog("iPhone / iCloud Drive Setup Notice", icloudMsg) {
			_ = bootstrap.OpenURL(bootstrap.GetICloudDownloadURL())
		}
	}

	updated, ok := gui.PromptSettingsWindow("UniteVault Settings", currentData)
	if !ok || updated.VaultPath == "" {
		return
	}

	driveClient := drive.NewClient("")

	// Check if rclone remote is configured
	if !driveClient.IsRemoteConfigured(context.Background(), updated.RcloneRemote) {
		warnMsg := fmt.Sprintf("Notice: rclone remote '%s' is not configured on this computer yet.\n\nTo backup to Google Drive, please run 'rclone config' in your terminal or set up OAuth.\n\nWould you like to open the rclone Google Drive setup guide in your browser?", updated.RcloneRemote)
		if confirmDialog("rclone Remote Setup Notice", warnMsg) {
			_ = bootstrap.OpenURL(bootstrap.GetRcloneDriveGuideURL())
		}
	}

	newCfg := &config.Config{
		VaultPath:       updated.VaultPath,
		RcloneRemote:    updated.RcloneRemote,
		RclonePath:      updated.RclonePath,
		IntervalSeconds: updated.IntervalSeconds,
	}
	_ = cfgMgr.SaveConfig(newCfg)

	// Initialize node
	hostname, _ := os.Hostname()
	bootstrapper := bootstrap.NewBootstrapper(cfgMgr, driveClient)
	remoteTarget := fmt.Sprintf("%s:%s", updated.RcloneRemote, updated.RclonePath)
	newRole, _ := bootstrapper.InitializeNode(context.Background(), updated.VaultPath, remoteTarget, hostname)

	gui.PromptMessage("UniteVault Configured", fmt.Sprintf("UniteVault settings saved successfully!\n\nVault: %s\nRemote Target: %s\nSync Interval: %d seconds\nRole: %s", updated.VaultPath, remoteTarget, updated.IntervalSeconds, newRole))
}

func confirmDialog(title, message string) bool {
	return gui.PromptConfirm(title, message)
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

	if !bootstrap.CheckGitInstalled() {
		fmt.Printf("Error: Git is required for 3-way merge conflict resolution, but was not found in PATH.\nPlease download and install Git from: %s\n", bootstrap.GetGitDownloadURL())
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

	if !bootstrap.CheckGitInstalled() {
		fmt.Printf("Error: Git is required for 3-way merge conflict resolution, but was not found in PATH.\nPlease download and install Git from: %s\n", bootstrap.GetGitDownloadURL())
		os.Exit(1)
	}

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
