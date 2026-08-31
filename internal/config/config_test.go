package config_test

import (
	"os"
	"path/filepath"
	"testing"

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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.EffectiveSyncMode(); got != c.want {
				t.Errorf("EffectiveSyncMode() = %q, want %q", got, c.want)
			}
		})
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
