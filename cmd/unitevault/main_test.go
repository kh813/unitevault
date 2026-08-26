package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"
	"github.com/kh813/unitevault/internal/config"
	"github.com/kh813/unitevault/internal/drive"
	"github.com/kh813/unitevault/internal/engine"
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

func (noopDriveRunner) Sync(ctx context.Context, srcPath, remoteTarget string) error { return nil }
func (noopDriveRunner) Copy(ctx context.Context, remoteSrc, dstPath string) error    { return nil }
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
	if data.RcloneRemote != "ObsidianVault" {
		t.Errorf("expected default RcloneRemote 'ObsidianVault', got %q", data.RcloneRemote)
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
