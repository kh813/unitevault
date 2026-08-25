package gui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// SettingsFormData represents everything shown/edited in the single Settings
// window: the Status section, the Obsidian Vault path, and the rclone
// details (spec section 3.5.2 / 8.3).
type SettingsFormData struct {
	// Status Info
	GitStatus    string
	RcloneStatus string
	DeviceRole   string
	// ICloudStatus is Windows-only ("Installed" / "Not Found"); leave "" on
	// platforms where iCloud isn't a separate install (macOS/iOS) to hide
	// this row entirely instead of showing it as "not found" everywhere.
	ICloudStatus string
	// DriveSyncStatus is a human-readable summary of the most recent Google
	// Drive backup attempt (e.g. "Last synced: 2026-08-25 15:04", "Last
	// sync failed: <error>", or a note that this device's role doesn't
	// perform Google Drive backup). Always shown when non-empty.
	DriveSyncStatus string

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
	// OnInstallICloud is the "Install iCloud..." equivalent of OnInstallGit,
	// shown only when ICloudStatus is non-empty (Windows).
	OnInstallICloud func(current SettingsFormData)
	// OnConfigureRemote is called with the form's current values when the
	// user taps "Configure Google Drive Remote...".
	OnConfigureRemote func(current SettingsFormData)
	// OnRemoveRemote is called (after the user confirms) with the form's
	// current values when they tap "Remove Remote Configuration...". Only
	// shown when the remote currently appears configured.
	OnRemoveRemote func(current SettingsFormData)
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
		content := buildSettingsContent(data, handlers)
		mainWindow.SetContent(content)
		mainWindow.SetTitle("UniteVault Settings")
		// Resize to fit this exact content every time: how many
		// Install/Configure/Remove buttons are showing changes the form's
		// natural height release to release (and even within one session,
		// e.g. once Git gets installed), so a fixed window size either
		// leaves blank space below a shorter form or forces scrolling for a
		// taller one. A small fixed margin comfortably covers the window's
		// own border/padding chrome around the content.
		min := content.MinSize()
		mainWindow.Resize(fyne.NewSize(min.Width+24, min.Height+24))
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
	// data.RclonePath's default is computed below, from the Vault folder's
	// name where possible (see targetPathEntry).

	// --- Obsidian Vault section ---
	vaultEntry := widget.NewEntry()
	vaultEntry.SetText(data.VaultPath)
	vaultEntry.SetPlaceHolder("Your Obsidian Vault folder")

	// --- rclone section ---
	// Everything about the Google Drive backup lives here, not in the
	// Obsidian Vault section above: none of it does anything until the
	// rclone remote is actually configured, including the target folder and
	// how often backups run.
	remoteEntry := widget.NewEntry()
	remoteEntry.SetText(data.RcloneRemote)

	// The target folder path defaults to the Vault folder's own name rather
	// than a fixed string. rclone sync mirrors its destination exactly
	// (deleting anything not present in the source), so if two different
	// Vaults ever synced to the same target path, switching which Vault
	// this device points at would silently delete the other Vault's
	// backed-up files on the very next cycle. Namespacing by Vault name
	// keeps that from ever happening as long as this suggestion isn't
	// overridden. lastAutoTargetPath tracks the most recent suggestion so
	// we know whether the field still holds "an auto-suggestion" (safe to
	// keep following the Vault) or something the user deliberately typed
	// (never touch it again).
	targetPathEntry := widget.NewEntry()
	lastAutoTargetPath := vaultBaseName(data.VaultPath)
	initialTargetPath := data.RclonePath
	if initialTargetPath == "" {
		initialTargetPath = orDefault(lastAutoTargetPath, "VaultBackup")
	}
	targetPathEntry.SetText(initialTargetPath)
	if initialTargetPath != lastAutoTargetPath {
		// Whatever's shown didn't come from today's auto-suggestion (e.g. a
		// previously-saved custom value) - stop tracking it as one so a
		// future Vault change won't clobber it.
		lastAutoTargetPath = ""
	}

	selectFolderBtn := widget.NewButton("Select Folder...", func() {
		PickFolder("Select Your Obsidian Vault Folder", func(path string, ok bool) {
			if !ok {
				return
			}
			vaultEntry.SetText(path)
			newSuggestion := vaultBaseName(path)
			if shouldFollowVaultRename(strings.TrimSpace(targetPathEntry.Text), lastAutoTargetPath) {
				targetPathEntry.SetText(newSuggestion)
			}
			lastAutoTargetPath = newSuggestion
		})
	})
	vaultRow := container.NewBorder(nil, nil, nil, selectFolderBtn, vaultEntry)

	vaultCard := widget.NewCard("Obsidian Vault", "", widget.NewForm(
		widget.NewFormItem("Vault Folder Location", vaultRow),
	))

	intervalEntry := widget.NewEntry()
	intervalEntry.SetText(fmt.Sprintf("%d", data.IntervalSeconds))

	// unsavedLabel flags edits the user hasn't saved yet by comparing every
	// field's live text against its value at the moment this content was
	// built (i.e. what's actually on disk). It never disables Save itself -
	// Save must always stay clickable so it can also perform first-time
	// setup/validation (see saveSettings' "Vault Required" check) even when
	// nothing has been "changed" yet.
	baselineVault := strings.TrimSpace(vaultEntry.Text)
	baselineRemote := strings.TrimSpace(remoteEntry.Text)
	baselineTargetPath := strings.TrimSpace(targetPathEntry.Text)
	baselineInterval := strings.TrimSpace(intervalEntry.Text)

	unsavedLabel := canvas.NewText("● Unsaved changes", theme.Color(theme.ColorNameWarning))
	unsavedLabel.TextStyle = fyne.TextStyle{Bold: true}
	unsavedLabel.Hidden = true

	updateDirtyState := func(string) {
		dirty := strings.TrimSpace(vaultEntry.Text) != baselineVault ||
			strings.TrimSpace(remoteEntry.Text) != baselineRemote ||
			strings.TrimSpace(targetPathEntry.Text) != baselineTargetPath ||
			strings.TrimSpace(intervalEntry.Text) != baselineInterval
		unsavedLabel.Hidden = !dirty
		unsavedLabel.Refresh()
	}
	vaultEntry.OnChanged = updateDirtyState
	remoteEntry.OnChanged = updateDirtyState
	targetPathEntry.OnChanged = updateDirtyState
	intervalEntry.OnChanged = updateDirtyState

	rcloneForm := widget.NewForm(
		widget.NewFormItem("Remote Name", remoteEntry),
		widget.NewFormItem("Remote Status", widget.NewLabel(orDefault(data.RcloneRemoteInfo, "Unknown"))),
		widget.NewFormItem("Executable", widget.NewLabel(orDefault(data.RcloneExecPath, "Not Found"))),
		widget.NewFormItem("Google Drive Target Folder Path", targetPathEntry),
		widget.NewFormItem("Sync Interval (seconds)", intervalEntry),
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
	rcloneButtons := []fyne.CanvasObject{configureRemoteBtn}
	if strings.HasPrefix(data.RcloneRemoteInfo, "Configured") && handlers.OnRemoveRemote != nil {
		removeRemoteBtn := widget.NewButton("Remove Remote Configuration...", func() {
			handlers.OnRemoveRemote(currentSnapshot())
		})
		rcloneButtons = append(rcloneButtons, removeRemoteBtn)
	}
	rcloneCard := widget.NewCard("rclone", "", container.NewVBox(append([]fyne.CanvasObject{rcloneForm}, rcloneButtons...)...))

	// --- Status section ---
	var installGit, installRclone, installICloud func()
	if data.GitStatus != "Installed" && handlers.OnInstallGit != nil {
		installGit = func() { handlers.OnInstallGit(currentSnapshot()) }
	}
	if data.RcloneStatus != "Installed" && handlers.OnInstallRclone != nil {
		installRclone = func() { handlers.OnInstallRclone(currentSnapshot()) }
	}
	if data.ICloudStatus != "" && data.ICloudStatus != "Installed" && handlers.OnInstallICloud != nil {
		installICloud = func() { handlers.OnInstallICloud(currentSnapshot()) }
	}
	statusRows := []fyne.CanvasObject{
		statusLine("Git status:", orDefault(data.GitStatus, "Unknown"), "Install Git...", installGit),
		statusLine("rclone status:", orDefault(data.RcloneStatus, "Unknown"), "Install rclone...", installRclone),
	}
	// ICloudStatus is only ever populated on Windows (see SettingsFormData) -
	// hiding the row entirely elsewhere instead of showing "Not Found" for a
	// concept (a separate iCloud install) that doesn't apply there.
	if data.ICloudStatus != "" {
		statusRows = append(statusRows, statusLine("iCloud status:", data.ICloudStatus, "Install iCloud...", installICloud))
	}
	if data.DriveSyncStatus != "" {
		statusRows = append(statusRows, statusLine("Google Drive sync:", data.DriveSyncStatus, "", nil))
	}
	statusRows = append(statusRows, statusLine("Device role:", orDefault(data.DeviceRole, "Not Initialized"), "", nil))
	statusCard := widget.NewCard("Status", "", container.NewVBox(statusRows...))

	// --- Bottom action buttons ---
	saveBtn := widget.NewButton("Save Settings", func() {
		if handlers.OnSave != nil {
			handlers.OnSave(currentSnapshot())
		}
	})
	saveBtn.Importance = widget.HighImportance

	cancelBtn := widget.NewButton("Cancel", func() {
		mainWindow.Hide()
	})

	resetBtn := widget.NewButton("Reset Configuration", func() {
		ConfirmDanger(
			"Reset Configuration",
			"Are you sure you want to reset UniteVault configuration?\nThis clears local settings and role info, returning this device to an uninitialized state.",
			func(confirmed bool) {
				if confirmed && handlers.OnReset != nil {
					handlers.OnReset()
				}
			},
		)
	})

	buttonRow := container.NewBorder(nil, nil, resetBtn, container.NewHBox(cancelBtn, saveBtn), container.NewCenter(unsavedLabel))

	// No scroll container: ShowSettingsWindow resizes the window to this
	// content's actual MinSize on every rebuild, so the window always fits
	// exactly - never leaving leftover blank space below a shorter form,
	// and never needing to scroll for a taller one.
	return container.NewBorder(
		nil, container.NewVBox(widget.NewSeparator(), buttonRow), nil, nil,
		container.NewVBox(statusCard, vaultCard, rcloneCard),
	)
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// shouldFollowVaultRename decides whether the Google Drive Target Folder
// Path should be updated to follow a newly picked Vault folder, given what
// currently shows there (currentTargetPath) and the most recent
// auto-suggested value (lastAutoTargetPath, "" if the current value isn't
// one - see the comment above targetPathEntry's declaration). It returns
// false whenever the field holds something the user deliberately chose, so
// an intentional customization is never silently overwritten.
func shouldFollowVaultRename(currentTargetPath, lastAutoTargetPath string) bool {
	return currentTargetPath == "" || currentTargetPath == lastAutoTargetPath || currentTargetPath == "VaultBackup"
}

// vaultBaseName returns the folder name of vaultPath, suitable as a
// suggested (and safely distinguishing) Google Drive target folder name.
// Returns "" for an empty vaultPath (filepath.Base("") is "." which would
// make a poor suggestion).
func vaultBaseName(vaultPath string) string {
	vaultPath = strings.TrimSpace(vaultPath)
	if vaultPath == "" {
		return ""
	}
	return filepath.Base(vaultPath)
}
