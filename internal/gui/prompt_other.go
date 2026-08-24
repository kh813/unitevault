//go:build !darwin && !windows

package gui

// PromptSettingsWindow displays a fallback native settings window on Linux and other Unix systems.
func PromptSettingsWindow(title string, current SettingsFormData) (SettingsFormData, bool) {
	if current.IntervalSeconds <= 0 {
		current.IntervalSeconds = 120
	}

	folderPath, ok := PromptFolder("Select Obsidian Vault Directory")
	if !ok || folderPath == "" {
		return current, false
	}

	res := current
	res.VaultPath = folderPath
	return res, true
}
