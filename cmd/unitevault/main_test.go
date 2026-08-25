package main

import (
	"testing"

	"github.com/kh813/unitevault/internal/config"
)

// newTestTrayApp returns a trayApp wired to an isolated, temporary config
// directory so tests never touch the real ~/.unitevault.
func newTestTrayApp(t *testing.T) *trayApp {
	t.Helper()
	cfgMgr := config.NewConfigManagerWithDir(t.TempDir())
	return &trayApp{cfgMgr: cfgMgr}
}

func TestBuildFormData_DefaultsWhenUnconfigured(t *testing.T) {
	tr := newTestTrayApp(t)

	data := tr.buildFormData()

	if data.DeviceRole != "Not Initialized" {
		t.Errorf("expected DeviceRole 'Not Initialized', got %q", data.DeviceRole)
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
	if data.IntervalSeconds != 120 {
		t.Errorf("expected default IntervalSeconds 120, got %d", data.IntervalSeconds)
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

	if data.IntervalSeconds != 120 {
		t.Errorf("expected a zero saved interval to fall back to the default 120, got %d", data.IntervalSeconds)
	}
}
