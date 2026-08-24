package gui

import (
	"fmt"
)

// PromptSettingsWindow displays an interactive settings wizard on macOS allowing modification of all parameters.
func PromptSettingsWindow(title string, current SettingsFormData) (SettingsFormData, bool) {
	if current.IntervalSeconds <= 0 {
		current.IntervalSeconds = 120
	}
	if current.RcloneRemote == "" {
		current.RcloneRemote = "gdrive"
	}
	if current.RclonePath == "" {
		current.RclonePath = "VaultBackup"
	}

	updated := current

	for {
		msg := fmt.Sprintf(`[ UniteVault Settings ]

• Status Info:
  - Git Status: %s
  - rclone Status: %s
  - Device Role: %s

• Current Configuration:
  1. Vault Path: %s
  2. Google Drive Target Path: %s
  3. Sync Interval: %d seconds
  4. rclone Remote: %s (%s)

Select an action below to change settings or save:`,
			updated.GitStatus, updated.RcloneStatus, updated.DeviceRole,
			updated.VaultPath, updated.RclonePath, updated.IntervalSeconds,
			updated.RcloneRemote, updated.RcloneRemoteInfo)

		// 1. Choice menu: Select Vault / Edit Config / Save & Apply / Cancel
		choice := PromptChoice(title, msg, "Save & Apply", "Change Settings...")
		if choice == 0 {
			// User canceled
			return current, false
		}

		if choice == 1 {
			// Save & Apply
			if updated.VaultPath == "" {
				// Must select Vault first
				PromptMessage("Vault Required", "Please select your Obsidian Vault directory before saving.")
				newFolder, ok := PromptFolder("Select Obsidian Vault Directory")
				if ok && newFolder != "" {
					updated.VaultPath = newFolder
				} else {
					continue
				}
			}
			return updated, true
		}

		// choice == 2: Change Settings menu
		settingMenuMsg := "Which setting would you like to change?"
		itemChoice := PromptChoice("Edit Configuration", settingMenuMsg, "Select Vault Folder", "Edit Target Path / Interval")
		if itemChoice == 1 {
			// Select Vault Directory using OS native Finder
			newFolder, ok := PromptFolder("Select Obsidian Vault Directory")
			if ok && newFolder != "" {
				updated.VaultPath = newFolder
			}
		} else if itemChoice == 2 {
			// Edit Google Drive Target Path or Interval
			subChoice := PromptChoice("Edit Target Path or Interval", "Select field to edit:", "Edit Target Folder", "Edit Interval (Sec)")
			if subChoice == 1 {
				newPath, ok := PromptTextInput("Google Drive Target Path", "Enter Google Drive Target Folder Path:", updated.RclonePath)
				if ok && newPath != "" {
					updated.RclonePath = newPath
				}
			} else if subChoice == 2 {
				newIntervalStr, ok := PromptTextInput("Sync Interval", "Enter Sync Interval in seconds (e.g. 120):", fmt.Sprintf("%d", updated.IntervalSeconds))
				if ok && newIntervalStr != "" {
					var sec int
					_, err := fmt.Sscanf(newIntervalStr, "%d", &sec)
					if err == nil && sec > 0 {
						updated.IntervalSeconds = sec
					}
				}
			}
		}
	}
}

