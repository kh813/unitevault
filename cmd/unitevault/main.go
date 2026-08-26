package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"github.com/kh813/unitevault/internal/bootstrap"
	"github.com/kh813/unitevault/internal/config"
	"github.com/kh813/unitevault/internal/drive"
	"github.com/kh813/unitevault/internal/engine"
	"github.com/kh813/unitevault/internal/gui"
	"github.com/kh813/unitevault/internal/selfupdate"
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

//go:embed assets/tray/icon@2x.png
var trayIconColorPNG []byte

//go:embed assets/tray/icon-mono.svg
var trayIconMonoSVG []byte

// trayApp bundles the long-lived state shared by the tray menu and the
// Settings window (spec 3.5.2/8.3), so callbacks don't need long parameter
// lists. All of its methods that touch the GUI either run directly on Fyne's
// main goroutine (menu/button callbacks) or explicitly hand off to a
// goroutine and marshal results back via the gui package helpers - see the
// package doc comment on internal/gui for the threading rules this follows.
type trayApp struct {
	ctx    context.Context
	cancel context.CancelFunc
	cfgMgr *config.ConfigManager
	menu   *fyne.Menu
	status *fyne.MenuItem

	icloudNoticeShown bool
}

func runTrayMode() {
	appIcon := fyne.NewStaticResource("unitevault-icon.png", trayIconColorPNG)
	gui.InitApp(appIcon)

	// macOS menu bars render either black-on-light or white-on-dark, so the
	// tray icon there uses a monochrome SVG glyph wrapped as a Fyne
	// ThemedResource - Fyne's desktop driver detects that type and hands it
	// to the OS as a native "template" image, which macOS then recolors
	// automatically to match the current menu bar appearance (an SVG, not a
	// PNG, is required here: ThemedResource always tries to parse its source
	// as SVG XML to recolor it, and only an actual SVG lets that succeed
	// instead of silently falling back with a logged warning). Windows/Linux
	// trays have no such mechanism, so they get the colored app icon
	// instead, matching how most tray apps look there.
	var trayIcon fyne.Resource = appIcon
	if runtime.GOOS == "darwin" {
		trayIcon = theme.NewThemedResource(fyne.NewStaticResource("unitevault-tray-mono.svg", trayIconMonoSVG))
	}

	// Route rclone's first-time-download progress (see drive.NewClient)
	// through a Fyne progress dialog instead of the default console logger.
	drive.ProgressFunc = gui.RunWithProgress

	cfgMgr, err := config.NewConfigManager()
	if err != nil {
		gui.Info("UniteVault Error", fmt.Sprintf("Failed to initialize local configuration: %v", err))
		gui.Run()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())

	mStatus := fyne.NewMenuItem("Status: Idle", nil)
	mStatus.Disabled = true
	mSyncNow := fyne.NewMenuItem("Sync Now", nil)
	mSettings := fyne.NewMenuItem("Settings...", nil)
	mCheckUpdate := fyne.NewMenuItem("Check for Update...", nil)
	mQuit := fyne.NewMenuItem("Quit UniteVault", nil)
	mQuit.IsQuit = true

	// Reset Configuration is intentionally only available inside the
	// Settings window (its own button there is gated by a confirm dialog),
	// not here - it's a rare, destructive action that doesn't belong one
	// click away in the everyday tray menu.
	menu := fyne.NewMenu("UniteVault",
		mStatus,
		fyne.NewMenuItemSeparator(),
		mSyncNow,
		mSettings,
		mCheckUpdate,
		fyne.NewMenuItemSeparator(),
		mQuit,
	)

	t := &trayApp{ctx: ctx, cancel: cancel, cfgMgr: cfgMgr, menu: menu, status: mStatus}

	mSyncNow.Action = func() { go t.syncNow() }
	mSettings.Action = func() { t.openSettingsGUI() }
	mCheckUpdate.Action = func() { go t.checkForUpdate() }
	mQuit.Action = func() {
		cancel()
		gui.Quit()
	}

	gui.SetTray(trayIcon, menu)

	go t.startup()

	gui.Run()
}

// startup loads local config and either opens Settings (first run / not yet
// configured) or starts the daemon sync loop. Runs on its own goroutine so it
// never blocks the Fyne event loop with I/O or the (potentially slow) initial
// sync cycle.
func (t *trayApp) startup() {
	cfg, err := t.cfgMgr.LoadConfig()
	if err != nil || cfg.VaultPath == "" {
		gui.SetMenuItemLabel(t.menu, t.status, "Status: Not Initialized")
		t.openSettingsGUI()
		t.maybeShowInstallReminder()
		return
	}

	role, _ := t.cfgMgr.LoadRole()
	if role != "" {
		gui.SetMenuItemLabel(t.menu, t.status, fmt.Sprintf("Status: Active (%s)", role))
	} else {
		gui.SetMenuItemLabel(t.menu, t.status, "Status: Active")
	}
	t.runDaemonLoop(cfg)
}

// maybeShowInstallReminder nags the user, once per app launch, about missing
// Git/rclone while the device hasn't finished initializing - the Status
// section alone is easy to miss since it only shows up once Settings is
// already open. Stays quiet once the user checks "Don't show this again", or
// once both tools are actually installed.
func (t *trayApp) maybeShowInstallReminder() {
	if t.cfgMgr.IsInstallReminderDismissed() {
		return
	}

	var missing []string
	if !bootstrap.CheckGitInstalled() {
		missing = append(missing, "Git")
	}
	if _, ok := drive.FindRcloneBinary(); !ok {
		missing = append(missing, "rclone")
	}
	if len(missing) == 0 {
		return
	}

	message := fmt.Sprintf(
		"UniteVault needs %s installed before it can sync your Vault.\n\nYou can install %s from the Status section of the Settings window.",
		strings.Join(missing, " and "),
		strings.Join(missing, " and "),
	)
	gui.InstallReminder("Setup Required", message, func(dontShowAgain bool) {
		if dontShowAgain {
			_ = t.cfgMgr.SetInstallReminderDismissed()
		}
	})
}

// runDaemonLoop runs the periodic sync cycle until t.ctx is cancelled (Quit).
func (t *trayApp) runDaemonLoop(cfg *config.Config) {
	hostname, _ := os.Hostname()
	eng := engine.NewSyncEngine(t.cfgMgr, cfg.VaultPath, hostname, nil)

	interval := cfg.IntervalSeconds
	if interval <= 0 {
		interval = 120
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-t.ctx.Done():
			return
		case <-ticker.C:
			gui.SetMenuItemLabel(t.menu, t.status, "Status: Syncing...")
			_ = eng.RunCycle(t.ctx)
			role, _ := t.cfgMgr.LoadRole()
			gui.SetMenuItemLabel(t.menu, t.status, fmt.Sprintf("Status: Active (%s) - %s", role, time.Now().Format("15:04")))
		}
	}
}

// syncNow handles the "Sync Now" tray menu action.
func (t *trayApp) syncNow() {
	cfg, err := t.cfgMgr.LoadConfig()
	if err != nil || cfg.VaultPath == "" {
		gui.SetMenuItemLabel(t.menu, t.status, "Status: Not Initialized")
		t.openSettingsGUI()
		return
	}

	gui.SetMenuItemLabel(t.menu, t.status, "Status: Syncing...")
	hostname, _ := os.Hostname()
	eng := engine.NewSyncEngine(t.cfgMgr, cfg.VaultPath, hostname, nil)
	err = eng.RunCycle(t.ctx)
	role, _ := t.cfgMgr.LoadRole()
	if err != nil {
		gui.SetMenuItemLabel(t.menu, t.status, fmt.Sprintf("Status: Error (%v)", err))
	} else {
		gui.SetMenuItemLabel(t.menu, t.status, fmt.Sprintf("Status: Active (%s) - %s", role, time.Now().Format("15:04")))
	}
}

// checkForUpdate handles the "Check for Update..." tray menu action:
// queries GitHub Releases, and if a newer version is available, offers to
// download and apply it (performSelfUpdate). Always safe to run regardless
// of init state - it doesn't touch Vault/config at all.
func (t *trayApp) checkForUpdate() {
	var info *selfupdate.ReleaseInfo
	err := gui.RunWithProgress(
		"Checking for Updates",
		"Contacting GitHub to check for a newer version...",
		func() error {
			var checkErr error
			info, checkErr = selfupdate.CheckLatest(context.Background())
			return checkErr
		},
	)
	if err != nil {
		gui.Info("Update Check Failed", fmt.Sprintf("Could not check for updates: %v", err))
		return
	}

	if !selfupdate.IsNewer(bootstrap.AppVersion, info.Version) {
		gui.Info("Up to Date", fmt.Sprintf("You're running the latest version (v%s).", bootstrap.AppVersion))
		return
	}

	if info.AssetURL == "" {
		gui.Confirm(
			"Update Available",
			fmt.Sprintf(
				"Version %s is available (you have v%s), but UniteVault couldn't find a matching automatic download for this platform.\n\nOpen the release page in your browser?",
				info.TagName, bootstrap.AppVersion,
			),
			func(confirmed bool) {
				if confirmed {
					_ = bootstrap.OpenURL(info.HTMLURL)
				}
			},
		)
		return
	}

	gui.Confirm(
		"Update Available",
		fmt.Sprintf(
			"Version %s is available (you have v%s).\n\nDownload and install it now? UniteVault will quit and restart automatically once the update is applied.",
			info.TagName, bootstrap.AppVersion,
		),
		func(confirmed bool) {
			if confirmed {
				go t.performSelfUpdate(info)
			}
		},
	)
}

// performSelfUpdate downloads and applies the update described by info, then
// quits so the detached helper process selfupdate.Apply started can finish
// replacing this app and relaunch it. If anything fails, nothing has been
// touched yet (Apply only hands off to the helper after a fully successful
// download+extract), so it's always safe to fall back to a manual download.
func (t *trayApp) performSelfUpdate(info *selfupdate.ReleaseInfo) {
	err := gui.RunWithProgress(
		"Updating UniteVault",
		fmt.Sprintf("Downloading version %s...", info.TagName),
		func() error { return selfupdate.Apply(context.Background(), info.AssetURL) },
	)
	if err != nil {
		gui.Info(
			"Update Failed",
			fmt.Sprintf(
				"Could not automatically update: %v\n\nYou can download the new version manually from:\n%s",
				err, info.HTMLURL,
			),
		)
		return
	}

	t.cancel()
	gui.Quit()
}

// performReset clears local config/role state and reopens Settings. Called
// as the Settings window's OnReset handler, whose Reset Configuration button
// is already gated by its own confirm dialog (buildSettingsContent) - Reset
// is deliberately not exposed anywhere in the tray menu itself.
func (t *trayApp) performReset() {
	_ = t.cfgMgr.ResetConfig()
	t.icloudNoticeShown = false
	gui.SetMenuItemLabel(t.menu, t.status, "Status: Not Initialized")
	t.openSettingsGUI()
}

func engineLogPath() string {
	dir, err := config.GetConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "engine.log")
}

// buildFormData gathers everything shown in the Settings window (spec
// 3.5.2/8.3): tool install status, device role, saved config, and rclone
// remote status. It never triggers a rclone/Git install by itself (uses
// bootstrap.CheckGitInstalled / drive.FindRcloneBinary, which only probe -
// they never download anything) so it's always safe to call directly on the
// Fyne main goroutine, e.g. right before showing the window.
func (t *trayApp) buildFormData() gui.SettingsFormData {
	cfg, _ := t.cfgMgr.LoadConfig()
	role, _ := t.cfgMgr.LoadRole()

	// engine.RunCycle only ever runs the rclone sync step (and records its
	// outcome) on a Primary device - a Secondary never attempts it, so
	// showing its (possibly stale, or simply absent) sync status would be
	// misleading rather than just showing why there isn't one.
	driveSyncStatus := "Never synced yet"
	if role == "" {
		driveSyncStatus = "N/A (not configured yet)"
		role = "N/A"
	} else if role == "secondary" {
		driveSyncStatus = "N/A (this device is Secondary - Google Drive backup runs on the Primary device)"
	} else if st, err := t.cfgMgr.LoadDriveSyncStatus(); err == nil && st != nil {
		displayTime := st.Time
		if ts, parseErr := time.Parse(time.RFC3339, st.Time); parseErr == nil {
			displayTime = ts.Local().Format("2006-01-02 15:04")
		}
		if st.Success {
			driveSyncStatus = fmt.Sprintf("Last synced: %s", displayTime)
		} else {
			driveSyncStatus = fmt.Sprintf("Last sync failed (%s): %s", displayTime, st.Error)
		}
	}

	gitStatus := "Not Found"
	if bootstrap.CheckGitInstalled() {
		gitStatus = "Installed"
	}

	rcloneStatus := "Not Found"
	rcloneExecPath := ""
	if p, ok := drive.FindRcloneBinary(); ok {
		rcloneStatus = "Installed"
		rcloneExecPath = p
	}

	// Only meaningful on Windows - iCloud ships with macOS/iOS, so there's
	// nothing separate to install/detect there (see SettingsFormData.ICloudStatus).
	icloudStatus := ""
	if runtime.GOOS == "windows" {
		icloudStatus = "Not Found"
		if bootstrap.CheckICloudInstalled() {
			icloudStatus = "Installed"
		} else if !bootstrap.IsAdministrator() {
			// Surface this here too, not just in the dialog "Install
			// iCloud..." shows after a failed attempt - so it's visible
			// without the user having to click the button at all first.
			icloudStatus = "Not Found (requires an administrator account to install)"
		}
	}

	data := gui.SettingsFormData{
		GitStatus:        gitStatus,
		RcloneStatus:     rcloneStatus,
		ICloudStatus:     icloudStatus,
		DriveSyncStatus:  driveSyncStatus,
		DeviceRole:       role,
		RcloneRemote:     "ObsidianVault",
		RclonePath:       "VaultBackup",
		IntervalSeconds:  120,
		RcloneExecPath:   rcloneExecPath,
		RcloneRemoteInfo: "rclone not installed yet",
	}

	if cfg != nil {
		data.VaultPath = cfg.VaultPath
		if cfg.RcloneRemote != "" {
			data.RcloneRemote = cfg.RcloneRemote
		}
		if cfg.RclonePath != "" {
			data.RclonePath = cfg.RclonePath
		}
		if cfg.IntervalSeconds > 0 {
			data.IntervalSeconds = cfg.IntervalSeconds
		}
	}

	// Only probe "rclone listremotes" if a binary is actually present -
	// avoids triggering drive.NewClient's auto-download side effect just to
	// render the window.
	if rcloneExecPath != "" {
		client := drive.NewClient(engineLogPath())
		if client.IsRemoteConfigured(context.Background(), data.RcloneRemote) {
			data.RcloneRemoteInfo = fmt.Sprintf("Configured (%s)", data.RcloneRemote)
		} else {
			data.RcloneRemoteInfo = fmt.Sprintf("Not configured - remote '%s' not found in rclone", data.RcloneRemote)
		}
	}

	return data
}

// openSettingsGUI (re)builds and shows the single-window Settings GUI (spec
// 3.5.2/8.3) from scratch (config.json + live Git/rclone probes). It always
// shows the window immediately regardless of Git/rclone install state - the
// Status section surfaces "Not Found" with inline install actions instead of
// blocking the window from appearing. Use this for the initial open; once
// the window is already open, use reopenSettingsGUI to refresh it without
// discarding whatever the user has typed but not saved yet.
func (t *trayApp) openSettingsGUI() {
	data := t.buildFormData()
	t.showSettingsGUI(data)

	// data.ICloudStatus == "Not Found" specifically (not just "starts with
	// Not Found") deliberately excludes the "requires an administrator
	// account" variant here: offering to install it right now and having
	// that dead-end into "ask your system administrator" on every single
	// startup would be a worse experience than just letting them discover
	// the requirement passively in Settings > Status when they get there.
	if runtime.GOOS == "windows" && !t.icloudNoticeShown && data.VaultPath == "" && data.ICloudStatus == "Not Found" {
		t.icloudNoticeShown = true
		gui.Confirm(
			"iPhone / iCloud Drive Setup Notice",
			"If you plan to sync this Vault with an iPhone (iOS), 'iCloud for Windows' must be installed and your Vault folder should be stored inside your iCloud Drive folder.\n\nInstall iCloud for Windows now? (You can also do this later from Settings > Status > Install iCloud...)",
			func(confirmed bool) {
				if confirmed {
					t.installICloud(data)
				}
			},
		)
	}
}

// reopenSettingsGUI refreshes the Settings window after a Status/rclone
// section action (install Git/rclone, configure remote) completes. It
// re-probes Git/rclone/role status like buildFormData, but keeps whatever
// the user currently has typed in the form (current) instead of overwriting
// it with what's saved on disk - Config/rclone section actions can complete
// before the user ever presses "Save Settings", and buildFormData alone
// would otherwise reset those fields back to disk/default values.
func (t *trayApp) reopenSettingsGUI(current gui.SettingsFormData) {
	data := t.buildFormData()
	data.VaultPath = current.VaultPath
	data.RcloneRemote = current.RcloneRemote
	data.RclonePath = current.RclonePath
	data.IntervalSeconds = current.IntervalSeconds

	if _, ok := drive.FindRcloneBinary(); ok {
		client := drive.NewClient(engineLogPath())
		if client.IsRemoteConfigured(context.Background(), data.RcloneRemote) {
			data.RcloneRemoteInfo = fmt.Sprintf("Configured (%s)", data.RcloneRemote)
		} else {
			data.RcloneRemoteInfo = fmt.Sprintf("Not configured - remote '%s' not found in rclone", data.RcloneRemote)
		}
	}

	t.showSettingsGUI(data)
}

func (t *trayApp) showSettingsGUI(data gui.SettingsFormData) {
	gui.ShowSettingsWindow(data, gui.SettingsHandlers{
		OnInstallGit:      t.installGit,
		OnInstallRclone:   t.installRclone,
		OnInstallICloud:   t.installICloud,
		OnConfigureRemote: t.configureRemote,
		OnRemoveRemote:    t.removeRemote,
		OnSave:            t.saveSettings,
		OnReset:           t.performReset,
	})
}

// installGit handles the Status section's "Install Git..." button.
func (t *trayApp) installGit(current gui.SettingsFormData) {
	go func() {
		_ = gui.RunWithProgress(
			"Installing Git",
			"Attempting to automatically install Git (Homebrew / Xcode Command Line Tools on macOS, winget / official installer on Windows)...\nThis may take a moment.",
			func() error { return bootstrap.AutoInstallGit() },
		)

		if bootstrap.CheckGitInstalled() {
			gui.Info("Git Installed", "Git was successfully installed!")
		} else {
			gui.Info(
				"Git Installation In Progress",
				"The Git installer was launched. Please complete the on-screen installation, then reopen this Status section to confirm.",
			)
		}
		t.reopenSettingsGUI(current)
	}()
}

// installRclone handles the Status section's "Install rclone..." button.
func (t *trayApp) installRclone(current gui.SettingsFormData) {
	go func() {
		targetPath, err := drive.GetDefaultRcloneTargetPath()
		if err != nil {
			gui.Info("rclone Install Failed", fmt.Sprintf("Could not determine an install location: %v", err))
			t.reopenSettingsGUI(current)
			return
		}

		installErr := gui.RunWithProgress(
			"Installing rclone",
			"Downloading the official rclone binary for your platform...",
			func() error { return drive.EnsureRcloneBinary(targetPath) },
		)

		if installErr == nil {
			gui.Info("rclone Installed", fmt.Sprintf("rclone was successfully installed to:\n%s", targetPath))
		} else {
			gui.Info(
				"rclone Install Failed",
				fmt.Sprintf("Automatic download failed: %v\n\nYou can download it manually from:\n%s", installErr, bootstrap.GetRcloneDownloadURL()),
			)
		}
		t.reopenSettingsGUI(current)
	}()
}

// installICloud handles the Status section's "Install iCloud..." button
// (Windows only - see SettingsFormData.ICloudStatus). Unlike Git/rclone,
// even a successful install leaves setup incomplete: signing in with an
// Apple ID and turning on iCloud Drive both need interactive input (often
// 2FA) that can't be automated, so AutoInstallICloud best-effort launches
// the app afterward to land the user on that step directly.
func (t *trayApp) installICloud(current gui.SettingsFormData) {
	go func() {
		installErr := gui.RunWithProgress(
			"Installing iCloud",
			"Attempting to automatically install iCloud for Windows (winget)...\nThis may take a moment.",
			func() error { return bootstrap.AutoInstallICloud() },
		)

		switch {
		case installErr == nil:
			gui.Info(
				"iCloud Installed",
				"iCloud for Windows was successfully installed and launched.\n\nPlease sign in with your Apple ID and turn on iCloud Drive, then return here to select your Vault folder.",
			)
		case errors.Is(installErr, bootstrap.ErrICloudRequiresAdministrator):
			gui.Info(
				"Administrator Privileges Required",
				fmt.Sprintf("%v.\n\niCloud for Windows registers system-level components (Explorer integration, Outlook/Photos add-ins, registry entries) that only an administrator account can install, even through the Microsoft Store.\n\nPlease ask your system administrator to install it, or download it yourself from:\n%s", installErr, bootstrap.GetICloudDownloadURL()),
			)
		default:
			gui.Info(
				"iCloud Install Failed",
				fmt.Sprintf("Automatic installation failed: %v\n\nYou can download it manually from:\n%s", installErr, bootstrap.GetICloudDownloadURL()),
			)
		}
		t.reopenSettingsGUI(current)
	}()
}

// configureRemote handles the rclone section's "Configure Google Drive
// Remote..." button, letting the user set up OAuth (or a manual/CLI config)
// without needing to Save first.
func (t *trayApp) configureRemote(current gui.SettingsFormData) {
	remoteName := strings.TrimSpace(current.RcloneRemote)
	if remoteName == "" {
		remoteName = "ObsidianVault"
	}
	current.RcloneRemote = remoteName

	if _, ok := drive.FindRcloneBinary(); !ok {
		gui.Info("rclone Required", "Please install rclone first (see the Status section) before configuring a Google Drive remote.")
		return
	}

	gui.Choice(
		"Configure Google Drive Remote",
		fmt.Sprintf("Set up how UniteVault should connect to Google Drive remote '%s':", remoteName),
		"New Setup (Recommended)",
		"Existing / CLI Config",
		func(choice int) {
			client := drive.NewClient(engineLogPath())
			switch choice {
			case 1:
				go func() {
					err := gui.RunWithProgress(
						"Google Drive Setup",
						"Opening your browser for Google Drive authentication...\nPlease grant permissions, then return here.",
						func() error { return client.CreateGoogleDriveRemote(context.Background(), remoteName) },
					)
					if err != nil || !client.IsRemoteConfigured(context.Background(), remoteName) {
						gui.Info("Setup Failed", "Automatic setup did not complete. Launching a terminal for manual configuration instead...")
						_ = bootstrap.LaunchTerminalRcloneConfig(client.GetBinaryPath())
					} else {
						gui.Info("Google Drive Connected", fmt.Sprintf("Successfully connected Google Drive remote '%s'!", remoteName))
					}
					t.reopenSettingsGUI(current)
				}()
			case 2:
				_ = bootstrap.LaunchTerminalRcloneConfig(client.GetBinaryPath())
				gui.Info("Terminal Launched", "Complete the rclone configuration in the opened terminal window, then come back and press Save Settings.")
			}
		},
	)
}

// removeRemote handles the rclone section's "Remove Remote Configuration..."
// button, letting the user cleanly delete an rclone remote's saved Google
// Drive credentials so they can set it up again from scratch (e.g. with a
// different Google account), which previously had no dedicated flow at all.
func (t *trayApp) removeRemote(current gui.SettingsFormData) {
	remoteName := strings.TrimSpace(current.RcloneRemote)
	if remoteName == "" {
		return
	}

	gui.ConfirmDanger(
		"Remove rclone Remote",
		fmt.Sprintf(
			"Remove the rclone remote '%s'?\n\nThis deletes its saved Google Drive credentials from rclone's configuration (the files already backed up on Google Drive are not affected). You can set it up again afterwards.",
			remoteName,
		),
		func(confirmed bool) {
			if !confirmed {
				return
			}
			go func() {
				client := drive.NewClient(engineLogPath())
				if err := client.RemoveRemote(context.Background(), remoteName); err != nil {
					gui.Info("Remove Failed", fmt.Sprintf("Failed to remove remote '%s': %v", remoteName, err))
				} else {
					gui.Info("Remote Removed", fmt.Sprintf("Removed rclone remote '%s'.", remoteName))
				}
				t.reopenSettingsGUI(current)
			}()
		},
	)
}

// saveSettings handles the "Save Settings" button: validates input, warns
// about a Vault switch that would silently overwrite a previous Vault's
// Google Drive backup, saves config.json, ensures the Google Drive remote is
// configured, and runs primary/secondary node initialization (spec
// 3.6.1.1).
func (t *trayApp) saveSettings(data gui.SettingsFormData) {
	if data.VaultPath == "" {
		gui.Info("Vault Required", "Please select your Obsidian Vault directory before saving.")
		return
	}

	// rclone sync mirrors its destination exactly, deleting anything not
	// present in the source. If the Vault changed since the last save but
	// the Google Drive Target Folder Path didn't, the next sync would wipe
	// out the previous Vault's backed-up files. The Settings window already
	// defaults the target path to the Vault's own folder name and keeps it
	// following Vault changes (buildSettingsContent), so this only fires
	// when the user has kept (or retyped) the same target path on purpose.
	if prevCfg, err := t.cfgMgr.LoadConfig(); err == nil && vaultChangedWithSameTarget(prevCfg, data) {
		gui.ConfirmDanger(
			"Vault Changed - Same Backup Target",
			fmt.Sprintf(
				"You're changing the Vault from:\n%s\nto:\n%s\n\nbut the Google Drive Target Folder Path is still '%s'.\n\n"+
					"Google Drive backup mirrors the Vault exactly, so the next sync will delete the previous Vault's files there and replace them with the new Vault's.\n\n"+
					"Continue with this target folder anyway?",
				prevCfg.VaultPath, data.VaultPath, data.RclonePath,
			),
			func(confirmed bool) {
				if confirmed {
					t.saveSettingsConfirmed(data)
				}
			},
		)
		return
	}

	t.saveSettingsConfirmed(data)
}

// vaultChangedWithSameTarget reports whether saving data would point a
// *different* Vault at the *same* Google Drive backup target the
// previously-saved config used - the scenario where the next rclone sync
// would silently delete the old Vault's backed-up files (see saveSettings).
func vaultChangedWithSameTarget(prevCfg *config.Config, data gui.SettingsFormData) bool {
	if prevCfg == nil || prevCfg.VaultPath == "" {
		return false
	}
	return prevCfg.VaultPath != data.VaultPath && prevCfg.RclonePath == data.RclonePath
}

// saveSettingsConfirmed does the actual work of saveSettings, once any
// Vault-change warning has been confirmed (or didn't apply).
func (t *trayApp) saveSettingsConfirmed(data gui.SettingsFormData) {
	go func() {
		driveClient := drive.NewClient(engineLogPath())

		if !driveClient.IsRemoteConfigured(context.Background(), data.RcloneRemote) {
			choiceCh := make(chan int, 1)
			gui.Choice(
				"Configure Google Drive Remote",
				fmt.Sprintf("Google Drive remote '%s' is not configured yet.\n\nChoose how you'd like to set it up:", data.RcloneRemote),
				"New Setup (Recommended)",
				"Existing / CLI Config",
				func(choice int) { choiceCh <- choice },
			)

			switch <-choiceCh {
			case 1:
				err := gui.RunWithProgress(
					"Google Drive Setup",
					"Opening your browser for Google Drive authentication...\nPlease grant permissions, then return here.",
					func() error { return driveClient.CreateGoogleDriveRemote(context.Background(), data.RcloneRemote) },
				)
				if err != nil || !driveClient.IsRemoteConfigured(context.Background(), data.RcloneRemote) {
					gui.Info("Setup Failed", "Automatic setup did not complete. Launching a terminal for manual configuration; please retry Save afterwards.")
					_ = bootstrap.LaunchTerminalRcloneConfig(driveClient.GetBinaryPath())
					return
				}
			case 2:
				_ = bootstrap.LaunchTerminalRcloneConfig(driveClient.GetBinaryPath())
				gui.Info("Terminal Launched", "Complete the rclone configuration in the opened terminal window, then press Save Settings again.")
				return
			default:
				return
			}
		}

		newCfg := &config.Config{
			VaultPath:       data.VaultPath,
			RcloneRemote:    data.RcloneRemote,
			RclonePath:      data.RclonePath,
			IntervalSeconds: data.IntervalSeconds,
		}
		if err := t.cfgMgr.SaveConfig(newCfg); err != nil {
			gui.Info("Save Failed", fmt.Sprintf("Failed to save configuration: %v", err))
			return
		}

		hostname, _ := os.Hostname()
		bootstrapper := bootstrap.NewBootstrapper(t.cfgMgr, driveClient)
		remoteTarget := fmt.Sprintf("%s:%s", data.RcloneRemote, data.RclonePath)

		var newRole string
		err := gui.RunWithProgress(
			"Initializing UniteVault",
			"Determining Primary/Secondary role and syncing initial state with Google Drive...",
			func() error {
				var initErr error
				newRole, initErr = bootstrapper.InitializeNode(context.Background(), data.VaultPath, remoteTarget, hostname)
				return initErr
			},
		)
		if err != nil {
			gui.Info("Initialization Failed", fmt.Sprintf("UniteVault could not finish initializing: %v", err))
			return
		}

		gui.SetMenuItemLabel(t.menu, t.status, fmt.Sprintf("Status: Active (%s)", newRole))
		gui.Info("UniteVault Configured", fmt.Sprintf(
			"Settings saved successfully!\n\nVault: %s\nRemote Target: %s\nSync Interval: %d seconds\nRole: %s",
			data.VaultPath, remoteTarget, data.IntervalSeconds, newRole,
		))
		gui.HideWindow()

		go t.runDaemonLoop(newCfg)
	}()
}

func handleInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	vaultPath := fs.String("vault", "", "Path to Obsidian Vault directory")
	remoteName := fs.String("remote", "ObsidianVault", "rclone remote name")
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
