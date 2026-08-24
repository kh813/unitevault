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
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
)

// AppID is the unique application identifier used for Fyne preferences storage.
const AppID = "com.unitevault.app"

var (
	fyneApp    fyne.App
	mainWindow fyne.Window
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

	fyneApp = app.NewWithID(AppID)
	if appIcon != nil {
		fyneApp.SetIcon(appIcon)
	}

	mainWindow = fyneApp.NewWindow("UniteVault Settings")
	mainWindow.Resize(fyne.NewSize(600, 640))
	// Hide (don't quit) when the user closes the window with the titlebar
	// control; the app keeps running from the tray. Since we always set a
	// system tray menu, closing the only window will not exit the app anyway,
	// but Hide() also avoids destroying/recreating window state.
	mainWindow.SetCloseIntercept(func() {
		mainWindow.Hide()
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
	fyne.Do(func() {
		mainWindow.Hide()
	})
}

// SetMenuItemLabel updates a tray menu item's label and refreshes the menu so
// the change is reflected immediately. Safe to call from any goroutine.
func SetMenuItemLabel(menu *fyne.Menu, item *fyne.MenuItem, label string) {
	fyne.Do(func() {
		item.Label = label
		menu.Refresh()
	})
}

// ensureWindowVisible shows the shared window if it isn't already. Dialogs
// (Info/Confirm/Choice/...) are attached as an overlay on mainWindow's
// canvas, so if the window itself is currently hidden (e.g. the user closed
// Settings, then triggers "Reset Configuration" from the tray menu) the
// dialog would be rendered onto a canvas nobody can see. Must be called from
// the Fyne main thread (i.e. from within a func already wrapped in fyne.Do).
func ensureWindowVisible() {
	mainWindow.Show()
}
