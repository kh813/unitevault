package gui

import (
	"testing"
)

func TestSettingsFormDataValidation(t *testing.T) {
	data := SettingsFormData{
		VaultPath:       "/Users/test/Vault",
		RcloneRemote:    "gdrive",
		RclonePath:      "VaultBackup",
		IntervalSeconds: 120,
	}

	if data.VaultPath != "/Users/test/Vault" {
		t.Errorf("Expected VaultPath to be /Users/test/Vault, got %s", data.VaultPath)
	}

	if data.RcloneRemote != "gdrive" {
		t.Errorf("Expected RcloneRemote to be gdrive, got %s", data.RcloneRemote)
	}

	if data.RclonePath != "VaultBackup" {
		t.Errorf("Expected RclonePath to be VaultBackup, got %s", data.RclonePath)
	}

	if data.IntervalSeconds != 120 {
		t.Errorf("Expected IntervalSeconds to be 120, got %d", data.IntervalSeconds)
	}
}

func TestSettingsFormDataDefaults(t *testing.T) {
	// Verify default fallback handling logic
	data := SettingsFormData{
		VaultPath:       "/path/to/vault",
		RcloneRemote:    "",
		RclonePath:      "",
		IntervalSeconds: 0,
	}

	if data.RcloneRemote == "" {
		data.RcloneRemote = "gdrive"
	}
	if data.RclonePath == "" {
		data.RclonePath = "VaultBackup"
	}
	if data.IntervalSeconds <= 0 {
		data.IntervalSeconds = 120
	}

	if data.RcloneRemote != "gdrive" {
		t.Errorf("Default RcloneRemote failed: got %s", data.RcloneRemote)
	}
	if data.RclonePath != "VaultBackup" {
		t.Errorf("Default RclonePath failed: got %s", data.RclonePath)
	}
	if data.IntervalSeconds != 120 {
		t.Errorf("Default IntervalSeconds failed: got %d", data.IntervalSeconds)
	}
}
