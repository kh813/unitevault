package gui

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
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

// TestPickFolder_NeverUsesDoAndWait guards a real, reported bug: PickFolder
// is called directly and synchronously from settings_window.go's "Select
// Folder..." button - i.e. from the Fyne main goroutine itself, while the
// app is already running. fyne.DoAndWait there deadlocks the entire app
// solid the instant the button is clicked (it blocks the calling goroutine
// waiting for Fyne's main loop to process a closure it just queued, but
// the main loop *is* that same, now-blocked goroutine - nothing is left
// to ever drain the queue). PickFolder must use the non-blocking fyne.Do,
// which only enqueues and returns, for every fyne package call it makes.
//
// This can't be caught by actually calling PickFolder in this test suite:
// the fyne/test driver used here runs DoFromGoroutine synchronously with
// no queue at all regardless of wait, so it can never reproduce the
// deadlock that only happens under the real (glfw) driver - hence this
// guards the source text directly instead.
func TestPickFolder_NeverUsesDoAndWait(t *testing.T) {
	src, err := os.ReadFile("dialogs.go")
	if err != nil {
		t.Fatalf("failed to read dialogs.go: %v", err)
	}

	body, ok := funcBody(string(src), "func PickFolder(")
	if !ok {
		t.Fatal("expected to find func PickFolder in dialogs.go")
	}
	if strings.Contains(body, "fyne.DoAndWait(") {
		t.Error("PickFolder must never call fyne.DoAndWait - it deadlocks the app when PickFolder is called from the Fyne main goroutine (e.g. a button's OnTapped), which is exactly how it's actually used. Use fyne.Do instead.")
	}
}

// funcBody extracts the brace-delimited body text of the first top-level
// function whose declaration starts with marker (e.g. "func Foo(") in src,
// by counting braces from that function's opening "{" - good enough for
// this package's own well-formatted source, without pulling in go/parser
// for a single test.
func funcBody(src, marker string) (string, bool) {
	start := strings.Index(src, marker)
	if start == -1 {
		return "", false
	}
	open := strings.IndexByte(src[start:], '{')
	if open == -1 {
		return "", false
	}
	open += start

	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[open : i+1], true
			}
		}
	}
	return "", false
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

// TestChoiceN_InvokesOnChoiceWithSelectedIndex guards ChoiceN's core
// contract: tapping the Nth labeled button reports that 1-based index,
// regardless of how many options were given.
func TestChoiceN_InvokesOnChoiceWithSelectedIndex(t *testing.T) {
	newTestWindow()

	var got int
	called := false
	ChoiceN("Resolve Conflict", "Pick a version.", []string{"mac-mini", "iphone", "windows-pc"}, func(result int) {
		called = true
		got = result
	})

	btn := findDialogButtonByLabel(t, "iphone")
	test.Tap(btn)

	if !called {
		t.Fatal("expected onChoice to be invoked when an option is tapped")
	}
	if got != 2 {
		t.Errorf("expected the 2nd option's 1-based index (2), got %d", got)
	}
}

// TestChoiceN_CancelReportsZero guards the "dismissed without choosing"
// case, matching Choice's own convention.
func TestChoiceN_CancelReportsZero(t *testing.T) {
	newTestWindow()

	var got = -1
	ChoiceN("Resolve Conflict", "Pick a version.", []string{"mac-mini", "iphone"}, func(result int) {
		got = result
	})

	overlays := mainWindow.Canvas().Overlays().List()
	if len(overlays) == 0 {
		t.Fatal("expected the dialog to be shown as an overlay")
	}
	var cancelBtn *widget.Button
	for _, o := range overlays {
		for _, obj := range test.LaidOutObjects(o) {
			if b, ok := obj.(*widget.Button); ok && b.Text != "mac-mini" && b.Text != "iphone" {
				cancelBtn = b
			}
		}
	}
	if cancelBtn == nil {
		t.Fatal("expected to find the dialog's Cancel button")
	}
	test.Tap(cancelBtn)

	if got != 0 {
		t.Errorf("expected onChoice(0) when cancelled, got %d", got)
	}
}

// findDialogButtonByLabel locates a button in the overlay dialog by its
// exact label text.
func findDialogLabel(t *testing.T, text string) *widget.Label {
	t.Helper()
	overlays := mainWindow.Canvas().Overlays().List()
	if len(overlays) == 0 {
		t.Fatal("expected the dialog to be shown as an overlay")
	}
	for _, o := range overlays {
		for _, obj := range test.LaidOutObjects(o) {
			if l, ok := obj.(*widget.Label); ok && l.Text == text {
				return l
			}
		}
	}
	t.Fatalf("expected to find a label with text %q", text)
	return nil
}

// TestChoiceN_LongMessageWraps guards a real, previously-shipped bug: the
// message label in Choice/ChoiceN's own hand-built dialog.NewCustom (unlike
// Fyne's own Confirm/Info dialogs, which wrap correctly out of the box)
// defaulted to no wrapping at all, so a long message (e.g. the iCloud
// migration reminder, spec 1.6.1/1.6.7) rendered as one unwrapped line wide
// enough to overflow past the window/screen edge instead of wrapping
// downward within it.
func TestChoiceN_LongMessageWraps(t *testing.T) {
	newTestWindow()
	mainWindow.Resize(fyne.NewSize(900, 700))

	longMessage := "Your Obsidian Vault currently isn't in UniteVault's own local folder:\n/Users/someone/Library/Mobile Documents/iCloud~md~obsidian/Documents/MyVaultName\n\nIf this location is also synced by another service (iCloud Drive, Google Drive Desktop, Dropbox, ...), that service's own sync daemon can edit the same files UniteVault and Obsidian are editing at the same time, which can lead to duplicate or conflicted files."

	ChoiceN("Move Your Vault?", longMessage, []string{"Migrate Now", "Don't Show This Again"}, func(int) {})

	label := findDialogLabel(t, longMessage)
	if label.Wrapping != fyne.TextWrapWord {
		t.Fatal("expected the message label to wrap words")
	}

	unwrappedWidth := widget.NewLabel(longMessage).MinSize().Width
	if label.Size().Width >= unwrappedWidth {
		t.Errorf("expected the wrapped label's actual width (%v) to be well under its unwrapped natural width (%v)", label.Size().Width, unwrappedWidth)
	}
	// A correctly wrapped multi-paragraph message must span several text
	// rows, so its height should be well above a single line's height -
	// an unwrapped label would instead report just one line's height
	// regardless of how wide (or narrow) it ends up.
	oneLineHeight := widget.NewLabel("single line").MinSize().Height
	if label.Size().Height <= oneLineHeight*2 {
		t.Errorf("expected the wrapped label to span multiple lines (height %v, one line is %v), looks unwrapped", label.Size().Height, oneLineHeight)
	}
}

func findDialogButtonByLabel(t *testing.T, label string) *widget.Button {
	t.Helper()
	overlays := mainWindow.Canvas().Overlays().List()
	if len(overlays) == 0 {
		t.Fatal("expected the dialog to be shown as an overlay")
	}
	for _, o := range overlays {
		for _, obj := range test.LaidOutObjects(o) {
			if b, ok := obj.(*widget.Button); ok && b.Text == label {
				return b
			}
		}
	}
	t.Fatalf("expected to find a button labeled %q", label)
	return nil
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

// stubInfo replaces infoFunc for the duration of the test.
func stubInfo(t *testing.T, fn func(title, message string) error) {
	t.Helper()
	orig := infoFunc
	infoFunc = fn
	t.Cleanup(func() { infoFunc = orig })
}

// TestInfo_CallsNativeDialogInvoker guards that Info delegates to the OS native dialog
// without altering the main Settings window's visible state.
func TestInfo_CallsNativeDialogInvoker(t *testing.T) {
	newTestWindow()
	windowVisible = false

	called := make(chan struct{})
	var gotTitle, gotMessage string
	stubInfo(t, func(title, message string) error {
		gotTitle, gotMessage = title, message
		close(called)
		return nil
	})

	Info("Up to Date", "You're running the latest version.")

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for infoFunc to be called")
	}

	if gotTitle != "Up to Date" || gotMessage != "You're running the latest version." {
		t.Errorf("unexpected args: title=%q, message=%q", gotTitle, gotMessage)
	}

	if windowVisible {
		t.Error("expected windowVisible to remain unchanged when Info is triggered")
	}
}
