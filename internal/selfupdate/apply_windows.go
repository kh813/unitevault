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
	"syscall"
)

// detachedProcess mirrors windows.DETACHED_PROCESS so the helper script
// survives after this process exits, without pulling in golang.org/x/sys.
const detachedProcess = 0x00000008

// updateHelperScript renames the current exe out of the way, moves the
// newly downloaded exe into its place, relaunches it, and cleans up after
// itself. All paths are bound via PowerShell's own -File parameter binding
// (see Apply), never interpolated into the script text, so they can't be
// misinterpreted regardless of spaces or special characters.
//
// Two things make this more than a plain rename-and-go:
//   - The old process is only *asked* to quit right before Apply starts this
//     helper (see Apply's doc comment) - it isn't guaranteed to have fully
//     exited by the time this script runs. A flat sleep here previously
//     caused real update failures (a slow shutdown outlasting the sleep
//     leaves the exe still locked, and the swap below fails permanently
//     with no retry). Waiting on the actual PID, with a forced kill as a
//     fallback, is the reliable version of "kill the old process first"
//     rather than hoping it's already gone.
//   - Windows Defender's real-time scan of the newly downloaded exe (or any
//     other transient handle) can hold a brief lock on either file even
//     after the old process is confirmed gone, so the rename itself is
//     retried for a few seconds instead of failing on the first attempt.
const updateHelperScript = `param(
    [string]$ExePath,
    [string]$NewExePath,
    [string]$OldExePath,
    [int]$ProcessId
)

try {
    Wait-Process -Id $ProcessId -Timeout 10 -ErrorAction Stop
} catch {
    # Either it already exited (the common case - Apply's caller quits
    # immediately) or it's still running after the timeout; force it closed
    # either way so a wedged shutdown can't leave the update stuck forever.
    Stop-Process -Id $ProcessId -Force -ErrorAction SilentlyContinue
}

$swapped = $false
for ($i = 0; $i -lt 10; $i++) {
    try {
        if (Test-Path -LiteralPath $ExePath) {
            Remove-Item -Force -ErrorAction SilentlyContinue -LiteralPath $OldExePath
            Rename-Item -LiteralPath $ExePath -NewName (Split-Path -Leaf $OldExePath) -ErrorAction Stop
        }
        if (Test-Path -LiteralPath $NewExePath) {
            Rename-Item -LiteralPath $NewExePath -NewName (Split-Path -Leaf $ExePath) -ErrorAction Stop
        }
        $swapped = $true
        break
    } catch {
        Start-Sleep -Milliseconds 500
    }
}

if ($swapped) {
    Start-Process -FilePath $ExePath
}
Remove-Item -Force -ErrorAction SilentlyContinue -LiteralPath $OldExePath
Remove-Item -Force -ErrorAction SilentlyContinue -LiteralPath $PSCommandPath
`

// Apply downloads assetURL (a zipped UniteVault.exe), then hands off to a
// detached PowerShell helper that waits for this process to exit (see
// updateHelperScript - it forcibly kills this process by PID if it hasn't
// exited on its own after a few seconds), swaps the executable, and
// relaunches it. The caller should still quit shortly after Apply returns
// nil rather than relying on the forced kill, which exists as a safety net
// for a wedged shutdown, not the primary shutdown path.
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

	scriptPath := filepath.Join(os.TempDir(), "unitevault-update-helper.ps1")
	if err := os.WriteFile(scriptPath, []byte(updateHelperScript), 0644); err != nil {
		_ = os.Remove(newExePath)
		return fmt.Errorf("failed to write the update helper script: %w", err)
	}

	oldExePath := exePath + ".old"
	cmd := exec.Command("powershell",
		"-NoProfile", "-WindowStyle", "Hidden", "-ExecutionPolicy", "Bypass",
		"-File", scriptPath,
		"-ExePath", exePath,
		"-NewExePath", newExePath,
		"-OldExePath", oldExePath,
		"-ProcessId", strconv.Itoa(os.Getpid()),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: detachedProcess}
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
