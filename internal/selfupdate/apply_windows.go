//go:build windows

package selfupdate

import (
	"archive/zip"
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// detachedProcess mirrors windows.DETACHED_PROCESS so the helper survives
// after this process exits, without pulling in golang.org/x/sys.
const detachedProcess = 0x00000008

// createNoWindow mirrors windows.CREATE_NO_WINDOW. Belt-and-suspenders
// alongside HideWindow below: the helper binary (updateHelperBinary) is
// itself built with "-H=windowsgui" and so has no console subsystem to
// ever flash a window for in the first place, but this costs nothing to
// also set.
const createNoWindow = 0x08000000

// updateHelperBinary is a full compiled Windows executable
// (cmd/unitevault-updatehelper, built by .github/workflows/release.yml
// with the same "-H=windowsgui" flag as UniteVault.exe itself), embedded
// here so Apply can write it out and run it without any separate download.
//
// This replaces an earlier version of this helper that was a plain cmd.exe
// batch script (deliberately *not* PowerShell - an even earlier version
// used PowerShell, but real-world updates kept failing with the exact same
// symptom, UniteVault.exe.new left on disk and never swapped, regardless of
// how the swap logic itself was hardened, strongly pointing at the script
// never actually running at all on some machines due to Group Policy /
// AppLocker / a system-wide execution policy that even `-ExecutionPolicy
// Bypass` doesn't override - batch files sidestepped that, since cmd.exe
// has no equivalent restriction).
//
// The batch script itself was later found to have its own real bug: it
// needed a delay/force-kill/retry sequence, implemented with `ping` (as a
// portable sleep - see the helper's own source for why `timeout` doesn't
// work here) and `taskkill`, both of which are separate console-subsystem
// executables. Even with this same CREATE_NO_WINDOW flag set on the
// parent cmd.exe, Windows could still briefly flash a console for such a
// child process in some environments (a real user-reported regression that
// persisted across multiple attempts at fixing it via process-creation
// flags alone). Compiling the whole sequence into a single windowsgui-
// subsystem binary - so there is no console-subsystem process anywhere in
// the chain at all - fixes this by construction rather than by flag-
// tuning, and as a side benefit needs no script-execution policy either.
//
//go:embed updatehelper_windows_amd64.exe
var updateHelperBinary []byte

// Apply downloads assetURL (a zipped UniteVault.exe), then hands off to a
// detached helper (updateHelperBinary, see its own doc comment) that
// force-kills this process by PID, swaps the executable, and relaunches it.
// The caller should still quit shortly after Apply returns nil rather than
// relying on the forced kill, which exists as a safety net for a wedged
// shutdown, not the primary shutdown path.
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

	// Written to a fixed path (not a fresh temp name per update) so it's
	// simply overwritten next time rather than needing cleanup - the
	// helper can't reliably delete its own .exe file while still running
	// (unlike the old .bat script, which cmd.exe doesn't lock the same
	// way), so this one is deliberately left behind in %TEMP% between
	// updates instead.
	helperPath := filepath.Join(os.TempDir(), "unitevault-update-helper.exe")
	if err := os.WriteFile(helperPath, updateHelperBinary, 0755); err != nil {
		_ = os.Remove(newExePath)
		return fmt.Errorf("failed to write the update helper: %w", err)
	}

	oldExePath := exePath + ".old"
	cmd := exec.Command(helperPath, exePath, newExePath, oldExePath, strconv.Itoa(os.Getpid()))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: detachedProcess | createNoWindow}
	if err := cmd.Start(); err != nil {
		_ = os.Remove(newExePath)
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
