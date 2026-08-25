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

	targetEntry := findEntry(t, content, "VaultBackup")
	if targetEntry.Text != "VaultBackup" {
		t.Errorf("expected default target path 'VaultBackup', got %q", targetEntry.Text)
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
