package gui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
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
	// CanPromoteToPrimary shows "Promote to Primary..." next to Device
	// role when true - normally only when this device is Secondary, but
	// also while PrimaryConflictActive (see below), since resolving a
	// conflict is the same action from this device's side (spec 3.6.1.4).
	CanPromoteToPrimary bool
	// PrimaryConflictActive is true while this device has an unresolved
	// multi-Primary conflict (spec 3.6.1.4) - Google Drive sync is paused
	// on every device that sees it, whichever side of the disagreement
	// they're on, until a human resolves it via "Promote to Primary...".
	PrimaryConflictActive bool
	// PrimaryConflictMessage is a human-readable, already-localized
	// explanation of the conflict (which other device is involved and
	// since when), shown only when PrimaryConflictActive.
	PrimaryConflictMessage string

	// Configurable Form
	VaultPath       string
	RcloneRemote    string
	RclonePath      string
	IntervalSeconds int

	// rclone Details
	RcloneExecPath   string
	RcloneRemoteInfo string
	// RcloneConfigured reports whether RcloneRemote is actually registered
	// with rclone (i.e. client.IsRemoteConfigured), independent of
	// RcloneRemoteInfo's own (localized, so unsafe to string-match)
	// display text - it's what actually gates showing "Remove Remote
	// Configuration...".
	RcloneConfigured bool
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
	// OnPromoteToPrimary is called (after the user confirms) with the
	// form's current values when they tap "Promote to Primary...". Shown
	// whenever data.CanPromoteToPrimary is true (spec 3.6.1.2 / 3.6.1.4).
	OnPromoteToPrimary func(current SettingsFormData)
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
		mainWindow.SetTitle(lang.L("UniteVault Settings"))
		// Resize to fit this exact content every time: how many
		// Install/Configure/Remove buttons are showing changes the form's
		// natural height release to release (and even within one session,
		// e.g. once Git gets installed), so a fixed window size either
		// leaves blank space below a shorter form or forces scrolling for a
		// taller one. A small fixed margin comfortably covers the window's
		// own border/padding chrome around the content.
		min := content.MinSize()
		mainWindow.Resize(fyne.NewSize(min.Width+24, min.Height+24))
		windowVisible = true
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
		// Mirrors config.DefaultIntervalSeconds - not imported directly to
		// keep this package independent of internal/config.
		data.IntervalSeconds = 600
	}
	if data.RcloneRemote == "" {
		data.RcloneRemote = "ObsidianVault"
	}
	// data.RclonePath's default is computed below, from the Vault folder's
	// name where possible (see targetPathEntry).

	// --- Obsidian Vault section ---
	vaultEntry := widget.NewEntry()
	vaultEntry.SetText(data.VaultPath)
	vaultEntry.SetPlaceHolder(lang.L("Your Obsidian Vault folder"))

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

	selectFolderBtn := widget.NewButton(lang.L("Select Folder..."), func() {
		PickFolder(lang.L("Select Your Obsidian Vault Folder"), func(path string, ok bool) {
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

	vaultCard := widget.NewCard(lang.L("Obsidian Vault"), "", widget.NewForm(
		widget.NewFormItem(lang.L("Vault Folder Location"), vaultRow),
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

	unsavedLabel := canvas.NewText("● "+lang.L("Unsaved changes"), theme.Color(theme.ColorNameWarning))
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

	rcloneBasicForm := widget.NewForm(
		// data.RcloneRemoteInfo is built in main.go via lang.L with template
		// data (it embeds a variable remote name), so it already arrives
		// pre-localized - it must not be wrapped in lang.L again here.
		widget.NewFormItem(lang.L("Remote Status"), widget.NewLabel(orDefault(data.RcloneRemoteInfo, lang.L("Unknown")))),
		widget.NewFormItem(lang.L("Executable"), widget.NewLabel(orDefault(data.RcloneExecPath, lang.L("Not Found")))),
	)

	// Remote Name / Target Folder Path / Sync Interval all have sensible
	// defaults that just work (ObsidianVault / the Vault's own folder name /
	// 600s) - collapsed by default as "Advanced Options" so the common case
	// isn't cluttered with fields nobody needs to touch, while still being
	// one click away for anyone who does want to customize them.
	rcloneAdvancedForm := widget.NewForm(
		widget.NewFormItem(lang.L("Remote Name"), remoteEntry),
		widget.NewFormItem(lang.L("Google Drive Target Folder Path"), targetPathEntry),
		widget.NewFormItem(lang.L("Sync Interval (seconds)"), intervalEntry),
	)
	rcloneAdvanced := widget.NewAccordion(widget.NewAccordionItem(lang.L("Advanced Options"), rcloneAdvancedForm))

	// currentSnapshot captures the form's fields exactly as currently typed.
	// Every handler that may trigger a window rebuild (install/configure
	// buttons, not just Save) must pass this through so a background action
	// never clobbers input the user hasn't saved yet.
	currentSnapshot := func() SettingsFormData {
		sec, err := strconv.Atoi(strings.TrimSpace(intervalEntry.Text))
		if err != nil || sec <= 0 {
			sec = 600 // mirrors config.DefaultIntervalSeconds
		}
		return SettingsFormData{
			GitStatus:              data.GitStatus,
			RcloneStatus:           data.RcloneStatus,
			DeviceRole:             data.DeviceRole,
			VaultPath:              strings.TrimSpace(vaultEntry.Text),
			RcloneRemote:           strings.TrimSpace(remoteEntry.Text),
			RclonePath:             strings.TrimSpace(targetPathEntry.Text),
			IntervalSeconds:        sec,
			RcloneExecPath:         data.RcloneExecPath,
			RcloneRemoteInfo:       data.RcloneRemoteInfo,
			RcloneConfigured:       data.RcloneConfigured,
			CanPromoteToPrimary:    data.CanPromoteToPrimary,
			PrimaryConflictActive:  data.PrimaryConflictActive,
			PrimaryConflictMessage: data.PrimaryConflictMessage,
		}
	}

	configureRemoteBtn := widget.NewButton(lang.L("Configure Google Drive Remote..."), func() {
		if handlers.OnConfigureRemote != nil {
			handlers.OnConfigureRemote(currentSnapshot())
		}
	})
	var rcloneButtonRow fyne.CanvasObject
	if data.RcloneConfigured && handlers.OnRemoveRemote != nil {
		removeRemoteBtn := widget.NewButton(lang.L("Remove Remote Configuration..."), func() {
			// Always remove the remote actually reported as "Configured"
			// (baselineRemote, captured when this window was built), not
			// whatever name currently sits typed-but-unsaved in the Remote
			// Name field - an unsaved edit there must never change which
			// remote gets deleted.
			snapshot := currentSnapshot()
			snapshot.RcloneRemote = baselineRemote
			handlers.OnRemoveRemote(snapshot)
		})
		rcloneButtonRow = container.NewGridWithColumns(2, configureRemoteBtn, removeRemoteBtn)
	} else {
		rcloneButtonRow = container.NewHBox(configureRemoteBtn)
	}
	rcloneCardContent := []fyne.CanvasObject{rcloneBasicForm, rcloneButtonRow, rcloneAdvanced}
	rcloneCard := widget.NewCard(lang.L("rclone"), "", container.NewVBox(rcloneCardContent...))

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
	var promoteToPrimaryLabel string
	var promoteToPrimary func()
	if data.CanPromoteToPrimary && handlers.OnPromoteToPrimary != nil {
		promoteToPrimaryLabel = lang.L("Promote to Primary...")
		message := lang.L("Promote this device to Primary?\n\nThis device will take over running the sync engine and Google Drive backups. If another device is still running as Primary, the conflict is detected automatically and Google Drive sync pauses on both devices until resolved.")
		if data.PrimaryConflictActive {
			message = lang.L("Authorize this device as Primary?\n\nThis resolves the current Primary conflict in favor of this device. The other device will automatically step down and resume as Secondary once it reconnects.")
		}
		promoteToPrimary = func() {
			snapshot := currentSnapshot()
			Confirm(promoteToPrimaryLabel, message, func(confirmed bool) {
				if confirmed {
					handlers.OnPromoteToPrimary(snapshot)
				}
			})
		}
	}
	statusRows := []fyne.CanvasObject{
		statusLine(lang.L("Git status:"), lang.L(orDefault(data.GitStatus, "Unknown")), lang.L("Install Git..."), installGit),
		statusLine(lang.L("rclone status:"), lang.L(orDefault(data.RcloneStatus, "Unknown")), lang.L("Install rclone..."), installRclone),
	}
	// ICloudStatus is only ever populated on Windows (see SettingsFormData) -
	// hiding the row entirely elsewhere instead of showing "Not Found" for a
	// concept (a separate iCloud install) that doesn't apply there.
	if data.ICloudStatus != "" {
		statusRows = append(statusRows, statusLine(lang.L("iCloud status:"), lang.L(data.ICloudStatus), lang.L("Install iCloud..."), installICloud))
	}
	if data.DriveSyncStatus != "" {
		// data.DriveSyncStatus is built in main.go via lang.L with template
		// data (it embeds a variable timestamp/error), so it already
		// arrives pre-localized - it must not be wrapped in lang.L again.
		statusRows = append(statusRows, statusLine(lang.L("Google Drive sync:"), data.DriveSyncStatus, "", nil))
	}
	statusRows = append(statusRows, statusLine(lang.L("Device role:"), lang.L(orDefault(data.DeviceRole, "N/A")), promoteToPrimaryLabel, promoteToPrimary))
	if data.PrimaryConflictActive {
		// data.PrimaryConflictMessage is built in main.go via lang.L with
		// template data (it embeds the other device's label/timestamp), so
		// it already arrives pre-localized.
		statusRows = append(statusRows, statusLine(lang.L("⚠ Primary conflict:"), data.PrimaryConflictMessage, "", nil))
	}
	statusCard := widget.NewCard(lang.L("Status"), "", container.NewVBox(statusRows...))

	// --- Bottom action buttons ---
	saveBtn := widget.NewButton(lang.L("Save Settings"), func() {
		if handlers.OnSave != nil {
			handlers.OnSave(currentSnapshot())
		}
	})
	saveBtn.Importance = widget.HighImportance

	cancelBtn := widget.NewButton(lang.L("Cancel"), func() {
		hideWindowNow()
	})

	resetBtn := widget.NewButton(lang.L("Reset Configuration"), func() {
		ConfirmDanger(
			lang.L("Reset Configuration"),
			lang.L("Are you sure you want to reset UniteVault configuration?\nThis clears local settings and role info, returning this device to an uninitialized state."),
			func(confirmed bool) {
				if confirmed && handlers.OnReset != nil {
					handlers.OnReset()
				}
			},
		)
	})

	buttonRow := container.NewBorder(nil, nil, resetBtn, container.NewHBox(cancelBtn, saveBtn), container.NewCenter(unsavedLabel))

	// ShowSettingsWindow resizes the window to this content's actual MinSize
	// (computed with the "Advanced Options" accordion closed) on every
	// rebuild, so the window fits exactly for the common case - never
	// leaving leftover blank space below a shorter form. But opening the
	// accordion grows the content *without* triggering a rebuild/resize
	// (Accordion has no toggle callback to hook), so the top section is
	// wrapped in VScroll as a safety net: it keeps the window's fixed size
	// while still making the expanded fields and the fixed bottom button row
	// reachable instead of being clipped outside the window bounds.
	//
	// A bare Scroll's own MinSize() ignores its content's natural height
	// entirely (Fyne hardcodes a 32px floor for a vertical-only scroller),
	// so leaving it unset shrank the whole window down to ~32px tall on
	// every open - SetMinSize pins it back to the topContent's real
	// (accordion-closed) height so ShowSettingsWindow's resize is unchanged,
	// while scrolling for an expanded accordion still works exactly the
	// same (that's evaluated against the window's actual on-screen size at
	// layout time, independent of this MinSize hint).
	topContent := container.NewVBox(statusCard, vaultCard, rcloneCard)
	scroll := container.NewVScroll(topContent)
	scroll.SetMinSize(topContent.MinSize())

	return container.NewBorder(
		nil, container.NewVBox(widget.NewSeparator(), buttonRow), nil, nil,
		scroll,
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
