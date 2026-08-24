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
		// Multi-input form dialog for macOS using native AppleScript
		script := fmt.Sprintf(`tell application "System Events"
	activate
	set currentVault to %q
	set currentRemote to %q
	set currentPath to %q
	set currentInterval to %q

	try
		set res to display dialog "Configure UniteVault Settings:\n\nEnter Obsidian Vault Directory Path, rclone Remote Name, Backup Folder Path, and Sync Interval (seconds) separated by commas:" default answer (currentVault & ", " & currentRemote & ", " & currentPath & ", " & currentInterval) buttons {"Cancel", "Browse...", "Save"} default button "Save" with title %q
		
		set btn to button returned of res
		if btn is "Cancel" then
			return "CANCELLED"
		else if btn is "Browse..." then
			set folderChoice to choose folder with prompt "Select Obsidian Vault Directory:"
			set chosenFolder to POSIX path of folderChoice
			set res2 to display dialog "Configure UniteVault Settings:\n\nObsidian Vault Directory Path set to:\n" & chosenFolder & "\n\nEnter rclone Remote Name, Backup Folder Path, and Sync Interval (seconds) separated by commas:" default answer (currentRemote & ", " & currentPath & ", " & currentInterval) buttons {"Cancel", "Save"} default button "Save" with title %q
			if button returned of res2 is "Save" then
				return chosenFolder & "::" & text returned of res2
			else
				return "CANCELLED"
			end if
		else
			return text returned of res
		end if
	on error
		return "CANCELLED"
	end try
end tell`, current.VaultPath, current.RcloneRemote, current.RclonePath, fmt.Sprintf("%d", current.IntervalSeconds), title, title)

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

		var vPath, rRemote, rPath string
		var sec int = current.IntervalSeconds

		if strings.Contains(raw, "::") {
			parts := strings.Split(raw, "::")
			vPath = strings.TrimSpace(parts[0])
			rest := strings.Split(parts[1], ",")
			if len(rest) >= 1 && strings.TrimSpace(rest[0]) != "" {
				rRemote = strings.TrimSpace(rest[0])
			}
			if len(rest) >= 2 && strings.TrimSpace(rest[1]) != "" {
				rPath = strings.TrimSpace(rest[1])
			}
			if len(rest) >= 3 {
				sec, _ = strconv.Atoi(strings.TrimSpace(rest[2]))
			}
		} else {
			parts := strings.Split(raw, ",")
			if len(parts) >= 1 {
				vPath = strings.TrimSpace(parts[0])
			}
			if len(parts) >= 2 {
				rRemote = strings.TrimSpace(parts[1])
			}
			if len(parts) >= 3 {
				rPath = strings.TrimSpace(parts[2])
			}
			if len(parts) >= 4 {
				sec, _ = strconv.Atoi(strings.TrimSpace(parts[3]))
			}
		}

		if rRemote == "" {
			rRemote = "gdrive"
		}
		if rPath == "" {
			rPath = "VaultBackup"
		}
		if sec <= 0 {
			sec = 120
		}

		return SettingsFormData{
			VaultPath:       vPath,
			RcloneRemote:    rRemote,
			RclonePath:      rPath,
			IntervalSeconds: sec,
		}, true

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
