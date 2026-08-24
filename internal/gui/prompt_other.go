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

// PromptSettingsWindow displays a single native Forms GUI window on Windows containing Status, Config settings, and rclone details.
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
$form.Size = New-Object System.Drawing.Size(520, 480)
$form.StartPosition = 'CenterScreen'
$form.FormBorderStyle = 'FixedDialog'
$form.MaximizeBox = $false

# Status Group Box
$grpStatus = New-Object System.Windows.Forms.GroupBox
$grpStatus.Text = "Status"
$grpStatus.Location = New-Object System.Drawing.Point(15, 10)
$grpStatus.Size = New-Object System.Drawing.Size(475, 90)

$lblGit = New-Object System.Windows.Forms.Label
$lblGit.Text = "Git Status: %s"
$lblGit.Location = New-Object System.Drawing.Point(15, 22)
$lblGit.AutoSize = $true
$grpStatus.Controls.Add($lblGit)

$lblRcloneStatus = New-Object System.Windows.Forms.Label
$lblRcloneStatus.Text = "rclone Status: %s"
$lblRcloneStatus.Location = New-Object System.Drawing.Point(15, 42)
$lblRcloneStatus.AutoSize = $true
$grpStatus.Controls.Add($lblRcloneStatus)

$lblRole = New-Object System.Windows.Forms.Label
$lblRole.Text = "Device Role: %s"
$lblRole.Location = New-Object System.Drawing.Point(15, 62)
$lblRole.AutoSize = $true
$grpStatus.Controls.Add($lblRole)

$form.Controls.Add($grpStatus)

# Config Settings Group Box
$grpConfig = New-Object System.Windows.Forms.GroupBox
$grpConfig.Text = "Config Settings"
$grpConfig.Location = New-Object System.Drawing.Point(15, 110)
$grpConfig.Size = New-Object System.Drawing.Size(475, 180)

# Vault Path
$lblVault = New-Object System.Windows.Forms.Label
$lblVault.Location = New-Object System.Drawing.Point(15, 25)
$lblVault.Text = "Obsidian Vault Path:"
$lblVault.AutoSize = $true
$grpConfig.Controls.Add($lblVault)

$txtVault = New-Object System.Windows.Forms.TextBox
$txtVault.Location = New-Object System.Drawing.Point(15, 45)
$txtVault.Size = New-Object System.Drawing.Size(340, 23)
$txtVault.Text = %q
$grpConfig.Controls.Add($txtVault)

$btnBrowse = New-Object System.Windows.Forms.Button
$btnBrowse.Location = New-Object System.Drawing.Point(365, 44)
$btnBrowse.Size = New-Object System.Drawing.Size(95, 25)
$btnBrowse.Text = "Select Folder"
$btnBrowse.Add_Click({
	$dlg = New-Object System.Windows.Forms.FolderBrowserDialog
	if ($dlg.ShowDialog() -eq 'OK') { $txtVault.Text = $dlg.SelectedPath }
})
$grpConfig.Controls.Add($btnBrowse)

# Remote Target Folder
$lblPath = New-Object System.Windows.Forms.Label
$lblPath.Location = New-Object System.Drawing.Point(15, 80)
$lblPath.Text = "Google Drive Target Folder:"
$lblPath.AutoSize = $true
$grpConfig.Controls.Add($lblPath)

$txtPath = New-Object System.Windows.Forms.TextBox
$txtPath.Location = New-Object System.Drawing.Point(15, 100)
$txtPath.Size = New-Object System.Drawing.Size(445, 23)
$txtPath.Text = %q
$grpConfig.Controls.Add($txtPath)

# Sync Interval
$lblInterval = New-Object System.Windows.Forms.Label
$lblInterval.Location = New-Object System.Drawing.Point(15, 135)
$lblInterval.Text = "Sync Interval (seconds):"
$lblInterval.AutoSize = $true
$grpConfig.Controls.Add($lblInterval)

$txtInterval = New-Object System.Windows.Forms.TextBox
$txtInterval.Location = New-Object System.Drawing.Point(160, 132)
$txtInterval.Size = New-Object System.Drawing.Size(100, 23)
$txtInterval.Text = %q
$grpConfig.Controls.Add($txtInterval)

$form.Controls.Add($grpConfig)

# rclone Status Group Box
$grpRclone = New-Object System.Windows.Forms.GroupBox
$grpRclone.Text = "rclone Configuration"
$grpRclone.Location = New-Object System.Drawing.Point(15, 300)
$grpRclone.Size = New-Object System.Drawing.Size(475, 95)

$lblRemote = New-Object System.Windows.Forms.Label
$lblRemote.Location = New-Object System.Drawing.Point(15, 22)
$lblRemote.Text = "Remote Name:"
$lblRemote.AutoSize = $true
$grpRclone.Controls.Add($lblRemote)

$txtRemote = New-Object System.Windows.Forms.TextBox
$txtRemote.Location = New-Object System.Drawing.Point(110, 19)
$txtRemote.Size = New-Object System.Drawing.Size(150, 23)
$txtRemote.Text = %q
$grpRclone.Controls.Add($txtRemote)

$lblExec = New-Object System.Windows.Forms.Label
$lblExec.Location = New-Object System.Drawing.Point(15, 48)
$lblExec.Text = "Executable: %s"
$lblExec.AutoSize = $true
$grpRclone.Controls.Add($lblExec)

$lblRemoteInfo = New-Object System.Windows.Forms.Label
$lblRemoteInfo.Location = New-Object System.Drawing.Point(15, 68)
$lblRemoteInfo.Text = "Remote Status: %s"
$lblRemoteInfo.AutoSize = $true
$grpRclone.Controls.Add($lblRemoteInfo)

$form.Controls.Add($grpRclone)

# Buttons
$btnSave = New-Object System.Windows.Forms.Button
$btnSave.Location = New-Object System.Drawing.Point(280, 405)
$btnSave.Size = New-Object System.Drawing.Size(100, 30)
$btnSave.Text = "Save Settings"
$btnSave.DialogResult = [System.Windows.Forms.DialogResult]::OK
$form.Controls.Add($btnSave)

$btnCancel = New-Object System.Windows.Forms.Button
$btnCancel.Location = New-Object System.Drawing.Point(390, 405)
$btnCancel.Size = New-Object System.Drawing.Size(100, 30)
$btnCancel.Text = "Cancel"
$btnCancel.DialogResult = [System.Windows.Forms.DialogResult]::Cancel
$form.Controls.Add($btnCancel)

$form.AcceptButton = $btnSave
$form.CancelButton = $btnCancel

if ($form.ShowDialog() -eq 'OK') {
	Write-Output ($txtVault.Text + '::' + $txtRemote.Text + '::' + $txtPath.Text + '::' + $txtInterval.Text)
}
`, title,
			current.GitStatus, current.RcloneStatus, current.DeviceRole,
			current.VaultPath, current.RclonePath, fmt.Sprintf("%d", current.IntervalSeconds),
			current.RcloneRemote, current.RcloneExecPath, current.RcloneRemoteInfo)

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
			res := current
			res.VaultPath = strings.TrimSpace(parts[0])
			res.RcloneRemote = strings.TrimSpace(parts[1])
			res.RclonePath = strings.TrimSpace(parts[2])
			res.IntervalSeconds = sec
			return res, true
		}
		return current, false
	}

	return current, false
}
