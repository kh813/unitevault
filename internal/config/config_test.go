package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kh813/unitevault/internal/config"
)

// TestConfig_EffectiveSyncMode guards spec 1.6.10's backward-compat
// requirement: every config saved before SyncMode existed has it as the
// empty string, and must be treated identically to SyncModeDrive (the
// only behavior that ever existed) rather than as some unrecognized third
// state - existing installs must keep working unchanged with no
// migration needed.
func TestConfig_EffectiveSyncMode(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want config.SyncMode
	}{
		{name: "nil config", cfg: nil, want: config.SyncModeDrive},
		{name: "zero-value config (pre-SyncMode save)", cfg: &config.Config{}, want: config.SyncModeDrive},
		{name: "explicit drive mode", cfg: &config.Config{SyncMode: config.SyncModeDrive}, want: config.SyncModeDrive},
		{name: "explicit icloud mode", cfg: &config.Config{SyncMode: config.SyncModeICloud}, want: config.SyncModeICloud},
		{name: "explicit gdrive_desktop mode", cfg: &config.Config{SyncMode: config.SyncModeGDriveDesktop}, want: config.SyncModeGDriveDesktop},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.EffectiveSyncMode(); got != c.want {
				t.Errorf("EffectiveSyncMode() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestConfigManager_ExtraExcludesRoundTrips guards a real user request:
// additional rclone --exclude patterns (spec 1.6.10) must survive a
// Save/Load round-trip, and a config saved before this field existed (or
// with nothing configured) must come back as an empty/nil slice rather
// than erroring or panicking.
func TestConfigManager_ExtraExcludesRoundTrips(t *testing.T) {
	tempDir := t.TempDir()
	cm := config.NewConfigManagerWithDir(tempDir)

	want := []string{"Attachments/**", "**/*.mp4"}
	if err := cm.SaveConfig(&config.Config{VaultPath: "/tmp/v", ExtraExcludes: want}); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	got, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(got.ExtraExcludes) != len(want) {
		t.Fatalf("expected ExtraExcludes %v, got %v", want, got.ExtraExcludes)
	}
	for i, p := range want {
		if got.ExtraExcludes[i] != p {
			t.Errorf("expected ExtraExcludes[%d] = %q, got %q", i, p, got.ExtraExcludes[i])
		}
	}

	if err := cm.SaveConfig(&config.Config{VaultPath: "/tmp/v"}); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}
	got, err = cm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(got.ExtraExcludes) != 0 {
		t.Errorf("expected empty ExtraExcludes when none configured, got %v", got.ExtraExcludes)
	}
}

// TestConfigManager_LogIncludeFilenamesDefaultsToFalse guards a real user
// request: diagnostic logs must not include note filenames unless the user
// explicitly opts in via Settings, and a config saved before this field
// existed (Go zero value) must come back false with no migration needed.
func TestConfigManager_LogIncludeFilenamesDefaultsToFalse(t *testing.T) {
	tempDir := t.TempDir()
	cm := config.NewConfigManagerWithDir(tempDir)

	if err := cm.SaveConfig(&config.Config{VaultPath: "/tmp/v"}); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}
	got, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if got.LogIncludeFilenames {
		t.Error("expected LogIncludeFilenames to default to false")
	}

	if err := cm.SaveConfig(&config.Config{VaultPath: "/tmp/v", LogIncludeFilenames: true}); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}
	got, err = cm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !got.LogIncludeFilenames {
		t.Error("expected LogIncludeFilenames=true to round-trip")
	}
}

func TestConfigManager_DeviceID(t *testing.T) {
	tempDir := t.TempDir()
	cm := config.NewConfigManagerWithDir(tempDir)

	// First call should generate and save a new UUID
	id1, err := cm.GetDeviceID()
	if err != nil {
		t.Fatalf("expected no error on first GetDeviceID, got: %v", err)
	}
	if id1 == "" {
		t.Fatal("expected non-empty device ID")
	}

	// File should exist
	if _, err := os.Stat(cm.DeviceIDPath()); os.IsNotExist(err) {
		t.Fatalf("device_id file was not created at %s", cm.DeviceIDPath())
	}

	// Second call should return the exact same UUID
	id2, err := cm.GetDeviceID()
	if err != nil {
		t.Fatalf("expected no error on second GetDeviceID, got: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("expected device ID to be persistent, got %s vs %s", id1, id2)
	}
}

func TestConfigManager_Config(t *testing.T) {
	tempDir := t.TempDir()
	cm := config.NewConfigManagerWithDir(tempDir)

	// Load should return defaults when file doesn't exist
	cfg, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("expected no error loading initial config, got: %v", err)
	}
	if cfg.IntervalSeconds != config.DefaultIntervalSeconds {
		t.Errorf("expected default IntervalSeconds=%d, got %d", config.DefaultIntervalSeconds, cfg.IntervalSeconds)
	}

	// Save modified config
	cfg.VaultPath = "/path/to/vault"
	cfg.RcloneRemote = "gdrive"
	cfg.RclonePath = "VaultBackup"
	cfg.IntervalSeconds = 60

	if err := cm.SaveConfig(cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	// Reload and verify
	reloaded, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}

	if reloaded.VaultPath != "/path/to/vault" {
		t.Errorf("expected VaultPath /path/to/vault, got %s", reloaded.VaultPath)
	}
	if reloaded.RcloneRemote != "gdrive" {
		t.Errorf("expected RcloneRemote gdrive, got %s", reloaded.RcloneRemote)
	}
	if reloaded.RclonePath != "VaultBackup" {
		t.Errorf("expected RclonePath VaultBackup, got %s", reloaded.RclonePath)
	}
	if reloaded.IntervalSeconds != 60 {
		t.Errorf("expected IntervalSeconds 60, got %d", reloaded.IntervalSeconds)
	}
}

func TestConfigManager_Role(t *testing.T) {
	tempDir := t.TempDir()
	cm := config.NewConfigManagerWithDir(tempDir)

	role, err := cm.LoadRole()
	if err != nil {
		t.Fatalf("expected no error loading non-existent role, got %v", err)
	}
	if role != "" {
		t.Errorf("expected empty initial role, got %s", role)
	}

	if err := cm.SaveRole("primary"); err != nil {
		t.Fatalf("failed to save role: %v", err)
	}

	savedRole, err := cm.LoadRole()
	if err != nil {
		t.Fatalf("failed to load saved role: %v", err)
	}
	if savedRole != "primary" {
		t.Errorf("expected role 'primary', got '%s'", savedRole)
	}
}

func TestConfigManager_InstallReminderDismissed(t *testing.T) {
	tempDir := t.TempDir()
	cm := config.NewConfigManagerWithDir(tempDir)

	if cm.IsInstallReminderDismissed() {
		t.Fatal("expected reminder to not be dismissed initially")
	}

	if err := cm.SetInstallReminderDismissed(); err != nil {
		t.Fatalf("failed to set install reminder dismissed: %v", err)
	}
	if !cm.IsInstallReminderDismissed() {
		t.Fatal("expected reminder to be dismissed after SetInstallReminderDismissed")
	}

	if err := cm.ResetConfig(); err != nil {
		t.Fatalf("ResetConfig failed: %v", err)
	}
	if cm.IsInstallReminderDismissed() {
		t.Fatal("expected ResetConfig to clear the install reminder dismissal")
	}
}

// TestConfigManager_DisclaimerAccepted guards the first-launch disclaimer
// gate's persistence, and specifically that ResetConfig does NOT clear
// it - unlike the dismissible reminders (Install Reminder, iCloud
// Migration Reminder), this is a one-time, permanent acknowledgment per
// install, not tied to sync configuration state, so resetting sync
// settings must never make the disclaimer reappear.
func TestConfigManager_DisclaimerAccepted(t *testing.T) {
	tempDir := t.TempDir()
	cm := config.NewConfigManagerWithDir(tempDir)

	if cm.IsDisclaimerAccepted() {
		t.Fatal("expected the disclaimer to not be accepted initially")
	}

	if err := cm.SetDisclaimerAccepted(); err != nil {
		t.Fatalf("failed to set disclaimer accepted: %v", err)
	}
	if !cm.IsDisclaimerAccepted() {
		t.Fatal("expected the disclaimer to be accepted after SetDisclaimerAccepted")
	}

	if err := cm.ResetConfig(); err != nil {
		t.Fatalf("ResetConfig failed: %v", err)
	}
	if !cm.IsDisclaimerAccepted() {
		t.Error("expected ResetConfig to NOT clear disclaimer acceptance")
	}
}

// TestConfigManager_LastUpdateCheck guards the persisted marker the
// periodic background update check (cmd/unitevault's
// maybeCheckForUpdatePeriodically) relies on to survive across app
// restarts: unset initially, round-trips through Save/Load, and - like
// DisclaimerAccepted above, and for the same reason (unrelated to sync
// configuration state) - is NOT cleared by ResetConfig.
func TestConfigManager_LastUpdateCheck(t *testing.T) {
	tempDir := t.TempDir()
	cm := config.NewConfigManagerWithDir(tempDir)

	got, err := cm.LoadLastUpdateCheck()
	if err != nil {
		t.Fatalf("LoadLastUpdateCheck failed before any check was ever recorded: %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("expected the zero time before any check was ever recorded, got %v", got)
	}

	want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := cm.SaveLastUpdateCheck(want); err != nil {
		t.Fatalf("SaveLastUpdateCheck failed: %v", err)
	}
	got, err = cm.LoadLastUpdateCheck()
	if err != nil {
		t.Fatalf("LoadLastUpdateCheck failed: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("expected LoadLastUpdateCheck to round-trip %v, got %v", want, got)
	}

	if err := cm.ResetConfig(); err != nil {
		t.Fatalf("ResetConfig failed: %v", err)
	}
	got, err = cm.LoadLastUpdateCheck()
	if err != nil {
		t.Fatalf("LoadLastUpdateCheck failed after ResetConfig: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("expected ResetConfig to NOT clear the last update check time, got %v", got)
	}
}

func TestConfigManager_ICloudMigrationReminderDismissed(t *testing.T) {
	tempDir := t.TempDir()
	cm := config.NewConfigManagerWithDir(tempDir)

	if cm.IsICloudMigrationReminderDismissed() {
		t.Fatal("expected reminder to not be dismissed initially")
	}

	if err := cm.SetICloudMigrationReminderDismissed(); err != nil {
		t.Fatalf("failed to set iCloud migration reminder dismissed: %v", err)
	}
	if !cm.IsICloudMigrationReminderDismissed() {
		t.Fatal("expected reminder to be dismissed after SetICloudMigrationReminderDismissed")
	}

	if err := cm.ResetConfig(); err != nil {
		t.Fatalf("ResetConfig failed: %v", err)
	}
	if cm.IsICloudMigrationReminderDismissed() {
		t.Fatal("expected ResetConfig to clear the iCloud migration reminder dismissal")
	}
}

func TestConfigManager_DriveSyncStatus(t *testing.T) {
	tempDir := t.TempDir()
	cm := config.NewConfigManagerWithDir(tempDir)

	status, err := cm.LoadDriveSyncStatus()
	if err != nil {
		t.Fatalf("expected no error loading non-existent drive sync status, got %v", err)
	}
	if status != nil {
		t.Errorf("expected nil status before any sync has been recorded, got %+v", status)
	}

	success := config.DriveSyncStatus{Time: "2026-08-25T15:04:00+09:00", Success: true}
	if err := cm.SaveDriveSyncStatus(success); err != nil {
		t.Fatalf("failed to save drive sync status: %v", err)
	}
	loaded, err := cm.LoadDriveSyncStatus()
	if err != nil {
		t.Fatalf("failed to load saved drive sync status: %v", err)
	}
	if loaded == nil || !loaded.Success || loaded.Time != success.Time {
		t.Errorf("expected loaded status to round-trip %+v, got %+v", success, loaded)
	}

	failure := config.DriveSyncStatus{Time: "2026-08-25T16:00:00+09:00", Success: false, Error: "network error"}
	if err := cm.SaveDriveSyncStatus(failure); err != nil {
		t.Fatalf("failed to save drive sync status: %v", err)
	}
	loaded, err = cm.LoadDriveSyncStatus()
	if err != nil {
		t.Fatalf("failed to load saved drive sync status: %v", err)
	}
	if loaded == nil || loaded.Success || loaded.Error != "network error" {
		t.Errorf("expected loaded status to round-trip %+v, got %+v", failure, loaded)
	}

	if err := cm.ResetConfig(); err != nil {
		t.Fatalf("ResetConfig failed: %v", err)
	}
	status, err = cm.LoadDriveSyncStatus()
	if err != nil {
		t.Fatalf("expected no error loading drive sync status after reset, got %v", err)
	}
	if status != nil {
		t.Error("expected ResetConfig to clear the drive sync status")
	}
}

func TestConfigManager_PendingConflicts(t *testing.T) {
	tempDir := t.TempDir()
	cm := config.NewConfigManagerWithDir(tempDir)

	conflicts, err := cm.LoadPendingConflicts()
	if err != nil {
		t.Fatalf("expected no error loading non-existent pending conflicts, got %v", err)
	}
	if conflicts != nil {
		t.Errorf("expected nil before any conflict is recorded, got %+v", conflicts)
	}

	want := []config.PendingConflict{
		{
			RelPath:     "Notes/foo.md",
			DetectedAt:  "2026-08-27T10:00:00+09:00",
			WrittenHash: "hash-with-markers",
			Versions: []config.PendingConflictVersion{
				{DeviceID: "dev-a", Label: "mac-mini", Content: "A's version"},
				{DeviceID: "dev-b", Label: "iphone", Content: "B's version"},
			},
		},
	}
	if err := cm.SavePendingConflicts(want); err != nil {
		t.Fatalf("failed to save pending conflicts: %v", err)
	}

	loaded, err := cm.LoadPendingConflicts()
	if err != nil {
		t.Fatalf("failed to load saved pending conflicts: %v", err)
	}
	if len(loaded) != 1 || loaded[0].RelPath != "Notes/foo.md" || len(loaded[0].Versions) != 2 {
		t.Fatalf("expected pending conflicts to round-trip %+v, got %+v", want, loaded)
	}
	if loaded[0].Versions[1].Content != "B's version" {
		t.Errorf("expected version content to round-trip, got %+v", loaded[0].Versions[1])
	}

	if err := cm.ClearPendingConflicts(); err != nil {
		t.Fatalf("ClearPendingConflicts failed: %v", err)
	}
	loaded, err = cm.LoadPendingConflicts()
	if err != nil {
		t.Fatalf("expected no error loading pending conflicts after clear, got %v", err)
	}
	if loaded != nil {
		t.Error("expected ClearPendingConflicts to remove the recorded conflicts")
	}

	if err := cm.SavePendingConflicts(want); err != nil {
		t.Fatalf("failed to save pending conflicts: %v", err)
	}
	if err := cm.ResetConfig(); err != nil {
		t.Fatalf("ResetConfig failed: %v", err)
	}
	loaded, err = cm.LoadPendingConflicts()
	if err != nil {
		t.Fatalf("expected no error loading pending conflicts after reset, got %v", err)
	}
	if loaded != nil {
		t.Error("expected ResetConfig to clear pending conflicts")
	}
}

func TestGetConfigDir(t *testing.T) {
	dir, err := config.GetConfigDir()
	if err != nil {
		t.Fatalf("GetConfigDir returned error: %v", err)
	}
	if dir == "" {
		t.Fatal("GetConfigDir returned empty path")
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("expected absolute path, got %s", dir)
	}
}
