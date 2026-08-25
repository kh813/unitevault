package gui

import (
	"errors"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// stubPickFolder replaces pickFolderFunc for the duration of the calling
// test so PickFolder never actually spawns a real OS folder dialog (which
// would hang a headless test run waiting for a user that isn't there).
func stubPickFolder(t *testing.T, path string, err error) {
	t.Helper()
	orig := pickFolderFunc
	pickFolderFunc = func(string) (string, error) { return path, err }
	t.Cleanup(func() { pickFolderFunc = orig })
}

// TestPickFolder_ReturnsChosenPath guards PickFolder's contract with its
// caller: it must report the platform folder picker's result via onPicked
// with ok=true, running on the Fyne main thread. PickFolder itself no
// longer touches Fyne's own dialog.FileDialog at all (see pickFolderFunc) -
// it shells out to the OS's native folder picker instead, since Fyne's
// built-in one looked visibly out of place next to real OS dialogs.
func TestPickFolder_ReturnsChosenPath(t *testing.T) {
	newTestWindow()
	stubPickFolder(t, "/Users/me/Vaults/MyVault", nil)

	done := make(chan struct{})
	var gotPath string
	var gotOK bool
	PickFolder("Select Your Obsidian Vault Folder", func(path string, ok bool) {
		gotPath, gotOK = path, ok
		close(done)
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for PickFolder's callback")
	}

	if !gotOK || gotPath != "/Users/me/Vaults/MyVault" {
		t.Errorf("expected ok=true path=%q, got ok=%v path=%q", "/Users/me/Vaults/MyVault", gotOK, gotPath)
	}
}

// TestPickFolder_ReportsCancellation guards that dismissing the native
// dialog (which reports an error, e.g. zenity.ErrCanceled) surfaces as
// ok=false rather than a panic or a phantom path.
func TestPickFolder_ReportsCancellation(t *testing.T) {
	newTestWindow()
	stubPickFolder(t, "", errors.New("dialog canceled"))

	done := make(chan struct{})
	var gotOK bool
	PickFolder("Select Your Obsidian Vault Folder", func(path string, ok bool) {
		gotOK = ok
		close(done)
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for PickFolder's callback")
	}

	if gotOK {
		t.Error("expected ok=false when the native dialog reports an error/cancellation")
	}
}

// TestConfirmDanger_UsesDangerImportanceOnConfirmButton guards the split
// between a button that merely *opens* a confirmation dialog (which should
// stay visually calm) and the dialog's own confirm ("Yes") button, which is
// where the red/danger color actually belongs for a genuinely destructive
// action. Confirm (non-danger) must keep the normal HighImportance styling.
func TestConfirmDanger_UsesDangerImportanceOnConfirmButton(t *testing.T) {
	newTestWindow()

	ConfirmDanger("Reset Configuration", "Are you sure?", func(confirmed bool) {})

	confirmBtn := findDialogConfirmButton(t)
	if confirmBtn.Importance != widget.DangerImportance {
		t.Errorf("expected ConfirmDanger's confirm button to use DangerImportance, got %v", confirmBtn.Importance)
	}
}

func TestConfirm_UsesHighImportanceOnConfirmButton(t *testing.T) {
	newTestWindow()

	Confirm("Update Available", "Install it now?", func(confirmed bool) {})

	confirmBtn := findDialogConfirmButton(t)
	if confirmBtn.Importance != widget.HighImportance {
		t.Errorf("expected Confirm's confirm button to keep HighImportance, got %v", confirmBtn.Importance)
	}
}

// findDialogConfirmButton locates the overlay dialog's confirm button by its
// non-default Importance rather than its label text, since dialog.NewConfirm
// localizes "Yes"/"No" (e.g. lang.L("Yes")) based on the environment's
// locale - only the confirm button ever has its Importance set explicitly
// (see ConfirmDialog.SetConfirmImportance), so it's the only one that won't
// be widget.MediumImportance (the zero value, used by the dismiss button).
func findDialogConfirmButton(t *testing.T) *widget.Button {
	t.Helper()
	overlays := mainWindow.Canvas().Overlays().List()
	if len(overlays) == 0 {
		t.Fatal("expected the confirm dialog to be shown as an overlay")
	}

	var found *widget.Button
	for _, o := range overlays {
		for _, obj := range test.LaidOutObjects(o) {
			if b, ok := obj.(*widget.Button); ok && b.Importance != widget.MediumImportance {
				found = b
			}
		}
	}
	if found == nil {
		t.Fatal("expected to find the dialog's confirm button")
	}
	return found
}

func TestInstallReminder_ChecksCheckboxState(t *testing.T) {
	newTestWindow()

	var gotDontShowAgain bool
	called := false

	InstallReminder("Setup Required", "Git and rclone are missing.", func(dontShowAgain bool) {
		called = true
		gotDontShowAgain = dontShowAgain
	})

	// The dialog is shown as an overlay on the shared window's canvas.
	overlays := mainWindow.Canvas().Overlays().List()
	if len(overlays) == 0 {
		t.Fatal("expected the reminder dialog to be shown as an overlay")
	}

	var check *widget.Check
	var okBtn *widget.Button
	for _, o := range overlays {
		for _, obj := range test.LaidOutObjects(o) {
			if c, ok := obj.(*widget.Check); ok {
				check = c
			}
			if b, ok := obj.(*widget.Button); ok && b.Text == "OK" {
				okBtn = b
			}
		}
	}
	if check == nil {
		t.Fatal("expected to find the 'Don't show this again' checkbox")
	}
	if okBtn == nil {
		t.Fatal("expected to find the OK button")
	}

	check.SetChecked(true)
	test.Tap(okBtn)

	if !called {
		t.Fatal("expected onClose to be invoked when OK is tapped")
	}
	if !gotDontShowAgain {
		t.Error("expected dontShowAgain=true after checking the box before tapping OK")
	}
}

// findOnlyDialogButton locates the single button in the overlay dialog -
// used for dialogs (like Info) that only ever have one button, where
// matching by label text is unreliable since Fyne localizes built-in
// button labels like "OK" based on the environment's locale.
func findOnlyDialogButton(t *testing.T) *widget.Button {
	t.Helper()
	overlays := mainWindow.Canvas().Overlays().List()
	if len(overlays) == 0 {
		t.Fatal("expected the dialog to be shown as an overlay")
	}
	var found *widget.Button
	for _, o := range overlays {
		for _, obj := range test.LaidOutObjects(o) {
			if b, ok := obj.(*widget.Button); ok {
				found = b
			}
		}
	}
	if found == nil {
		t.Fatal("expected to find the dialog's button")
	}
	return found
}

// TestInfo_HidesWindowAgainIfItWasHidden guards the fix for a real reported
// bug: triggering Info (e.g. "Check for Update..." -> "Up to Date") from the
// tray menu while Settings was closed forced the shared window open just to
// host the dialog, then left it sitting open (empty, or showing stale
// leftover Settings content) after the user dismissed what looked like it
// should've been a standalone dialog. The window must go back to hidden
// once the dialog it was shown for closes.
func TestInfo_HidesWindowAgainIfItWasHidden(t *testing.T) {
	newTestWindow()
	if windowVisible {
		t.Fatal("expected the window to start hidden")
	}

	Info("Up to Date", "You're running the latest version.")

	if !windowVisible {
		t.Fatal("expected Info to show the window in order to host its dialog")
	}

	test.Tap(findOnlyDialogButton(t))

	if windowVisible {
		t.Error("expected the window to hide again once the dialog it was shown for closes")
	}
}

// TestInfo_LeavesWindowOpenIfAlreadyVisible is the counterpart: if Settings
// was already open before Info was called, dismissing the dialog must not
// close a window the user opened on purpose.
func TestInfo_LeavesWindowOpenIfAlreadyVisible(t *testing.T) {
	newTestWindow()
	windowVisible = true // simulate Settings already being open

	Info("Up to Date", "You're running the latest version.")
	test.Tap(findOnlyDialogButton(t))

	if !windowVisible {
		t.Error("expected the window to remain open since it was already open before Info was called")
	}
}
