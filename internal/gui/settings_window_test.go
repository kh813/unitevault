package gui

import (
	"testing"

	"fyne.io/fyne/v2"
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
