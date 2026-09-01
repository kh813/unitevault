package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/widget"
	"github.com/ncruces/zenity"
)

// Info shows an informational/alert dialog as an overlay on the shared
// window (see ensureWindowVisible), the same pattern Confirm/Choice/ChoiceN
// use. It used to shell out to zenity for a separate OS-native dialog
// window instead - a real, previously-shipped bug on Windows: that
// separate window has no parent/owner relationship with the shared window,
// so nothing tells the window manager to keep it above an already-focused
// Settings window, and it could render behind it (e.g. after "Remove
// rclone Remote..." completes while Settings is open). An overlay on the
// same window Settings itself uses can't end up behind it, since it isn't
// a different window at all. Safe to call from any goroutine.
func Info(title, message string) {
	fyne.Do(func() {
		wasHidden := ensureWindowVisible()
		d := dialog.NewInformation(title, message, mainWindow)
		if wasHidden {
			d.SetOnClosed(hideWindowNow)
		}
		d.Show()
		mainWindow.RequestFocus()
	})
}

// Confirm shows a Yes/No confirmation dialog. Fyne dialogs are non-blocking,
// so the result is delivered via onResult once the user responds. Safe to
// call from any goroutine; onResult always runs on the Fyne main thread.
func Confirm(title, message string, onResult func(confirmed bool)) {
	showConfirm(title, message, widget.HighImportance, onResult)
}

// ConfirmDanger is Confirm for a genuinely destructive action (deleting
// configuration, overwriting a previous backup, ...): the confirming button
// is styled as a warning (red) instead of the normal blue. Buttons that
// merely *open* this dialog should stay visually calm (default importance)
// - the warning color belongs on the one button that actually commits to
// the destructive action, not on something sitting on-screen at all times.
func ConfirmDanger(title, message string, onResult func(confirmed bool)) {
	showConfirm(title, message, widget.DangerImportance, onResult)
}

func showConfirm(title, message string, confirmImportance widget.Importance, onResult func(confirmed bool)) {
	fyne.Do(func() {
		wasHidden := ensureWindowVisible()
		d := dialog.NewConfirm(title, message, func(ok bool) {
			if onResult != nil {
				onResult(ok)
			}
		}, mainWindow)
		d.SetConfirmImportance(confirmImportance)
		if wasHidden {
			d.SetOnClosed(hideWindowNow)
		}
		d.Show()
	})
}

// wrappedMessageLabel returns a word-wrapping label for use as a custom
// dialog's message content. A plain widget.Label defaults to no wrapping
// (fyne.TextWrapOff) - Choice/ChoiceN build their own dialog.NewCustom
// rather than using Fyne's own Confirm/Info dialogs (which wrap correctly
// out of the box via a mechanism only Fyne's dialog package itself can
// use), so a long message silently renders as one long unwrapped line that
// overflows past the window/screen edge instead of growing the window
// downward - a real, previously-shipped bug. Must be paired with
// constrainWrappedDialogWidth (below), since setting Wrapping alone does
// nothing without also constraining the width it wraps against.
func wrappedMessageLabel(message string) *widget.Label {
	l := widget.NewLabel(message)
	l.Wrapping = fyne.TextWrapWord
	return l
}

// constrainWrappedDialogWidth caps d's width so a wrappedMessageLabel
// inside it actually wraps, instead of the dialog auto-sizing to whatever
// width the underlying label widget's own MinSize() currently reports -
// which, for wrapping text, reflects the width of its single longest
// unbreakable word (a handful of pixels), not a sensible paragraph width;
// left unconstrained, the dialog collapses to that width and the message
// renders as one absurdly narrow, absurdly tall column instead. Mirrors
// Fyne's own internal text-dialog sizing rule (dialog/text.go): the
// smaller of the message's natural single-line width, an absolute cap, and
// 90% of the parent window's width.
//
// Must be called after d.Show() (dialog.Dialog.Resize's own doc comment)
// and, empirically, twice: Resize derives the dialog's actual size from
// size.Max(d.MinSize()), and d.MinSize() one frame after Show still
// reflects the label's pre-wrap state - the same one-frame-stale-MinSize
// class of issue documented against Fyne issue #4648 in dialog/text.go.
// The first call re-wraps the label at the target width (still sizing the
// dialog off the old, taller MinSize); the second call reads the
// now-correct, properly-wrapped MinSize and settles the dialog at its
// actual height.
func constrainWrappedDialogWidth(d dialog.Dialog, message string, parent fyne.Window) {
	const maxAbsoluteWidth float32 = 600
	const maxWinPercentWidth float32 = 0.9

	width := widget.NewLabel(message).MinSize().Width + 48
	if width > maxAbsoluteWidth {
		width = maxAbsoluteWidth
	}
	if parent != nil {
		if maxWin := parent.Canvas().Size().Width * maxWinPercentWidth; width > maxWin {
			width = maxWin
		}
	}

	d.Resize(fyne.NewSize(width, 0))
	d.Resize(fyne.NewSize(width, 0))
}

// Choice shows a dialog with two named action buttons plus an implicit Cancel
// (closing the dialog without choosing). onChoice is called exactly once with
// 1 (btn1Text chosen), 2 (btn2Text chosen), or 0 (cancelled/closed). Safe to
// call from any goroutine; onChoice always runs on the Fyne main thread.
func Choice(title, message, btn1Text, btn2Text string, onChoice func(result int)) {
	fyne.Do(func() {
		wasHidden := ensureWindowVisible()
		result := 0
		var d dialog.Dialog

		b1 := widget.NewButton(btn1Text, func() {
			result = 1
			d.Hide()
		})
		b2 := widget.NewButton(btn2Text, func() {
			result = 2
			d.Hide()
		})
		b1.Importance = widget.HighImportance

		content := container.NewVBox(
			wrappedMessageLabel(message),
			container.NewGridWithColumns(2, b1, b2),
		)

		d = dialog.NewCustom(title, lang.L("Cancel"), content, mainWindow)
		d.SetOnClosed(func() {
			if onChoice != nil {
				onChoice(result)
			}
			if wasHidden {
				hideWindowNow()
			}
		})
		d.Show()
		constrainWrappedDialogWidth(d, message, mainWindow)
	})
}

// ChoiceN is Choice generalized to an arbitrary, dynamic number of named
// options (one button per row, rather than Choice's fixed 2-column grid) -
// used where the option set varies at runtime, e.g. picking one of several
// devices' conflicting versions of a file (spec 3.3.2). onChoice is called
// exactly once with a 1-based index into optionLabels, or 0 if the dialog
// was dismissed without a selection (Cancel, or closed). Safe to call from
// any goroutine; onChoice always runs on the Fyne main thread.
func ChoiceN(title, message string, optionLabels []string, onChoice func(result int)) {
	fyne.Do(func() {
		wasHidden := ensureWindowVisible()
		result := 0
		var d dialog.Dialog

		buttons := make([]fyne.CanvasObject, 0, len(optionLabels))
		for i, label := range optionLabels {
			idx := i + 1
			btn := widget.NewButton(label, func() {
				result = idx
				d.Hide()
			})
			if idx == 1 {
				btn.Importance = widget.HighImportance
			}
			buttons = append(buttons, btn)
		}

		content := container.NewVBox(
			wrappedMessageLabel(message),
			container.NewVBox(buttons...),
		)

		d = dialog.NewCustom(title, lang.L("Cancel"), content, mainWindow)
		d.SetOnClosed(func() {
			if onChoice != nil {
				onChoice(result)
			}
			if wasHidden {
				hideWindowNow()
			}
		})
		d.Show()
		constrainWrappedDialogWidth(d, message, mainWindow)
	})
}

// InstallReminder shows a dismissible reminder dialog with a "Don't show
// this again" checkbox, e.g. for nagging the user about missing Git/rclone
// on every startup until they either install them or opt out. onClose
// receives whether that checkbox was checked when the dialog's only button
// was pressed. Safe to call from any goroutine.
func InstallReminder(title, message string, onClose func(dontShowAgain bool)) {
	fyne.Do(func() {
		wasHidden := ensureWindowVisible()

		msgLabel := widget.NewLabel(message)
		msgLabel.Wrapping = fyne.TextWrapWord
		check := widget.NewCheck(lang.L("Don't show this again"), nil)

		content := container.NewVBox(msgLabel, check)

		d := dialog.NewCustom(title, lang.L("OK"), content, mainWindow)
		d.Resize(fyne.NewSize(420, 220))
		d.SetOnClosed(func() {
			if onClose != nil {
				onClose(check.Checked)
			}
			if wasHidden {
				hideWindowNow()
			}
		})
		d.Show()
	})
}

// pickFolderFunc opens the platform's own folder picker (Cocoa panel on
// macOS via AppleScript's "choose folder", the native IFileDialog on
// Windows) and blocks until the user responds. It's a var, not a direct call
// to zenity.SelectFile, purely so tests can stub it out instead of actually
// spawning a real OS dialog. Fyne's own dialog.NewFolderOpen looks
// noticeably out of place next to the OS's real dialogs, which is why this
// doesn't use it.
var pickFolderFunc = func(title string) (string, error) {
	return zenity.SelectFile(zenity.Title(title), zenity.Directory())
}

// PickFolder opens a native folder selection dialog. onPicked receives the
// chosen absolute path and ok=true, or ok=false if the user cancelled. Safe
// to call from any goroutine - including the Fyne main goroutine itself
// (e.g. directly from a button's OnTapped, which is how settings_window.go
// calls it): this MUST stay fyne.Do, never fyne.DoAndWait. DoAndWait blocks
// the calling goroutine until Fyne's main loop processes the queued
// closure - if the caller *is* the main loop goroutine (as it is here),
// nothing else can ever drain that queue and it deadlocks solid, freezing
// the whole app the instant "Select Folder..." is clicked (a real,
// reported bug - the fyne/test driver used by this package's own tests
// can't catch this: DoFromGoroutine there runs synchronously with no
// queue at all, so it never deadlocks even when this bug is present). Do
// is safe here specifically because it never waits - it only enqueues,
// and the spawned goroutine below only reads wasHidden after
// pickFolderFunc returns, by which point the queued closure that sets it
// has always long since run (see RunWithProgress for the one place in
// this file DoAndWait actually is correct - it's never called from the
// main goroutine).
func PickFolder(title string, onPicked func(path string, ok bool)) {
	var wasHidden bool
	fyne.Do(func() {
		wasHidden = ensureWindowVisible()
	})
	go func() {
		path, err := pickFolderFunc(title)
		fyne.Do(func() {
			if wasHidden {
				hideWindowNow()
			}
			if onPicked != nil {
				onPicked(path, err == nil)
			}
		})
	}()
}

// RunWithProgress shows an indeterminate progress dialog with the given
// title/message, runs work synchronously and returns its result, hiding the
// dialog before returning.
//
// MUST be called from a goroutine you started yourself, never from the Fyne
// main goroutine (i.e. never directly from a button/menu callback) - it
// blocks the calling goroutine until work() completes, and showing the
// dialog itself is marshalled onto the main thread and waited for.
func RunWithProgress(title, message string, work func() error) error {
	var prog *dialog.ProgressInfiniteDialog
	var wasHidden bool
	fyne.DoAndWait(func() {
		wasHidden = ensureWindowVisible()
		prog = dialog.NewProgressInfinite(title, message, mainWindow)
		prog.Show()
	})

	err := work()

	fyne.Do(func() {
		prog.Hide()
		if wasHidden {
			hideWindowNow()
		}
	})

	return err
}
