package gui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// walkObjects visits obj and every descendant it knows how to unpack
// (fyne.Container, widget.Card, widget.Form, container.Scroll), calling fn on
// each one. It intentionally only understands the composite types actually
// used by buildSettingsContent.
func walkObjects(obj fyne.CanvasObject, fn func(fyne.CanvasObject)) {
	if obj == nil {
		return
	}
	fn(obj)

	switch o := obj.(type) {
	case *fyne.Container:
		for _, child := range o.Objects {
			walkObjects(child, fn)
		}
	case *widget.Card:
		walkObjects(o.Content, fn)
	case *widget.Form:
		for _, item := range o.Items {
			walkObjects(item.Widget, fn)
		}
	case *container.Scroll:
		walkObjects(o.Content, fn)
	case *widget.Accordion:
		for _, item := range o.Items {
			walkObjects(item.Detail, fn)
		}
	}
}

func findButton(t *testing.T, root fyne.CanvasObject, text string) *widget.Button {
	t.Helper()
	var found *widget.Button
	walkObjects(root, func(o fyne.CanvasObject) {
		if b, ok := o.(*widget.Button); ok && b.Text == text {
			found = b
		}
	})
	if found == nil {
		t.Fatalf("button %q not found in settings window content", text)
	}
	return found
}

func findEntry(t *testing.T, root fyne.CanvasObject, placeholderOrText string) *widget.Entry {
	t.Helper()
	var found *widget.Entry
	walkObjects(root, func(o fyne.CanvasObject) {
		if e, ok := o.(*widget.Entry); ok && (e.Text == placeholderOrText || e.PlaceHolder == placeholderOrText) {
			found = e
		}
	})
	if found == nil {
		t.Fatalf("entry with text/placeholder %q not found in settings window content", placeholderOrText)
	}
	return found
}

func newTestWindow() {
	test.NewApp()
	mainWindow = test.NewWindow(nil)
	// Matches real startup: the shared window starts hidden until something
	// explicitly shows it (see ensureWindowVisible's wasHidden tracking).
	windowVisible = false
}

// hasFormItemText reports whether any widget.Form in the content tree has a
// field labeled exactly text (widget.FormItem.Text isn't a CanvasObject, so
// walkObjects can't see it - this walks the *widget.Form nodes directly).
func hasFormItemText(root fyne.CanvasObject, text string) bool {
	found := false
	walkObjects(root, func(o fyne.CanvasObject) {
		if f, ok := o.(*widget.Form); ok {
			for _, item := range f.Items {
				if item.Text == text {
					found = true
				}
			}
		}
	})
	return found
}

func TestBuildSettingsContent_FillsDefaults(t *testing.T) {
	newTestWindow()

	content := buildSettingsContent(SettingsFormData{VaultPath: "/tmp/vault"}, SettingsHandlers{})
	if content == nil {
		t.Fatal("expected non-nil content")
	}

	remoteEntry := findEntry(t, content, "ObsidianVault")
	if remoteEntry.Text != "ObsidianVault" {
		t.Errorf("expected default remote 'ObsidianVault', got %q", remoteEntry.Text)
	}

	// With a Vault path already set, the target path defaults to that
	// folder's own name rather than a fixed string shared by every Vault
	// (see TestBuildSettingsContent_TargetPathDefaultsToVaultName for why).
	targetEntry := findEntry(t, content, "vault")
	if targetEntry.Text != "vault" {
		t.Errorf("expected default target path to be the Vault folder name 'vault', got %q", targetEntry.Text)
	}

	intervalEntry := findEntry(t, content, "120")
	if intervalEntry.Text != "120" {
		t.Errorf("expected default interval '120', got %q", intervalEntry.Text)
	}
}

// TestBuildSettingsContent_VaultFieldUsesPlainLanguage guards against
// reintroducing developer jargon ("Directory", "Path") in the one field
// non-technical users see first - it should read "Vault Folder Location",
// not "Vault Directory Path".
func TestBuildSettingsContent_VaultFieldUsesPlainLanguage(t *testing.T) {
	newTestWindow()

	content := buildSettingsContent(SettingsFormData{}, SettingsHandlers{})

	if !hasFormItemText(content, "Vault Folder Location") {
		t.Error("expected the Vault field to be labeled 'Vault Folder Location'")
	}
	if hasFormItemText(content, "Vault Directory Path") {
		t.Error("expected the old 'Vault Directory Path' label to be gone")
	}
}

func TestBuildSettingsContent_SaveRoundTripsEditedValues(t *testing.T) {
	newTestWindow()

	var saved SettingsFormData
	saveCalled := false

	content := buildSettingsContent(SettingsFormData{
		VaultPath:    "/tmp/vault",
		RcloneRemote: "myremote",
		RclonePath:   "Backups/Vault",
	}, SettingsHandlers{
		OnSave: func(d SettingsFormData) {
			saveCalled = true
			saved = d
		},
	})

	saveBtn := findButton(t, content, "Save Settings")
	test.Tap(saveBtn)

	if !saveCalled {
		t.Fatal("expected OnSave to be invoked when Save Settings is tapped")
	}
	if saved.VaultPath != "/tmp/vault" {
		t.Errorf("expected VaultPath to round-trip, got %q", saved.VaultPath)
	}
	if saved.RcloneRemote != "myremote" {
		t.Errorf("expected RcloneRemote to round-trip, got %q", saved.RcloneRemote)
	}
	if saved.RclonePath != "Backups/Vault" {
		t.Errorf("expected RclonePath to round-trip, got %q", saved.RclonePath)
	}
	if saved.IntervalSeconds != 120 {
		t.Errorf("expected default IntervalSeconds 120, got %d", saved.IntervalSeconds)
	}
}

func TestBuildSettingsContent_InstallButtonsHiddenWhenInstalled(t *testing.T) {
	newTestWindow()

	installGitCalled := false
	content := buildSettingsContent(SettingsFormData{
		GitStatus:    "Installed",
		RcloneStatus: "Not Found",
	}, SettingsHandlers{
		OnInstallGit:    func(SettingsFormData) { installGitCalled = true },
		OnInstallRclone: func(SettingsFormData) {},
	})

	var gitButtonFound, rcloneButtonFound bool
	walkObjects(content, func(o fyne.CanvasObject) {
		if b, ok := o.(*widget.Button); ok {
			switch b.Text {
			case "Install Git...":
				gitButtonFound = true
			case "Install rclone...":
				rcloneButtonFound = true
			}
		}
	})

	if gitButtonFound {
		t.Error("expected no 'Install Git...' button when Git is already installed")
	}
	if !rcloneButtonFound {
		t.Error("expected 'Install rclone...' button when rclone is not found")
	}
	if installGitCalled {
		t.Error("OnInstallGit should not have been called")
	}
}

// TestBuildSettingsContent_ICloudRowVisibility guards ICloudStatus's
// tri-state behavior: "" hides the row entirely (non-Windows, where a
// separate iCloud install doesn't apply), "Not Found" shows the row with an
// install button, and "Installed" shows the row without one.
func TestBuildSettingsContent_ICloudRowVisibility(t *testing.T) {
	newTestWindow()

	notApplicable := buildSettingsContent(SettingsFormData{}, SettingsHandlers{
		OnInstallICloud: func(SettingsFormData) {},
	})
	if hasButton(notApplicable, "Install iCloud...") {
		t.Error("expected no iCloud row/button when ICloudStatus is empty (non-Windows)")
	}

	notFound := buildSettingsContent(SettingsFormData{ICloudStatus: "Not Found"}, SettingsHandlers{
		OnInstallICloud: func(SettingsFormData) {},
	})
	if !hasButton(notFound, "Install iCloud...") {
		t.Error("expected an 'Install iCloud...' button when ICloudStatus is 'Not Found'")
	}

	installed := buildSettingsContent(SettingsFormData{ICloudStatus: "Installed"}, SettingsHandlers{
		OnInstallICloud: func(SettingsFormData) {},
	})
	if hasButton(installed, "Install iCloud...") {
		t.Error("expected no 'Install iCloud...' button when ICloudStatus is 'Installed'")
	}
}

// TestBuildSettingsContent_DriveSyncStatusVisibility guards that the
// "Google Drive sync" status row only appears when DriveSyncStatus is
// populated, and always renders as a plain (non-actionable) row - there's
// nothing to click, it's just a summary of the last sync attempt.
func TestBuildSettingsContent_DriveSyncStatusVisibility(t *testing.T) {
	newTestWindow()

	empty := buildSettingsContent(SettingsFormData{}, SettingsHandlers{})
	if findLabelText(empty, "Google Drive sync:") {
		t.Error("expected no Google Drive sync row when DriveSyncStatus is empty")
	}

	content := buildSettingsContent(SettingsFormData{DriveSyncStatus: "Last synced: 2026-08-25 15:04"}, SettingsHandlers{})
	if !findLabelText(content, "Last synced: 2026-08-25 15:04") {
		t.Error("expected the Google Drive sync row to show the provided status text")
	}
}

func findLabelText(root fyne.CanvasObject, text string) bool {
	found := false
	walkObjects(root, func(o fyne.CanvasObject) {
		if l, ok := o.(*widget.Label); ok && l.Text == text {
			found = true
		}
	})
	return found
}

// TestBuildSettingsContent_AdvancedRcloneOptionsCollapsedByDefault guards
// that Remote Name / Google Drive Target Folder Path / Sync Interval - all
// of which have working defaults nobody needs to touch in the common case
// - live inside an Accordion that starts closed, rather than being shown
// inline every time the Settings window opens.
func TestBuildSettingsContent_AdvancedRcloneOptionsCollapsedByDefault(t *testing.T) {
	newTestWindow()

	content := buildSettingsContent(SettingsFormData{VaultPath: "/tmp/vault"}, SettingsHandlers{})

	var accordion *widget.Accordion
	walkObjects(content, func(o fyne.CanvasObject) {
		if a, ok := o.(*widget.Accordion); ok {
			accordion = a
		}
	})
	if accordion == nil {
		t.Fatal("expected an Accordion wrapping the advanced rclone options")
	}
	if len(accordion.Items) != 1 {
		t.Fatalf("expected exactly one Accordion item, got %d", len(accordion.Items))
	}
	if accordion.Items[0].Open {
		t.Error("expected the advanced options Accordion item to start closed")
	}

	// The fields must still exist (just collapsed) - findEntry walks into
	// the Accordion's Detail regardless of Open state, so this also guards
	// that they haven't been accidentally left out of the tree entirely.
	findEntry(t, content, "ObsidianVault")
}

// TestBuildSettingsContent_ScrollableAgainstAccordionExpansion guards
// against a regression where the window is only ever resized to the
// content's MinSize computed with the "Advanced Options" Accordion closed
// (see ShowSettingsWindow) - opening it later doesn't trigger a resize
// (Accordion exposes no toggle callback to hook), so without a scroll
// container the expanded fields and the Save/Cancel/Reset button row could
// be pushed outside the window's fixed, non-scrollable bounds.
func TestBuildSettingsContent_ScrollableAgainstAccordionExpansion(t *testing.T) {
	newTestWindow()

	content := buildSettingsContent(SettingsFormData{VaultPath: "/tmp/vault"}, SettingsHandlers{})

	var scroll *container.Scroll
	walkObjects(content, func(o fyne.CanvasObject) {
		if s, ok := o.(*container.Scroll); ok {
			scroll = s
		}
	})
	if scroll == nil {
		t.Fatal("expected the Settings content to include a scroll container so an expanded Advanced Options accordion can never be clipped outside the window")
	}
}

func TestBuildSettingsContent_ResetRequiresConfirmation(t *testing.T) {
	newTestWindow()

	resetCalled := false
	content := buildSettingsContent(SettingsFormData{VaultPath: "/tmp/vault"}, SettingsHandlers{
		OnReset: func() { resetCalled = true },
	})

	resetBtn := findButton(t, content, "Reset Configuration")
	test.Tap(resetBtn)

	// Reset goes through a Confirm dialog first; tapping the button alone
	// must not invoke OnReset synchronously.
	if resetCalled {
		t.Error("expected OnReset to require confirmation before firing")
	}
}

// TestBuildSettingsContent_ConfigureRemoteUsesEditedValue guards against a
// regression where editing the Remote Name field and then tapping "Configure
// Google Drive Remote..." passed the *original* (pre-edit) snapshot the
// window was built with, instead of what the user had just typed - making it
// look like the field couldn't be changed.
func TestBuildSettingsContent_ConfigureRemoteUsesEditedValue(t *testing.T) {
	newTestWindow()

	var gotRemote string
	content := buildSettingsContent(SettingsFormData{
		VaultPath:    "/tmp/vault",
		RcloneRemote: "ObsidianVault",
		RcloneStatus: "Installed",
	}, SettingsHandlers{
		OnConfigureRemote: func(current SettingsFormData) { gotRemote = current.RcloneRemote },
	})

	remoteEntry := findEntry(t, content, "ObsidianVault")
	remoteEntry.SetText("MyCustomRemote")

	configureBtn := findButton(t, content, "Configure Google Drive Remote...")
	test.Tap(configureBtn)

	if gotRemote != "MyCustomRemote" {
		t.Errorf("expected OnConfigureRemote to receive the edited remote name 'MyCustomRemote', got %q", gotRemote)
	}
}

// TestBuildSettingsContent_RemoveRemoteIgnoresUnsavedEdit guards against a
// bug where the "Remove Remote Configuration..." button removed whatever
// name was currently typed (but not yet saved) in the Remote Name field,
// instead of the remote actually reported as "Configured" in Status. An
// unsaved edit to that field must never change which remote gets deleted -
// otherwise the wrong remote could be removed, or a no-op could be
// misreported as success while the real configured remote and its Google
// Drive credentials are left untouched.
func TestBuildSettingsContent_RemoveRemoteIgnoresUnsavedEdit(t *testing.T) {
	newTestWindow()

	var gotRemote string
	content := buildSettingsContent(SettingsFormData{
		VaultPath:        "/tmp/vault",
		RcloneRemote:     "ObsidianVault",
		RcloneRemoteInfo: "Configured (ObsidianVault)",
		RcloneStatus:     "Installed",
	}, SettingsHandlers{
		OnRemoveRemote: func(current SettingsFormData) { gotRemote = current.RcloneRemote },
	})

	// Edit the Remote Name field without saving.
	remoteEntry := findEntry(t, content, "ObsidianVault")
	remoteEntry.SetText("SomeOtherUnsavedName")

	removeBtn := findButton(t, content, "Remove Remote Configuration...")
	test.Tap(removeBtn)

	if gotRemote != "ObsidianVault" {
		t.Errorf("expected OnRemoveRemote to receive the actually-configured remote 'ObsidianVault', got %q", gotRemote)
	}
}

func TestVaultBaseName(t *testing.T) {
	cases := map[string]string{
		"":                          "",
		"/Users/me/Vaults/MyVault":  "MyVault",
		"MyVault":                   "MyVault",
		"/Users/me/Vaults/MyVault/": "MyVault",
	}
	for input, want := range cases {
		if got := vaultBaseName(input); got != want {
			t.Errorf("vaultBaseName(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestShouldFollowVaultRename guards the rule that decides whether changing
// the Vault folder should also update the Google Drive Target Folder Path
// suggestion. This matters because rclone sync mirrors its destination
// exactly (deleting anything not in the source) - if the target path stayed
// fixed across a Vault switch, the next sync would silently delete the
// previous Vault's backed-up files. The rule must follow an untouched
// auto-suggestion but never overwrite a value the user deliberately typed.
func TestShouldFollowVaultRename(t *testing.T) {
	cases := []struct {
		name               string
		currentTargetPath  string
		lastAutoTargetPath string
		want               bool
	}{
		{"empty field always follows", "", "", true},
		{"empty field always follows even with a prior suggestion", "", "OldVault", true},
		{"matches the last auto-suggestion", "OldVault", "OldVault", true},
		{"legacy hardcoded default is treated as auto-generated", "VaultBackup", "", true},
		{"deliberately customized value is preserved", "MyCustomBackupName", "OldVault", false},
		{"a name that happens to match a different vault is not blindly followed", "SomeOtherVaultName", "OldVault", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shouldFollowVaultRename(c.currentTargetPath, c.lastAutoTargetPath)
			if got != c.want {
				t.Errorf("shouldFollowVaultRename(%q, %q) = %v, want %v", c.currentTargetPath, c.lastAutoTargetPath, got, c.want)
			}
		})
	}
}

// TestBuildSettingsContent_TargetPathDefaultsToVaultName guards against the
// silent-data-loss scenario directly: a brand new form (no saved
// RclonePath) should suggest the Vault folder's own name, not a fixed
// string shared by every Vault.
func TestBuildSettingsContent_TargetPathDefaultsToVaultName(t *testing.T) {
	newTestWindow()

	content := buildSettingsContent(SettingsFormData{
		VaultPath: "/Users/me/iCloud/MyResearchVault",
	}, SettingsHandlers{})

	targetEntry := findEntry(t, content, "MyResearchVault")
	if targetEntry.Text != "MyResearchVault" {
		t.Errorf("expected Google Drive Target Folder Path to default to the Vault folder name 'MyResearchVault', got %q", targetEntry.Text)
	}
}

// TestBuildSettingsContent_TargetPathFallsBackWithoutVault covers the case
// where there's no Vault path yet to derive a name from (e.g. a completely
// fresh, never-configured device) - the target path should still get some
// reasonable default rather than staying empty.
func TestBuildSettingsContent_TargetPathFallsBackWithoutVault(t *testing.T) {
	newTestWindow()

	content := buildSettingsContent(SettingsFormData{}, SettingsHandlers{})

	targetEntry := findEntry(t, content, "VaultBackup")
	if targetEntry.Text != "VaultBackup" {
		t.Errorf("expected fallback target path 'VaultBackup' when no Vault is set, got %q", targetEntry.Text)
	}
}

// TestBuildSettingsContent_RemoveRemoteButtonVisibility guards that the
// destructive "Remove Remote Configuration..." button only appears once
// there's actually something configured to remove.
func TestBuildSettingsContent_RemoveRemoteButtonVisibility(t *testing.T) {
	newTestWindow()

	notConfigured := buildSettingsContent(SettingsFormData{
		VaultPath:        "/tmp/vault",
		RcloneRemoteInfo: "Not configured - remote 'ObsidianVault' not found in rclone",
	}, SettingsHandlers{
		OnRemoveRemote: func(SettingsFormData) {},
	})
	if hasButton(notConfigured, "Remove Remote Configuration...") {
		t.Error("expected no Remove Remote Configuration button when the remote isn't configured")
	}

	configured := buildSettingsContent(SettingsFormData{
		VaultPath:        "/tmp/vault",
		RcloneRemoteInfo: "Configured (ObsidianVault)",
	}, SettingsHandlers{
		OnRemoveRemote: func(SettingsFormData) {},
	})
	if !hasButton(configured, "Remove Remote Configuration...") {
		t.Error("expected a Remove Remote Configuration button when the remote is configured")
	}

	configuredNoHandler := buildSettingsContent(SettingsFormData{
		VaultPath:        "/tmp/vault",
		RcloneRemoteInfo: "Configured (ObsidianVault)",
	}, SettingsHandlers{})
	if hasButton(configuredNoHandler, "Remove Remote Configuration...") {
		t.Error("expected no Remove Remote Configuration button when no OnRemoveRemote handler is wired")
	}
}

func hasButton(root fyne.CanvasObject, text string) bool {
	found := false
	walkObjects(root, func(o fyne.CanvasObject) {
		if b, ok := o.(*widget.Button); ok && b.Text == text {
			found = true
		}
	})
	return found
}

func findText(t *testing.T, root fyne.CanvasObject, text string) *canvas.Text {
	t.Helper()
	var found *canvas.Text
	walkObjects(root, func(o fyne.CanvasObject) {
		if c, ok := o.(*canvas.Text); ok && c.Text == text {
			found = c
		}
	})
	if found == nil {
		t.Fatalf("canvas text %q not found in settings window content", text)
	}
	return found
}

// TestBuildSettingsContent_DestructiveButtonsAreNotDanger guards the
// distinction between a button that merely *opens* a confirmation dialog
// (which should stay visually calm at all times) and the dialog's actual
// confirm button (which carries the danger color) - see ConfirmDanger.
// "Reset Configuration" and "Remove Remote Configuration..." sit on-screen
// permanently, so bright red there would overstate the risk of just looking
// at the Settings window; "Save Settings" is what should draw the eye.
func TestBuildSettingsContent_DestructiveButtonsAreNotDanger(t *testing.T) {
	newTestWindow()

	content := buildSettingsContent(SettingsFormData{
		VaultPath:        "/tmp/vault",
		RcloneRemoteInfo: "Configured (ObsidianVault)",
	}, SettingsHandlers{
		OnReset:        func() {},
		OnRemoveRemote: func(SettingsFormData) {},
	})

	resetBtn := findButton(t, content, "Reset Configuration")
	if resetBtn.Importance == widget.DangerImportance {
		t.Error("expected Reset Configuration to not use DangerImportance while just sitting on screen")
	}

	removeRemoteBtn := findButton(t, content, "Remove Remote Configuration...")
	if removeRemoteBtn.Importance == widget.DangerImportance {
		t.Error("expected Remove Remote Configuration... to not use DangerImportance while just sitting on screen")
	}

	saveBtn := findButton(t, content, "Save Settings")
	if saveBtn.Importance != widget.HighImportance {
		t.Error("expected Save Settings to remain the most prominent (HighImportance) button")
	}
}

// TestBuildSettingsContent_UnsavedChangesIndicator guards the "unsaved
// changes" hint: hidden on a freshly-built form, shown as soon as any field
// diverges from the value the window was built with, and hidden again if
// the user edits it back to that original value.
func TestBuildSettingsContent_UnsavedChangesIndicator(t *testing.T) {
	newTestWindow()

	content := buildSettingsContent(SettingsFormData{
		VaultPath:    "/tmp/vault",
		RcloneRemote: "ObsidianVault",
	}, SettingsHandlers{})

	indicator := findText(t, content, "● Unsaved changes")
	if !indicator.Hidden {
		t.Error("expected the unsaved-changes indicator to start hidden on a freshly-built form")
	}

	remoteEntry := findEntry(t, content, "ObsidianVault")
	remoteEntry.SetText("MyCustomRemote")
	if indicator.Hidden {
		t.Error("expected the unsaved-changes indicator to show once a field diverges from its original value")
	}

	remoteEntry.SetText("ObsidianVault")
	if !indicator.Hidden {
		t.Error("expected the unsaved-changes indicator to hide again once the field matches its original value")
	}
}

// TestBuildSettingsContent_RcloneButtonsLayout guards the layout of rclone buttons:
// - When remote is NOT configured (1 button): uses HBox
// - When remote IS configured (2 buttons): uses GridWithColumns(2)
func TestBuildSettingsContent_RcloneButtonsLayout(t *testing.T) {
	newTestWindow()

	t.Run("Single button layout when remote is not configured", func(t *testing.T) {
		content := buildSettingsContent(SettingsFormData{
			VaultPath:        "/tmp/vault",
			RcloneRemoteInfo: "Not configured",
		}, SettingsHandlers{})

		configureBtn := findButton(t, content, "Configure Google Drive Remote...")
		if configureBtn == nil {
			t.Fatal("expected Configure button")
		}
	})

	t.Run("Two button grid layout when remote is configured", func(t *testing.T) {
		content := buildSettingsContent(SettingsFormData{
			VaultPath:        "/tmp/vault",
			RcloneRemoteInfo: "Configured (ObsidianVault)",
		}, SettingsHandlers{
			OnRemoveRemote: func(SettingsFormData) {},
		})

		configureBtn := findButton(t, content, "Configure Google Drive Remote...")
		removeBtn := findButton(t, content, "Remove Remote Configuration...")
		if configureBtn == nil || removeBtn == nil {
			t.Fatal("expected both Configure and Remove buttons to be present")
		}
	})
}

