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
