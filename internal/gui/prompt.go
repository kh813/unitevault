package gui

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// PromptTextInput prompts the user for text input using front-most native OS dialogs.
func PromptTextInput(title, message, defaultValue string) (string, bool) {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`tell application "System Events"
	activate
	text returned of (display dialog %q default answer %q with title %q)
end tell`, message, defaultValue, title)
		cmd := exec.Command("osascript", "-e", script)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return "", false
		}
		return strings.TrimSpace(out.String()), true

	case "windows":
		psCmd := fmt.Sprintf(`Add-Type -AssemblyName Microsoft.VisualBasic; [Microsoft.VisualBasic.Interaction]::InputBox(%q, %q, %q)`, message, title, defaultValue)
		cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return "", false
		}
		res := strings.TrimSpace(out.String())
		if res == "" {
			return "", false
		}
		return res, true

	default:
		cmd := exec.Command("zenity", "--entry", "--title="+title, "--text="+message, "--entry-text="+defaultValue)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return "", false
		}
		return strings.TrimSpace(out.String()), true
	}
}

// PromptFolder opens a folder selection picker dialog and returns the selected directory path.
func PromptFolder(title string) (string, bool) {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`tell application "System Events"
	activate
	POSIX path of (choose folder with prompt %q)
end tell`, title)
		cmd := exec.Command("osascript", "-e", script)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return "", false
		}
		res := strings.TrimSpace(out.String())
		if res == "" {
			return "", false
		}
		return res, true

	case "windows":
		psCmd := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms; $f = New-Object System.Windows.Forms.FolderBrowserDialog; $f.Description = %q; if ($f.ShowDialog() -eq 'OK') { Write-Output $f.SelectedPath }`, title)
		cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return "", false
		}
		res := strings.TrimSpace(out.String())
		if res == "" {
			return "", false
		}
		return res, true

	default:
		cmd := exec.Command("zenity", "--file-selection", "--directory", "--title="+title)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return "", false
		}
		return strings.TrimSpace(out.String()), true
	}
}

// PromptConfirm shows a Yes/No (OK/Cancel) confirmation dialog.
func PromptConfirm(title, message string) bool {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`tell application "System Events"
	activate
	button returned of (display dialog %q with title %q buttons {"Cancel", "OK"} default button "OK")
end tell`, message, title)
		cmd := exec.Command("osascript", "-e", script)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return false
		}
		return strings.TrimSpace(out.String()) == "OK"

	case "windows":
		psCmd := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms; $res = [System.Windows.Forms.MessageBox]::Show(%q, %q, [System.Windows.Forms.MessageBoxButtons]::YesNo, [System.Windows.Forms.MessageBoxIcon]::Question); if ($res -eq 'Yes') { Write-Output 'YES' }`, message, title)
		cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return false
		}
		return strings.TrimSpace(out.String()) == "YES"

	default:
		cmd := exec.Command("zenity", "--question", "--title="+title, "--text="+message)
		return cmd.Run() == nil
	}
}

// SettingsFormData represents the configurable parameters in the Settings Window
type SettingsFormData struct {
	VaultPath       string
	RcloneRemote    string
	RclonePath      string
	IntervalSeconds int
}

// PromptSettingsWindow displays a unified native settings window for editing all parameters inline with Save / Cancel buttons.
func PromptSettingsWindow(title string, current SettingsFormData) (SettingsFormData, bool) {
	if current.IntervalSeconds <= 0 {
		current.IntervalSeconds = 120
	}

	switch runtime.GOOS {
	case "darwin":
		// Native macOS window with multiple input fields, browse button, and Save/Cancel buttons using AppleScript AppKit Cocoa dialog
		script := fmt.Sprintf(`
		use framework "AppKit"
		use scripting additions

		on run
			set mainApp to current application
			mainApp's NSApplication's sharedApplication()'s setActivationPolicy:(mainApp's NSApplicationActivationPolicyRegular)
			mainApp's NSApp's activateIgnoringOtherApps:true

			set window to mainApp's NSWindow's alloc()'s initWithContentRect:{{0, 0, 480, 290}} styleMask:15 backing:2 defer:false
			window's setTitle:%q
			window's center()
			window's makeKeyAndOrderFront:me
			window's orderFrontRegardless()

			set contentView to window's contentView()

			-- Vault Path Label & Field
			set lbl1 to mainApp's NSTextField's labelWithString:"Obsidian Vault Directory Path:"
			lbl1's setFrame:{{20, 240, 300, 20}}
			contentView's addSubview:lbl1

			set txtVault to mainApp's NSTextField's alloc()'s initWithFrame:{{20, 215, 330, 24}}
			txtVault's setStringValue:%q
			contentView's addSubview:txtVault

			set btnBrowse to mainApp's NSButton's alloc()'s initWithFrame:{{360, 214, 100, 25}}
			btnBrowse's setTitle:"Browse..."
			btnBrowse's setBezelStyle:(mainApp's NSBezelStyleRounded)
			contentView's addSubview:btnBrowse

			-- Remote Name
			set lbl2 to mainApp's NSTextField's labelWithString:"rclone Remote Name:"
			lbl2's setFrame:{{20, 185, 300, 20}}
			contentView's addSubview:lbl2

			set txtRemote to mainApp's NSTextField's alloc()'s initWithFrame:{{20, 160, 440, 24}}
			txtRemote's setStringValue:%q
			contentView's addSubview:txtRemote

			-- Remote Path
			set lbl3 to mainApp's NSTextField's labelWithString:"Google Drive Target Folder Path:"
			lbl3's setFrame:{{20, 130, 300, 20}}
			contentView's addSubview:lbl3

			set txtPath to mainApp's NSTextField's alloc()'s initWithFrame:{{20, 105, 440, 24}}
			txtPath's setStringValue:%q
			contentView's addSubview:txtPath

			-- Sync Interval
			set lbl4 to mainApp's NSTextField's labelWithString:"Sync Interval (seconds):"
			lbl4's setFrame:{{20, 75, 180, 20}}
			contentView's addSubview:lbl4

			set txtInterval to mainApp's NSTextField's alloc()'s initWithFrame:{{200, 72, 100, 24}}
			txtInterval's setStringValue:%q
			contentView's addSubview:txtInterval

			-- Save & Cancel Buttons
			set btnSave to mainApp's NSButton's alloc()'s initWithFrame:{{360, 20, 100, 32}}
			btnSave's setTitle:"Save"
			btnSave's setBezelStyle:(mainApp's NSBezelStyleRounded)
			contentView's addSubview:btnSave

			set btnCancel to mainApp's NSButton's alloc()'s initWithFrame:{{250, 20, 100, 32}}
			btnCancel's setTitle:"Cancel"
			btnCancel's setBezelStyle:(mainApp's NSBezelStyleRounded)
			contentView's addSubview:btnCancel

			-- Event loop for Cocoa window
			set userChoice to "CANCEL"
			repeat
				set theEvent to mainApp's NSApp's nextEventMatchingMask:(mainApp's NSEventMaskAny) untilDate:(mainApp's NSDate's distantFuture()) inMode:(mainApp's NSDefaultRunLoopMode) dequeue:true
				if theEvent is not missing value then
					mainApp's NSApp's sendEvent:theEvent
					if theEvent's type() = (mainApp's NSEventTypeLeftMouseUp) then
						set targetView to theEvent's window()'s contentView()'s hitTest:(theEvent's locationInWindow())
						if targetView is btnBrowse then
							set openPanel to mainApp's NSOpenPanel's openPanel()
							openPanel's setCanChooseFiles:false
							openPanel's setCanChooseDirectories:true
							openPanel's setAllowsMultipleSelection:false
							if openPanel's runModal() = 1 then
								set selectedURL to openPanel's |URL|()
								txtVault's setStringValue:(selectedURL's |path|())
							end if
						else if targetView is btnSave then
							set userChoice to "SAVE"
							exit repeat
						else if targetView is btnCancel then
							set userChoice to "CANCEL"
							exit repeat
						end if
					end if
				end if
			end repeat

			window's close()
			if userChoice is "SAVE" then
				return (txtVault's stringValue() as string) & "::" & (txtRemote's stringValue() as string) & "::" & (txtPath's stringValue() as string) & "::" & (txtInterval's stringValue() as string)
			else
				return "CANCELLED"
			end if
		end run`, title, current.VaultPath, current.RcloneRemote, current.RclonePath, fmt.Sprintf("%d", current.IntervalSeconds))

		cmd := exec.Command("osascript", "-e", script)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return current, false
		}
		raw := strings.TrimSpace(out.String())
		if raw == "CANCELLED" || raw == "" {
			return current, false
		}

		parts := strings.Split(raw, "::")
		if len(parts) >= 4 {
			sec, _ := strconv.Atoi(strings.TrimSpace(parts[3]))
			if sec <= 0 {
				sec = 120
			}
			return SettingsFormData{
				VaultPath:       strings.TrimSpace(parts[0]),
				RcloneRemote:    strings.TrimSpace(parts[1]),
				RclonePath:      strings.TrimSpace(parts[2]),
				IntervalSeconds: sec,
			}, true
		}
		return current, false

	case "windows":
		psCmd := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing

$form = New-Object System.Windows.Forms.Form
$form.Text = %q
$form.Size = New-Object System.Drawing.Size(460, 320)
$form.StartPosition = 'CenterScreen'
$form.FormBorderStyle = 'FixedDialog'
$form.MaximizeBox = $false

# Vault Path
$lblVault = New-Object System.Windows.Forms.Label
$lblVault.Location = New-Object System.Drawing.Point(20, 20)
$lblVault.Text = "Obsidian Vault Directory Path:"
$lblVault.AutoSize = $true
$form.Controls.Add($lblVault)

$txtVault = New-Object System.Windows.Forms.TextBox
$txtVault.Location = New-Object System.Drawing.Point(20, 42)
$txtVault.Size = New-Object System.Drawing.Size(310, 23)
$txtVault.Text = %q
$form.Controls.Add($txtVault)

$btnBrowse = New-Object System.Windows.Forms.Button
$btnBrowse.Location = New-Object System.Drawing.Point(340, 41)
$btnBrowse.Size = New-Object System.Drawing.Size(80, 25)
$btnBrowse.Text = "Browse..."
$btnBrowse.Add_Click({
	$dlg = New-Object System.Windows.Forms.FolderBrowserDialog
	if ($dlg.ShowDialog() -eq 'OK') { $txtVault.Text = $dlg.SelectedPath }
})
$form.Controls.Add($btnBrowse)

# rclone Remote
$lblRemote = New-Object System.Windows.Forms.Label
$lblRemote.Location = New-Object System.Drawing.Point(20, 80)
$lblRemote.Text = "rclone Remote Name:"
$lblRemote.AutoSize = $true
$form.Controls.Add($lblRemote)

$txtRemote = New-Object System.Windows.Forms.TextBox
$txtRemote.Location = New-Object System.Drawing.Point(20, 102)
$txtRemote.Size = New-Object System.Drawing.Size(400, 23)
$txtRemote.Text = %q
$form.Controls.Add($txtRemote)

# Remote Target Folder
$lblPath = New-Object System.Windows.Forms.Label
$lblPath.Location = New-Object System.Drawing.Point(20, 140)
$lblPath.Text = "Google Drive Target Folder Path:"
$lblPath.AutoSize = $true
$form.Controls.Add($lblPath)

$txtPath = New-Object System.Windows.Forms.TextBox
$txtPath.Location = New-Object System.Drawing.Point(20, 162)
$txtPath.Size = New-Object System.Drawing.Size(400, 23)
$txtPath.Text = %q
$form.Controls.Add($txtPath)

# Sync Interval
$lblInterval = New-Object System.Windows.Forms.Label
$lblInterval.Location = New-Object System.Drawing.Point(20, 200)
$lblInterval.Text = "Sync Interval (seconds):"
$lblInterval.AutoSize = $true
$form.Controls.Add($lblInterval)

$txtInterval = New-Object System.Windows.Forms.TextBox
$txtInterval.Location = New-Object System.Drawing.Point(180, 197)
$txtInterval.Size = New-Object System.Drawing.Size(100, 23)
$txtInterval.Text = %q
$form.Controls.Add($txtInterval)

# Buttons
$btnSave = New-Object System.Windows.Forms.Button
$btnSave.Location = New-Object System.Drawing.Point(240, 240)
$btnSave.Size = New-Object System.Drawing.Size(85, 30)
$btnSave.Text = "Save"
$btnSave.DialogResult = [System.Windows.Forms.DialogResult]::OK
$form.Controls.Add($btnSave)

$btnCancel = New-Object System.Windows.Forms.Button
$btnCancel.Location = New-Object System.Drawing.Point(335, 240)
$btnCancel.Size = New-Object System.Drawing.Size(85, 30)
$btnCancel.Text = "Cancel"
$btnCancel.DialogResult = [System.Windows.Forms.DialogResult]::Cancel
$form.Controls.Add($btnCancel)

$form.AcceptButton = $btnSave
$form.CancelButton = $btnCancel

if ($form.ShowDialog() -eq 'OK') {
	Write-Output ($txtVault.Text + '::' + $txtRemote.Text + '::' + $txtPath.Text + '::' + $txtInterval.Text)
}
`, title, current.VaultPath, current.RcloneRemote, current.RclonePath, fmt.Sprintf("%d", current.IntervalSeconds))

		cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return current, false
		}
		raw := strings.TrimSpace(out.String())
		if raw == "" {
			return current, false
		}

		parts := strings.Split(raw, "::")
		if len(parts) >= 4 {
			sec, _ := strconv.Atoi(strings.TrimSpace(parts[3]))
			if sec <= 0 {
				sec = 120
			}
			return SettingsFormData{
				VaultPath:       strings.TrimSpace(parts[0]),
				RcloneRemote:    strings.TrimSpace(parts[1]),
				RclonePath:      strings.TrimSpace(parts[2]),
				IntervalSeconds: sec,
			}, true
		}
		return current, false

	default:
		return current, false
	}
}

// PromptMessage shows an informational alert dialog.
func PromptMessage(title, message string) {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`tell application "System Events"
	activate
	display dialog %q with title %q buttons {"OK"} default button "OK"
end tell`, message, title)
		cmd := exec.Command("osascript", "-e", script)
		_ = cmd.Run()

	case "windows":
		psCmd := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.MessageBox]::Show(%q, %q, [System.Windows.Forms.MessageBoxButtons]::OK, [System.Windows.Forms.MessageBoxIcon]::Information)`, message, title)
		cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
		_ = cmd.Run()

	default:
		cmd := exec.Command("zenity", "--info", "--title="+title, "--text="+message)
		_ = cmd.Run()
	}
}

// LoadingDialog represents a running modal loading dialog that can be closed programmatically.
type LoadingDialog struct {
	closeFunc func()
}

// Close closes the loading dialog.
func (ld *LoadingDialog) Close() {
	if ld != nil && ld.closeFunc != nil {
		ld.closeFunc()
	}
}

// ShowLoadingDialog displays a non-blocking modal loading dialog and returns a LoadingDialog object to close it when done.
func ShowLoadingDialog(title, message string) *LoadingDialog {
	switch runtime.GOOS {
	case "darwin":
		// AppleScript window with progress indicator that closes when process is killed
		script := fmt.Sprintf(`tell application "System Events"
	activate
	display dialog %q with title %q buttons {} giving up after 300
end tell`, message, title)
		cmd := exec.Command("osascript", "-e", script)
		if err := cmd.Start(); err != nil {
			return nil
		}
		return &LoadingDialog{
			closeFunc: func() {
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				// Force close AppleScript dialog window
				_ = exec.Command("osascript", "-e", `tell application "System Events" to kill (processes whose name is "osascript")`).Run()
			},
		}

	case "windows":
		psCmd := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms; $form = New-Object System.Windows.Forms.Form; $form.Text = %q; $form.Size = New-Object System.Drawing.Size(350,130); $form.StartPosition = 'CenterScreen'; $form.FormBorderStyle = 'FixedDialog'; $form.ControlBox = $false; $label = New-Object System.Windows.Forms.Label; $label.Text = %q; $label.AutoSize = $true; $label.Location = New-Object System.Drawing.Point(30,30); $form.Controls.Add($label); $form.ShowDialog()`, title, message)
		cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
		if err := cmd.Start(); err != nil {
			return nil
		}
		return &LoadingDialog{
			closeFunc: func() {
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			},
		}

	default:
		cmd := exec.Command("zenity", "--progress", "--pulsate", "--title="+title, "--text="+message, "--no-cancel", "--auto-close")
		if err := cmd.Start(); err != nil {
			return nil
		}
		return &LoadingDialog{
			closeFunc: func() {
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			},
		}
	}
}
