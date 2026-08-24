package gui

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// PromptSettingsWindow displays a single native Fyne GUI window on macOS containing all input fields inline with Save & Cancel buttons.
func PromptSettingsWindow(title string, current SettingsFormData) (SettingsFormData, bool) {
	if current.IntervalSeconds <= 0 {
		current.IntervalSeconds = 120
	}

	appInst := app.NewWithID("com.unitevault.settings")
	win := appInst.NewWindow(title)
	win.Resize(fyne.NewSize(520, 320))
	win.CenterOnScreen()

	vaultEntry := widget.NewEntry()
	vaultEntry.SetText(current.VaultPath)
	vaultEntry.SetPlaceHolder("/path/to/Obsidian/Vault")

	browseBtn := widget.NewButton("Browse...", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err == nil && uri != nil {
				vaultEntry.SetText(uri.Path())
			}
		}, win)
	})

	vaultContainer := container.NewBorder(nil, nil, nil, browseBtn, vaultEntry)

	remoteEntry := widget.NewEntry()
	remoteEntry.SetText(current.RcloneRemote)
	remoteEntry.SetPlaceHolder("gdrive")

	pathEntry := widget.NewEntry()
	pathEntry.SetText(current.RclonePath)
	pathEntry.SetPlaceHolder("VaultBackup")

	intervalEntry := widget.NewEntry()
	intervalEntry.SetText(fmt.Sprintf("%d", current.IntervalSeconds))
	intervalEntry.SetPlaceHolder("120")

	form := &widget.Form{
		Items: []*widget.FormItem{
			{Text: "Obsidian Vault Path", Widget: vaultContainer},
			{Text: "rclone Remote Name", Widget: remoteEntry},
			{Text: "Google Drive Target Folder", Widget: pathEntry},
			{Text: "Sync Interval (seconds)", Widget: intervalEntry},
		},
	}

	resultData := current
	saved := false

	form.OnSubmit = func() {
		sec, _ := strconv.Atoi(strings.TrimSpace(intervalEntry.Text))
		if sec <= 0 {
			sec = 120
		}
		resultData = SettingsFormData{
			VaultPath:       strings.TrimSpace(vaultEntry.Text),
			RcloneRemote:    strings.TrimSpace(remoteEntry.Text),
			RclonePath:      strings.TrimSpace(pathEntry.Text),
			IntervalSeconds: sec,
		}
		if resultData.RcloneRemote == "" {
			resultData.RcloneRemote = "gdrive"
		}
		if resultData.RclonePath == "" {
			resultData.RclonePath = "VaultBackup"
		}
		saved = true
		win.Close()
	}

	form.OnCancel = func() {
		saved = false
		win.Close()
	}

	win.SetContent(container.NewPadded(form))
	win.ShowAndRun()

	return resultData, saved
}
