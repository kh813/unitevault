//go:build darwin

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
	"strings"
)

// Apply downloads assetURL (a zipped .app bundle), then hands off to a
// detached shell script that waits for this process to exit, replaces the
// currently-running .app bundle with the new one, and relaunches it. The
// caller must quit shortly after Apply returns nil - the script cannot
// safely touch the bundle while this process is still running from inside
// it.
func Apply(ctx context.Context, assetURL string) error {
	appPath, err := runningAppBundlePath()
	if err != nil {
		return err
	}

	zipData, err := downloadFile(ctx, assetURL)
	if err != nil {
		return err
	}

	stagingDir, err := os.MkdirTemp("", "unitevault-update-*")
	if err != nil {
		return fmt.Errorf("failed to create a staging directory: %w", err)
	}

	newAppPath, err := extractAppBundle(zipData, stagingDir)
	if err != nil {
		_ = os.RemoveAll(stagingDir)
		return err
	}

	// Paths are passed as positional script arguments ($1, $2, $3), not
	// interpolated into the script text, so they can't be misinterpreted by
	// the shell regardless of spaces or special characters. `mv` (rather
	// than Go's os.Rename) is used deliberately: the staging directory and
	// /Applications can be on different volumes, and mv falls back to
	// copy+delete across devices where a plain rename syscall would fail.
	const script = `set -e
sleep 1
rm -rf "$1"
mv "$2" "$1"
rm -rf "$3"
open "$1"
`
	cmd := exec.Command("/bin/sh", "-c", script, "unitevault-updater", appPath, newAppPath, stagingDir)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(stagingDir)
		return fmt.Errorf("failed to start the update helper: %w", err)
	}

	return nil
}

// runningAppBundlePath returns the .app bundle directory containing the
// currently running executable (e.g. /Applications/UniteVault.app). Returns
// an error if this process isn't running from inside a .app bundle at all
// (e.g. `go run` during development) - self-update only makes sense for a
// real installed app.
func runningAppBundlePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not determine the running executable's path: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	return bundlePathFromExecutable(exe)
}

// bundlePathFromExecutable is the pure part of runningAppBundlePath, split
// out so it's testable without needing to fake os.Executable().
func bundlePathFromExecutable(exe string) (string, error) {
	const marker = ".app/"
	idx := strings.Index(exe, marker)
	if idx == -1 {
		return "", fmt.Errorf("not running from an installed .app bundle (%s) - self-update is only supported for the packaged app", exe)
	}
	return exe[:idx+len(marker)-1], nil
}

// extractAppBundle extracts the top-level *.app directory found in zipData
// into destDir, preserving each entry's stored file mode (so the executable
// bit and, in turn, the ad-hoc code signature embedded in it survive
// byte-for-byte), and returns the extracted bundle's path.
func extractAppBundle(zipData []byte, destDir string) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", fmt.Errorf("failed to read the downloaded update archive: %w", err)
	}

	var appName string
	for _, f := range r.File {
		root := f.Name
		if i := strings.Index(root, "/"); i != -1 {
			root = root[:i]
		}
		if strings.HasSuffix(root, ".app") {
			appName = root
			break
		}
	}
	if appName == "" {
		return "", fmt.Errorf("the downloaded update archive did not contain a .app bundle")
	}

	for _, f := range r.File {
		target := filepath.Join(destDir, f.Name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return "", err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return "", err
		}

		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("failed to read %s from the update archive: %w", f.Name, err)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return "", err
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return "", fmt.Errorf("failed to write %s: %w", target, copyErr)
		}
		if closeErr != nil {
			return "", closeErr
		}
	}

	return filepath.Join(destDir, appName), nil
}
