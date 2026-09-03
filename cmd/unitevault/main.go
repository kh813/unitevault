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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/theme"
	"github.com/kh813/unitevault/internal/bootstrap"
	"github.com/kh813/unitevault/internal/config"
	"github.com/kh813/unitevault/internal/drive"
	"github.com/kh813/unitevault/internal/engine"
	"github.com/kh813/unitevault/internal/eventlog"
	"github.com/kh813/unitevault/internal/gui"
	"github.com/kh813/unitevault/internal/obsidianconfig"
	"github.com/kh813/unitevault/internal/selfupdate"
	"github.com/kh813/unitevault/internal/singleinstance"
	"github.com/kh813/unitevault/internal/syncdir"
	"github.com/kh813/unitevault/internal/watch"
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
	// checkConflicts is the tray menu's "Check for Conflicts and Merge"
	// item (spec 1.6.10, iCloud-centric Mode A only) - kept as a field so
	// refreshCheckConflictsMenuItem can toggle its enabled state whenever the
	// sync mode or Vault path could have changed, rather than only ever
	// deciding it once at startup.
	checkConflicts *fyne.MenuItem

	icloudNoticeShown bool

	// daemonMu guards daemonCancel: exactly one daemon loop goroutine may
	// ever be running at a time. Without this, every successful "Save
	// Settings" started a brand new runDaemonLoop goroutine on top of
	// whichever one(s) were already running (the first from startup(),
	// then one more per save) - t.ctx is only ever cancelled on Quit, so
	// nothing ever stopped the earlier ones. Each accumulated loop kept
	// ticking independently and forever, concurrently scanning/merging/
	// rclone-syncing (a Primary device's own past Vault path/config,
	// stale after later saves, in addition to the current one) - a real,
	// previously-unnoticed bug. startDaemonLoop/stopDaemonLoop are the
	// only places allowed to touch daemonCancel; always go through them,
	// never call runDaemonLoop directly.
	daemonMu     sync.Mutex
	daemonCancel context.CancelFunc

	// cycleMu serializes every SyncEngine.RunCycle invocation on this
	// device (from the daemon loop's ticker and "Sync Now" alike) so two
	// never run concurrently, and doubles as the gate for destructive
	// configuration changes (saveSettingsConfirmed, removeRemote,
	// configureRemote, performReset) that would be unsafe to run while a
	// cycle is mid-flight and actively reading/writing local sync state or
	// using the rclone remote being changed. Always acquire via
	// tryBeginExclusiveOp, never lock directly.
	cycleMu sync.Mutex
}

// tryBeginExclusiveOp attempts to acquire exclusive access to run a sync
// cycle or a destructive configuration change, without blocking. ok=false
// means a cycle or another destructive operation is already in progress -
// callers must warn the user and abort rather than proceeding or silently
// queueing behind it (spec 3.5.3).
func (t *trayApp) tryBeginExclusiveOp() (release func(), ok bool) {
	if !t.cycleMu.TryLock() {
		return nil, false
	}
	return t.cycleMu.Unlock, true
}

// refreshCheckConflictsMenuItem enables the tray menu's "Check for
// Conflicts and Merge..." item only when it could actually find anything:
// iCloud-centric Mode A (spec 1.6.10) with a Vault already configured.
// Rather than hiding the item outright when irrelevant, it's greyed out
// (matching mStatus's own always-present-but-disabled convention) so the
// menu's shape stays stable across sync modes. Call this whenever the sync
// mode or Vault path could have changed - startup, after Save Settings, and
// after Reset Configuration - since (unlike the Settings window's own
// removed "Check for Conflicts..." button, which recomputed this every time
// Settings was opened) the tray menu has no natural point to recompute it
// lazily on open.
func (t *trayApp) refreshCheckConflictsMenuItem() {
	cfg, err := t.cfgMgr.LoadConfig()
	enabled := err == nil && cfg != nil && cfg.VaultPath != "" && cfg.EffectiveSyncMode() == config.SyncModeICloud
	gui.SetMenuItemEnabled(t.menu, t.checkConflicts, enabled)
}

func runTrayMode() {
	appIcon := fyne.NewStaticResource("unitevault-icon.png", trayIconColorPNG)
	gui.InitApp(appIcon)

	// A real device report: nothing previously stopped a user from
	// launching UniteVault.app/UniteVault.exe a second time (double-
	// clicking it again, an OS "run at login" entry firing while a manual
	// launch is already open, ...), which would race two SyncEngines
	// against the same Vault. A failure of the lock mechanism itself
	// (err != nil) is treated as "couldn't tell, so run anyway" rather
	// than blocking startup over an unrelated environment problem.
	release, acquired, instanceErr := singleinstance.TryAcquire()
	if instanceErr == nil {
		if !acquired {
			gui.Info(lang.L("UniteVault Already Running"), lang.L("Another UniteVault instance is already running. Check your menu bar or system tray for its icon."))
			gui.Run()
			return
		}
		defer release()
	}

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
		gui.Info(lang.L("UniteVault Error"), lang.L("Failed to initialize local configuration: {{.Err}}", map[string]string{"Err": err.Error()}))
		gui.Run()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())

	mStatus := fyne.NewMenuItem(lang.L("Status: Idle"), nil)
	mStatus.Disabled = true
	mSyncNow := fyne.NewMenuItem(lang.L("Sync Now"), nil)
	mSettings := fyne.NewMenuItem(lang.L("Settings"), nil)
	// Starts disabled; refreshCheckConflictsMenuItem enables it once startup
	// has actually determined the sync mode - see that method's doc comment
	// for why this lives in the tray menu rather than the Settings window
	// (its previous home).
	mCheckConflicts := fyne.NewMenuItem(lang.L("Check for Conflicts and Merge"), nil)
	mCheckConflicts.Disabled = true
	mCheckUpdate := fyne.NewMenuItem(lang.L("Check for Update"), nil)
	mAbout := fyne.NewMenuItem(lang.L("About UniteVault"), nil)
	mQuit := fyne.NewMenuItem(lang.L("Quit UniteVault"), nil)
	mQuit.IsQuit = true

	// Reset Configuration is intentionally only available inside the
	// Settings window (its own button there is gated by a confirm dialog),
	// not here - it's a rare, destructive action that doesn't belong one
	// click away in the everyday tray menu.
	menu := fyne.NewMenu("UniteVault",
		mStatus,
		fyne.NewMenuItemSeparator(),
		mSyncNow,
		mCheckConflicts,
		mSettings,
		mCheckUpdate,
		mAbout,
		fyne.NewMenuItemSeparator(),
		mQuit,
	)

	t := &trayApp{ctx: ctx, cancel: cancel, cfgMgr: cfgMgr, menu: menu, status: mStatus, checkConflicts: mCheckConflicts}

	mSyncNow.Action = func() { go t.syncNow() }
	mSettings.Action = func() { t.openSettingsGUI() }
	mCheckConflicts.Action = func() { t.checkICloudConflicts(t.buildFormData()) }
	mCheckUpdate.Action = func() { go t.checkForUpdate() }
	mAbout.Action = func() {
		gui.Info(lang.L("About UniteVault"), lang.L("UniteVault v{{.Version}}", map[string]string{"Version": bootstrap.AppVersion}))
	}
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
// sync cycle. Gated on the first-launch disclaimer (see
// showDisclaimerGate) - nothing else in the app runs until the user has
// explicitly agreed to it, on every device, once.
func (t *trayApp) startup() {
	if !t.cfgMgr.IsDisclaimerAccepted() {
		t.showDisclaimerGate(t.startup)
		return
	}

	go t.runPeriodicUpdateCheck(t.ctx)

	t.refreshCheckConflictsMenuItem()

	cfg, err := t.cfgMgr.LoadConfig()
	if err != nil || cfg.VaultPath == "" {
		gui.SetMenuItemLabel(t.menu, t.status, lang.L("Status: Not Initialized"))
		t.openSettingsGUI()
		t.maybeShowInstallReminder()
		return
	}

	role, _ := t.cfgMgr.LoadRole()
	if role != "" {
		gui.SetMenuItemLabel(t.menu, t.status, lang.L("Status: Active ({{.Role}})", map[string]string{"Role": role}))
	} else {
		gui.SetMenuItemLabel(t.menu, t.status, lang.L("Status: Active"))
	}
	t.startDaemonLoop(cfg)
	t.maybeShowICloudMigrationReminder(cfg)
}

// showDisclaimerGate blocks first use behind an explicit "I Agree" -
// unlike the dismissible reminders elsewhere in this file (Install
// Reminder, iCloud Migration Reminder), declining doesn't just skip a
// suggestion, it quits the app entirely: UniteVault is independent,
// unofficial software that moves/merges/syncs the user's own Vault files
// automatically, and the same no-warranty terms already documented in
// README.md's "License" section are easy to never see before running a
// downloaded app. onAgreed runs (on the Fyne main thread, via gui.Choice)
// once the user accepts, continuing whatever startup() was doing.
func (t *trayApp) showDisclaimerGate(onAgreed func()) {
	gui.Choice(
		lang.L("UniteVault Disclaimer"),
		lang.L("UniteVault is an independent, unofficial tool - it is not made, endorsed, or supported by Obsidian.\n\nIt is provided \"as is\", with no warranty of any kind. The author is not liable for any data loss, file corruption, or other damage that may result from using it.\n\nThis software moves, merges, and synchronizes your Vault files automatically. Please keep your own backups, especially while you're still getting familiar with it.\n\nBy clicking \"I Agree\", you acknowledge and accept these terms."),
		lang.L("I Agree"),
		lang.L("Quit"),
		func(choice int) {
			if choice == 1 {
				_ = t.cfgMgr.SetDisclaimerAccepted()
				onAgreed()
				return
			}
			// Declined (explicit "Quit", or the dialog closed/cancelled
			// without an explicit choice) - must not proceed into any
			// sync/file-modifying behavior at all.
			t.cancel()
			gui.Quit()
		},
	)
}

// startDaemonLoop (re)starts the periodic sync cycle for cfg, first
// stopping any previously-running loop - see the trayApp.daemonMu doc
// comment for why this must never be skipped. Safe to call from any
// goroutine.
func (t *trayApp) startDaemonLoop(cfg *config.Config) {
	t.daemonMu.Lock()
	if t.daemonCancel != nil {
		t.daemonCancel()
	}
	loopCtx, cancel := context.WithCancel(t.ctx)
	t.daemonCancel = cancel
	t.daemonMu.Unlock()

	go t.runDaemonLoop(loopCtx, cfg)
}

// stopDaemonLoop stops any currently-running daemon loop without starting
// a replacement - used by performReset, since a reset device has no valid
// config left for a loop to run against. Safe to call from any goroutine.
func (t *trayApp) stopDaemonLoop() {
	t.daemonMu.Lock()
	if t.daemonCancel != nil {
		t.daemonCancel()
		t.daemonCancel = nil
	}
	t.daemonMu.Unlock()
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

	message := lang.L(
		"UniteVault needs {{.Tools}} installed before it can sync your Vault.\n\nYou can install {{.Tools}} from the Status section of the Settings window.",
		map[string]string{"Tools": strings.Join(missing, " and ")},
	)
	gui.InstallReminder(lang.L("Setup Required"), message, func(dontShowAgain bool) {
		if dontShowAgain {
			_ = t.cfgMgr.SetInstallReminderDismissed()
		}
	})
}

// vaultUnderManagedRoot reports whether vaultPath already sits under this
// app's own managed local folder (bootstrap.ManagedVaultParentDir, spec
// 1.6.7). This is the general, forward-compatible replacement for
// detecting "is this Vault at risk from some third-party sync daemon"
// (spec 1.6.1/3.6.1.6): rather than keeping a growing list of specific
// known-risky locations (iCloud Drive, iCloud's own Obsidian container,
// Google Drive Desktop's own sync folder, ...), anything that isn't under
// the one folder this app itself manages is treated as needing a move -
// closing the gap for any Vault location this app doesn't (or will never)
// know to name individually. If the home directory can't even be
// determined, treats vaultPath as "managed" (false alarm is better than a
// migration attempt that can't complete anyway).
func vaultUnderManagedRoot(vaultPath string) bool {
	root, err := bootstrap.ManagedVaultParentDir()
	if err != nil {
		return true
	}
	return pathIsUnder(root, vaultPath)
}

// maybeShowICloudMigrationReminder nags an already-configured user, once
// per app launch, if their Vault still sits outside this app's own managed
// local folder (spec 1.6.1/1.6.7, unitevault-todo.md Phase 18) - most
// commonly the legacy pre-1.6 architecture of a Vault placed directly
// inside iCloud Drive, but not limited to it (see vaultUnderManagedRoot).
// Silently does nothing once the user has dismissed it, or once the Vault
// is already under the managed folder (e.g. after a successful migration -
// no separate "already migrated" flag is needed, since this check alone
// naturally stops firing).
// shouldShowICloudMigrationReminder is maybeShowICloudMigrationReminder's
// decision, factored out as a pure predicate so it's testable without
// needing a real gui.mainWindow (gui.ChoiceN would otherwise be reached).
// syncModeManagesOwnVaultLocation reports whether mode deliberately keeps
// the Vault wherever some *other* sync mechanism already places it
// (Apple's iCloud for SyncModeICloud, the user's own Google Drive desktop
// app for SyncModeGDriveDesktop) rather than under this app's own managed
// local folder (spec 1.6.10). Vault Migration, and the reminder that
// offers it, exist only to catch a legacy/misplaced Vault for
// SyncModeDrive - firing them in either of these other modes would offer
// to move the Vault out of the exact location its cross-device sync
// depends on.
func syncModeManagesOwnVaultLocation(mode config.SyncMode) bool {
	return mode == config.SyncModeICloud || mode == config.SyncModeGDriveDesktop
}

func shouldShowICloudMigrationReminder(cfgMgr *config.ConfigManager, cfg *config.Config) bool {
	if syncModeManagesOwnVaultLocation(cfg.EffectiveSyncMode()) {
		return false
	}
	if cfgMgr.IsICloudMigrationReminderDismissed() {
		return false
	}
	return !vaultUnderManagedRoot(cfg.VaultPath)
}

func (t *trayApp) maybeShowICloudMigrationReminder(cfg *config.Config) {
	if !shouldShowICloudMigrationReminder(t.cfgMgr, cfg) {
		return
	}

	gui.ChoiceN(
		lang.L("Move Your Vault to UniteVault's Local Folder?"),
		lang.L(
			"Your Obsidian Vault currently isn't in UniteVault's own local folder:\n{{.Vault}}\n\nIf this location is also synced by another service (iCloud Drive, Google Drive Desktop, Dropbox, ...), that service's own sync daemon can edit the same files UniteVault and Obsidian are editing at the same time, which can lead to duplicate or conflicted files.\n\nUniteVault can move it for you now, or you can do this later from Settings > \"Migrate Vault to Local Folder...\".",
			map[string]string{"Vault": cfg.VaultPath},
		),
		[]string{lang.L("Migrate Now (Recommended)"), lang.L("Don't Show This Again")},
		func(choice int) {
			switch choice {
			case 1:
				cfgSnapshot, err := t.cfgMgr.LoadConfig()
				if err != nil || cfgSnapshot == nil {
					cfgSnapshot = &config.Config{}
				}
				t.confirmAndMigrateVault(cfg.VaultPath, gui.SettingsFormData{
					VaultPath:       cfg.VaultPath,
					RcloneRemote:    cfgSnapshot.RcloneRemote,
					RclonePath:      cfgSnapshot.RclonePath,
					IntervalSeconds: cfgSnapshot.IntervalSeconds,
				})
			case 2:
				_ = t.cfgMgr.SetICloudMigrationReminderDismissed()
			}
			// choice == 0 (dialog's own Cancel) - ask again next launch.
		},
	)
}

// runDaemonLoop runs the periodic sync cycle until ctx is cancelled (either
// Quit, or startDaemonLoop replacing this loop with a newer one). Only
// ever call via startDaemonLoop, never directly.
func (t *trayApp) runDaemonLoop(ctx context.Context, cfg *config.Config) {
	hostname, _ := os.Hostname()
	eng := engine.NewSyncEngine(t.cfgMgr, cfg.VaultPath, hostname, nil)

	// Attach an OS-level file watcher (spec 1.6.5) as a best-effort scan
	// optimization - RunCycle already tolerates a nil watcher (it just
	// always scans the whole Vault), so a failure to start one here just
	// means slightly more work per cycle, never a correctness problem.
	// MkdirAll first (matching RunCycle's own call) since the Vault folder
	// may not exist yet on a brand new device - watching a nonexistent
	// root silently watches nothing at all, forever, rather than erroring.
	_ = os.MkdirAll(cfg.VaultPath, 0755)
	if w, err := watch.New(cfg.VaultPath); err == nil {
		eng.SetWatcher(w)
		defer w.Close()
	}

	interval := cfg.IntervalSeconds
	if interval <= 0 {
		interval = config.DefaultIntervalSeconds
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	// Run the first cycle immediately rather than waiting for the ticker's
	// first tick (up to a full interval - 60s by default) - otherwise every
	// app launch/restart sits idle that long before anything happens at
	// all, which is most visible right after a fresh Primary/Secondary
	// election (spec 3.6.1.1/1.6.10): the tray/menu-bar Status label and
	// Settings > Status's Device role both stay stuck on their pre-role
	// "unknown" state until this first cycle actually runs and saves one.
	t.runCycleGuarded(ctx, eng, false)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// reportBusy=false: a routine tick finding a cycle already in
			// progress (from "Sync Now", most likely) just waits quietly
			// for the next one rather than interrupting the user.
			t.runCycleGuarded(ctx, eng, false)
		}
	}
}

// runCycleGuarded runs eng.RunCycle while holding cycleMu, so it can never
// overlap with another cycle or a destructive configuration change (see
// tryBeginExclusiveOp) - shared by runDaemonLoop's ticker and "Sync Now"
// so both paths get the same guarding and status-label updates.
func (t *trayApp) runCycleGuarded(ctx context.Context, eng *engine.SyncEngine, reportBusy bool) {
	release, ok := t.tryBeginExclusiveOp()
	if !ok {
		if reportBusy {
			gui.Info(lang.L("Sync In Progress"), lang.L("A sync is already running. Please wait for it to finish, then try again."))
		}
		return
	}
	defer release()

	gui.SetMenuItemLabel(t.menu, t.status, lang.L("Status: Syncing..."))
	err := eng.RunCycle(ctx)
	role, _ := t.cfgMgr.LoadRole()
	switch {
	case err != nil:
		gui.SetMenuItemLabel(t.menu, t.status, lang.L("Status: Error ({{.Err}})", map[string]string{"Err": err.Error()}))
	case t.hasUnresolvedConflict():
		gui.SetMenuItemLabel(t.menu, t.status, lang.L("Status: Conflict"))
	default:
		gui.SetMenuItemLabel(t.menu, t.status, lang.L("Status: Active ({{.Role}}) - {{.Time}}", map[string]string{"Role": role, "Time": time.Now().Format("15:04")}))
	}
}

// hasUnresolvedConflict reports whether this device currently has an
// unresolved multi-Primary conflict (spec 3.6.1.4) or genuine content
// conflict (spec 3.3.2) - either shows as "Status: Conflict" in the tray/
// menu bar instead of the ordinary "Active" status (spec 3.5.2).
func (t *trayApp) hasUnresolvedConflict() bool {
	if c, err := t.cfgMgr.LoadPrimaryConflict(); err == nil && c != nil {
		return true
	}
	if pending, err := t.cfgMgr.LoadPendingConflicts(); err == nil && len(pending) > 0 {
		return true
	}
	return false
}

// syncNow handles the "Sync Now" tray menu action.
func (t *trayApp) syncNow() {
	cfg, err := t.cfgMgr.LoadConfig()
	if err != nil || cfg.VaultPath == "" {
		gui.SetMenuItemLabel(t.menu, t.status, lang.L("Status: Not Initialized"))
		t.openSettingsGUI()
		return
	}

	hostname, _ := os.Hostname()
	eng := engine.NewSyncEngine(t.cfgMgr, cfg.VaultPath, hostname, nil)
	t.runCycleGuarded(t.ctx, eng, true)
}

// checkForUpdate handles the "Check for Update" tray menu action:
// queries GitHub Releases, and if a newer version is available, offers to
// download and apply it (performSelfUpdate). Always safe to run regardless
// of init state - it doesn't touch Vault/config at all.
func (t *trayApp) checkForUpdate() {
	var info *selfupdate.ReleaseInfo
	err := gui.RunWithProgress(
		lang.L("Checking for Updates"),
		lang.L("Contacting GitHub to check for a newer version..."),
		func() error {
			var checkErr error
			info, checkErr = selfupdate.CheckLatest(context.Background())
			return checkErr
		},
	)
	if err != nil {
		gui.Info(lang.L("Update Check Failed"), lang.L("Could not check for updates: {{.Err}}", map[string]string{"Err": err.Error()}))
		return
	}

	_ = t.cfgMgr.SaveLastUpdateCheck(time.Now())

	if !selfupdate.IsNewer(bootstrap.AppVersion, info.Version) {
		gui.Info(lang.L("Up to Date"), lang.L("You're running the latest version (v{{.Version}}).", map[string]string{"Version": bootstrap.AppVersion}))
		return
	}

	t.offerUpdate(info)
}

// offerUpdate shows the "Update Available" prompt - or, if no automatic
// download matches this platform, an offer to open the release page
// instead - shared by both the manual "Check for Update" menu action and
// the periodic background check (maybeCheckForUpdatePeriodically). Safe to
// call from any goroutine (gui.Confirm is).
func (t *trayApp) offerUpdate(info *selfupdate.ReleaseInfo) {
	if info.AssetURL == "" {
		gui.Confirm(
			lang.L("Update Available"),
			lang.L(
				"Version {{.Latest}} is available (you have v{{.Current}}), but UniteVault couldn't find a matching automatic download for this platform.\n\nOpen the release page in your browser?",
				map[string]string{"Latest": info.TagName, "Current": bootstrap.AppVersion},
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
		lang.L("Update Available"),
		lang.L(
			"Version {{.Latest}} is available (you have v{{.Current}}).\n\nDownload and install it now? UniteVault will quit and restart automatically once the update is applied.",
			map[string]string{"Latest": info.TagName, "Current": bootstrap.AppVersion},
		),
		func(confirmed bool) {
			if confirmed {
				go t.performSelfUpdate(info)
			}
		},
	)
}

// updateCheckInterval is how often UniteVault checks GitHub Releases for a
// newer version in the background, without any user action - separate from
// "Check for Update" in the tray menu, which always checks immediately
// regardless of when the last check happened.
const updateCheckInterval = 7 * 24 * time.Hour

// runPeriodicUpdateCheck loops for the lifetime of ctx, checking roughly
// once an hour whether updateCheckInterval has elapsed since the last
// recorded check (LoadLastUpdateCheck, persisted to disk) - so quitting and
// relaunching the app daily doesn't reset a weekly cadence back to zero
// every time. Silent unless an update is actually found: unlike the manual
// "Check for Update" menu action, this never shows a progress dialog or
// an "Up to Date" message on its own - only the same "Update Available"
// prompt the manual check shows (offerUpdate), and only when there's
// actually something to offer. Must be started at most once per app
// lifetime - see startup, its only caller, which only ever reaches the line
// that starts this after the user has passed the first-launch disclaimer
// gate.
func (t *trayApp) runPeriodicUpdateCheck(ctx context.Context) {
	t.maybeCheckForUpdatePeriodically()

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.maybeCheckForUpdatePeriodically()
		}
	}
}

// maybeCheckForUpdatePeriodically runs a silent background update check if
// at least updateCheckInterval has passed since the last recorded check (or
// none has ever been recorded on this device). A failed check (e.g. no
// network) deliberately doesn't update the recorded time, so it's retried
// on the next hourly tick instead of waiting a full week for connectivity
// to come back.
func (t *trayApp) maybeCheckForUpdatePeriodically() {
	last, _ := t.cfgMgr.LoadLastUpdateCheck()
	if time.Since(last) < updateCheckInterval {
		return
	}

	info, err := selfupdate.CheckLatest(context.Background())
	if err != nil {
		return
	}
	_ = t.cfgMgr.SaveLastUpdateCheck(time.Now())

	if !selfupdate.IsNewer(bootstrap.AppVersion, info.Version) {
		return
	}
	t.offerUpdate(info)
}

// performSelfUpdate downloads and applies the update described by info, then
// quits so the detached helper process selfupdate.Apply started can finish
// replacing this app and relaunch it. If anything fails, nothing has been
// touched yet (Apply only hands off to the helper after a fully successful
// download+extract), so it's always safe to fall back to a manual download.
func (t *trayApp) performSelfUpdate(info *selfupdate.ReleaseInfo) {
	err := gui.RunWithProgress(
		lang.L("Updating UniteVault"),
		lang.L("Downloading version {{.Version}}...", map[string]string{"Version": info.TagName}),
		func() error { return selfupdate.Apply(context.Background(), info.AssetURL) },
	)
	if err != nil {
		gui.Info(
			lang.L("Update Failed"),
			lang.L(
				"Could not automatically update: {{.Err}}\n\nYou can download the new version manually from:\n{{.URL}}",
				map[string]string{"Err": err.Error(), "URL": info.HTMLURL},
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
	// Same reasoning as saveSettings' multi-device warning: if this device
	// is Primary and other devices appear to share this Vault, resetting it
	// stops Google Drive backups for the shared Vault until one of them
	// takes over (spec 3.6.1.5).
	if role, _ := t.cfgMgr.LoadRole(); role == "primary" {
		if cfg, err := t.cfgMgr.LoadConfig(); err == nil && cfg != nil && cfg.VaultPath != "" {
			deviceID, idErr := t.cfgMgr.GetDeviceID()
			others, othersErr := knownActiveOtherDevices(cfg.VaultPath, deviceID)
			if idErr == nil && othersErr == nil && len(others) > 0 {
				gui.ConfirmDanger(
					lang.L("Other Devices Use This Vault"),
					lang.L("This device is Primary, and at least one other device appears to share this Vault.\n\nResetting this device stops it syncing the shared Vault via Google Drive until another device takes over via \"Promote to Primary...\".\n\nContinue resetting anyway?"),
					func(confirmed bool) {
						if confirmed {
							t.performResetConfirmed()
						}
					},
				)
				return
			}
		}
	}
	t.performResetConfirmed()
}

// performResetConfirmed does the actual work of performReset, once any
// multi-device warning has been confirmed (or didn't apply).
func (t *trayApp) performResetConfirmed() {
	release, ok := t.tryBeginExclusiveOp()
	if !ok {
		gui.Info(lang.L("Sync In Progress"), lang.L("A sync is currently running. Please wait for it to finish, then try again."))
		return
	}
	defer release()

	// Tell every other device this one is deliberately leaving, before the
	// local role/config that identifies it as ever having participated gets
	// cleared below - otherwise there'd be no way to tell "left on purpose"
	// apart from "just went quiet" (see EventDeviceDecommissioned,
	// knownActiveOtherDevices).
	if cfg, err := t.cfgMgr.LoadConfig(); err == nil && cfg != nil && cfg.VaultPath != "" {
		hostname, _ := os.Hostname()
		decommissionSelf(t.cfgMgr, cfg.VaultPath, hostname)
	}

	t.stopDaemonLoop()
	_ = t.cfgMgr.ResetConfig()
	t.icloudNoticeShown = false
	gui.SetMenuItemLabel(t.menu, t.status, lang.L("Status: Not Initialized"))
	t.refreshCheckConflictsMenuItem()
	t.openSettingsGUI()
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
	gdriveDesktopMode := cfg.EffectiveSyncMode() == config.SyncModeGDriveDesktop

	// engine.RunCycle only ever runs the rclone sync step (and records its
	// outcome) on a Primary device - a Secondary never attempts it (in
	// either sync mode - spec 1.6.10 elects a Primary/Secondary in iCloud
	// mode too, exactly like Drive mode, so Google Drive always has one
	// canonical publisher), so showing its (possibly stale, or simply
	// absent) sync status would be misleading rather than just showing why
	// there isn't one. Mode D never touches Google Drive at all (its own
	// desktop app handles sync), so it has no status to show or hide
	// behind an explanation - leaving this "" hides the row entirely (see
	// settings_window.go).
	var driveSyncStatus string
	if gdriveDesktopMode {
		// leave empty
	} else if role == "" {
		driveSyncStatus = lang.L("N/A (not configured yet)")
		role = "N/A"
	} else if role == "secondary" {
		driveSyncStatus = lang.L("N/A (this device is Secondary - Google Drive backup runs on the Primary device)")
	} else if st, err := t.cfgMgr.LoadDriveSyncStatus(); err == nil && st != nil {
		displayTime := st.Time
		if ts, parseErr := time.Parse(time.RFC3339, st.Time); parseErr == nil {
			displayTime = ts.Local().Format("2006-01-02 15:04")
		}
		if st.Success {
			driveSyncStatus = lang.L("Last synced: {{.Time}}", map[string]string{"Time": displayTime})
		} else {
			driveSyncStatus = lang.L("Last sync failed ({{.Time}}): {{.Error}}", map[string]string{"Time": displayTime, "Error": st.Error})
		}
	} else {
		driveSyncStatus = lang.L("Never synced yet")
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
		RcloneRemote:     "Vault",
		RclonePath:       "VaultBackup",
		IntervalSeconds:  config.DefaultIntervalSeconds,
		RcloneExecPath:   rcloneExecPath,
		RcloneRemoteInfo: lang.L("rclone not installed yet"),
	}

	if cfg != nil {
		data.VaultPath = cfg.VaultPath
		data.SyncMode = string(cfg.EffectiveSyncMode())
		if cfg.RcloneRemote != "" {
			data.RcloneRemote = cfg.RcloneRemote
		}
		if cfg.RclonePath != "" {
			data.RclonePath = cfg.RclonePath
		}
		if cfg.IntervalSeconds > 0 {
			data.IntervalSeconds = cfg.IntervalSeconds
		}
		data.ExtraExcludes = strings.Join(cfg.ExtraExcludes, "\n")
		data.LogIncludeFilenames = cfg.LogIncludeFilenames
	}

	// Only probe "rclone listremotes" if a binary is actually present -
	// avoids triggering drive.NewClient's auto-download side effect just to
	// render the window.
	if rcloneExecPath != "" {
		client := drive.NewClient(config.EngineLogPath())
		data.RcloneConfigured = client.IsRemoteConfigured(context.Background(), data.RcloneRemote)
		if data.RcloneConfigured {
			data.RcloneRemoteInfo = lang.L("Configured ({{.Remote}})", map[string]string{"Remote": data.RcloneRemote})
		} else {
			data.RcloneRemoteInfo = lang.L("Not configured - remote '{{.Remote}}' not found in rclone", map[string]string{"Remote": data.RcloneRemote})
		}
	}

	applyPrimaryConflictStatus(t.cfgMgr, &data)

	// MultiDeviceStatus (spec 3.6.1.5) only ever applies to a Primary - it
	// tells them whether any other device has shown up at all ("Standalone"
	// vs "Syncing"), which is real signal since nothing else in Settings
	// says that. A Secondary always implies a Primary exists somewhere
	// (even if currently unreachable), so it would always read "Syncing" -
	// a constant that carries no information, and appending it to "Device
	// role: Secondary" only duplicated the separate Google Drive sync
	// status row - a real user complaint, so a Secondary now leaves this
	// empty and shows just its role.
	if role == "primary" && cfg != nil && cfg.VaultPath != "" {
		if deviceID, err := t.cfgMgr.GetDeviceID(); err == nil {
			if others, err := knownActiveOtherDevices(cfg.VaultPath, deviceID); err == nil {
				if len(others) == 0 {
					data.MultiDeviceStatus = lang.L("Standalone")
				} else {
					data.MultiDeviceStatus = lang.L("Syncing")
				}
			}
		}
	}

	// PendingConflictCount (spec 3.3.2): merging - and therefore genuine
	// content conflicts - only ever happens on the Primary device.
	if role == "primary" {
		if pending, err := t.cfgMgr.LoadPendingConflicts(); err == nil {
			data.PendingConflictCount = len(pending)
		}
	}

	return data
}

// applyPrimaryConflictStatus fills in CanPromoteToPrimary /
// PrimaryConflictActive / PrimaryConflictMessage from the locally cached
// multi-Primary conflict state (see bootstrap.Bootstrapper.
// VerifyPrimaryStatus, spec 3.6.1.4) - that cache, refreshed every sync
// cycle, is what the Settings window reads rather than a live Google Drive
// round-trip on every render (matching how DriveSyncStatus is sourced
// above).
func applyPrimaryConflictStatus(cfgMgr *config.ConfigManager, data *gui.SettingsFormData) {
	data.CanPromoteToPrimary = data.DeviceRole == "secondary"

	conflict, err := cfgMgr.LoadPrimaryConflict()
	if err != nil || conflict == nil {
		return
	}

	data.PrimaryConflictActive = true
	data.CanPromoteToPrimary = true

	other := conflict.OtherLabel
	if other == "" {
		other = lang.L("another device")
	}
	if conflict.Role == config.ConflictRoleClaimed {
		data.PrimaryConflictMessage = lang.L("{{.Other}} also believes it is Primary. Google Drive sync is paused on both devices until this is resolved.", map[string]string{"Other": other})
	} else {
		data.PrimaryConflictMessage = lang.L("{{.Other}} was promoted to Primary. Google Drive sync is paused until this is resolved.", map[string]string{"Other": other})
	}
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
			lang.L("iPhone / iCloud Drive Setup Notice"),
			lang.L("If you plan to sync this Vault with an iPhone (iOS), 'iCloud for Windows' must be installed and your Vault folder should be stored inside your iCloud Drive folder.\n\nInstall iCloud for Windows now? (You can also do this later from Settings > Status > Install iCloud...)"),
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
	data.ExtraExcludes = current.ExtraExcludes
	data.LogIncludeFilenames = current.LogIncludeFilenames

	if _, ok := drive.FindRcloneBinary(); ok {
		client := drive.NewClient(config.EngineLogPath())
		data.RcloneConfigured = client.IsRemoteConfigured(context.Background(), data.RcloneRemote)
		if data.RcloneConfigured {
			data.RcloneRemoteInfo = lang.L("Configured ({{.Remote}})", map[string]string{"Remote": data.RcloneRemote})
		} else {
			data.RcloneRemoteInfo = lang.L("Not configured - remote '{{.Remote}}' not found in rclone", map[string]string{"Remote": data.RcloneRemote})
		}
	}

	t.showSettingsGUI(data)
}

func (t *trayApp) showSettingsGUI(data gui.SettingsFormData) {
	gui.ShowSettingsWindow(data, gui.SettingsHandlers{
		OnInstallGit:       t.installGit,
		OnInstallRclone:    t.installRclone,
		OnInstallICloud:    t.installICloud,
		OnConfigureRemote:  t.configureRemote,
		OnRemoveRemote:     t.removeRemote,
		OnPromoteToPrimary: t.promoteToPrimary,
		OnMigrateVault:     t.migrateVault,
		OnResolveConflicts: t.resolveConflicts,
		OnSave:             t.saveSettings,
		OnReset:            t.performReset,
	})
}

// installGit handles the Status section's "Install Git..." button.
func (t *trayApp) installGit(current gui.SettingsFormData) {
	go func() {
		_ = gui.RunWithProgress(
			lang.L("Installing Git"),
			lang.L("Attempting to automatically install Git (Homebrew / Xcode Command Line Tools on macOS, winget / official installer on Windows)...\nThis may take a moment."),
			func() error { return bootstrap.AutoInstallGit() },
		)

		if bootstrap.CheckGitInstalled() {
			gui.Info(lang.L("Git Installed"), lang.L("Git was successfully installed!"))
		} else {
			gui.Info(
				lang.L("Git Installation In Progress"),
				lang.L("The Git installer was launched. Please complete the on-screen installation, then reopen this Status section to confirm."),
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
			gui.Info(lang.L("rclone Install Failed"), lang.L("Could not determine an install location: {{.Err}}", map[string]string{"Err": err.Error()}))
			t.reopenSettingsGUI(current)
			return
		}

		installErr := gui.RunWithProgress(
			lang.L("Installing rclone"),
			lang.L("Downloading the official rclone binary for your platform..."),
			func() error { return drive.EnsureRcloneBinary(targetPath) },
		)

		if installErr == nil {
			gui.Info(lang.L("rclone Installed"), lang.L("rclone was successfully installed to:\n{{.Path}}", map[string]string{"Path": targetPath}))
		} else {
			gui.Info(
				lang.L("rclone Install Failed"),
				lang.L("Automatic download failed: {{.Err}}\n\nYou can download it manually from:\n{{.URL}}", map[string]string{"Err": installErr.Error(), "URL": bootstrap.GetRcloneDownloadURL()}),
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
			lang.L("Installing iCloud"),
			lang.L("Attempting to automatically install iCloud for Windows (winget)...\nThis may take a moment."),
			func() error { return bootstrap.AutoInstallICloud() },
		)

		switch {
		case installErr == nil:
			gui.Info(
				lang.L("iCloud Installed"),
				lang.L("iCloud for Windows was successfully installed and launched.\n\nPlease sign in with your Apple ID and turn on iCloud Drive, then return here to select your Vault folder."),
			)
		case errors.Is(installErr, bootstrap.ErrICloudRequiresAdministrator):
			gui.Info(
				lang.L("Administrator Privileges Required"),
				lang.L("{{.Err}}.\n\niCloud for Windows registers system-level components (Explorer integration, Outlook/Photos add-ins, registry entries) that only an administrator account can install, even through the Microsoft Store.\n\nPlease ask your system administrator to install it, or download it yourself from:\n{{.URL}}", map[string]string{"Err": installErr.Error(), "URL": bootstrap.GetICloudDownloadURL()}),
			)
		default:
			gui.Info(
				lang.L("iCloud Install Failed"),
				lang.L("Automatic installation failed: {{.Err}}\n\nYou can download it manually from:\n{{.URL}}", map[string]string{"Err": installErr.Error(), "URL": bootstrap.GetICloudDownloadURL()}),
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
		remoteName = "Vault"
	}
	current.RcloneRemote = remoteName

	if _, ok := drive.FindRcloneBinary(); !ok {
		gui.Info(lang.L("rclone Required"), lang.L("Please install rclone first (see the Status section) before configuring a Google Drive remote."))
		return
	}

	gui.Choice(
		lang.L("Configure Google Drive Remote"),
		lang.L("Set up how UniteVault should connect to Google Drive remote '{{.Remote}}':", map[string]string{"Remote": remoteName}),
		lang.L("New Setup (Recommended)"),
		lang.L("Existing / CLI Config"),
		func(choice int) {
			client := drive.NewClient(config.EngineLogPath())
			switch choice {
			case 1:
				go func() {
					release, ok := t.tryBeginExclusiveOp()
					if !ok {
						gui.Info(lang.L("Sync In Progress"), lang.L("A sync is currently running. Please wait for it to finish, then try again."))
						return
					}
					defer release()

					err := gui.RunWithProgress(
						lang.L("Google Drive Setup"),
						lang.L("Opening your browser for Google Drive authentication...\nThis can take up to a minute or two before the browser tab appears - please wait. Then grant permissions and return here."),
						func() error { return client.CreateGoogleDriveRemote(context.Background(), remoteName) },
					)
					if err != nil || !client.IsRemoteConfigured(context.Background(), remoteName) {
						gui.Info(lang.L("Setup Failed"), lang.L("Automatic setup did not complete. Launching a terminal for manual configuration instead..."))
						_ = bootstrap.LaunchTerminalRcloneConfig(client.GetBinaryPath())
					} else {
						gui.Info(lang.L("Google Drive Connected"), lang.L("Successfully connected Google Drive remote '{{.Remote}}'!", map[string]string{"Remote": remoteName}))
					}
					t.reopenSettingsGUI(current)
				}()
			case 2:
				_ = bootstrap.LaunchTerminalRcloneConfig(client.GetBinaryPath())
				gui.Info(lang.L("Terminal Launched"), lang.L("Complete the rclone configuration in the opened terminal window, then come back and press Save Settings."))
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

	// Same reasoning as performReset's multi-device warning (spec 3.6.1.5):
	// removing the remote on a Primary stops Google Drive sync for the
	// shared Vault - both this device's own publish and every Secondary's
	// push/pull (spec 1.6.4) - until it's reconfigured or another device
	// takes over via "Promote to Primary...".
	if role, _ := t.cfgMgr.LoadRole(); role == "primary" {
		if cfg, err := t.cfgMgr.LoadConfig(); err == nil && cfg != nil && cfg.VaultPath != "" {
			deviceID, idErr := t.cfgMgr.GetDeviceID()
			others, othersErr := knownActiveOtherDevices(cfg.VaultPath, deviceID)
			if idErr == nil && othersErr == nil && len(others) > 0 {
				gui.ConfirmDanger(
					lang.L("Other Devices Use This Vault"),
					lang.L("This device is Primary, and at least one other device appears to share this Vault.\n\nRemoving the Google Drive remote stops this device syncing the shared Vault (publishing merged changes, and receiving other devices' changes) until it's reconfigured.\n\nContinue removing the remote anyway?"),
					func(confirmed bool) {
						if confirmed {
							t.removeRemoteConfirmed(current, remoteName)
						}
					},
				)
				return
			}
		}
	}
	t.removeRemoteConfirmed(current, remoteName)
}

// removeRemoteConfirmed does the actual work of removeRemote, once any
// multi-device warning has been confirmed (or didn't apply).
func (t *trayApp) removeRemoteConfirmed(current gui.SettingsFormData, remoteName string) {
	gui.ConfirmDanger(
		lang.L("Remove rclone Remote"),
		lang.L(
			"Remove the rclone remote '{{.Remote}}'?\n\nThis deletes its saved Google Drive credentials from rclone's configuration (the files already backed up on Google Drive are not affected). You can set it up again afterwards.",
			map[string]string{"Remote": remoteName},
		),
		func(confirmed bool) {
			if !confirmed {
				return
			}
			go func() {
				release, ok := t.tryBeginExclusiveOp()
				if !ok {
					gui.Info(lang.L("Sync In Progress"), lang.L("A sync is currently running and may be using this remote. Please wait for it to finish, then try again."))
					return
				}
				defer release()

				client := drive.NewClient(config.EngineLogPath())
				if err := client.RemoveRemote(context.Background(), remoteName); err != nil {
					gui.Info(lang.L("Remove Failed"), lang.L("Failed to remove remote '{{.Remote}}': {{.Err}}", map[string]string{"Remote": remoteName, "Err": err.Error()}))
					t.reopenSettingsGUI(current)
					return
				}

				clearRemoteConfig(t.cfgMgr)
				gui.Info(lang.L("Remote Removed"), lang.L("Removed rclone remote '{{.Remote}}'.", map[string]string{"Remote": remoteName}))

				reopenData := current
				reopenData.RcloneRemote = ""
				reopenData.RclonePath = ""
				t.reopenSettingsGUI(reopenData)
			}()
		},
	)
}

// promoteToPrimary handles the Status section's "Promote to Primary..."
// button (spec 3.6.1.2 / 3.6.1.4 / 3.5.3), shown whenever
// SettingsFormData.CanPromoteToPrimary is true - either this device is a
// plain Secondary wanting to take over (e.g. the old Primary is
// unreachable), or it's resolving an active multi-Primary conflict in this
// device's favor. settings_window.go already confirms with the user
// (using different wording for each case) before calling this, so it
// proceeds directly. Guarded by tryBeginExclusiveOp since
// bootstrapper.PromoteToPrimary writes the same local role file and
// PRIMARY_MARKER.json/PRIMARY_CONFLICT.json that a concurrently-running
// RunCycle reads and writes.
func (t *trayApp) promoteToPrimary(current gui.SettingsFormData) {
	go func() {
		release, ok := t.tryBeginExclusiveOp()
		if !ok {
			gui.Info(lang.L("Sync In Progress"), lang.L("A sync is currently running. Please wait for it to finish, then try again."))
			return
		}
		defer release()

		vaultPath := strings.TrimSpace(current.VaultPath)
		if vaultPath == "" {
			gui.Info(lang.L("Vault Required"), lang.L("Please select your Obsidian Vault directory before saving."))
			t.reopenSettingsGUI(current)
			return
		}

		remoteName := strings.TrimSpace(current.RcloneRemote)
		if remoteName == "" {
			remoteName = "Vault"
		}
		remoteTarget := fmt.Sprintf("%s:%s", remoteName, strings.TrimSpace(current.RclonePath))

		hostname, _ := os.Hostname()
		bootstrapper := bootstrap.NewBootstrapper(t.cfgMgr, drive.NewClient(config.EngineLogPath()))

		err := gui.RunWithProgress(
			lang.L("Promoting to Primary"),
			lang.L("Updating Primary status on Google Drive..."),
			func() error {
				return bootstrapper.PromoteToPrimary(context.Background(), vaultPath, remoteTarget, hostname)
			},
		)
		if err != nil {
			gui.Info(lang.L("Promotion Failed"), lang.L("Could not promote this device to Primary: {{.Err}}", map[string]string{"Err": err.Error()}))
			t.reopenSettingsGUI(current)
			return
		}

		role, _ := t.cfgMgr.LoadRole()
		if role == "primary" {
			gui.Info(lang.L("Promoted to Primary"), lang.L("This device is now Primary and will run the sync engine and Google Drive backups."))
			gui.SetMenuItemLabel(t.menu, t.status, lang.L("Status: Active ({{.Role}})", map[string]string{"Role": role}))
		} else {
			// Lost a race to another device promoting at the same instant
			// (see bootstrap.PromoteToPrimary's own doc comment) - this
			// device remains Secondary instead.
			gui.Info(lang.L("Not Promoted"), lang.L("Another device became Primary at the same time. This device remains Secondary."))
		}
		t.reopenSettingsGUI(current)
	}()
}

// resolveConflicts handles the Status section's "Resolve Conflicts..."
// button (spec 3.3.2), shown whenever SettingsFormData.PendingConflictCount
// is > 0 (Primary-only - merging, and therefore genuine content conflicts,
// only ever happens there).
func (t *trayApp) resolveConflicts(current gui.SettingsFormData) {
	vaultPath := strings.TrimSpace(current.VaultPath)
	if vaultPath == "" {
		return
	}

	pending, err := t.cfgMgr.LoadPendingConflicts()
	if err != nil || len(pending) == 0 {
		gui.Info(lang.L("No Conflicts"), lang.L("There are no unresolved conflicts."))
		t.reopenSettingsGUI(current)
		return
	}

	deviceID, err := t.cfgMgr.GetDeviceID()
	if err != nil {
		gui.Info(lang.L("Resolve Conflicts Failed"), lang.L("Could not determine this device's ID: {{.Err}}", map[string]string{"Err": err.Error()}))
		return
	}
	hostname, _ := os.Hostname()

	t.resolveNextConflict(vaultPath, deviceID, hostname, pending, current)
}

// resolveNextConflict shows the resolution dialog for pending[0], then
// recurses onto the remainder once the user responds - Fyne dialogs are
// non-blocking, so a loop can't drive this sequentially. Once pending is
// empty, reopens Settings so the resolved count refreshes.
func (t *trayApp) resolveNextConflict(vaultPath, deviceID, label string, pending []config.PendingConflict, current gui.SettingsFormData) {
	if len(pending) == 0 {
		t.reopenSettingsGUI(current)
		return
	}
	conflict := pending[0]

	optionLabels := make([]string, len(conflict.Versions))
	for i, v := range conflict.Versions {
		optionLabels[i] = v.Label
		if optionLabels[i] == "" {
			optionLabels[i] = v.DeviceID
		}
	}

	gui.ChoiceN(
		lang.L("Resolve Conflict"),
		lang.L("{{.Path}}\n\nMultiple devices edited the same part of this file. Pick which version to keep (or Cancel to leave it unresolved and edit it manually in Obsidian instead):", map[string]string{"Path": conflict.RelPath}),
		optionLabels,
		func(result int) {
			if result >= 1 && result <= len(conflict.Versions) {
				chosen := conflict.Versions[result-1]
				if err := engine.ResolvePendingConflict(t.cfgMgr, vaultPath, conflict, chosen.DeviceID, deviceID, label); err != nil {
					gui.Info(
						lang.L("Resolve Conflicts Failed"),
						lang.L("Could not resolve the conflict in {{.Path}}: {{.Err}}", map[string]string{"Path": conflict.RelPath, "Err": err.Error()}),
					)
				}
			}
			// Whether resolved or left pending (Cancel), move on to the next one.
			t.resolveNextConflict(vaultPath, deviceID, label, pending[1:], current)
		},
	)
}

// checkICloudConflicts handles the tray menu's "Check for Conflicts and
// Merge..." item (spec 1.6.10, iCloud-centric Mode A only; see
// refreshCheckConflictsMenuItem for why it lives there rather than in the
// Settings window, its previous home) - a manual, on-demand scan for
// iCloud's own conflict-copy naming convention ("Name (suffix).ext"
// alongside "Name.ext"). Deliberately not run automatically: a
// false-positive match is at worst a surprising prompt the user can decline
// here, never a silent background rewrite.
func (t *trayApp) checkICloudConflicts(current gui.SettingsFormData) {
	vaultPath := strings.TrimSpace(current.VaultPath)
	if vaultPath == "" {
		return
	}

	go func() {
		release, ok := t.tryBeginExclusiveOp()
		if !ok {
			gui.Info(lang.L("Sync In Progress"), lang.L("A sync is currently running. Please wait for it to finish, then try again."))
			return
		}

		var result engine.ICloudConflictCheckResult
		err := gui.RunWithProgress(
			lang.L("Checking for Conflicts"),
			lang.L("Scanning your Vault for iCloud conflict copies..."),
			func() error {
				var checkErr error
				result, checkErr = engine.CheckAndMergeICloudConflictCopies(t.cfgMgr, vaultPath)
				return checkErr
			},
		)
		release()
		if err != nil {
			gui.Info(lang.L("Check Failed"), lang.L("Could not check for conflicts: {{.Err}}", map[string]string{"Err": err.Error()}))
			return
		}

		if result.AutoMerged == 0 && result.NeedsReview == 0 && len(result.Failed) == 0 {
			gui.Info(lang.L("No Conflicts Found"), lang.L("No iCloud conflict copies were found in your Vault."))
			return
		}

		gui.Info(lang.L("Conflict Check Complete"), lang.L(
			"Auto-merged (no overlap): {{.AutoMerged}}\nNeeds your review: {{.NeedsReview}}\nCould not be checked: {{.Failed}}",
			map[string]string{
				"AutoMerged":  strconv.Itoa(result.AutoMerged),
				"NeedsReview": strconv.Itoa(result.NeedsReview),
				"Failed":      strconv.Itoa(len(result.Failed)),
			},
		))

		if result.NeedsReview > 0 {
			// Walk straight into the same review flow "Resolve Conflicts..."
			// itself uses - these are now ordinary PendingConflicts.
			t.resolveConflicts(current)
		} else {
			t.reopenSettingsGUI(current)
		}
	}()
}

// migrateVault handles the Obsidian Vault section's "Migrate Vault to
// Local Folder..." button (spec 1.6, "Vault Migration"). Lets the user
// pick an existing Vault folder (typically one living inside iCloud
// Drive - spec 3.6.1.6 explains why that's risky) via the OS folder
// picker, then moves it into this app's own local folder.
func (t *trayApp) migrateVault(current gui.SettingsFormData) {
	gui.PickFolder(lang.L("Select the Vault Folder to Migrate"), func(oldPath string, ok bool) {
		if !ok {
			return
		}
		t.confirmAndMigrateVault(oldPath, current)
	})
}

// confirmAndMigrateVault shows the standard "Migrate Vault" confirmation
// dialog for moving oldPath into this app's own managed local folder
// (bootstrap.ManagedVaultParentDir, spec 1.6.7), then runs runVaultMigration
// if confirmed. Shared by every site that can trigger a Vault move: the
// manual "Migrate Vault to Local Folder..." button (migrateVault), the
// existing-user reminder (maybeShowICloudMigrationReminder), and Save
// Settings auto-migrating a freshly selected Vault that isn't already under
// the managed folder (see vaultNeedsAutoMigration/vaultUnderManagedRoot).
func (t *trayApp) confirmAndMigrateVault(oldPath string, current gui.SettingsFormData) {
	root, err := bootstrap.ManagedVaultParentDir()
	if err != nil {
		gui.Info(lang.L("Migration Failed"), lang.L("Could not determine your home folder: {{.Err}}", map[string]string{"Err": err.Error()}))
		return
	}
	newPath := filepath.Join(root, filepath.Base(oldPath))

	gui.ConfirmDanger(
		lang.L("Migrate Vault"),
		lang.L(
			"Move this Vault:\n{{.Old}}\n\nto:\n{{.New}}\n\nUniteVault, Obsidian, and Google Drive sync will all be updated to look for it in the new location. Continue?",
			map[string]string{"Old": oldPath, "New": newPath},
		),
		func(confirmed bool) {
			if confirmed {
				t.runVaultMigration(oldPath, newPath, current)
			}
		},
	)
}

// vaultMigrationSourceIsBridge reports whether oldPath is already exactly
// the iCloud Bridge location Vault Migration would otherwise seed a new
// copy at (bridgeParent, bootstrap.ObsidianICloudContainerRoot's return
// value, when bridgeAvailable) - a common case, since a Vault created on
// iPhone via Obsidian's own "iCloud" storage option lands exactly there.
// If so, runVaultMigration must copy oldPath rather than move it, and
// leave it in place as the Bridge going forward, rather than moving it out
// and immediately reseeding a fresh copy at the very same path - a real,
// previously-shipped bug (spec 1.6.3/1.6.7): iCloud's own daemon can
// briefly see the path vacated and then recreated and misidentify it as a
// conflict, producing a duplicate folder.
func vaultMigrationSourceIsBridge(oldPath, bridgeParent string, bridgeAvailable bool) bool {
	return bridgeAvailable && filepath.Dir(oldPath) == bridgeParent
}

// runVaultMigration does the actual work of migrateVault, once confirmed:
// moves (or, per vaultMigrationSourceIsBridge, copies) the folder, then
// best-effort updates Obsidian's own vault list and sets up the iCloud
// Bridge (spec 1.6.3) if available, then hands off to
// saveSettingsConfirmed for the usual "ensure Google Drive remote
// configured, initialize, start the daemon loop" tail - exactly as if the
// user had just typed the new Vault path in and pressed Save Settings.
func (t *trayApp) runVaultMigration(oldPath, newPath string, current gui.SettingsFormData) {
	go func() {
		release, ok := t.tryBeginExclusiveOp()
		if !ok {
			gui.Info(lang.L("Sync In Progress"), lang.L("A sync is currently running. Please wait for it to finish, then try again."))
			return
		}

		bridgeParent, bridgeAvailable := bootstrap.ObsidianICloudContainerRoot()
		sourceIsBridge := vaultMigrationSourceIsBridge(oldPath, bridgeParent, bridgeAvailable)

		err := gui.RunWithProgress(
			lang.L("Migrating Vault"),
			lang.L("Closing Obsidian (if it's currently running) and moving your Vault to its new location..."),
			func() error {
				// Best-effort: if Obsidian has the Vault open, it can hold
				// file handles that make the move fail outright (Windows)
				// or leave it pointed at a now-stale location (macOS).
				// Unconditionally targets Obsidian by name rather than
				// first checking whether this specific folder is the one
				// open in it - see QuitObsidian's own doc comment.
				bootstrap.QuitObsidian(context.Background())
				if sourceIsBridge {
					return bootstrap.CopyVaultFolder(oldPath, newPath)
				}
				return bootstrap.MoveVaultFolder(oldPath, newPath)
			},
		)
		if err != nil {
			release()
			gui.Info(lang.L("Migration Failed"), lang.L("Could not move the Vault: {{.Err}}", map[string]string{"Err": err.Error()}))
			return
		}

		if sourceIsBridge {
			// oldPath's own .sync/ (if any at all - only present if oldPath
			// was already independently functioning as a Bridge before this
			// migration) belonged to that previous life, not this copy's
			// new life as the main Vault - strip it (best-effort) so it
			// starts from a clean scan state, mirroring how a freshly
			// seeded Bridge below has the same done to it in the other
			// direction.
			_ = os.RemoveAll(filepath.Join(newPath, syncdir.Name))
		}

		var notes []string
		if _, err := obsidianconfig.UpdateVaultPath(oldPath, newPath); err != nil {
			notes = append(notes, lang.L(
				"Could not automatically update Obsidian's own vault list ({{.Err}}). Open the new folder from Obsidian manually (File > Open Vault).",
				map[string]string{"Err": err.Error()},
			))
		}

		switch {
		case sourceIsBridge:
			// oldPath already *is* the Bridge location and was never
			// touched (copy-only above) - it's already exactly what the
			// Bridge should contain, so just record it.
			cfg, err := t.cfgMgr.LoadConfig()
			if err != nil || cfg == nil {
				cfg = &config.Config{}
			}
			cfg.ICloudBridgePath = oldPath
			_ = t.cfgMgr.SaveConfig(cfg)
		case bridgeAvailable:
			bridgePath := filepath.Join(bridgeParent, filepath.Base(newPath))
			if err := bootstrap.SeedICloudBridge(newPath, bridgePath); err != nil {
				notes = append(notes, lang.L(
					"Could not set up the iCloud Bridge copy for iPhone/iPad ({{.Err}}).",
					map[string]string{"Err": err.Error()},
				))
			} else {
				// CopyDirRecursive has no exclusions, so it just copied the
				// Vault's own bookkeeping directory into the Bridge folder
				// too - strip it back out (best-effort) so ScanBridgeAndLog
				// starts the Bridge's own, independent one from scratch.
				_ = os.RemoveAll(filepath.Join(bridgePath, syncdir.Name))
				cfg, err := t.cfgMgr.LoadConfig()
				if err != nil || cfg == nil {
					cfg = &config.Config{}
				}
				cfg.ICloudBridgePath = bridgePath
				_ = t.cfgMgr.SaveConfig(cfg)
			}
		}

		release()

		if len(notes) > 0 {
			gui.Info(lang.L("Vault Moved - Action Needed"), strings.Join(notes, "\n\n"))
		}

		newData := current
		newData.VaultPath = newPath
		t.saveSettingsConfirmed(newData)
	}()
}

// saveSettings handles the "Save Settings" button: validates input, warns
// about a Vault switch that would silently overwrite a previous Vault's
// Google Drive backup, saves config.json, ensures the Google Drive remote is
// configured, and runs primary/secondary node initialization (spec
// 3.6.1.1).
func (t *trayApp) saveSettings(data gui.SettingsFormData) {
	if data.VaultPath == "" {
		gui.Info(lang.L("Vault Required"), lang.L("Please select your Obsidian Vault directory before saving."))
		return
	}

	prevCfg, prevErr := t.cfgMgr.LoadConfig()

	// A configured rclone remote actively backs up whatever Vault this
	// device currently points at. Letting a Vault change through while it's
	// still configured would silently redirect that remote's next sync onto
	// an entirely different folder's contents - remote removal must happen
	// first (an explicit, deliberate step) so there's never a moment where
	// the remote is configured but pointed at a stale/wrong Vault.
	if prevErr == nil && vaultChangeNeedsRemoteRemoval(prevCfg, data) {
		remote := strings.TrimSpace(prevCfg.RcloneRemote)
		if drive.NewClient(config.EngineLogPath()).IsRemoteConfigured(context.Background(), remote) {
			gui.Info(
				lang.L("Remove the Google Drive Remote First"),
				lang.L(
					"This device's Vault folder can't be changed while the Google Drive remote '{{.Remote}}' is still configured - the next sync would silently switch to backing up the new Vault's contents at the old one's Google Drive location.\n\nRemove the remote first (Remove Remote Configuration... button), change the Vault folder, then set the remote up again.",
					map[string]string{"Remote": remote},
				),
			)
			return
		}
	}

	// A freshly selected Vault that isn't under this app's own managed local
	// folder gets moved there automatically, the same way the manual
	// "Migrate Vault to Local Folder..." button would. Runs instead of the
	// normal save - confirmAndMigrateVault's own flow reaches
	// saveSettingsConfirmed once the move completes.
	if prevErr == nil && vaultNeedsAutoMigration(prevCfg, data) {
		t.confirmAndMigrateVault(data.VaultPath, data)
		return
	}

	proceedPastMultiDeviceCheck := func() {
		// rclone sync mirrors its destination exactly, deleting anything not
		// present in the source. If the Vault changed since the last save but
		// the Google Drive Target Folder Path didn't, the next sync would wipe
		// out the previous Vault's backed-up files. The Settings window already
		// defaults the target path to the Vault's own folder name and keeps it
		// following Vault changes (buildSettingsContent), so this only fires
		// when the user has kept (or retyped) the same target path on purpose.
		if prevErr == nil && vaultChangedWithSameTarget(prevCfg, data) {
			gui.ConfirmDanger(
				lang.L("Vault Changed - Same Backup Target"),
				lang.L(
					"You're changing the Vault from:\n{{.OldVault}}\nto:\n{{.NewVault}}\n\nbut the Google Drive Target Folder Path is still '{{.Target}}'.\n\n"+
						"Google Drive backup mirrors the Vault exactly, so the next sync will delete the previous Vault's files there and replace them with the new Vault's.\n\n"+
						"Continue with this target folder anyway?",
					map[string]string{"OldVault": prevCfg.VaultPath, "NewVault": data.VaultPath, "Target": data.RclonePath},
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

	// Changing this device's Vault folder only ever affects this device -
	// any other device sharing this Vault keeps syncing the old folder
	// regardless. If this device is Primary, changing away also means it
	// stops syncing the *shared* Vault via Google Drive (another device
	// can take over via "Promote to Primary..."). Warn (rather than
	// block outright) since knownActiveOtherDevices is a heuristic, not a
	// liveness check - it can't tell "another device is actively editing
	// this Vault right now" from "one was, a long time ago, and nobody ever
	// ran Reset Configuration on it" (spec 3.6.1.5).
	if prevErr == nil && vaultPathChanging(prevCfg, data) {
		if role, _ := t.cfgMgr.LoadRole(); role == "primary" {
			deviceID, idErr := t.cfgMgr.GetDeviceID()
			others, othersErr := knownActiveOtherDevices(prevCfg.VaultPath, deviceID)
			if idErr == nil && othersErr == nil && len(others) > 0 {
				gui.ConfirmDanger(
					lang.L("Other Devices Use This Vault"),
					lang.L("This device is Primary, and at least one other device appears to share this Vault.\n\nChanging the Vault folder here only affects this device - other devices will keep syncing the current folder, and this device will stop syncing the shared Vault via Google Drive (another device can take over via \"Promote to Primary...\").\n\nContinue changing the Vault folder anyway?"),
					func(confirmed bool) {
						if confirmed {
							proceedPastMultiDeviceCheck()
						}
					},
				)
				return
			}
		}
	}

	proceedPastMultiDeviceCheck()
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

// vaultPathChanging reports whether data would change this device's Vault
// path away from prevCfg's - the shared precondition behind
// vaultChangeNeedsRemoteRemoval and saveSettings' multi-device warning. A
// first-ever save (no prevCfg, or one that never had a Vault set) is never
// considered a change.
func vaultPathChanging(prevCfg *config.Config, data gui.SettingsFormData) bool {
	return prevCfg != nil && prevCfg.VaultPath != "" && prevCfg.VaultPath != data.VaultPath
}

// vaultChangeNeedsRemoteRemoval reports whether data would change this
// device's Vault path away from prevCfg's while prevCfg still names an
// rclone remote - the precondition for saveSettings to require removing
// that remote first (see its call site). Doesn't itself check whether the
// remote is actually configured in rclone - the caller does that, since it
// requires shelling out.
func vaultChangeNeedsRemoteRemoval(prevCfg *config.Config, data gui.SettingsFormData) bool {
	return vaultPathChanging(prevCfg, data) && strings.TrimSpace(prevCfg.RcloneRemote) != ""
}

// vaultNeedsAutoMigration reports whether saveSettings should redirect a
// freshly selected Vault (data.VaultPath) into the same automatic move the
// manual "Migrate Vault to Local Folder..." button performs, rather than
// saving it as-is. True for first-time setup or a changed selection
// (Select Folder is disabled once a remote is configured, so this never
// fires for an already-configured device's unrelated settings changes)
// whose path isn't already under bootstrap.ManagedVaultParentDir - see
// vaultUnderManagedRoot's doc comment for why "outside the managed folder"
// replaced a growing per-service (iCloud Drive, ...) detection list.
func vaultNeedsAutoMigration(prevCfg *config.Config, data gui.SettingsFormData) bool {
	// Mode A/D's Vault (spec 1.6.10) lives permanently wherever iCloud or
	// the user's Google Drive desktop app already placed it - auto-
	// migrating it into ~/Obsidian/ the way an unmanaged Mode B/C Vault
	// gets moved would fight the very sync mechanism these modes rely on,
	// so it must never fire here.
	if syncModeManagesOwnVaultLocation(lockedSyncMode(prevCfg, data)) {
		return false
	}
	freshSelection := prevCfg == nil || prevCfg.VaultPath == "" || prevCfg.VaultPath != data.VaultPath
	return freshSelection && !vaultUnderManagedRoot(data.VaultPath)
}

// lockedSyncMode returns the SyncMode a save should actually persist: once
// prevCfg has ever recorded one, it permanently overrides whatever data
// carries - spec 1.6.10 fixes the sync mode at first setup with no
// switching in v1, and the Settings window itself stops offering the
// selector once a Vault is configured (see SettingsFormData.SyncMode). Only
// a first-ever save (no prevCfg, or one saved before SyncMode existed) uses
// data's own selection, falling back to SyncModeDrive if even that is
// unset - the same default config.EffectiveSyncMode applies everywhere
// else.
func lockedSyncMode(prevCfg *config.Config, data gui.SettingsFormData) config.SyncMode {
	if prevCfg != nil && prevCfg.SyncMode != "" {
		return prevCfg.SyncMode
	}
	if data.SyncMode != "" {
		return config.SyncMode(data.SyncMode)
	}
	return config.SyncModeDrive
}

// buildSaveConfig constructs the config.Config that saveSettingsConfirmed
// persists from the Settings form data, carrying ICloudBridgePath forward
// from prevCfg (spec 1.6.3) - it's not a field on the form itself (only
// Vault Migration ever sets it), so an ordinary Save Settings must never
// silently wipe it back to "". A real, previously-shipped bug came from
// building this config inline without doing so.
func buildSaveConfig(prevCfg *config.Config, data gui.SettingsFormData) *config.Config {
	var icloudBridgePath string
	if prevCfg != nil {
		icloudBridgePath = prevCfg.ICloudBridgePath
	}
	return &config.Config{
		VaultPath:           data.VaultPath,
		RcloneRemote:        data.RcloneRemote,
		RclonePath:          data.RclonePath,
		IntervalSeconds:     data.IntervalSeconds,
		SyncMode:            lockedSyncMode(prevCfg, data),
		ICloudBridgePath:    icloudBridgePath,
		ExtraExcludes:       parseExtraExcludes(data.ExtraExcludes),
		LogIncludeFilenames: data.LogIncludeFilenames,
	}
}

// parseExtraExcludes splits the Settings window's multi-line "Exclude from
// Backup" text (gui.SettingsFormData.ExtraExcludes) into individual rclone
// --exclude patterns, one per line, dropping blank lines so an empty field
// (or trailing blank lines) round-trips to a nil/empty slice rather than a
// slice of empty strings (which rclone would reject as an invalid pattern).
func parseExtraExcludes(text string) []string {
	var excludes []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			excludes = append(excludes, line)
		}
	}
	return excludes
}

// clearRemoteConfig removes the (now possibly stale) rclone remote name
// and target path from config.json, without touching any other field -
// called after successfully removing an rclone remote itself, so the app
// falls into the well-defined "no remote configured" state (spec 1.6.4)
// instead of every following sync cycle retrying, and failing, against a
// remote that no longer exists. A real, previously-shipped bug came from
// "Remove Remote Configuration..." only removing the rclone-level remote
// and never this.
func clearRemoteConfig(cfgMgr *config.ConfigManager) {
	cfg, err := cfgMgr.LoadConfig()
	if err != nil || cfg == nil {
		return
	}
	cfg.RcloneRemote = ""
	cfg.RclonePath = ""
	_ = cfgMgr.SaveConfig(cfg)
}

// pathIsUnder reports whether path is root itself or somewhere inside it -
// the precondition behind maybeShowICloudMigrationReminder's "is the Vault
// inside iCloud Drive" check (spec 1.6.1/1.6.7). filepath.Rel returns a
// ".."-prefixed (or exactly "..") result for anything outside root, and a
// plain relative path (including "." for an exact match) for anything at
// or under it.
func pathIsUnder(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// knownActiveOtherDevices returns every device (other than selfDeviceID)
// that this Vault's event log has ever heard from and that hasn't since
// explicitly decommissioned itself (spec 3.6.1.5) - the signal behind both
// MultiDeviceStatus and the Primary-only warnings on changing the Vault
// folder or running Reset Configuration. It's a heuristic, not a liveness
// check: a device that's simply offline (powered off, never reset) still
// counts as "active" here, since there's no way to tell that apart from one
// still in daily use. Only ever reports PCs (Mac/Windows) - iPhone/iPad
// never run this app at all (spec 1.4), so they never write an event log
// and can never appear here.
func knownActiveOtherDevices(vaultPath, selfDeviceID string) (map[string]eventlog.EventEntry, error) {
	latest, err := eventlog.NewManager(vaultPath).LatestEventForEachDevice()
	if err != nil {
		return nil, err
	}
	for id, entry := range latest {
		if id == selfDeviceID || entry.Event == eventlog.EventDeviceDecommissioned {
			delete(latest, id)
		}
	}
	return latest, nil
}

// decommissionSelf tells every other device that this one is deliberately
// leaving vaultPath, by appending EventDeviceDecommissioned to its own
// event log (see EventDeviceDecommissioned, knownActiveOtherDevices).
// Errors are deliberately swallowed by the caller - a failed decommission
// write must never block Reset Configuration itself from proceeding.
func decommissionSelf(cfgMgr *config.ConfigManager, vaultPath, label string) {
	deviceID, err := cfgMgr.GetDeviceID()
	if err != nil {
		return
	}
	_ = eventlog.NewManager(vaultPath).Append(deviceID, label, eventlog.EventDeviceDecommissioned, nil)
}

// saveSettingsConfirmed does the actual work of saveSettings, once any
// Vault-change warning has been confirmed (or didn't apply).
func (t *trayApp) saveSettingsConfirmed(data gui.SettingsFormData) {
	go func() {
		release, ok := t.tryBeginExclusiveOp()
		if !ok {
			gui.Info(lang.L("Sync In Progress"), lang.L("A sync is currently running. Please wait for it to finish, then try Save Settings again."))
			return
		}
		defer release()

		prevCfg, _ := t.cfgMgr.LoadConfig()
		mode := lockedSyncMode(prevCfg, data)

		// Mode D (spec 1.6.10): the Vault's cross-device consistency is
		// entirely the user's own Google Drive desktop app's job - this
		// app never touches Google Drive itself in this mode (no rclone
		// remote, no Primary/Secondary election), so none of the usual
		// "ensure a remote is configured, then initialize" work applies.
		gdriveDesktopMode := mode == config.SyncModeGDriveDesktop

		var driveClient *drive.Client
		if !gdriveDesktopMode {
			driveClient = drive.NewClient(config.EngineLogPath())

			if !driveClient.IsRemoteConfigured(context.Background(), data.RcloneRemote) {
				choiceCh := make(chan int, 1)
				gui.Choice(
					lang.L("Configure Google Drive Remote"),
					lang.L("Google Drive remote '{{.Remote}}' is not configured yet.\n\nChoose how you'd like to set it up:", map[string]string{"Remote": data.RcloneRemote}),
					lang.L("New Setup (Recommended)"),
					lang.L("Existing / CLI Config"),
					func(choice int) { choiceCh <- choice },
				)

				switch <-choiceCh {
				case 1:
					err := gui.RunWithProgress(
						lang.L("Google Drive Setup"),
						lang.L("Opening your browser for Google Drive authentication...\nThis can take up to a minute or two before the browser tab appears - please wait. Then grant permissions and return here."),
						func() error { return driveClient.CreateGoogleDriveRemote(context.Background(), data.RcloneRemote) },
					)
					if err != nil || !driveClient.IsRemoteConfigured(context.Background(), data.RcloneRemote) {
						gui.Info(lang.L("Setup Failed"), lang.L("Automatic setup did not complete. Launching a terminal for manual configuration; please retry Save afterwards."))
						_ = bootstrap.LaunchTerminalRcloneConfig(driveClient.GetBinaryPath())
						return
					}
				case 2:
					_ = bootstrap.LaunchTerminalRcloneConfig(driveClient.GetBinaryPath())
					gui.Info(lang.L("Terminal Launched"), lang.L("Complete the rclone configuration in the opened terminal window, then press Save Settings again."))
					return
				default:
					return
				}
			}
		}

		newCfg := buildSaveConfig(prevCfg, data)
		if err := t.cfgMgr.SaveConfig(newCfg); err != nil {
			gui.Info(lang.L("Save Failed"), lang.L("Failed to save configuration: {{.Err}}", map[string]string{"Err": err.Error()}))
			return
		}

		var newRole string
		var remoteTarget string
		if gdriveDesktopMode {
			newRole = lang.L("N/A (Google Drive app handles sync)")
		} else {
			remoteTarget = fmt.Sprintf("%s:%s", data.RcloneRemote, data.RclonePath)

			// Primary/Secondary election (spec 3.6.1.1) applies in both
			// remaining sync modes: iCloud-centric (Mode A) still needs
			// exactly one device publishing to Google Drive, so downstream
			// consumers of that backup (e.g. feeding it to an external
			// analysis tool) always see one canonical, unambiguous copy
			// rather than whichever of several independently-writing
			// devices happened to sync last (spec 1.6.10). InitializeNode's
			// Secondary path never reads Vault content back from Drive -
			// only bookkeeping (PRIMARY_MARKER.json, an empty per-device
			// log file) - so it's just as safe to run here as it always
			// was for Drive mode.
			hostname, _ := os.Hostname()
			bootstrapper := bootstrap.NewBootstrapper(t.cfgMgr, driveClient)
			err := gui.RunWithProgress(
				lang.L("Initializing UniteVault"),
				lang.L("Determining Primary/Secondary role and syncing initial state with Google Drive..."),
				func() error {
					var initErr error
					newRole, initErr = bootstrapper.InitializeNode(context.Background(), data.VaultPath, remoteTarget, hostname)
					return initErr
				},
			)
			if err != nil {
				gui.Info(lang.L("Initialization Failed"), lang.L("UniteVault could not finish initializing: {{.Err}}", map[string]string{"Err": err.Error()}))
				return
			}
		}

		gui.SetMenuItemLabel(t.menu, t.status, lang.L("Status: Active ({{.Role}})", map[string]string{"Role": newRole}))
		t.refreshCheckConflictsMenuItem()
		if gdriveDesktopMode {
			gui.Info(lang.L("UniteVault Configured"), lang.L(
				"Settings saved successfully!\n\nVault: {{.Vault}}\nSync Interval: {{.Interval}} seconds\n\nGoogle Drive's own desktop app handles syncing this Vault across your devices - UniteVault won't sync it itself.",
				map[string]any{"Vault": data.VaultPath, "Interval": data.IntervalSeconds},
			))
		} else {
			gui.Info(lang.L("UniteVault Configured"), lang.L(
				"Settings saved successfully!\n\nVault: {{.Vault}}\nRemote Target: {{.Target}}\nSync Interval: {{.Interval}} seconds\nRole: {{.Role}}",
				map[string]any{"Vault": data.VaultPath, "Target": remoteTarget, "Interval": data.IntervalSeconds, "Role": newRole},
			))
		}
		gui.HideWindow()

		t.startDaemonLoop(newCfg)
	}()
}

func handleInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	vaultPath := fs.String("vault", "", "Path to Obsidian Vault directory")
	remoteName := fs.String("remote", "Vault", "rclone remote name")
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
		IntervalSeconds: config.DefaultIntervalSeconds,
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

	// See runDaemonLoop's identical MkdirAll+watch.New comment - the same
	// reasoning applies to this CLI daemon mode.
	_ = os.MkdirAll(cfg.VaultPath, 0755)
	if w, err := watch.New(cfg.VaultPath); err == nil {
		eng.SetWatcher(w)
		defer w.Close()
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
