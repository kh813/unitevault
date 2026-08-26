package bootstrap

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// findICloudShortcut looks for the Start Menu shortcut Apple's classic
// iCloud for Windows installer creates. Used both to detect whether iCloud
// is installed and to launch it: it's more version-resilient than tracking
// an exact executable path, which has moved across iCloud releases, since
// Windows always points the shortcut at whatever the current version's real
// executable is.
func findICloudShortcut() (string, bool) {
	var candidates []string
	if programData := os.Getenv("ProgramData"); programData != "" {
		candidates = append(candidates, filepath.Join(programData, `Microsoft\Windows\Start Menu\Programs\iCloud.lnk`))
	}
	if appData := os.Getenv("APPDATA"); appData != "" {
		candidates = append(candidates, filepath.Join(appData, `Microsoft\Windows\Start Menu\Programs\iCloud.lnk`))
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

// CheckICloudInstalled reports whether Apple's classic iCloud for Windows
// client appears to be installed. Best-effort: there's no officially
// documented, version-stable way to probe this, since the install layout
// has shifted across iCloud releases - mirrors CheckGitInstalled's approach
// of falling back to a list of common install locations.
func CheckICloudInstalled() bool {
	if _, ok := findICloudShortcut(); ok {
		return true
	}

	commonExePaths := []string{
		`C:\Program Files\Common Files\Apple\Internet Services\iCloudServices.exe`,
		`C:\Program Files (x86)\Common Files\Apple\Internet Services\iCloudServices.exe`,
	}
	for _, p := range commonExePaths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// launchICloud best-effort opens the iCloud app via its Start Menu shortcut
// so the user lands on Apple's sign-in screen right away instead of having
// to go find it themselves. Signing in (and turning on iCloud Drive) needs
// interactive input - an Apple ID password and often 2FA - that can't be
// automated, so this is as far as automation can take the setup. Failure is
// silently ignored: it's a convenience on top of a successful install, not
// something that should surface as an install failure.
func launchICloud() {
	if p, ok := findICloudShortcut(); ok {
		_ = exec.Command("cmd", "/c", "start", "", p).Start()
	}
}

// icloudMSStoreID is Apple's iCloud product ID on the Microsoft Store
// (visible at https://apps.microsoft.com/detail/9pktq5699m62).
const icloudMSStoreID = "9PKTQ5699M62"

// AutoInstallICloud attempts to install Apple's iCloud for Windows client
// via winget (Windows only - iCloud ships with macOS/iOS, so this has
// nothing to do there).
//
// Tries the Microsoft Store version first: Store (AppX/MSIX) packages
// install per-user with no administrator elevation, unlike the classic
// installer below - a real UAC prompt is a meaningfully worse experience
// for something this app triggers on the user's behalf. If that fails (no
// Store access, an older winget that still requires a signed-in Store
// account, corporate policy blocking Store installs, ...), falls back to
// the classic/legacy winget package ("Apple.iCloud"), which does require
// elevation but is more predictably scriptable via --silent.
func AutoInstallICloud() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("iCloud auto-installation is only applicable on Windows")
	}
	if CheckICloudInstalled() {
		launchICloud()
		return nil
	}

	wingetPath, err := exec.LookPath("winget")
	if err != nil {
		return fmt.Errorf("winget was not found - please install iCloud for Windows manually from %s", GetICloudDownloadURL())
	}

	// storeOutput is kept even on success so a failed classic fallback can
	// explain *why* the preferred, no-admin Store install didn't work
	// instead of only ever reporting the classic package's own failure -
	// the Store path is the only one that can install without elevation at
	// all, so its failure reason is the actionable one for anyone who wants
	// to avoid the UAC prompt entirely. Both stdout and stderr are captured
	// together: winget prints its actual user-facing diagnosis (e.g. "No
	// Microsoft Store account found") to stdout, not stderr, so stderr
	// alone previously captured nothing worth showing.
	storeCmd := exec.Command(wingetPath, "install", "--id", icloudMSStoreID, "-e", "--source", "msstore", "--accept-source-agreements", "--accept-package-agreements", "--silent")
	var storeOutput bytes.Buffer
	storeCmd.Stdout = &storeOutput
	storeCmd.Stderr = &storeOutput
	storeErr := storeCmd.Run()
	if storeErr == nil && CheckICloudInstalled() {
		launchICloud()
		return nil
	}

	classicCmd := exec.Command(wingetPath, "install", "--id", "Apple.iCloud", "-e", "--source", "winget", "--accept-source-agreements", "--accept-package-agreements", "--silent")
	if err := classicCmd.Run(); err != nil {
		msg := fmt.Sprintf("winget install failed: %v", err)
		if hint := wingetInstallErrorHint(err); hint != "" {
			msg += "\n\n" + hint
		}
		if detail := strings.TrimSpace(storeOutput.String()); storeErr != nil && detail != "" {
			msg += "\n\nThe no-admin Microsoft Store install was tried first and also failed:\n" + detail
		}
		return errors.New(msg)
	}
	if !CheckICloudInstalled() {
		return fmt.Errorf("iCloud installation did not complete - please install manually from %s", GetICloudDownloadURL())
	}

	launchICloud()
	return nil
}

// wingetInstallErrorCodeHint maps a handful of well-known winget exit codes
// (see APPINSTALLER_CLI_ERROR_* in
// https://github.com/microsoft/winget-cli/blob/master/src/AppInstallerSharedLib/Public/AppInstallerErrors.h)
// to a short, actionable hint, or "" if exitCode isn't one of them. Kept
// separate from wingetInstallErrorHint (which extracts the code from an
// error) purely so it's testable with a plain int literal, without needing
// to fabricate an *exec.ExitError.
func wingetInstallErrorCodeHint(exitCode int) string {
	switch exitCode {
	case 0x8A150006: // APPINSTALLER_CLI_ERROR_SHELLEXEC_INSTALL_FAILED
		// The classic "Apple.iCloud" package requires administrator
		// elevation (see AutoInstallICloud's doc comment) - this specific
		// code means winget found and downloaded the installer but the OS
		// itself failed to launch it, which in practice almost always means
		// the Windows administrator permission (UAC) prompt it triggered
		// was denied, dismissed, or timed out before being approved.
		return `This usually means a Windows administrator permission (UAC) prompt appeared and wasn't approved - try again and click "Yes" on the prompt when it appears.`
	default:
		return ""
	}
}

// wingetInstallErrorHint is wingetInstallErrorCodeHint applied to whatever
// exit code err carries, or "" if err isn't a process exit error at all
// (e.g. winget wasn't found, or was killed rather than exiting normally).
func wingetInstallErrorHint(err error) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return ""
	}
	return wingetInstallErrorCodeHint(exitErr.ExitCode())
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
