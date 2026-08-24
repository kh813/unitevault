package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// Info shows a simple informational/alert dialog anchored to the shared window.
// Safe to call from any goroutine.
func Info(title, message string) {
	fyne.Do(func() {
		ensureWindowVisible()
		dialog.ShowInformation(title, message, mainWindow)
	})
}

// Confirm shows a Yes/No confirmation dialog. Fyne dialogs are non-blocking,
// so the result is delivered via onResult once the user responds. Safe to
// call from any goroutine; onResult always runs on the Fyne main thread.
func Confirm(title, message string, onResult func(confirmed bool)) {
	fyne.Do(func() {
		ensureWindowVisible()
		dialog.ShowConfirm(title, message, func(ok bool) {
			if onResult != nil {
				onResult(ok)
			}
		}, mainWindow)
	})
}

// Choice shows a dialog with two named action buttons plus an implicit Cancel
// (closing the dialog without choosing). onChoice is called exactly once with
// 1 (btn1Text chosen), 2 (btn2Text chosen), or 0 (cancelled/closed). Safe to
// call from any goroutine; onChoice always runs on the Fyne main thread.
func Choice(title, message, btn1Text, btn2Text string, onChoice func(result int)) {
	fyne.Do(func() {
		ensureWindowVisible()
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
			widget.NewLabel(message),
			container.NewGridWithColumns(2, b1, b2),
		)

		d = dialog.NewCustom(title, "Cancel", content, mainWindow)
		d.SetOnClosed(func() {
			if onChoice != nil {
				onChoice(result)
			}
		})
		d.Show()
	})
}

// PickFolder opens a native folder selection dialog. onPicked receives the
// chosen absolute path and ok=true, or ok=false if the user cancelled. Safe
// to call from any goroutine; onPicked always runs on the Fyne main thread.
func PickFolder(title string, onPicked func(path string, ok bool)) {
	fyne.Do(func() {
		ensureWindowVisible()
		fd := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				if onPicked != nil {
					onPicked("", false)
				}
				return
			}
			if onPicked != nil {
				onPicked(uri.Path(), true)
			}
		}, mainWindow)
		fd.Resize(fyne.NewSize(760, 500))
		fd.Show()
	})
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
	fyne.DoAndWait(func() {
		ensureWindowVisible()
		prog = dialog.NewProgressInfinite(title, message, mainWindow)
		prog.Show()
	})

	err := work()

	fyne.Do(func() {
		prog.Hide()
	})

	return err
}
