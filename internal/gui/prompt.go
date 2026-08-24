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
		script := fmt.Sprintf(`POSIX path of (choose folder with prompt %q)`, title)
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
		script := fmt.Sprintf(`button returned of (display dialog %q with title %q buttons {"Cancel", "OK"} default button "OK")`, message, title)
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
