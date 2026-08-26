// Package gui implements UniteVault's cross-platform menu bar / system tray icon
// and the single-window Settings GUI (spec section 3.5.2 / 8.3), built entirely
// on Fyne (fyne.io/fyne/v2). Using one GUI toolkit for both the tray icon and the
// window avoids the dual event-loop conflicts that come from mixing Fyne with a
// separate systray library (see fyne's own note: "SetSystemTrayMenu doesn't work
// when run in goroutine") or with per-OS scripted dialogs (AppleScript/PowerShell),
// which additionally cannot render a real multi-field form in a single window.
//
// Threading rules (required by Fyne's driver):
//   - InitApp, SetTray and Run must all be called from the same goroutine that
//     will become "main" (i.e. call them directly from func main(), never from a
//     spawned goroutine).
//   - Once Run() is executing, callbacks Fyne itself invokes (button taps, menu
//     item actions, dialog callbacks) already run on the correct thread - call
//     gui/dialog functions from them directly.
//   - Any code running on a goroutine you started yourself (background installs,
//     network calls, sync cycles) must marshal UI updates back via fyne.Do().
package gui

import (
	"embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/lang"
)

// AppID is the unique application identifier used for Fyne preferences storage.
const AppID = "com.unitevault.app"

// translationFiles holds this app's own UI translation bundles (spec section
// 8.5) - go-i18n format JSON, one file per locale (e.g. "ja.json"), keyed by
// the English string as it appears in a lang.L(...) call. English itself has
// no file here since it's always the fallback/original text passed to
// lang.L, not a translation of anything. Loaded via lang.AddTranslationsFS
// in InitApp, alongside Fyne's own built-in translations (e.g. the Yes/No
// buttons on dialog.NewConfirm) which are loaded automatically.
//
//go:embed translations
var translationFiles embed.FS

var (
	fyneApp    fyne.App
	mainWindow fyne.Window
	// windowVisible mirrors mainWindow's shown/hidden state. fyne.Window has
	// no Visible() getter, so this is tracked by hand across every path that
	// shows or hides it (showWindow/hideWindowNow, and the close intercept
	// below) - ensureWindowVisible relies on it to tell a dialog whether it
	// was the one that had to open the window just to host itself.
	windowVisible bool
)

// InitApp creates the singleton Fyne application and the shared utility window
// used to host the Settings form and all dialogs. Must be called once from the
// main goroutine before Run(). appIcon may be nil.
func InitApp(appIcon fyne.Resource) fyne.App {
	// Declare that this app follows the fyne.Do threading model everywhere it
	// touches the UI from a background goroutine (see the package doc
	// comment) - this silences Fyne's transitional "not migrated" warning,
	// which otherwise assumes non-CLI-built apps haven't been updated yet.
	app.SetMetadata(fyne.AppMetadata{
		ID:         AppID,
		Name:       "UniteVault",
		Migrations: map[string]bool{"fyneDo": true},
	})

	if err := lang.AddTranslationsFS(translationFiles, "translations"); err != nil {
		fyne.LogError("Failed to load UI translations", err)
	}

	fyneApp = app.NewWithID(AppID)
	if appIcon != nil {
		fyneApp.SetIcon(appIcon)
	}

	mainWindow = fyneApp.NewWindow(lang.L("UniteVault Settings"))
	// A modest placeholder size, only ever visible if a dialog (Info/
	// Confirm/...) needs to show before ShowSettingsWindow has built any
	// real content yet. ShowSettingsWindow resizes this window to fit its
	// actual content exactly every time it's shown, so this initial size
	// doesn't need to anticipate the Settings form's size at all.
	mainWindow.Resize(fyne.NewSize(480, 320))
	// Hide (don't quit) when the user closes the window with the titlebar
	// control; the app keeps running from the tray. Since we always set a
	// system tray menu, closing the only window will not exit the app anyway,
	// but Hide() also avoids destroying/recreating window state.
	mainWindow.SetCloseIntercept(func() {
		hideWindowNow()
	})

	return fyneApp
}

// App returns the singleton Fyne application. InitApp must be called first.
func App() fyne.App {
	return fyneApp
}

// Window returns the shared window used for the Settings form and dialogs.
func Window() fyne.Window {
	return mainWindow
}

// SetTray configures the desktop system tray icon and menu. Returns false if
// the current driver has no desktop tray support (e.g. mobile/web builds).
func SetTray(icon fyne.Resource, menu *fyne.Menu) bool {
	deskApp, ok := fyneApp.(desktop.App)
	if !ok {
		return false
	}
	if icon != nil {
		deskApp.SetSystemTrayIcon(icon)
	}
	deskApp.SetSystemTrayMenu(menu)
	return true
}

// Run blocks running the Fyne event loop. Must be called once from main(),
// after InitApp/SetTray have been configured.
func Run() {
	fyneApp.Run()
}

// Quit terminates the application and closes all windows.
func Quit() {
	fyneApp.Quit()
}

// HideWindow hides the shared Settings window without quitting the app
// (the tray menu keeps running). Safe to call from any goroutine.
func HideWindow() {
	fyne.Do(hideWindowNow)
}

// hideWindowNow is the actual hide + bookkeeping, callable directly from
// code that's already on the Fyne main thread (e.g. a dialog's own
// SetOnClosed callback) without wrapping it in another fyne.Do.
func hideWindowNow() {
	windowVisible = false
	mainWindow.Hide()
}

// SetMenuItemLabel updates a tray menu item's label and refreshes the menu so
// the change is reflected immediately. Safe to call from any goroutine.
func SetMenuItemLabel(menu *fyne.Menu, item *fyne.MenuItem, label string) {
	fyne.Do(func() {
		item.Label = label
		menu.Refresh()
	})
}

// ensureWindowVisible shows the shared window if it isn't already, and
// reports whether it *was* hidden. Dialogs (Info/Confirm/Choice/...) are
// attached as an overlay on mainWindow's canvas, so if the window itself is
// currently hidden (e.g. the user closed Settings, then triggers "Check for
// Update..." from the tray menu) the dialog would be rendered onto a canvas
// nobody can see - callers that only need the window visible to host such a
// transient dialog use the returned bool to hide it again once that dialog
// closes (see Info/showConfirm/Choice/InstallReminder/RunWithProgress),
// instead of leaving an empty (or stale leftover Settings content) window
// sitting open after the user dismisses what looked like it should've been
// a standalone dialog. Must be called from the Fyne main thread.
func ensureWindowVisible() bool {
	wasHidden := !windowVisible
	if wasHidden {
		windowVisible = true
		mainWindow.Show()
	}
	return wasHidden
}
