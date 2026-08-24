package gui

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// PromptTextInput prompts the user for text input using front-most native OS dialogs.
func PromptTextInput(title, message, defaultValue string) (string, bool) {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`text returned of (display dialog %q default answer %q with title %q)`, message, defaultValue, title)
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
		script := fmt.Sprintf(`tell application (path to frontmost application as text) to set f to (choose folder with prompt %q)
return POSIX path of f`, title)
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
		script := fmt.Sprintf(`tell application (path to frontmost application as text) to set res to button returned of (display dialog %q with title %q buttons {"Cancel", "OK"} default button "OK")
return res`, message, title)
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

// PromptChoice displays a dialog with two custom choice buttons (btn1 returns 1, btn2 returns 2, cancel/close returns 0).
func PromptChoice(title, message, btn1Text, btn2Text string) int {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`tell application (path to frontmost application as text) to set res to button returned of (display dialog %q with title %q buttons {"Cancel", %q, %q} default button %q)
return res`, message, title, btn2Text, btn1Text, btn1Text)
		cmd := exec.Command("osascript", "-e", script)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return 0
		}
		res := strings.TrimSpace(out.String())
		if res == btn1Text {
			return 1
		} else if res == btn2Text {
			return 2
		}
		return 0

	case "windows":
		psCmd := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$form = New-Object System.Windows.Forms.Form
$form.Text = %q
$form.Size = New-Object System.Drawing.Size(420, 200)
$form.StartPosition = 'CenterScreen'
$form.FormBorderStyle = 'FixedDialog'
$form.MaximizeBox = $false
$form.TopMost = $true
$form.Add_Shown({ $form.Activate() })
$lbl = New-Object System.Windows.Forms.Label
$lbl.Text = %q
$lbl.Location = New-Object System.Drawing.Point(20, 20)
$lbl.Size = New-Object System.Drawing.Size(360, 60)
$form.Controls.Add($lbl)

$btn1 = New-Object System.Windows.Forms.Button
$btn1.Text = %q
$btn1.Location = New-Object System.Drawing.Point(20, 95)
$btn1.Size = New-Object System.Drawing.Size(170, 35)
$btn1.DialogResult = [System.Windows.Forms.DialogResult]::OK
$form.Controls.Add($btn1)

$btn2 = New-Object System.Windows.Forms.Button
$btn2.Text = %q
$btn2.Location = New-Object System.Drawing.Point(210, 95)
$btn2.Size = New-Object System.Drawing.Size(170, 35)
$btn2.DialogResult = [System.Windows.Forms.DialogResult]::Yes
$form.Controls.Add($btn2)

$res = $form.ShowDialog()
if ($res -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output "1" }
elseif ($res -eq [System.Windows.Forms.DialogResult]::Yes) { Write-Output "2" }
else { Write-Output "0" }
`, title, message, btn1Text, btn2Text)

		cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return 0
		}
		switch strings.TrimSpace(out.String()) {
		case "1":
			return 1
		case "2":
			return 2
		default:
			return 0
		}

	default:
		cmd := exec.Command("zenity", "--question", "--title="+title, "--text="+message, "--ok-label="+btn1Text, "--cancel-label="+btn2Text)
		if cmd.Run() == nil {
			return 1
		}
		return 2
	}
}


// SettingsFormData represents the status and configurable parameters in the Settings Window
type SettingsFormData struct {
	// Status Info
	GitStatus    string
	RcloneStatus string
	DeviceRole   string

	// Configurable Form
	VaultPath       string
	RcloneRemote    string
	RclonePath      string
	IntervalSeconds int

	// rclone Details
	RcloneExecPath   string
	RcloneRemoteInfo string
}

// PromptMessage shows an informational alert dialog.
func PromptMessage(title, message string) {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`tell application (path to frontmost application as text) to display dialog %q with title %q buttons {"OK"} default button "OK"`, message, title)
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
		script := fmt.Sprintf(`tell application (path to frontmost application as text) to display dialog %q with title %q buttons {} giving up after 300`, message, title)
		cmd := exec.Command("osascript", "-e", script)
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
