//go:build windows

package selfupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// detachedProcess mirrors windows.DETACHED_PROCESS so the helper script
// survives after this process exits, without pulling in golang.org/x/sys.
const detachedProcess = 0x00000008

// createNoWindow mirrors windows.CREATE_NO_WINDOW. DETACHED_PROCESS alone
// only stops the helper from *inheriting* this process's console - cmd.exe
// and the console commands it runs internally (ping, taskkill, ...) are
// still console-subsystem programs, so without this flag Windows can
// allocate them a brand new console anyway and briefly flash it on screen
// (e.g. showing ping's loopback output) before HideWindow below hides it.
// CREATE_NO_WINDOW tells Windows not to create that console's window at
// all, which is the actual fix for the flash - HideWindow alone hides a
// window after it's created, which isn't fast enough to prevent it from
// ever being visible.
const createNoWindow = 0x08000000

// updateHelperScript is a plain cmd.exe batch script, deliberately *not*
// PowerShell: an earlier version used a PowerShell script here (even with
// -ExecutionPolicy Bypass), and real-world updates kept failing with the
// exact same symptom (UniteVault.exe.new left on disk, exe never swapped)
// regardless of how the swap logic itself was hardened - strongly pointing
// at the script never actually running at all on some machines (Group
// Policy / AppLocker / a system-wide execution policy that Bypass doesn't
// override). Batch files have no equivalent restriction, so this sidesteps
// the problem entirely rather than trying to out-flag it.
//
// Args (positional, passed by Apply - never interpolated into the script
// text, so they can't be misinterpreted regardless of spaces or special
// characters): %1=ExePath %2=NewExePath %3=OldExePath %4=ProcessId.
//
// Force-kills the old process by PID (it's only *asked* to quit right
// before Apply starts this helper - see Apply's doc comment - so it isn't
// guaranteed to have fully exited yet) after a brief pause, then retries
// the actual rename/swap for a few seconds before giving up, since a
// transient lock (e.g. Windows Defender scanning the newly-downloaded exe)
// can persist for a moment even after the old process is confirmed gone.
//
// Pauses use `ping -n 2 127.0.0.1` rather than the more obvious `timeout`:
// this process (a -H=windowsgui build) has no console at all, and `timeout`
// specifically errors out ("Input redirection is not supported") when
// stdin isn't a real console - `ping` doesn't care and blocks for the same
// ~1s regardless. It never actually touches the network (127.0.0.1 is the
// loopback address) - it's purely a delay with no real ping behind it. A
// visible cmd.exe window briefly flashing ping's output is a separate,
// unrelated bug in how Apply launches this whole script (see createNoWindow
// below) - swapping ping for something else here wouldn't have fixed that.
//
// The backup at OLDEXE is deliberately deleted only once, *before* the
// retry loop starts, and again only after the swap has actually succeeded -
// never inside the loop itself. An earlier version re-deleted OLDEXE at the
// top of every retry iteration, which destroyed the just-created backup one
// iteration after the EXE->OLDEXE rename succeeded but the still-locked
// NEWEXE->EXE rename kept failing (e.g. an antivirus scan holding the
// freshly-downloaded exe open longer than the retry window): by the time
// all 10 attempts were exhausted, EXE no longer existed (renamed away in
// attempt 1) and OLDEXE had already been wiped (deleted in attempt 2's
// cleanup), so the "start if the swap didn't happen" fallback found nothing
// left to start at all - the update silently uninstalled the app with no
// way to recover. If every attempt still fails now, OLDEXE (untouched
// throughout the loop) is moved back to EXE and relaunched instead, so a
// failed update degrades to "still running the previous version" rather
// than "nothing is installed any more".
const updateHelperScript = `@echo off
setlocal
set "EXE=%~1"
set "NEWEXE=%~2"
set "OLDEXE=%~3"
set "PID=%~4"

ping -n 2 127.0.0.1 >nul
taskkill /F /PID %PID% >nul 2>&1
ping -n 2 127.0.0.1 >nul

if exist "%OLDEXE%" del /f /q "%OLDEXE%" >nul 2>&1

for /L %%i in (1,1,10) do (
    if exist "%EXE%" move /y "%EXE%" "%OLDEXE%" >nul 2>&1
    if exist "%NEWEXE%" move /y "%NEWEXE%" "%EXE%" >nul 2>&1
    if not exist "%NEWEXE%" goto swapped
    ping -n 2 127.0.0.1 >nul
)

if exist "%OLDEXE%" move /y "%OLDEXE%" "%EXE%" >nul 2>&1
if exist "%EXE%" start "" "%EXE%"
del /f /q "%~f0" >nul 2>&1
exit /b 1

:swapped
start "" "%EXE%"
if exist "%OLDEXE%" del /f /q "%OLDEXE%" >nul 2>&1
del /f /q "%~f0" >nul 2>&1
`

// Apply downloads assetURL (a zipped UniteVault.exe), then hands off to a
// detached batch-script helper (see updateHelperScript for why it's a .bat
// and not PowerShell) that force-kills this process by PID, swaps the
// executable, and relaunches it. The caller should still quit shortly after
// Apply returns nil rather than relying on the forced kill, which exists as
// a safety net for a wedged shutdown, not the primary shutdown path.
func Apply(ctx context.Context, assetURL string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine the running executable's path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(exePath)

	zipData, err := downloadFile(ctx, assetURL)
	if err != nil {
		return err
	}

	newExePath := filepath.Join(dir, "UniteVault.exe.new")
	if err := extractExe(zipData, newExePath); err != nil {
		return err
	}

	// cmd.exe's batch parser has known quirks with LF-only line endings
	// inside multi-line ( ... ) blocks (like the for /L loop below) - the Go
	// source has plain LF newlines, so convert to the CRLF real .bat files
	// use before writing, rather than risk it.
	scriptCRLF := strings.ReplaceAll(updateHelperScript, "\n", "\r\n")
	scriptPath := filepath.Join(os.TempDir(), "unitevault-update-helper.bat")
	if err := os.WriteFile(scriptPath, []byte(scriptCRLF), 0644); err != nil {
		_ = os.Remove(newExePath)
		return fmt.Errorf("failed to write the update helper script: %w", err)
	}

	oldExePath := exePath + ".old"
	cmd := exec.Command("cmd", "/c", scriptPath, exePath, newExePath, oldExePath, strconv.Itoa(os.Getpid()))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: detachedProcess | createNoWindow}
	if err := cmd.Start(); err != nil {
		_ = os.Remove(newExePath)
		_ = os.Remove(scriptPath)
		return fmt.Errorf("failed to start the update helper: %w", err)
	}

	return nil
}

// extractExe writes the first .exe entry found in zipData to destPath.
func extractExe(zipData []byte, destPath string) error {
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("failed to read the downloaded update archive: %w", err)
	}

	for _, f := range r.File {
		if f.FileInfo().IsDir() || filepath.Ext(f.Name) != ".exe" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("failed to read %s from the update archive: %w", f.Name, err)
		}
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return fmt.Errorf("failed to write %s: %w", destPath, copyErr)
		}
		return closeErr
	}

	return fmt.Errorf("the downloaded update archive did not contain an .exe")
}
