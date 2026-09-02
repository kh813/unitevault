package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"
	"github.com/kh813/unitevault/internal/bootstrap"
	"github.com/kh813/unitevault/internal/config"
	"github.com/kh813/unitevault/internal/drive"
	"github.com/kh813/unitevault/internal/engine"
	"github.com/kh813/unitevault/internal/eventlog"
	"github.com/kh813/unitevault/internal/gui"
)

// TestMain loads this app's own translation bundle once before any test
// runs. buildFormData (exercised directly by these tests, without going
// through gui.InitApp/runTrayMode) calls lang.L(...) - without this, every
// such call on a machine whose OS locale is Japanese would find Fyne's own
// built-in "ja" bundle already registered (loaded by the lang package's
// own init()) but none of this app's custom strings in it, logging a noisy
// (harmless - it still falls back to English) "Translation failure" for
// every single one.
func TestMain(m *testing.M) {
	if err := gui.LoadTranslations(); err != nil {
		fyne.LogError("failed to load UI translations for tests", err)
	}
	// gui.SetMenuItemLabel (used by runCycleGuarded) goes through fyne.Do,
	// which requires a current Fyne app to be set - without this, any test
	// that exercises runCycleGuarded's status-label updates panics.
	test.NewApp()
	os.Exit(m.Run())
}

// newTestTrayApp returns a trayApp wired to an isolated, temporary config
// directory so tests never touch the real ~/.unitevault. ctx/cancel are set
// to a real, cancellable context (mirroring what runTrayMode wires up)
// since startDaemonLoop derives its loop context from t.ctx - leaving it
// nil would panic any test that exercises startDaemonLoop.
func newTestTrayApp(t *testing.T) *trayApp {
	t.Helper()
	cfgMgr := config.NewConfigManagerWithDir(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &trayApp{cfgMgr: cfgMgr, ctx: ctx, cancel: cancel}
}

// noopDriveRunner is a minimal drive.RcloneRunner stub for tests that need
// to exercise engine.SyncEngine.RunCycle without touching a real rclone
// binary or network. FileExists always reports "not found" so RunCycle
// takes the primary-initialization path; the tests below only care whether
// RunCycle started at all (observable via the device_id file), not whether
// it completes successfully.
type noopDriveRunner struct{}

func (noopDriveRunner) Sync(ctx context.Context, srcPath, remoteTarget string, excludes ...string) error {
	return nil
}
func (noopDriveRunner) Copy(ctx context.Context, remoteSrc, dstPath string, excludes ...string) error {
	return nil
}
func (noopDriveRunner) FileExists(ctx context.Context, remoteTargetFile string) (bool, error) {
	return false, nil
}
func (noopDriveRunner) DownloadFile(ctx context.Context, remoteSourceFile, localDstFile string) error {
	return nil
}
func (noopDriveRunner) UploadFile(ctx context.Context, localSrcFile, remoteTargetFile string) error {
	return nil
}
func (noopDriveRunner) DeleteFile(ctx context.Context, remoteTargetFile string) error { return nil }

var _ drive.RcloneRunner = noopDriveRunner{}

// TestTryBeginExclusiveOp_SerializesAccess is the core regression test for
// the busy-guard mechanism: a second caller must be refused while the
// first still holds the lock, and must succeed again once released.
func TestTryBeginExclusiveOp_SerializesAccess(t *testing.T) {
	tr := newTestTrayApp(t)

	release1, ok1 := tr.tryBeginExclusiveOp()
	if !ok1 {
		t.Fatal("expected the first call to acquire successfully")
	}

	if _, ok2 := tr.tryBeginExclusiveOp(); ok2 {
		t.Fatal("expected a second concurrent call to fail while the first is still held")
	}

	release1()

	release3, ok3 := tr.tryBeginExclusiveOp()
	if !ok3 {
		t.Fatal("expected a call after release to succeed")
	}
	release3()
}

// TestStartDaemonLoop_CancelsPreviousLoop is the regression test for the
// duplicate-daemon-loop bug: starting a new loop must cancel any
// previously-running one rather than leaving it running alongside the new
// one forever (see the trayApp.daemonMu doc comment).
func TestStartDaemonLoop_CancelsPreviousLoop(t *testing.T) {
	tr := newTestTrayApp(t)
	// runDaemonLoop now runs its first cycle immediately (not just on the
	// ticker's first tick), which calls gui.SetMenuItemLabel - needs real
	// menu items set, matching TestRunCycleGuarded_*'s own setup, or that
	// call panics on a nil tr.status/tr.menu.
	tr.status = fyne.NewMenuItem("", nil)
	tr.menu = fyne.NewMenu("", tr.status)

	var oldCancelled bool
	tr.daemonMu.Lock()
	tr.daemonCancel = func() { oldCancelled = true }
	tr.daemonMu.Unlock()

	cfg := &config.Config{VaultPath: filepath.Join(t.TempDir(), "Vault"), IntervalSeconds: 3600}
	tr.startDaemonLoop(cfg)

	if !oldCancelled {
		t.Error("expected startDaemonLoop to cancel the previously-running loop before starting a new one")
	}

	tr.daemonMu.Lock()
	newCancel := tr.daemonCancel
	tr.daemonMu.Unlock()
	if newCancel == nil {
		t.Fatal("expected a new daemonCancel to be installed")
	}
	newCancel() // stop the real loop goroutine startDaemonLoop just spawned

	// runDaemonLoop's immediate first cycle (see its own doc comment) isn't
	// gated on ctx, so it still runs (and still touches cfg.VaultPath,
	// under t.TempDir()) even though the loop was cancelled above -
	// without waiting for it to actually finish, it can still be running
	// when this test returns and t.TempDir()'s cleanup tries to remove the
	// same tree out from under it. A short grace period lets the goroutine
	// actually get scheduled and acquire cycleMu; the bounded poll after
	// it then waits for that cycle to release cycleMu (i.e. finish) no
	// matter how long the real rclone/network calls inside it take, up to
	// a generous timeout.
	time.Sleep(20 * time.Millisecond)
	deadline := time.Now().Add(5 * time.Second)
	for !tr.cycleMu.TryLock() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the daemon loop's background cycle to finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	tr.cycleMu.Unlock()
}

// TestRefreshCheckConflictsMenuItem_EnabledOnlyForICloudModeWithVault guards
// the tray menu's "Check for Conflicts and Merge" item (spec 1.6.10,
// moved here from the Settings window): it should only ever be actionable
// in iCloud-centric Mode A, once a Vault is actually configured to scan.
func TestRefreshCheckConflictsMenuItem_EnabledOnlyForICloudModeWithVault(t *testing.T) {
	tr := newTestTrayApp(t)
	tr.checkConflicts = fyne.NewMenuItem("Check for Conflicts and Merge", nil)
	tr.menu = fyne.NewMenu("", tr.checkConflicts)

	// No config saved yet at all.
	tr.refreshCheckConflictsMenuItem()
	if !tr.checkConflicts.Disabled {
		t.Error("expected the item to stay disabled before any Vault is configured")
	}

	if err := tr.cfgMgr.SaveConfig(&config.Config{VaultPath: "/tmp/vault", SyncMode: config.SyncModeDrive}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	tr.refreshCheckConflictsMenuItem()
	if !tr.checkConflicts.Disabled {
		t.Error("expected the item to stay disabled in Drive mode, which has no iCloud conflict-copy convention to scan for")
	}

	if err := tr.cfgMgr.SaveConfig(&config.Config{VaultPath: "/tmp/vault", SyncMode: config.SyncModeICloud}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	tr.refreshCheckConflictsMenuItem()
	if tr.checkConflicts.Disabled {
		t.Error("expected the item to be enabled once iCloud mode has a configured Vault")
	}
}

// TestMaybeCheckForUpdatePeriodically_SkipsWhenRecentlyChecked guards the
// periodic background update check's cadence gate: it must not attempt a
// network call (or touch the recorded time) again until updateCheckInterval
// has actually elapsed since the last recorded check - otherwise this test
// would hang/fail trying to reach GitHub in a sandboxed test run.
func TestMaybeCheckForUpdatePeriodically_SkipsWhenRecentlyChecked(t *testing.T) {
	tr := newTestTrayApp(t)
	// Truncated to a whole second and stripped of its monotonic reading
	// (time.Now()'s own) to match RFC3339's on-disk precision - LastUpdateCheck
	// round-trips through that format, so comparing against the untruncated
	// original would spuriously fail even when nothing is actually wrong.
	recent := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := tr.cfgMgr.SaveLastUpdateCheck(recent); err != nil {
		t.Fatalf("SaveLastUpdateCheck: %v", err)
	}

	tr.maybeCheckForUpdatePeriodically()

	got, err := tr.cfgMgr.LoadLastUpdateCheck()
	if err != nil {
		t.Fatalf("LoadLastUpdateCheck: %v", err)
	}
	if !got.Equal(recent) {
		t.Errorf("expected the recorded check time to stay unchanged (no check attempted) when still within updateCheckInterval, got %v want %v", got, recent)
	}
}

func TestStopDaemonLoop_CancelsAndClears(t *testing.T) {
	tr := newTestTrayApp(t)

	var cancelled bool
	tr.daemonMu.Lock()
	tr.daemonCancel = func() { cancelled = true }
	tr.daemonMu.Unlock()

	tr.stopDaemonLoop()

	if !cancelled {
		t.Error("expected stopDaemonLoop to cancel the running loop")
	}
	tr.daemonMu.Lock()
	got := tr.daemonCancel
	tr.daemonMu.Unlock()
	if got != nil {
		t.Error("expected daemonCancel to be cleared to nil after stopDaemonLoop")
	}
}

func TestStopDaemonLoop_NoOpWhenNoneRunning(t *testing.T) {
	tr := newTestTrayApp(t)
	tr.stopDaemonLoop() // must not panic when no loop is running
}

// TestRunCycleGuarded_SkipsWhenBusy verifies that a cycle never starts
// while cycleMu is already held (e.g. by another cycle, or by a
// destructive config change in progress) - proven by the fact that
// RunCycle's very first action, GetDeviceID, never runs.
func TestRunCycleGuarded_SkipsWhenBusy(t *testing.T) {
	tr := newTestTrayApp(t)
	tr.status = fyne.NewMenuItem("", nil)
	tr.menu = fyne.NewMenu("", tr.status)
	vaultPath := filepath.Join(t.TempDir(), "Vault")
	eng := engine.NewSyncEngine(tr.cfgMgr, vaultPath, "test-device", noopDriveRunner{})

	release, ok := tr.tryBeginExclusiveOp()
	if !ok {
		t.Fatal("expected to acquire the lock for test setup")
	}
	defer release()

	tr.runCycleGuarded(context.Background(), eng, false)

	if _, err := os.Stat(tr.cfgMgr.DeviceIDPath()); err == nil {
		t.Error("expected RunCycle to never start (no device_id file created) while cycleMu is already held")
	}
}

// TestRunCycleGuarded_RunsWhenFree is the companion case: with no
// exclusive op in progress, runCycleGuarded must actually invoke RunCycle.
func TestRunCycleGuarded_RunsWhenFree(t *testing.T) {
	tr := newTestTrayApp(t)
	tr.status = fyne.NewMenuItem("", nil)
	tr.menu = fyne.NewMenu("", tr.status)
	vaultPath := filepath.Join(t.TempDir(), "Vault")
	eng := engine.NewSyncEngine(tr.cfgMgr, vaultPath, "test-device", noopDriveRunner{})

	tr.runCycleGuarded(context.Background(), eng, false)

	if _, err := os.Stat(tr.cfgMgr.DeviceIDPath()); err != nil {
		t.Error("expected RunCycle to have started (device_id file created) when cycleMu was free")
	}
}

func TestBuildFormData_DefaultsWhenUnconfigured(t *testing.T) {
	tr := newTestTrayApp(t)

	data := tr.buildFormData()

	if data.DeviceRole != "N/A" {
		t.Errorf("expected DeviceRole 'N/A', got %q", data.DeviceRole)
	}
	if data.VaultPath != "" {
		t.Errorf("expected empty VaultPath before any config is saved, got %q", data.VaultPath)
	}
	if data.RcloneRemote != "Vault" {
		t.Errorf("expected default RcloneRemote 'Vault', got %q", data.RcloneRemote)
	}
	if data.RclonePath != "VaultBackup" {
		t.Errorf("expected default RclonePath 'VaultBackup', got %q", data.RclonePath)
	}
	if data.IntervalSeconds != config.DefaultIntervalSeconds {
		t.Errorf("expected default IntervalSeconds %d, got %d", config.DefaultIntervalSeconds, data.IntervalSeconds)
	}
	// GitStatus/RcloneStatus reflect this machine's actual PATH, so we only
	// assert they're populated with one of the two known values rather than
	// a specific one (the CI/dev environment may or may not have either
	// tool installed).
	if data.GitStatus != "Installed" && data.GitStatus != "Not Found" {
		t.Errorf("expected GitStatus to be Installed or Not Found, got %q", data.GitStatus)
	}
	if data.RcloneStatus != "Installed" && data.RcloneStatus != "Not Found" {
		t.Errorf("expected RcloneStatus to be Installed or Not Found, got %q", data.RcloneStatus)
	}
}

func TestBuildFormData_ReflectsSavedConfig(t *testing.T) {
	tr := newTestTrayApp(t)

	if err := tr.cfgMgr.SaveConfig(&config.Config{
		VaultPath:       "/tmp/my-vault",
		RcloneRemote:    "myremote",
		RclonePath:      "Backups/Obsidian",
		IntervalSeconds: 300,
	}); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}
	if err := tr.cfgMgr.SaveRole("primary"); err != nil {
		t.Fatalf("failed to save role: %v", err)
	}

	data := tr.buildFormData()

	if data.VaultPath != "/tmp/my-vault" {
		t.Errorf("expected VaultPath to round-trip, got %q", data.VaultPath)
	}
	if data.RcloneRemote != "myremote" {
		t.Errorf("expected RcloneRemote to round-trip, got %q", data.RcloneRemote)
	}
	if data.RclonePath != "Backups/Obsidian" {
		t.Errorf("expected RclonePath to round-trip, got %q", data.RclonePath)
	}
	if data.IntervalSeconds != 300 {
		t.Errorf("expected IntervalSeconds to round-trip, got %d", data.IntervalSeconds)
	}
	if data.DeviceRole != "primary" {
		t.Errorf("expected DeviceRole 'primary', got %q", data.DeviceRole)
	}
}

// TestBuildFormData_ICloudMode_DriveSyncStatusUsesSameRoleLogicAsDriveMode
// guards spec 1.6.10: iCloud mode elects a Primary/Secondary exactly like
// Drive mode does (Google Drive needs exactly one canonical publisher
// there too, not every device racing to overwrite the same backup), so
// buildFormData's role-based driveSyncStatus derivation needs no
// mode-specific branching at all - a Primary's recorded backup status
// surfaces the same way in both modes.
func TestBuildFormData_ICloudMode_DriveSyncStatusUsesSameRoleLogicAsDriveMode(t *testing.T) {
	tr := newTestTrayApp(t)

	if err := tr.cfgMgr.SaveConfig(&config.Config{
		VaultPath:    "/tmp/my-vault",
		RcloneRemote: "myremote",
		RclonePath:   "Backup",
		SyncMode:     config.SyncModeICloud,
	}); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}
	if err := tr.cfgMgr.SaveRole("primary"); err != nil {
		t.Fatalf("failed to save role: %v", err)
	}

	if err := tr.cfgMgr.SaveDriveSyncStatus(config.DriveSyncStatus{
		Time:    "2026-08-25T15:04:00+09:00",
		Success: true,
	}); err != nil {
		t.Fatalf("failed to save drive sync status: %v", err)
	}

	data := tr.buildFormData()

	if data.DriveSyncStatus == "" || data.DriveSyncStatus == lang.L("N/A (not configured yet)") {
		t.Errorf("expected the recorded backup status to surface for an iCloud-mode Primary, got %q", data.DriveSyncStatus)
	}
}

func TestBuildFormData_IgnoresZeroOrNegativeSavedInterval(t *testing.T) {
	tr := newTestTrayApp(t)

	if err := tr.cfgMgr.SaveConfig(&config.Config{
		VaultPath:       "/tmp/my-vault",
		IntervalSeconds: 0,
	}); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	data := tr.buildFormData()

	if data.IntervalSeconds != config.DefaultIntervalSeconds {
		t.Errorf("expected a zero saved interval to fall back to the default %d, got %d", config.DefaultIntervalSeconds, data.IntervalSeconds)
	}
}

// TestVaultChangedWithSameTarget guards the data-loss warning shown when
// Save Settings would point a different Vault at the same Google Drive
// backup target as before: rclone sync mirrors its destination exactly, so
// that combination would silently delete the previous Vault's backed-up
// files on the next sync.
func TestVaultChangedWithSameTarget(t *testing.T) {
	cases := []struct {
		name    string
		prevCfg *config.Config
		data    gui.SettingsFormData
		want    bool
	}{
		{
			name:    "no previous config at all",
			prevCfg: nil,
			data:    gui.SettingsFormData{VaultPath: "/vaults/A", RclonePath: "A"},
			want:    false,
		},
		{
			name:    "previous config never had a Vault set (first save)",
			prevCfg: &config.Config{RclonePath: "A"},
			data:    gui.SettingsFormData{VaultPath: "/vaults/A", RclonePath: "A"},
			want:    false,
		},
		{
			name:    "same Vault, same target - an ordinary re-save",
			prevCfg: &config.Config{VaultPath: "/vaults/A", RclonePath: "A"},
			data:    gui.SettingsFormData{VaultPath: "/vaults/A", RclonePath: "A"},
			want:    false,
		},
		{
			name:    "Vault changed, target followed along - the safe/expected case",
			prevCfg: &config.Config{VaultPath: "/vaults/A", RclonePath: "A"},
			data:    gui.SettingsFormData{VaultPath: "/vaults/B", RclonePath: "B"},
			want:    false,
		},
		{
			name:    "Vault changed but target path did not - the dangerous case",
			prevCfg: &config.Config{VaultPath: "/vaults/A", RclonePath: "A"},
			data:    gui.SettingsFormData{VaultPath: "/vaults/B", RclonePath: "A"},
			want:    true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := vaultChangedWithSameTarget(c.prevCfg, c.data); got != c.want {
				t.Errorf("vaultChangedWithSameTarget(%+v, %+v) = %v, want %v", c.prevCfg, c.data, got, c.want)
			}
		})
	}
}

// TestVaultChangeNeedsRemoteRemoval guards the block on changing this
// device's Vault folder while it still names an rclone remote: doing so
// would let a configured remote silently start backing up an entirely
// different Vault at its old target (saveSettings requires removing the
// remote first instead - see vaultChangeNeedsRemoteRemoval's doc comment).
func TestVaultChangeNeedsRemoteRemoval(t *testing.T) {
	cases := []struct {
		name    string
		prevCfg *config.Config
		data    gui.SettingsFormData
		want    bool
	}{
		{
			name:    "no previous config at all",
			prevCfg: nil,
			data:    gui.SettingsFormData{VaultPath: "/vaults/A", RcloneRemote: "ObsidianVault"},
			want:    false,
		},
		{
			name:    "previous config never had a Vault set (first save)",
			prevCfg: &config.Config{RcloneRemote: "ObsidianVault"},
			data:    gui.SettingsFormData{VaultPath: "/vaults/A", RcloneRemote: "ObsidianVault"},
			want:    false,
		},
		{
			name:    "same Vault - an ordinary re-save",
			prevCfg: &config.Config{VaultPath: "/vaults/A", RcloneRemote: "ObsidianVault"},
			data:    gui.SettingsFormData{VaultPath: "/vaults/A", RcloneRemote: "ObsidianVault"},
			want:    false,
		},
		{
			name:    "Vault changed, no remote named in the previous config",
			prevCfg: &config.Config{VaultPath: "/vaults/A", RcloneRemote: ""},
			data:    gui.SettingsFormData{VaultPath: "/vaults/B", RcloneRemote: "ObsidianVault"},
			want:    false,
		},
		{
			name:    "Vault changed while a remote was named - the dangerous case",
			prevCfg: &config.Config{VaultPath: "/vaults/A", RcloneRemote: "ObsidianVault"},
			data:    gui.SettingsFormData{VaultPath: "/vaults/B", RcloneRemote: "ObsidianVault"},
			want:    true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := vaultChangeNeedsRemoteRemoval(c.prevCfg, c.data); got != c.want {
				t.Errorf("vaultChangeNeedsRemoteRemoval(%+v, %+v) = %v, want %v", c.prevCfg, c.data, got, c.want)
			}
		})
	}
}

// TestVaultNeedsAutoMigration guards the rule behind Save Settings
// auto-migrating a freshly selected Vault (spec 1.6.7): only a genuinely
// new selection (first-time setup, or a changed path) outside the managed
// folder should trigger it - an ordinary re-save of an unrelated setting,
// or a Vault that's already managed, must not.
func TestVaultNeedsAutoMigration(t *testing.T) {
	root, err := bootstrap.ManagedVaultParentDir()
	if err != nil {
		t.Fatalf("ManagedVaultParentDir failed: %v", err)
	}
	managedPath := filepath.Join(root, "Vault")
	unmanagedPath := filepath.Join(filepath.Dir(root), "Documents", "Vault")

	cases := []struct {
		name    string
		prevCfg *config.Config
		data    gui.SettingsFormData
		want    bool
	}{
		{
			name:    "first-time setup, outside the managed folder",
			prevCfg: &config.Config{},
			data:    gui.SettingsFormData{VaultPath: unmanagedPath},
			want:    true,
		},
		{
			name:    "first-time setup, already under the managed folder",
			prevCfg: &config.Config{},
			data:    gui.SettingsFormData{VaultPath: managedPath},
			want:    false,
		},
		{
			name:    "changed selection, outside the managed folder",
			prevCfg: &config.Config{VaultPath: managedPath},
			data:    gui.SettingsFormData{VaultPath: unmanagedPath},
			want:    true,
		},
		{
			name:    "ordinary re-save of the same, unmanaged Vault",
			prevCfg: &config.Config{VaultPath: unmanagedPath},
			data:    gui.SettingsFormData{VaultPath: unmanagedPath},
			want:    false,
		},
		{
			name:    "ordinary re-save of an already-managed Vault",
			prevCfg: &config.Config{VaultPath: managedPath},
			data:    gui.SettingsFormData{VaultPath: managedPath},
			want:    false,
		},
		{
			name:    "first-time setup, iCloud mode selected, outside the managed folder",
			prevCfg: &config.Config{},
			data:    gui.SettingsFormData{VaultPath: unmanagedPath, SyncMode: "icloud"},
			want:    false,
		},
		{
			name:    "already-locked to iCloud mode, outside the managed folder",
			prevCfg: &config.Config{VaultPath: unmanagedPath, SyncMode: config.SyncModeICloud},
			data:    gui.SettingsFormData{VaultPath: unmanagedPath, SyncMode: "icloud"},
			want:    false,
		},
		{
			name:    "first-time setup, gdrive_desktop mode selected, outside the managed folder",
			prevCfg: &config.Config{},
			data:    gui.SettingsFormData{VaultPath: unmanagedPath, SyncMode: "gdrive_desktop"},
			want:    false,
		},
		{
			name:    "already-locked to gdrive_desktop mode, outside the managed folder",
			prevCfg: &config.Config{VaultPath: unmanagedPath, SyncMode: config.SyncModeGDriveDesktop},
			data:    gui.SettingsFormData{VaultPath: unmanagedPath, SyncMode: "gdrive_desktop"},
			want:    false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := vaultNeedsAutoMigration(c.prevCfg, c.data); got != c.want {
				t.Errorf("vaultNeedsAutoMigration(%+v, %+v) = %v, want %v", c.prevCfg, c.data, got, c.want)
			}
		})
	}
}

// TestLockedSyncMode guards spec 1.6.10's "fixed at setup, no switching in
// v1" rule at the persistence layer itself, not just via the Settings
// window hiding the selector - a prior save's SyncMode must always win once
// set, regardless of what a later (buggy or malicious) form snapshot
// carries.
func TestLockedSyncMode(t *testing.T) {
	cases := []struct {
		name    string
		prevCfg *config.Config
		data    gui.SettingsFormData
		want    config.SyncMode
	}{
		{name: "first-ever save, no selection made", prevCfg: nil, data: gui.SettingsFormData{}, want: config.SyncModeDrive},
		{name: "first-ever save, drive selected", prevCfg: nil, data: gui.SettingsFormData{SyncMode: "drive"}, want: config.SyncModeDrive},
		{name: "first-ever save, icloud selected", prevCfg: nil, data: gui.SettingsFormData{SyncMode: "icloud"}, want: config.SyncModeICloud},
		{name: "pre-SyncMode legacy config, icloud now selected", prevCfg: &config.Config{VaultPath: "/v"}, data: gui.SettingsFormData{SyncMode: "icloud"}, want: config.SyncModeICloud},
		{
			name:    "already locked to icloud, form somehow carries drive",
			prevCfg: &config.Config{VaultPath: "/v", SyncMode: config.SyncModeICloud},
			data:    gui.SettingsFormData{SyncMode: "drive"},
			want:    config.SyncModeICloud,
		},
		{
			name:    "already locked to drive, form somehow carries icloud",
			prevCfg: &config.Config{VaultPath: "/v", SyncMode: config.SyncModeDrive},
			data:    gui.SettingsFormData{SyncMode: "icloud"},
			want:    config.SyncModeDrive,
		},
		{name: "first-ever save, gdrive_desktop selected", prevCfg: nil, data: gui.SettingsFormData{SyncMode: "gdrive_desktop"}, want: config.SyncModeGDriveDesktop},
		{
			name:    "already locked to gdrive_desktop, form somehow carries drive",
			prevCfg: &config.Config{VaultPath: "/v", SyncMode: config.SyncModeGDriveDesktop},
			data:    gui.SettingsFormData{SyncMode: "drive"},
			want:    config.SyncModeGDriveDesktop,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := lockedSyncMode(c.prevCfg, c.data); got != c.want {
				t.Errorf("lockedSyncMode(%+v, %+v) = %q, want %q", c.prevCfg, c.data, got, c.want)
			}
		})
	}
}

// TestVaultMigrationSourceIsBridge guards the real, previously-shipped
// bug this check fixes (spec 1.6.7): migrating a Vault that's already
// sitting exactly at the iCloud Bridge location must copy rather than
// move it (see runVaultMigration), since moving it out and immediately
// reseeding a fresh copy at the very same path is what triggered iCloud's
// own conflict handling and produced a duplicate folder.
func TestVaultMigrationSourceIsBridge(t *testing.T) {
	const bridgeParent = "/Users/me/Library/Mobile Documents/iCloud~md~obsidian/Documents"

	cases := []struct {
		name            string
		oldPath         string
		bridgeParent    string
		bridgeAvailable bool
		want            bool
	}{
		{
			name:            "a direct child of the bridge parent",
			oldPath:         bridgeParent + "/MyVault",
			bridgeParent:    bridgeParent,
			bridgeAvailable: true,
			want:            true,
		},
		{
			name:            "bridge unavailable on this OS/device",
			oldPath:         bridgeParent + "/MyVault",
			bridgeParent:    bridgeParent,
			bridgeAvailable: false,
			want:            false,
		},
		{
			name:            "an unrelated local folder",
			oldPath:         "/Users/me/Documents/MyVault",
			bridgeParent:    bridgeParent,
			bridgeAvailable: true,
			want:            false,
		},
		{
			name:            "nested deeper than a direct child",
			oldPath:         bridgeParent + "/Sub/MyVault",
			bridgeParent:    bridgeParent,
			bridgeAvailable: true,
			want:            false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := vaultMigrationSourceIsBridge(c.oldPath, c.bridgeParent, c.bridgeAvailable); got != c.want {
				t.Errorf("vaultMigrationSourceIsBridge(%q, %q, %v) = %v, want %v", c.oldPath, c.bridgeParent, c.bridgeAvailable, got, c.want)
			}
		})
	}
}

// TestShouldShowICloudMigrationReminder guards a real bug: the reminder
// used to fire for every non-managed-folder Vault regardless of sync mode,
// which is exactly how an iCloud-centric (Mode A, spec 1.6.10) device's
// Vault is *supposed* to be set up - accepting "Migrate Now" there would
// move the Vault out of iCloud into UniteVault's local folder, breaking the
// cross-device sync the user deliberately chose that mode for.
func TestShouldShowICloudMigrationReminder(t *testing.T) {
	root, err := bootstrap.ManagedVaultParentDir()
	if err != nil {
		t.Fatalf("ManagedVaultParentDir failed: %v", err)
	}
	unmanagedPath := filepath.Join(filepath.Dir(root), "iCloud", "Vault")
	managedPath := filepath.Join(root, "Vault")

	t.Run("Drive mode, unmanaged Vault, not dismissed", func(t *testing.T) {
		tr := newTestTrayApp(t)
		cfg := &config.Config{VaultPath: unmanagedPath, SyncMode: config.SyncModeDrive}
		if !shouldShowICloudMigrationReminder(tr.cfgMgr, cfg) {
			t.Error("expected the reminder for a Drive-mode device with an unmanaged Vault")
		}
	})

	t.Run("iCloud mode, unmanaged Vault - suppressed regardless", func(t *testing.T) {
		tr := newTestTrayApp(t)
		cfg := &config.Config{VaultPath: unmanagedPath, SyncMode: config.SyncModeICloud}
		if shouldShowICloudMigrationReminder(tr.cfgMgr, cfg) {
			t.Error("expected no reminder for an iCloud-mode device - its Vault belongs in iCloud by design")
		}
	})

	t.Run("gdrive_desktop mode, unmanaged Vault - suppressed regardless", func(t *testing.T) {
		tr := newTestTrayApp(t)
		cfg := &config.Config{VaultPath: unmanagedPath, SyncMode: config.SyncModeGDriveDesktop}
		if shouldShowICloudMigrationReminder(tr.cfgMgr, cfg) {
			t.Error("expected no reminder for a gdrive_desktop-mode device - its Vault belongs in the Google Drive desktop app's synced folder by design")
		}
	})

	t.Run("Drive mode, already under the managed root", func(t *testing.T) {
		tr := newTestTrayApp(t)
		cfg := &config.Config{VaultPath: managedPath, SyncMode: config.SyncModeDrive}
		if shouldShowICloudMigrationReminder(tr.cfgMgr, cfg) {
			t.Error("expected no reminder once the Vault is already under the managed folder")
		}
	})

	t.Run("Drive mode, previously dismissed", func(t *testing.T) {
		tr := newTestTrayApp(t)
		if err := tr.cfgMgr.SetICloudMigrationReminderDismissed(); err != nil {
			t.Fatalf("failed to dismiss reminder: %v", err)
		}
		cfg := &config.Config{VaultPath: unmanagedPath, SyncMode: config.SyncModeDrive}
		if shouldShowICloudMigrationReminder(tr.cfgMgr, cfg) {
			t.Error("expected no reminder once previously dismissed")
		}
	})
}

// TestPathIsUnder guards the detection logic behind
// maybeShowICloudMigrationReminder (spec 1.6.1/1.6.7, Phase 18): whether a
// configured Vault path sits inside iCloud Drive.
func TestPathIsUnder(t *testing.T) {
	cases := []struct {
		name string
		root string
		path string
		want bool
	}{
		{name: "direct child", root: "/Users/me/iCloud Drive", path: "/Users/me/iCloud Drive/Vault", want: true},
		{name: "nested child", root: "/Users/me/iCloud Drive", path: "/Users/me/iCloud Drive/Obsidian/Vault", want: true},
		{name: "exactly the root itself", root: "/Users/me/iCloud Drive", path: "/Users/me/iCloud Drive", want: true},
		{name: "sibling folder, not under root", root: "/Users/me/iCloud Drive", path: "/Users/me/Documents/Vault", want: false},
		{name: "a folder that merely shares the root's name as a prefix", root: "/Users/me/iCloud Drive", path: "/Users/me/iCloud Drive2/Vault", want: false},
		{name: "root itself is a subpath of the candidate (reversed nesting)", root: "/Users/me/iCloud Drive/Vault", path: "/Users/me/iCloud Drive", want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pathIsUnder(c.root, c.path); got != c.want {
				t.Errorf("pathIsUnder(%q, %q) = %v, want %v", c.root, c.path, got, c.want)
			}
		})
	}
}

// TestVaultUnderManagedRoot guards the generalized "does this Vault need
// migrating" rule (spec 1.6.1/1.6.7) that replaced a growing per-service
// (iCloud Drive, Obsidian's iCloud container, ...) detection list: only a
// Vault already under bootstrap.ManagedVaultParentDir (~/Obsidian) counts
// as managed - anything else (an iCloud path, a Google Drive Desktop
// folder, or any other plain local folder) needs migrating.
func TestVaultUnderManagedRoot(t *testing.T) {
	root, err := bootstrap.ManagedVaultParentDir()
	if err != nil {
		t.Fatalf("ManagedVaultParentDir failed: %v", err)
	}

	cases := []struct {
		name string
		path string
		want bool
	}{
		{name: "direct child of the managed root", path: filepath.Join(root, "Vault"), want: true},
		{name: "the managed root itself", path: root, want: true},
		{name: "an unrelated local folder", path: filepath.Join(filepath.Dir(root), "Documents", "Vault"), want: false},
		{name: "an iCloud path", path: filepath.Join(filepath.Dir(root), "Library", "Mobile Documents", "com~apple~CloudDocs", "Vault"), want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := vaultUnderManagedRoot(c.path); got != c.want {
				t.Errorf("vaultUnderManagedRoot(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

// TestBuildSaveConfig guards a real, previously-shipped bug: an ordinary
// Save Settings (which has no ICloudBridgePath field on its form at all)
// used to silently wipe out a Bridge path Vault Migration had set up,
// because the config it built didn't carry ICloudBridgePath forward from
// what was already saved.
func TestBuildSaveConfig(t *testing.T) {
	cases := []struct {
		name         string
		prevCfg      *config.Config
		data         gui.SettingsFormData
		wantBridge   string
		wantSyncMode config.SyncMode
	}{
		{
			name:         "no previous config at all (first-ever save)",
			prevCfg:      nil,
			data:         gui.SettingsFormData{VaultPath: "/vaults/A", RcloneRemote: "ObsidianVault", RclonePath: "Vault", IntervalSeconds: 60},
			wantBridge:   "",
			wantSyncMode: config.SyncModeDrive,
		},
		{
			name:         "previous config never had a Bridge path",
			prevCfg:      &config.Config{VaultPath: "/vaults/A"},
			data:         gui.SettingsFormData{VaultPath: "/vaults/A", RcloneRemote: "ObsidianVault", RclonePath: "Vault", IntervalSeconds: 60},
			wantBridge:   "",
			wantSyncMode: config.SyncModeDrive,
		},
		{
			name:         "an ordinary re-save carries the Bridge path forward",
			prevCfg:      &config.Config{VaultPath: "/vaults/A", ICloudBridgePath: "/icloud/Obsidian/A"},
			data:         gui.SettingsFormData{VaultPath: "/vaults/A", RcloneRemote: "ObsidianVault", RclonePath: "Vault", IntervalSeconds: 60},
			wantBridge:   "/icloud/Obsidian/A",
			wantSyncMode: config.SyncModeDrive,
		},
		{
			name:         "first-ever save, iCloud mode selected",
			prevCfg:      nil,
			data:         gui.SettingsFormData{VaultPath: "/vaults/A", RcloneRemote: "ObsidianVault", RclonePath: "Vault", IntervalSeconds: 60, SyncMode: "icloud"},
			wantBridge:   "",
			wantSyncMode: config.SyncModeICloud,
		},
		{
			name:         "re-save of an already-locked iCloud-mode Vault",
			prevCfg:      &config.Config{VaultPath: "/vaults/A", SyncMode: config.SyncModeICloud},
			data:         gui.SettingsFormData{VaultPath: "/vaults/A", RcloneRemote: "ObsidianVault", RclonePath: "Vault", IntervalSeconds: 60},
			wantBridge:   "",
			wantSyncMode: config.SyncModeICloud,
		},
		{
			name:         "first-ever save, gdrive_desktop mode selected",
			prevCfg:      nil,
			data:         gui.SettingsFormData{VaultPath: "/vaults/A", RcloneRemote: "ObsidianVault", RclonePath: "Vault", IntervalSeconds: 60, SyncMode: "gdrive_desktop"},
			wantBridge:   "",
			wantSyncMode: config.SyncModeGDriveDesktop,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildSaveConfig(c.prevCfg, c.data)
			if got.ICloudBridgePath != c.wantBridge {
				t.Errorf("ICloudBridgePath = %q, want %q", got.ICloudBridgePath, c.wantBridge)
			}
			if got.SyncMode != c.wantSyncMode {
				t.Errorf("SyncMode = %q, want %q", got.SyncMode, c.wantSyncMode)
			}
			if got.VaultPath != c.data.VaultPath || got.RcloneRemote != c.data.RcloneRemote ||
				got.RclonePath != c.data.RclonePath || got.IntervalSeconds != c.data.IntervalSeconds {
				t.Errorf("expected the form's own fields to round-trip untouched, got %+v from data %+v", got, c.data)
			}
		})
	}
}

// TestClearRemoteConfig guards a real, previously-shipped bug: "Remove
// Remote Configuration..." removed the rclone-level remote but left its
// now-stale name in config.json, so every following sync cycle kept
// retrying (and failing) against a remote that no longer existed, instead
// of cleanly falling back to "no remote configured" - a state RunCycle
// already tolerates gracefully for both roles (spec 1.6.4).
func TestClearRemoteConfig(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewConfigManagerWithDir(tempDir)
	if err := cfgMgr.SaveConfig(&config.Config{
		VaultPath:        "/vaults/A",
		RcloneRemote:     "ObsidianVault",
		RclonePath:       "VaultBackup",
		IntervalSeconds:  60,
		ICloudBridgePath: "/icloud/Obsidian/A",
	}); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	clearRemoteConfig(cfgMgr)

	got, err := cfgMgr.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if got.RcloneRemote != "" {
		t.Errorf("expected RcloneRemote to be cleared, got %q", got.RcloneRemote)
	}
	if got.RclonePath != "" {
		t.Errorf("expected RclonePath to be cleared, got %q", got.RclonePath)
	}
	if got.VaultPath != "/vaults/A" {
		t.Errorf("expected VaultPath to survive untouched, got %q", got.VaultPath)
	}
	if got.IntervalSeconds != 60 {
		t.Errorf("expected IntervalSeconds to survive untouched, got %d", got.IntervalSeconds)
	}
	if got.ICloudBridgePath != "/icloud/Obsidian/A" {
		t.Errorf("expected ICloudBridgePath to survive untouched, got %q", got.ICloudBridgePath)
	}
}

// TestKnownActiveOtherDevices guards the heuristic behind both
// MultiDeviceStatus and the Primary-only multi-device warnings (spec
// 3.6.1.5): a device that never wrote an event doesn't count, the caller's
// own device ID is always excluded, and a device whose *latest* event is
// EventDeviceDecommissioned drops out even though it once participated.
// (iPhone/iPad never appear here at all in practice - they never run this
// app, so they never write an event log in the first place; spec 1.4.)
func TestKnownActiveOtherDevices(t *testing.T) {
	t.Run("no devices at all", func(t *testing.T) {
		vaultPath := filepath.Join(t.TempDir(), "Vault")
		got, err := knownActiveOtherDevices(vaultPath, "self")
		if err != nil {
			t.Fatalf("knownActiveOtherDevices failed: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected no known devices, got %+v", got)
		}
	})

	t.Run("excludes self and decommissioned devices, keeps active ones", func(t *testing.T) {
		vaultPath := filepath.Join(t.TempDir(), "Vault")
		m := eventlog.NewManager(vaultPath)
		if err := m.Append("self", "this-mac", eventlog.EventInitializedAsPrimary, nil); err != nil {
			t.Fatalf("seed Append failed: %v", err)
		}
		if err := m.Append("dev-active", "windows-pc", eventlog.EventInitializedAsSecondary, nil); err != nil {
			t.Fatalf("seed Append failed: %v", err)
		}
		if err := m.Append("dev-gone", "old-windows-pc", eventlog.EventInitializedAsSecondary, nil); err != nil {
			t.Fatalf("seed Append failed: %v", err)
		}
		if err := m.Append("dev-gone", "old-windows-pc", eventlog.EventDeviceDecommissioned, nil); err != nil {
			t.Fatalf("seed Append failed: %v", err)
		}

		got, err := knownActiveOtherDevices(vaultPath, "self")
		if err != nil {
			t.Fatalf("knownActiveOtherDevices failed: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected exactly 1 active other device, got %d: %+v", len(got), got)
		}
		if _, ok := got["dev-active"]; !ok {
			t.Errorf("expected dev-active to be present, got %+v", got)
		}
	})
}

// TestDecommissionSelf guards the Reset Configuration decommission trail
// (spec 3.6.1.5): it must append EventDeviceDecommissioned, under this
// device's own ID/label, to the given Vault's event log.
func TestDecommissionSelf(t *testing.T) {
	tr := newTestTrayApp(t)
	vaultPath := filepath.Join(t.TempDir(), "Vault")
	deviceID, err := tr.cfgMgr.GetDeviceID()
	if err != nil {
		t.Fatalf("GetDeviceID failed: %v", err)
	}

	decommissionSelf(tr.cfgMgr, vaultPath, "my-mac")

	entries, err := eventlog.NewManager(vaultPath).ReadDeviceLog(deviceID)
	if err != nil {
		t.Fatalf("ReadDeviceLog failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 event, got %d: %+v", len(entries), entries)
	}
	if entries[0].Event != eventlog.EventDeviceDecommissioned {
		t.Errorf("expected EventDeviceDecommissioned, got %+v", entries[0])
	}
	if entries[0].Label != "my-mac" {
		t.Errorf("expected label %q, got %q", "my-mac", entries[0].Label)
	}
}

// TestBuildFormData_MultiDeviceStatus guards how buildFormData surfaces
// MultiDeviceStatus (spec 3.6.1.5): "Standalone" for a Primary with no
// other PC's event log showing up as active, "Syncing" as soon as one
// does, and empty for a Secondary - a Secondary always implies a Primary
// exists somewhere (even if unreachable), so "Syncing" there would be a
// constant that carries no information and only duplicated the separate
// Google Drive sync status row (a real user complaint).
func TestBuildFormData_MultiDeviceStatus(t *testing.T) {
	standalone := lang.L("Standalone")
	syncing := lang.L("Syncing")

	t.Run("Primary with no other devices at all", func(t *testing.T) {
		tr := newTestTrayApp(t)
		vaultPath := filepath.Join(t.TempDir(), "Vault")
		_ = tr.cfgMgr.SaveConfig(&config.Config{VaultPath: vaultPath})
		_ = tr.cfgMgr.SaveRole("primary")

		if got := tr.buildFormData().MultiDeviceStatus; got != standalone {
			t.Errorf("expected MultiDeviceStatus %q when no other device has ever written an event, got %q", standalone, got)
		}
	})

	t.Run("Primary with an active other device", func(t *testing.T) {
		tr := newTestTrayApp(t)
		vaultPath := filepath.Join(t.TempDir(), "Vault")
		_ = tr.cfgMgr.SaveConfig(&config.Config{VaultPath: vaultPath})
		_ = tr.cfgMgr.SaveRole("primary")
		if err := eventlog.NewManager(vaultPath).Append("dev-b", "windows-pc", eventlog.EventInitializedAsSecondary, nil); err != nil {
			t.Fatalf("seed Append failed: %v", err)
		}

		if got := tr.buildFormData().MultiDeviceStatus; got != syncing {
			t.Errorf("expected MultiDeviceStatus %q while another device's event log shows it active, got %q", syncing, got)
		}
	})

	t.Run("Secondary shows no MultiDeviceStatus", func(t *testing.T) {
		tr := newTestTrayApp(t)
		vaultPath := filepath.Join(t.TempDir(), "Vault")
		_ = tr.cfgMgr.SaveConfig(&config.Config{VaultPath: vaultPath})
		_ = tr.cfgMgr.SaveRole("secondary")

		if got := tr.buildFormData().MultiDeviceStatus; got != "" {
			t.Errorf("expected empty MultiDeviceStatus for a Secondary device (it's always \"Syncing\" and would just duplicate the Google Drive sync status row), got %q", got)
		}
	})

	t.Run("unconfigured role shows no status", func(t *testing.T) {
		tr := newTestTrayApp(t)

		if got := tr.buildFormData().MultiDeviceStatus; got != "" {
			t.Errorf("expected empty MultiDeviceStatus before any role is set, got %q", got)
		}
	})
}

// TestBuildFormData_PendingConflictCount guards how buildFormData surfaces
// PendingConflictCount (spec 3.3.2): populated for a Primary, always zero
// for a Secondary (merging - and therefore genuine content conflicts -
// only ever happens on the Primary).
func TestBuildFormData_PendingConflictCount(t *testing.T) {
	t.Run("Primary with pending conflicts", func(t *testing.T) {
		tr := newTestTrayApp(t)
		_ = tr.cfgMgr.SaveRole("primary")
		if err := tr.cfgMgr.SavePendingConflicts([]config.PendingConflict{
			{RelPath: "a.md"}, {RelPath: "b.md"},
		}); err != nil {
			t.Fatalf("failed to seed pending conflicts: %v", err)
		}

		if got := tr.buildFormData().PendingConflictCount; got != 2 {
			t.Errorf("expected PendingConflictCount 2, got %d", got)
		}
	})

	t.Run("Primary with no pending conflicts", func(t *testing.T) {
		tr := newTestTrayApp(t)
		_ = tr.cfgMgr.SaveRole("primary")

		if got := tr.buildFormData().PendingConflictCount; got != 0 {
			t.Errorf("expected PendingConflictCount 0, got %d", got)
		}
	})

	t.Run("Secondary never reports pending conflicts", func(t *testing.T) {
		tr := newTestTrayApp(t)
		_ = tr.cfgMgr.SaveRole("secondary")
		if err := tr.cfgMgr.SavePendingConflicts([]config.PendingConflict{{RelPath: "a.md"}}); err != nil {
			t.Fatalf("failed to seed pending conflicts: %v", err)
		}

		if got := tr.buildFormData().PendingConflictCount; got != 0 {
			t.Errorf("expected PendingConflictCount 0 for a Secondary, got %d", got)
		}
	})
}

// TestHasUnresolvedConflict guards the tray/menu bar "Status: Conflict"
// trigger (spec 3.5.2): true if either an active multi-Primary conflict or
// any pending content conflict is recorded, false otherwise.
func TestHasUnresolvedConflict(t *testing.T) {
	t.Run("no conflicts", func(t *testing.T) {
		tr := newTestTrayApp(t)
		if tr.hasUnresolvedConflict() {
			t.Error("expected false when nothing is recorded")
		}
	})

	t.Run("active primary conflict", func(t *testing.T) {
		tr := newTestTrayApp(t)
		if err := tr.cfgMgr.SavePrimaryConflict(config.PrimaryConflict{
			DetectedAt: "2026-08-27T10:00:00+09:00",
			Role:       config.ConflictRoleSuperseded,
		}); err != nil {
			t.Fatalf("failed to seed primary conflict: %v", err)
		}
		if !tr.hasUnresolvedConflict() {
			t.Error("expected true when a primary conflict is recorded")
		}
	})

	t.Run("pending content conflict", func(t *testing.T) {
		tr := newTestTrayApp(t)
		if err := tr.cfgMgr.SavePendingConflicts([]config.PendingConflict{{RelPath: "a.md"}}); err != nil {
			t.Fatalf("failed to seed pending conflict: %v", err)
		}
		if !tr.hasUnresolvedConflict() {
			t.Error("expected true when a pending content conflict is recorded")
		}
	})
}

func TestBuildFormData_DriveSyncStatusAndRoleVariations(t *testing.T) {
	t.Run("Secondary role shows N/A secondary explanation", func(t *testing.T) {
		tr := newTestTrayApp(t)
		_ = tr.cfgMgr.SaveRole("secondary")

		data := tr.buildFormData()
		if data.DeviceRole != "secondary" {
			t.Errorf("expected DeviceRole 'secondary', got %q", data.DeviceRole)
		}
		// Computed via lang.L (not hardcoded English) so this assertion
		// holds regardless of which locale TestMain's loaded translations
		// resolve to on the machine running the test.
		want := lang.L("N/A (this device is Secondary - Google Drive backup runs on the Primary device)")
		if data.DriveSyncStatus != want {
			t.Errorf("unexpected DriveSyncStatus for secondary: got %q, want %q", data.DriveSyncStatus, want)
		}
	})

	t.Run("Primary role with success sync status", func(t *testing.T) {
		tr := newTestTrayApp(t)
		_ = tr.cfgMgr.SaveRole("primary")
		_ = tr.cfgMgr.SaveDriveSyncStatus(config.DriveSyncStatus{
			Success: true,
			Time:    "2026-08-25T15:04:00Z",
		})

		data := tr.buildFormData()
		if data.DeviceRole != "primary" {
			t.Errorf("expected DeviceRole 'primary', got %q", data.DeviceRole)
		}
		if data.DriveSyncStatus == "" || data.DriveSyncStatus == "Never synced yet" {
			t.Errorf("expected populated success DriveSyncStatus, got %q", data.DriveSyncStatus)
		}
	})

	t.Run("Primary role with failed sync status", func(t *testing.T) {
		tr := newTestTrayApp(t)
		_ = tr.cfgMgr.SaveRole("primary")
		_ = tr.cfgMgr.SaveDriveSyncStatus(config.DriveSyncStatus{
			Success: false,
			Time:    "2026-08-25T15:04:00Z",
			Error:   "network timeout",
		})

		data := tr.buildFormData()
		if data.DeviceRole != "primary" {
			t.Errorf("expected DeviceRole 'primary', got %q", data.DeviceRole)
		}
		if data.DriveSyncStatus == "" || data.DriveSyncStatus == "Never synced yet" {
			t.Errorf("expected populated failure DriveSyncStatus, got %q", data.DriveSyncStatus)
		}
	})
}
