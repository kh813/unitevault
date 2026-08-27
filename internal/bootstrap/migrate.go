package bootstrap

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// ICloudDriveRoot returns this OS's default iCloud Drive folder and whether
// it currently exists (a reasonable proxy for "iCloud Drive is set up and
// signed in" - there's no officially documented way to check this more
// precisely, matching CheckICloudInstalled's own best-effort approach).
// Both paths are user-configurable in current iCloud client versions, so a
// user who moved their iCloud Drive folder elsewhere won't be detected here
// - that's an accepted limitation of a best-effort default, not something
// this function tries to discover.
func ICloudDriveRoot() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}

	var root string
	if runtime.GOOS == "windows" {
		root = filepath.Join(home, "iCloud Drive")
	} else {
		root = filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs")
	}

	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return root, true
	}
	return "", false
}

// MoveVaultFolder moves the Vault at oldPath to newPath (spec: Vault
// Migration). Fails if newPath already exists, rather than silently
// merging into or overwriting whatever's there. Tries a plain rename
// first (instant, the common case since newPath is normally on the same
// volume as the home directory); falls back to a recursive copy-then-
// delete for cross-volume moves, which os.Rename can't do on any OS.
func MoveVaultFolder(oldPath, newPath string) error {
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("destination %s already exists", newPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to check destination %s: %w", newPath, err)
	}

	if err := os.Rename(oldPath, newPath); err == nil {
		return nil
	}
	// os.Rename failed - most likely a cross-volume move (e.g. the old
	// path is on an iCloud-managed volume/mount distinct from the home
	// directory's own volume). Fall back to copy-then-delete; leaves
	// oldPath untouched if the copy itself fails, so a failed migration
	// never loses data.
	if err := CopyDirRecursive(oldPath, newPath); err != nil {
		_ = os.RemoveAll(newPath) // clean up a partial copy
		return fmt.Errorf("failed to copy %s to %s: %w", oldPath, newPath, err)
	}
	if err := os.RemoveAll(oldPath); err != nil {
		return fmt.Errorf("copied to %s but failed to remove the original at %s: %w", newPath, oldPath, err)
	}
	return nil
}

// CopyDirRecursive copies every file and subdirectory under src into dst
// (created if needed), preserving each file's permission bits. Used by
// MoveVaultFolder's cross-volume fallback and by the iCloud Bridge seed
// copy (spec 1.6.3) - both need a plain recursive copy, not a sync/mirror
// (dst is always freshly created or expected to be a clean destination).
func CopyDirRecursive(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
