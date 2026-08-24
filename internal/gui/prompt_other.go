//go:build !darwin

package gui

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// PromptSettingsWindow displays a single Win32 Form GUI window on Windows containing all input fields inline with Save & Cancel buttons.
func PromptSettingsWindow(title string, current SettingsFormData) (SettingsFormData, bool) {
	if current.IntervalSeconds <= 0 {
		current.IntervalSeconds = 120
	}

	if runtime.GOOS == "windows" {
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
	}

	return current, false
}
