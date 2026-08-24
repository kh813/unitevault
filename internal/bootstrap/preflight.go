package bootstrap

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// CheckGitInstalled checks if git is available in PATH or common install directories.
func CheckGitInstalled() bool {
	if _, err := exec.LookPath("git"); err == nil {
		return true
	}

	if runtime.GOOS == "windows" {
		commonPaths := []string{
			`C:\Program Files\Git\cmd\git.exe`,
			`C:\Program Files (x86)\Git\cmd\git.exe`,
		}
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			commonPaths = append(commonPaths, filepath.Join(localAppData, "Programs", "Git", "cmd", "git.exe"))
		}
		for _, p := range commonPaths {
			if _, err := os.Stat(p); err == nil {
				return true
			}
		}
	} else if runtime.GOOS == "darwin" {
		commonPaths := []string{
			"/usr/bin/git",
			"/usr/local/bin/git",
			"/opt/homebrew/bin/git",
		}
		for _, p := range commonPaths {
			if _, err := os.Stat(p); err == nil {
				return true
			}
		}
	}

	return false
}

// AutoInstallGit attempts to trigger/install Git on the system automatically.
func AutoInstallGit() error {
	if CheckGitInstalled() {
		return nil
	}

	switch runtime.GOOS {
	case "darwin":
		// 1. Check brew
		if brewPath, err := exec.LookPath("brew"); err == nil {
			cmd := exec.Command(brewPath, "install", "git")
			if err := cmd.Run(); err == nil && CheckGitInstalled() {
				return nil
			}
		}
		// 2. Trigger Apple Developer Tools prompt
		cmd := exec.Command("xcode-select", "--install")
		_ = cmd.Run()
		return nil

	case "windows":
		// 1. Try winget silent install
		if wingetPath, err := exec.LookPath("winget"); err == nil {
			cmd := exec.Command(wingetPath, "install", "--id", "Git.Git", "-e", "--source", "winget", "--accept-source-agreements", "--accept-package-agreements", "--silent")
			if err := cmd.Run(); err == nil && CheckGitInstalled() {
				return nil
			}
		}

		// 2. Download official Git for Windows installer to temp and launch installer
		gitInstallerURL := "https://github.com/git-for-windows/git/releases/download/v2.46.0.windows.1/Git-2.46.0-64-bit.exe"
		tempInstaller := filepath.Join(os.TempDir(), "Git-Installer.exe")

		resp, err := http.Get(gitInstallerURL)
		if err != nil {
			return fmt.Errorf("failed to download Git installer: %w", err)
		}
		defer resp.Body.Close()

		out, err := os.Create(tempInstaller)
		if err != nil {
			return fmt.Errorf("failed to create temp installer file: %w", err)
		}
		_, copyErr := io.Copy(out, resp.Body)
		out.Close()
		if copyErr != nil {
			return fmt.Errorf("failed to write installer file: %w", copyErr)
		}

		// Execute installer silently or launch
		instCmd := exec.Command(tempInstaller, "/VERYSILENT", "/NORESTART")
		if err := instCmd.Run(); err != nil {
			_ = exec.Command(tempInstaller).Start()
		}
		return nil

	default:
		return fmt.Errorf("unsupported OS for Git auto-installation: %s", runtime.GOOS)
	}
}

// GetGitDownloadURL returns the appropriate Git download URL for the OS.
func GetGitDownloadURL() string {
	switch runtime.GOOS {
	case "windows":
		return "https://gitforwindows.org/"
	case "darwin":
		return "https://git-scm.com/download/mac"
	default:
		return "https://git-scm.com/downloads"
	}
}

// GetICloudDownloadURL returns the download URL for iCloud for Windows.
func GetICloudDownloadURL() string {
	return "https://support.apple.com/ja-jp/108994"
}

// GetRcloneDownloadURL returns the official download URL for rclone.
func GetRcloneDownloadURL() string {
	return "https://rclone.org/downloads/"
}

// GetRcloneDriveGuideURL returns the setup guide URL for Google Drive in rclone.
func GetRcloneDriveGuideURL() string {
	return "https://rclone.org/drive/"
}

// CheckRcloneInstalled checks if rclone is available in PATH or user config bin folder.
func CheckRcloneInstalled() bool {
	binName := "rclone"
	if runtime.GOOS == "windows" {
		binName = "rclone.exe"
	}
	if _, err := exec.LookPath(binName); err == nil {
		return true
	}
	// Check user config bin folder
	home, err := os.UserHomeDir()
	if err == nil {
		var baseDir string
		if runtime.GOOS == "windows" {
			appData := os.Getenv("APPDATA")
			if appData == "" {
				appData = filepath.Join(home, "AppData", "Roaming")
			}
			baseDir = filepath.Join(appData, "unitevault")
		} else {
			baseDir = filepath.Join(home, ".unitevault")
		}
		targetPath := filepath.Join(baseDir, "bin", binName)
		if _, err := os.Stat(targetPath); err == nil {
			return true
		}
	}
	return false
}

// LaunchTerminalRcloneConfig launches interactive `rclone config` inside a new Terminal window (Terminal.app on macOS, PowerShell/cmd on Windows).
func LaunchTerminalRcloneConfig(rcloneBin string) error {
	if rcloneBin == "" {
		rcloneBin = "rclone"
	}

	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`tell application "Terminal"
	do script %q
	activate
end tell`, fmt.Sprintf("%s config", rcloneBin))
		return exec.Command("osascript", "-e", script).Start()

	case "windows":
		cmdStr := fmt.Sprintf("Start-Process powershell -ArgumentList '-NoExit', '-Command', '& {%s config}'", rcloneBin)
		return exec.Command("powershell", "-NoProfile", "-Command", cmdStr).Start()

	default:
		// Linux: try x-terminal-emulator or gnome-terminal
		if term, err := exec.LookPath("x-terminal-emulator"); err == nil {
			return exec.Command(term, "-e", rcloneBin+" config").Start()
		}
		if term, err := exec.LookPath("gnome-terminal"); err == nil {
			return exec.Command(term, "--", rcloneBin, "config").Start()
		}
		return exec.Command("xterm", "-e", rcloneBin+" config").Start()
	}
}

// OpenURL opens a URL in the default web browser.
func OpenURL(urlStr string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", urlStr)
	case "darwin":
		cmd = exec.Command("open", urlStr)
	default:
		cmd = exec.Command("xdg-open", urlStr)
	}
	return cmd.Start()
}
