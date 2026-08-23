package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kh813/unitevault/internal/config"
)

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
	if cfg.IntervalSeconds != 120 {
		t.Errorf("expected default IntervalSeconds=120, got %d", cfg.IntervalSeconds)
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
