package bootstrap

import (
	"os/exec"
	"runtime"
)

// CheckGitInstalled checks if git is available in PATH.
func CheckGitInstalled() bool {
	_, err := exec.LookPath("git")
	return err == nil
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
