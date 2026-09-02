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
	// MultiDeviceStatus is an already-localized "Standalone" or "Syncing"
	// (or "" while DeviceRole is still N/A) - whether any other *PC*
	// (Mac/Windows - iPhone/iPad never run this app at all, so they never
	// count; spec 1.4) shows any sign of currently participating in this
	// Vault (spec 3.6.1.5). A Secondary is always "Syncing" (it implies a
	// Primary exists somewhere); a Primary is "Standalone" only once every
	// other PC that ever joined has since explicitly decommissioned itself
	// (Reset Configuration), or none ever did. Purely informational here;
	// it doesn't gate anything in this window - the Vault-change/Reset-
	// Configuration warnings it relaxes live in main.go.
	MultiDeviceStatus string
	// PrimaryConflictActive is true while this device has an unresolved
	// multi-Primary conflict (spec 3.6.1.4) - Google Drive sync is paused
	// on every device that sees it, whichever side of the disagreement
	// they're on, until a human resolves it via "Promote to Primary...".
	PrimaryConflictActive bool
	// PrimaryConflictMessage is a human-readable, already-localized
	// explanation of the conflict (which other device is involved and
	// since when), shown only when PrimaryConflictActive.
	PrimaryConflictMessage string
	// PendingConflictCount is the number of unresolved genuine content
	// conflicts (spec 3.3.2) - files where two or more devices changed the
	// same region since their common base, which auto-merge alone can't
	// resolve. Only ever non-zero on the Primary device (merging only ever
	// happens there). Shows a warning row and "Resolve Conflicts..."
	// button when > 0.
	PendingConflictCount int

	// Configurable Form
	VaultPath       string
	RcloneRemote    string
	RclonePath      string
	IntervalSeconds int
	// SyncMode is "drive", "icloud", or "gdrive_desktop" (spec 1.6.10) -
	// deliberately a plain string rather than config.SyncMode to keep this
	// package independent of internal/config (see targetPathEntry's
	// comment above for the same convention elsewhere in this struct). ""
	// means "not yet chosen" (a brand new, unconfigured device) and is
	// treated identically to "drive" everywhere this is read, mirroring
	// config.EffectiveSyncMode. Fixed permanently the first time a Vault
	// is ever saved - see SettingsHandlers.OnSave and this window's own
	// "locked" rendering below, keyed off VaultPath already being
	// non-empty.
	SyncMode string

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
	// OnMigrateVault is called with the form's current values when the
	// user taps "Migrate Vault to Local Folder..." (spec 1.6, "Vault
	// Migration"). Always shown when non-nil, regardless of whether a
	// Vault is already configured.
	OnMigrateVault func(current SettingsFormData)
	// OnResolveConflicts is called with the form's current values when the
	// user taps "Resolve Conflicts...". Shown whenever
	// data.PendingConflictCount > 0 (spec 3.3.2).
	OnResolveConflicts func(current SettingsFormData)
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
// settingsWindowWidthFactor widens the Settings window beyond content's own
// bare MinSize().Width - statusLine's own Border layout (see its own doc
// comment) no longer lets one long dynamic status value force the window
// wide, which fixed a real overflow bug, but as a side effect made
// content.MinSize().Width reflect only the narrowest width everything
// technically still fits at, which then looked visibly cramped on real
// devices (a follow-up user report, on both Mac and Windows).
const settingsWindowWidthFactor float32 = 1.55

// settingsWindowMaxHeight caps the window's height regardless of how tall
// its content naturally wants to be, so it always fits comfortably even on
// a small/laptop-resolution screen (e.g. 1366x768) once the title bar and
// window chrome are accounted for - a real, previously-shipped bug: the
// first-time-setup form (the interactive 3-way Sync Mode selector, each
// option with its own wrapped description, spec 1.6.10) is tall enough
// that on a real Windows device the window ended up sized taller than the
// actual screen, and got positioned such that its very top (the Sync Mode
// card - the first section of this content) rendered off-screen and
// unreachable, looking like it had vanished entirely rather than merely
// needing the window moved or scrolled. Fyne has no cross-platform API to
// query the actual screen size here, so this is a conservative fixed
// value; content taller than it simply scrolls (topContent is already
// wrapped in a VScroll, see buildSettingsContent) instead of forcing the
// window past it.
const settingsWindowMaxHeight float32 = 700

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
		//
		// Resizing twice - once to size the layout at the actual target
		// (widened) width, then re-measuring content.MinSize() for the
		// height that width really needs - mirrors
		// constrainWrappedDialogWidth's own "MinSize is one frame stale"
		// workaround (dialogs.go): a wrapped label's MinSize only reports
		// the correct wrapped height once it's actually been laid out at
		// its final width, and a wider window means every wrapped value
		// (e.g. the Google Drive sync status) re-flows into fewer, longer
		// lines than measuring at the original (narrower) width would
		// suggest.
		min := content.MinSize()
		width := min.Width*settingsWindowWidthFactor + 24
		mainWindow.Resize(fyne.NewSize(width, fyne.Min(min.Height+24, settingsWindowMaxHeight)))
		reflowed := content.MinSize()
		mainWindow.Resize(fyne.NewSize(width, fyne.Min(reflowed.Height+24, settingsWindowMaxHeight)))
		windowVisible = true
		mainWindow.Show()
		mainWindow.RequestFocus()
	})
}

// statusLine renders one Status card row. The value uses container.NewBorder
// (matching this file's own vaultRow pattern) rather than NewHBox: some
// values are dynamic, unbounded-length text (e.g. an rclone error message in
// the Google Drive sync row) - inside an HBox that has no width cap, such a
// value forces the whole row, and therefore the Settings window itself
// (ShowSettingsWindow sizes the window from content.MinSize()), wider than
// the desktop - a real, previously-shipped bug on Windows. Border's center
// slot fills only the width left over from the fixed-width label/button on
// either side and wraps within it instead.
func statusLine(label, value, actionLabel string, action func()) fyne.CanvasObject {
	valueLabel := widget.NewLabel(value)
	valueLabel.Wrapping = fyne.TextWrapWord

	var trailing fyne.CanvasObject
	if action != nil {
		trailing = widget.NewButton(actionLabel, action)
	}

	return container.NewBorder(
		nil, nil,
		widget.NewLabelWithStyle(label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		trailing,
		valueLabel,
	)
}

// newExclusiveCheckGroup builds a set of mutually-exclusive widget.Check
// options for Sync Mode's 3-way choice (spec 1.6.10), laid out via
// layout.NewFormLayout so each option's Check and its description sit side
// by side on one row, with every description's start column-aligned to
// the widest Check label - a real user request, after an earlier design
// (one row per option, the description wrapped on its own line below the
// Check) was reverted back to this more compact one-row-per-option look.
// widget.RadioGroup can't be used here since its per-option label is a
// canvas.Text, which (per its own doc comment: "No formatting or text
// parsing will be performed") can't wrap; FormLayout's content column
// stretches to fill the remaining row width and still lets the
// description's own Wrapping kick in on the rare row a description
// doesn't fit on one line, rather than ever overflowing past the window's
// edge. onSelect is called with the newly-selected option's index whenever
// the selection changes; exactly one option stays checked at all times
// (clicking the already-selected check back off is refused, mirroring an
// actual radio group).
func newExclusiveCheckGroup(labels, descriptions []string, selectedIndex int, onSelect func(index int)) fyne.CanvasObject {
	checks := make([]*widget.Check, len(labels))
	rows := make([]fyne.CanvasObject, 0, len(labels)*2)
	for i := range labels {
		i := i
		c := widget.NewCheck(labels[i], nil)
		c.Checked = i == selectedIndex
		c.OnChanged = func(checked bool) {
			if checked {
				selectedIndex = i
				for j, other := range checks {
					if j != i && other.Checked {
						other.Checked = false
						other.Refresh()
					}
				}
				onSelect(i)
			} else if i == selectedIndex {
				// Refuse to leave nothing selected - setting .Checked
				// directly (rather than calling SetChecked) and calling
				// Refresh instead avoids re-entering this same handler.
				c.Checked = true
				c.Refresh()
			}
		}
		checks[i] = c
		desc := widget.NewLabel(descriptions[i])
		desc.Wrapping = fyne.TextWrapWord
		rows = append(rows, c, desc)
	}
	return container.New(layout.NewFormLayout(), rows...)
}

func buildSettingsContent(data SettingsFormData, handlers SettingsHandlers) fyne.CanvasObject {
	if data.IntervalSeconds <= 0 {
		// Mirrors config.DefaultIntervalSeconds - not imported directly to
		// keep this package independent of internal/config.
		data.IntervalSeconds = 60
	}
	if data.RcloneRemote == "" {
		data.RcloneRemote = "Vault"
	}
	// data.RclonePath's default is computed below, from the Vault folder's
	// name where possible (see targetPathEntry).

	// --- Sync Mode section (spec 1.6.10) ---
	// Fixed permanently the first time a Vault is ever saved on this
	// device - modeLocked keys off VaultPath rather than RcloneConfigured
	// (unlike vaultChangeDisabled below) because the choice must stick even
	// before Google Drive gets configured, not just after.
	modeLocked := data.VaultPath != ""
	syncMode := data.SyncMode
	if syncMode == "" {
		syncMode = "drive"
	}
	syncModeValues := []string{"drive", "icloud", "gdrive_desktop"}
	syncModeLabels := map[string]string{
		"drive":          lang.L("Google Drive-centric"),
		"icloud":         lang.L("iCloud-centric"),
		"gdrive_desktop": lang.L("Google Drive (desktop app)"),
	}
	// Each option's description deliberately leads with the one fact that
	// actually decides which mode fits: whether the user needs iPhone/iPad
	// support (only iCloud-centric offers it) or already has this Vault
	// living inside a folder Google Drive's own desktop app syncs (in
	// which case this app must stay out of the way entirely, spec 1.6.10 -
	// running its own rclone-based sync on top of that app's would mean
	// two independent daemons touching the same files).
	syncModeDescriptions := map[string]string{
		"drive":          lang.L("Mac/Windows only (iPhone/iPad won't run Obsidian)"),
		"icloud":         lang.L("iPhone/iPad will run Obsidian and sync with Mac/Windows - required in this case"),
		"gdrive_desktop": lang.L("Vault already lives in a folder Google Drive's desktop app syncs (Mac/Windows only, no iPhone/iPad)"),
	}
	var syncModeContent fyne.CanvasObject
	if modeLocked {
		modeDisplay := syncModeLabels[syncMode]
		if modeDisplay == "" {
			modeDisplay = syncModeLabels["drive"]
		}
		syncModeContent = widget.NewForm(widget.NewFormItem(lang.L("Sync Mode"), widget.NewLabel(modeDisplay)))
	} else {
		selectedIndex := 0
		for i, v := range syncModeValues {
			if v == syncMode {
				selectedIndex = i
			}
		}
		labels := make([]string, len(syncModeValues))
		descriptions := make([]string, len(syncModeValues))
		for i, v := range syncModeValues {
			labels[i] = syncModeLabels[v]
			descriptions[i] = syncModeDescriptions[v]
		}
		selector := newExclusiveCheckGroup(labels, descriptions, selectedIndex, func(index int) {
			syncMode = syncModeValues[index]
		})
		hint := widget.NewLabel(lang.L("This can't be changed later without resetting configuration."))
		hint.Wrapping = fyne.TextWrapWord
		syncModeContent = container.NewVBox(selector, hint)
	}
	syncModeCard := widget.NewCard(lang.L("Sync Mode"), "", syncModeContent)

	// --- Obsidian Vault section ---
	vaultEntry := widget.NewEntry()
	vaultEntry.SetText(data.VaultPath)
	vaultPlaceholder := lang.L("Your Obsidian Vault folder")
	if !modeLocked {
		// Mode A/D's Vault lives inside a folder some other app already
		// syncs (iCloud Drive, or the user's own Google Drive desktop app),
		// not this app's managed ~/Obsidian/ folder - hinting at that up
		// front saves a round trip through "Select Folder..." to the wrong
		// place.
		vaultPlaceholder = lang.L("Your Obsidian Vault folder (for iCloud-centric mode: the Vault folder inside iCloud Drive, e.g. where Obsidian's \"iCloud\" storage option created it. For Google Drive (desktop app) mode: the Vault folder inside your Google Drive desktop app's synced folder)")
	}
	vaultEntry.SetPlaceHolder(vaultPlaceholder)

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

	// saveSettings refuses this exact combination server-side (spec 3.5.0.1)
	// - a configured remote is actively backing up whatever Vault this
	// device currently points at, so changing it here without removing the
	// remote first would eventually mis-target that remote's next sync.
	// Disabling both input widgets surfaces that constraint up front rather
	// than letting the user fill in a new path and only finding out it's
	// rejected after tapping Save Settings.
	vaultChangeDisabled := data.VaultPath != "" && data.RcloneConfigured
	vaultFormItems := []*widget.FormItem{widget.NewFormItem(lang.L("Vault Folder Location"), vaultRow)}
	if vaultChangeDisabled {
		vaultEntry.Disable()
		selectFolderBtn.Disable()
		hint := widget.NewLabel(lang.L("Remove the Google Drive remote first (rclone section below) to change the Vault folder."))
		hint.Wrapping = fyne.TextWrapWord
		vaultFormItems = append(vaultFormItems, widget.NewFormItem("", hint))
	}

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
	// defaults that just work (Vault / the Vault's own folder name /
	// 60s) - collapsed by default as "Advanced Options" so the common case
	// isn't cluttered with fields nobody needs to touch, while still being
	// one click away for anyone who does want to customize them.
	intervalHint := widget.NewLabel(lang.L("Local changes are scanned and merged every tick. When both Google Drive and an iCloud Bridge are configured, each one gets a turn on alternating ticks, so its effective interval is roughly double this value."))
	intervalHint.Wrapping = fyne.TextWrapWord
	rcloneAdvancedForm := widget.NewForm(
		widget.NewFormItem(lang.L("Remote Name"), remoteEntry),
		widget.NewFormItem(lang.L("Google Drive Target Folder Path"), targetPathEntry),
		widget.NewFormItem(lang.L("Sync Interval (seconds)"), intervalEntry),
		widget.NewFormItem("", intervalHint),
	)
	rcloneAdvanced := widget.NewAccordion(widget.NewAccordionItem(lang.L("Advanced Options"), rcloneAdvancedForm))

	// currentSnapshot captures the form's fields exactly as currently typed.
	// Every handler that may trigger a window rebuild (install/configure
	// buttons, not just Save) must pass this through so a background action
	// never clobbers input the user hasn't saved yet.
	currentSnapshot := func() SettingsFormData {
		sec, err := strconv.Atoi(strings.TrimSpace(intervalEntry.Text))
		if err != nil || sec <= 0 {
			sec = 60 // mirrors config.DefaultIntervalSeconds
		}
		return SettingsFormData{
			GitStatus:              data.GitStatus,
			RcloneStatus:           data.RcloneStatus,
			DeviceRole:             data.DeviceRole,
			VaultPath:              strings.TrimSpace(vaultEntry.Text),
			RcloneRemote:           strings.TrimSpace(remoteEntry.Text),
			RclonePath:             strings.TrimSpace(targetPathEntry.Text),
			IntervalSeconds:        sec,
			SyncMode:               syncMode,
			RcloneExecPath:         data.RcloneExecPath,
			RcloneRemoteInfo:       data.RcloneRemoteInfo,
			RcloneConfigured:       data.RcloneConfigured,
			CanPromoteToPrimary:    data.CanPromoteToPrimary,
			PrimaryConflictActive:  data.PrimaryConflictActive,
			PrimaryConflictMessage: data.PrimaryConflictMessage,
			PendingConflictCount:   data.PendingConflictCount,
		}
	}

	// Migrate Vault to Local Folder... (spec 1.6, "Vault Migration") moves
	// an existing Vault (typically one currently living inside iCloud
	// Drive) to this app's own local folder, sets up Google Drive sync
	// there, and seeds an iCloud Bridge copy if iCloud Drive is available -
	// always available (not gated on data.VaultPath), since it's also how
	// a brand new user with an existing iCloud-resident Vault gets started.
	// Vault Migration moves a Vault OUT of iCloud (or out of a Google Drive
	// desktop app's synced folder) into this app's own local folder -
	// exactly the opposite of what Mode A/D (spec 1.6.10) need, since both
	// deliberately keep the Vault wherever that other sync mechanism
	// already placed it permanently. Hidden whenever either mode is
	// selected/locked, mirroring shouldShowICloudMigrationReminder's own
	// same-reasoning suppression in main.go.
	var vaultCardContent []fyne.CanvasObject
	vaultCardContent = append(vaultCardContent, widget.NewForm(vaultFormItems...))
	if handlers.OnMigrateVault != nil && syncMode != "icloud" && syncMode != "gdrive_desktop" {
		migrateVaultBtn := widget.NewButton(lang.L("Migrate Vault to Local Folder..."), func() {
			handlers.OnMigrateVault(currentSnapshot())
		})
		vaultCardContent = append(vaultCardContent, migrateVaultBtn)
	}
	vaultCard := widget.NewCard(lang.L("Obsidian Vault"), "", container.NewVBox(vaultCardContent...))

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
	// The Status card is split into two columns to keep its height in check
	// as more rows accumulate (Git/rclone/iCloud install checks, Google
	// Drive sync, Device role, ...) - tall enough on a small screen to spill
	// past the display otherwise. Left column: install checks (a fixed,
	// small set - Git/rclone/iCloud, all "is this tool present" checks).
	// Right column: this device's actual runtime status (Google Drive sync
	// outcome, Device role) - conceptually different information, so
	// splitting along that line (rather than, say, alternating rows) keeps
	// each column internally consistent.
	// Mode D (spec 1.6.10) never uses Git or rclone at all - it never
	// merges (no cross-device coordination happens in this app for that
	// mode) and never touches Google Drive itself, so neither install
	// check is meaningful there.
	var installRows []fyne.CanvasObject
	if syncMode != "gdrive_desktop" {
		installRows = append(installRows,
			statusLine(lang.L("Git status:"), lang.L(orDefault(data.GitStatus, "Unknown")), lang.L("Install Git..."), installGit),
			statusLine(lang.L("rclone status:"), lang.L(orDefault(data.RcloneStatus, "Unknown")), lang.L("Install rclone..."), installRclone),
		)
	}
	// ICloudStatus is only ever populated on Windows (see SettingsFormData) -
	// hiding the row entirely elsewhere instead of showing "Not Found" for a
	// concept (a separate iCloud install) that doesn't apply there.
	if data.ICloudStatus != "" {
		installRows = append(installRows, statusLine(lang.L("iCloud status:"), lang.L(data.ICloudStatus), lang.L("Install iCloud..."), installICloud))
	}

	var operationalRows []fyne.CanvasObject
	if data.DriveSyncStatus != "" {
		// data.DriveSyncStatus is built in main.go via lang.L with template
		// data (it embeds a variable timestamp/error), so it already
		// arrives pre-localized - it must not be wrapped in lang.L again.
		operationalRows = append(operationalRows, statusLine(lang.L("Google Drive sync:"), data.DriveSyncStatus, "", nil))
	}
	// Primary/Secondary applies to both remaining sync modes (spec
	// 1.6.10) - iCloud mode still elects one, so Google Drive there always
	// gets exactly one canonical publisher instead of every device racing
	// to overwrite the same backup. Mode D has no such concept at all (no
	// cross-device coordination happens in this app for that mode).
	if syncMode != "gdrive_desktop" {
		deviceRoleValue := lang.L(orDefault(data.DeviceRole, "N/A"))
		if data.MultiDeviceStatus != "" {
			// MultiDeviceStatus is already localized (built via lang.L in
			// main.go), so it must not be wrapped in lang.L again - only the
			// surrounding template here needs its own localization.
			deviceRoleValue = lang.L("{{.Role}} ({{.Status}})", map[string]string{"Role": deviceRoleValue, "Status": data.MultiDeviceStatus})
		}
		operationalRows = append(operationalRows, statusLine(lang.L("Device role:"), deviceRoleValue, promoteToPrimaryLabel, promoteToPrimary))
	}
	statusRows := []fyne.CanvasObject{
		container.NewGridWithColumns(2, container.NewVBox(installRows...), container.NewVBox(operationalRows...)),
	}
	// A Secondary with no working Google Drive remote is otherwise
	// invisible to the user: it never errors (RunCycle just skips the
	// Drive push/pull entirely, spec 1.6.4), so nothing else in this
	// window would ever hint that it's not actually receiving Primary's
	// changes at all - Google Drive is the only channel a Secondary has
	// for that since the 1.6 migration away from iCloud-as-transport. Does
	// not apply in iCloud mode: there, a Secondary receives every other
	// device's changes via iCloud itself and never touches Google Drive at
	// all (spec 1.6.10), so an unconfigured remote there is harmless.
	if syncMode != "icloud" && data.DeviceRole == "secondary" && !data.RcloneConfigured {
		var configureRemote func()
		if handlers.OnConfigureRemote != nil {
			configureRemote = func() { handlers.OnConfigureRemote(currentSnapshot()) }
		}
		statusRows = append(statusRows, statusLine(
			lang.L("⚠ Google Drive not configured:"),
			lang.L("This device won't receive changes from other devices until Google Drive is set up"),
			lang.L("Configure Google Drive Remote..."), configureRemote,
		))
	}
	if data.PrimaryConflictActive {
		// data.PrimaryConflictMessage is built in main.go via lang.L with
		// template data (it embeds the other device's label/timestamp), so
		// it already arrives pre-localized.
		statusRows = append(statusRows, statusLine(lang.L("⚠ Primary conflict:"), data.PrimaryConflictMessage, "", nil))
	}
	if data.PendingConflictCount > 0 {
		var resolveConflicts func()
		if handlers.OnResolveConflicts != nil {
			resolveConflicts = func() { handlers.OnResolveConflicts(currentSnapshot()) }
		}
		statusRows = append(statusRows, statusLine(
			lang.L("⚠ Unresolved conflicts:"),
			lang.L("{{.Count}} file(s) need your input", map[string]string{"Count": strconv.Itoa(data.PendingConflictCount)}),
			lang.L("Resolve Conflicts..."), resolveConflicts,
		))
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
	// Mode D (spec 1.6.10) never touches rclone/Google Drive at all - the
	// user's own Google Drive desktop app handles sync entirely, so the
	// whole rclone card (remote status, target folder, sync interval) has
	// nothing to configure and would only be confusing here.
	//
	// syncModeCard goes first, ahead of statusCard - a real, previously-
	// shipped bug: statusCard's own height is unbounded (e.g. a long
	// rclone/Google Drive sync error message wraps into many lines, see
	// statusLine's own doc comment), and with the window's own height now
	// capped (settingsWindowMaxHeight), putting it first could push
	// syncModeCard - small and fixed-size, and the single fact most worth
	// seeing at a glance - down past the capped height, requiring a scroll
	// to reach it on every ordinary open instead of only in the rare case
	// that actually needs one.
	topCards := []fyne.CanvasObject{syncModeCard, statusCard, vaultCard}
	if syncMode != "gdrive_desktop" {
		topCards = append(topCards, rcloneCard)
	}
	topContent := container.NewVBox(topCards...)
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
