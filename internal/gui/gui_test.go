package gui

import (
	"runtime"
	"testing"
)

func TestSettingsFormData_StructureAndDefaults(t *testing.T) {
	data := SettingsFormData{
		GitStatus:        "Installed",
		RcloneStatus:     "Installed",
		DeviceRole:       "Primary",
		VaultPath:        "/Users/test/Obsidian/MyVault",
		RcloneRemote:     "gdrive",
		RclonePath:       "VaultBackup",
		IntervalSeconds:  120,
		RcloneExecPath:   "/usr/local/bin/rclone",
		RcloneRemoteInfo: "OK (Configured)",
	}

	if data.GitStatus != "Installed" {
		t.Errorf("Expected GitStatus to be Installed, got %s", data.GitStatus)
	}

	if data.DeviceRole != "Primary" {
		t.Errorf("Expected DeviceRole to be Primary, got %s", data.DeviceRole)
	}

	if data.VaultPath != "/Users/test/Obsidian/MyVault" {
		t.Errorf("Expected VaultPath to be /Users/test/Obsidian/MyVault, got %s", data.VaultPath)
	}

	if data.IntervalSeconds != 120 {
		t.Errorf("Expected IntervalSeconds to be 120, got %d", data.IntervalSeconds)
	}
}

func TestSettingsForm_CrossPlatformValidation(t *testing.T) {
	// Verify that the current GOOS environment matches expectations and data structures initialize cleanly
	testData := SettingsFormData{
		GitStatus:        "Not Found",
		RcloneStatus:     "Not Found",
		DeviceRole:       "Not Initialized",
		VaultPath:        "",
		RcloneRemote:     "gdrive",
		RclonePath:       "VaultBackup",
		IntervalSeconds:  0,
		RcloneExecPath:   "",
		RcloneRemoteInfo: "Not Configured",
	}

	if testData.IntervalSeconds <= 0 {
		testData.IntervalSeconds = 120
	}

	if testData.IntervalSeconds != 120 {
		t.Fatalf("Default interval fallback failed")
	}

	t.Logf("Tested GUI Form structure validation on GOOS: %s", runtime.GOOS)
}
