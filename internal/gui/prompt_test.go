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

	if data.IntervalSeconds != 120 {
		t.Errorf("Expected IntervalSeconds to be 120, got %d", data.IntervalSeconds)
	}
}
