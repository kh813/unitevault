package gui

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// PromptSettingsWindow displays a structured native Fyne GUI window on macOS with Status, Config form, and rclone details.
func PromptSettingsWindow(title string, current SettingsFormData) (SettingsFormData, bool) {
	if current.IntervalSeconds <= 0 {
		current.IntervalSeconds = 120
	}

	appInst := app.NewWithID("com.unitevault.settings")
	win := appInst.NewWindow(title)
	win.Resize(fyne.NewSize(580, 480))
	win.CenterOnScreen()

	// --- 1. Status Section ---
	statusTitle := widget.NewLabelWithStyle("[ Status ]", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	gitLabel := widget.NewLabel(fmt.Sprintf("  - Git Status: %s", current.GitStatus))
	rcloneStatusLabel := widget.NewLabel(fmt.Sprintf("  - rclone Status: %s", current.RcloneStatus))
	roleLabel := widget.NewLabel(fmt.Sprintf("  - Device Role: %s", current.DeviceRole))

	statusGroup := container.NewVBox(
		statusTitle,
		gitLabel,
		rcloneStatusLabel,
		roleLabel,
	)

	// --- 2. Settings Config Section ---
	configTitle := widget.NewLabelWithStyle("[ Config Settings ]", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	vaultEntry := widget.NewEntry()
	vaultEntry.SetText(current.VaultPath)
	vaultEntry.SetPlaceHolder("/path/to/Obsidian/Vault")

	selectBtn := widget.NewButton("Select Folder", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err == nil && uri != nil {
				vaultEntry.SetText(uri.Path())
			}
		}, win)
	})

	vaultContainer := container.NewBorder(nil, nil, nil, selectBtn, vaultEntry)

	pathEntry := widget.NewEntry()
	pathEntry.SetText(current.RclonePath)
	pathEntry.SetPlaceHolder("VaultBackup")

	intervalEntry := widget.NewEntry()
	intervalEntry.SetText(fmt.Sprintf("%d", current.IntervalSeconds))
	intervalEntry.SetPlaceHolder("120")

	remoteEntry := widget.NewEntry()
	remoteEntry.SetText(current.RcloneRemote)
	remoteEntry.SetPlaceHolder("gdrive")

	configForm := &widget.Form{
		Items: []*widget.FormItem{
			{Text: "Vault Directory", Widget: vaultContainer},
			{Text: "Google Drive Target Path", Widget: pathEntry},
			{Text: "Sync Interval (seconds)", Widget: intervalEntry},
		},
	}

	configGroup := container.NewVBox(
		configTitle,
		configForm,
	)

	// --- 3. rclone Status Section ---
	rcloneTitle := widget.NewLabelWithStyle("[ rclone Configuration ]", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	rcloneForm := &widget.Form{
		Items: []*widget.FormItem{
			{Text: "rclone Remote Name", Widget: remoteEntry},
		},
	}

	execPathLabel := widget.NewLabel(fmt.Sprintf("  - Executable Path: %s", current.RcloneExecPath))
	remoteStatusLabel := widget.NewLabel(fmt.Sprintf("  - Remote Status: %s", current.RcloneRemoteInfo))

	rcloneGroup := container.NewVBox(
		rcloneTitle,
		rcloneForm,
		execPathLabel,
		remoteStatusLabel,
	)

	// --- 4. Action Buttons ---
	resultData := current
	saved := false

	saveBtn := widget.NewButtonWithIcon("Save Settings", nil, func() {
		sec, _ := strconv.Atoi(strings.TrimSpace(intervalEntry.Text))
		if sec <= 0 {
			sec = 120
		}
		resultData.VaultPath = strings.TrimSpace(vaultEntry.Text)
		resultData.RcloneRemote = strings.TrimSpace(remoteEntry.Text)
		resultData.RclonePath = strings.TrimSpace(pathEntry.Text)
		resultData.IntervalSeconds = sec

		if resultData.RcloneRemote == "" {
			resultData.RcloneRemote = "gdrive"
		}
		if resultData.RclonePath == "" {
			resultData.RclonePath = "VaultBackup"
		}
		saved = true
		win.Close()
	})
	saveBtn.Importance = widget.HighImportance

	cancelBtn := widget.NewButton("Cancel", func() {
		saved = false
		win.Close()
	})

	buttonContainer := container.NewHBox(
		layout.NewSpacer(),
		cancelBtn,
		saveBtn,
	)

	// Main Layout
	mainContent := container.NewVBox(
		statusGroup,
		widget.NewSeparator(),
		configGroup,
		widget.NewSeparator(),
		rcloneGroup,
		widget.NewSeparator(),
		buttonContainer,
	)

	win.SetContent(container.NewPadded(mainContent))
	win.ShowAndRun()

	return resultData, saved
}
