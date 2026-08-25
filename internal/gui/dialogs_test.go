package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

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
