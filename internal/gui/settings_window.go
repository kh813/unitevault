package gui

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// SettingsFormData represents everything shown/edited in the single Settings
// window: the Status section, the Config form, and the rclone details
// (spec section 3.5.2 / 8.3).
type SettingsFormData struct {
	// Status Info
	GitStatus    string
	RcloneStatus string
	DeviceRole   string

	// Configurable Form
	VaultPath       string
	RcloneRemote    string
	RclonePath      string
	IntervalSeconds int

	// rclone Details
	RcloneExecPath   string
	RcloneRemoteInfo string
}

// SettingsHandlers wires the Settings window's actions back to the business
// logic in main(). All fields are invoked on the Fyne main thread (they are
// triggered directly by widget callbacks) - handlers that need to perform
// slow work (installing Git/rclone, contacting Google Drive, initializing the
// node) must hand off to a goroutine themselves, e.g. via RunWithProgress,
// and call back into gui.* (Info/Confirm/ShowSettingsWindow/...) to update
// the UI once done.
type SettingsHandlers struct {
	// OnInstallGit is called with the form's *current* values (as currently
	// typed, not the snapshot the window was built with) when the user taps
	// "Install Git..." in the Status section. Omit (nil) to hide the button.
	OnInstallGit func(current SettingsFormData)
	// OnInstallRclone is the "Install rclone..." equivalent of OnInstallGit.
	// Omit (nil) to hide the button entirely.
	OnInstallRclone func(current SettingsFormData)
	// OnConfigureRemote is called with the form's current values when the
	// user taps "Configure Google Drive Remote...".
	OnConfigureRemote func(current SettingsFormData)
	// OnSave is called with the current form values when the user taps
	// "Save Settings".
	OnSave func(data SettingsFormData)
	// OnReset is called after the user confirms "Reset Configuration".
	OnReset func()
}

// ShowSettingsWindow (re)builds the shared window's content from data and
// handlers, then shows/focuses it. Safe to call from any goroutine; calling
// it again (e.g. after a background install finishes) refreshes the
// displayed status without losing the window's open/closed state.
func ShowSettingsWindow(data SettingsFormData, handlers SettingsHandlers) {
	fyne.Do(func() {
		mainWindow.SetContent(buildSettingsContent(data, handlers))
		mainWindow.SetTitle("UniteVault Settings")
		mainWindow.Show()
		mainWindow.RequestFocus()
	})
}

func statusLine(label, value, actionLabel string, action func()) fyne.CanvasObject {
	objs := []fyne.CanvasObject{
		widget.NewLabelWithStyle(label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel(value),
		layout.NewSpacer(),
	}
	if action != nil {
		objs = append(objs, widget.NewButton(actionLabel, action))
	}
	return container.NewHBox(objs...)
}

func buildSettingsContent(data SettingsFormData, handlers SettingsHandlers) fyne.CanvasObject {
	if data.IntervalSeconds <= 0 {
		data.IntervalSeconds = 120
	}
	if data.RcloneRemote == "" {
		data.RcloneRemote = "ObsidianVault"
	}
	if data.RclonePath == "" {
		data.RclonePath = "VaultBackup"
	}

	// --- Config section ---
	vaultEntry := widget.NewEntry()
	vaultEntry.SetText(data.VaultPath)
	vaultEntry.SetPlaceHolder("Path to your Obsidian Vault folder")

	selectFolderBtn := widget.NewButton("Select Folder...", func() {
		PickFolder("Select Obsidian Vault Directory", func(path string, ok bool) {
			if ok {
				vaultEntry.SetText(path)
			}
		})
	})
	vaultRow := container.NewBorder(nil, nil, nil, selectFolderBtn, vaultEntry)

	targetPathEntry := widget.NewEntry()
	targetPathEntry.SetText(data.RclonePath)

	intervalEntry := widget.NewEntry()
	intervalEntry.SetText(fmt.Sprintf("%d", data.IntervalSeconds))

	configCard := widget.NewCard("Config", "", widget.NewForm(
		widget.NewFormItem("Vault Directory Path", vaultRow),
		widget.NewFormItem("Google Drive Target Folder Path", targetPathEntry),
		widget.NewFormItem("Sync Interval (seconds)", intervalEntry),
	))

	// --- rclone section ---
	remoteEntry := widget.NewEntry()
	remoteEntry.SetText(data.RcloneRemote)

	rcloneForm := widget.NewForm(
		widget.NewFormItem("Remote Name", remoteEntry),
		widget.NewFormItem("Remote Status", widget.NewLabel(orDefault(data.RcloneRemoteInfo, "Unknown"))),
		widget.NewFormItem("Executable", widget.NewLabel(orDefault(data.RcloneExecPath, "Not Found"))),
	)

	// currentSnapshot captures the form's fields exactly as currently typed.
	// Every handler that may trigger a window rebuild (install/configure
	// buttons, not just Save) must pass this through so a background action
	// never clobbers input the user hasn't saved yet.
	currentSnapshot := func() SettingsFormData {
		sec, err := strconv.Atoi(strings.TrimSpace(intervalEntry.Text))
		if err != nil || sec <= 0 {
			sec = 120
		}
		return SettingsFormData{
			GitStatus:        data.GitStatus,
			RcloneStatus:     data.RcloneStatus,
			DeviceRole:       data.DeviceRole,
			VaultPath:        strings.TrimSpace(vaultEntry.Text),
			RcloneRemote:     strings.TrimSpace(remoteEntry.Text),
			RclonePath:       strings.TrimSpace(targetPathEntry.Text),
			IntervalSeconds:  sec,
			RcloneExecPath:   data.RcloneExecPath,
			RcloneRemoteInfo: data.RcloneRemoteInfo,
		}
	}

	configureRemoteBtn := widget.NewButton("Configure Google Drive Remote...", func() {
		if handlers.OnConfigureRemote != nil {
			handlers.OnConfigureRemote(currentSnapshot())
		}
	})
	rcloneCard := widget.NewCard("rclone", "", container.NewVBox(rcloneForm, configureRemoteBtn))

	// --- Status section ---
	var installGit, installRclone func()
	if data.GitStatus != "Installed" && handlers.OnInstallGit != nil {
		installGit = func() { handlers.OnInstallGit(currentSnapshot()) }
	}
	if data.RcloneStatus != "Installed" && handlers.OnInstallRclone != nil {
		installRclone = func() { handlers.OnInstallRclone(currentSnapshot()) }
	}
	statusCard := widget.NewCard("Status", "", container.NewVBox(
		statusLine("Git status:", orDefault(data.GitStatus, "Unknown"), "Install Git...", installGit),
		statusLine("rclone status:", orDefault(data.RcloneStatus, "Unknown"), "Install rclone...", installRclone),
		statusLine("Device role:", orDefault(data.DeviceRole, "Not Initialized"), "", nil),
	))

	// --- Bottom action buttons ---
	saveBtn := widget.NewButton("Save Settings", func() {
		if handlers.OnSave != nil {
			handlers.OnSave(currentSnapshot())
		}
	})
	saveBtn.Importance = widget.HighImportance

	// Save has nothing meaningful to do without a Vault - keep it disabled
	// until one is set, rather than letting the user click it and then
	// explaining why nothing happened. This also means the button starts
	// disabled right after Reset Configuration reopens an empty form, with
	// no separate "reset succeeded" state to track.
	updateSaveButtonState := func() {
		if strings.TrimSpace(vaultEntry.Text) == "" {
			saveBtn.Disable()
		} else {
			saveBtn.Enable()
		}
	}
	updateSaveButtonState()
	vaultEntry.OnChanged = func(string) { updateSaveButtonState() }

	cancelBtn := widget.NewButton("Cancel", func() {
		mainWindow.Hide()
	})

	resetBtn := widget.NewButton("Reset Configuration", func() {
		Confirm(
			"Reset Configuration",
			"Are you sure you want to reset UniteVault configuration?\nThis clears local settings and role info, returning this device to an uninitialized state.",
			func(confirmed bool) {
				if confirmed && handlers.OnReset != nil {
					handlers.OnReset()
				}
			},
		)
	})
	resetBtn.Importance = widget.DangerImportance

	buttonRow := container.NewBorder(nil, nil, resetBtn, container.NewHBox(cancelBtn, saveBtn))

	return container.NewBorder(
		nil, container.NewVBox(widget.NewSeparator(), buttonRow), nil, nil,
		container.NewVScroll(container.NewVBox(statusCard, configCard, rcloneCard)),
	)
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
