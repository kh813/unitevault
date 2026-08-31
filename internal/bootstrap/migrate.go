package bootstrap

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// ManagedVaultParentDir returns the folder this app moves a Vault into
// during Vault Migration (spec 1.6.7): ~/Obsidian (Windows:
// %USERPROFILE%\Obsidian), the same on every OS. Centralized here (rather
// than each call site joining "Obsidian" onto the home dir itself) so the
// convention only needs to change in one place.
func ManagedVaultParentDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Obsidian"), nil
}

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
		// The Windows iCloud client's File Explorer sidebar labels this
		// "iCloud Drive" (with a space), but the actual folder on disk is
		// "iCloudDrive" (no space) directly under the user's home directory
		// - confirmed against a real device (support.apple.com/guide/icloud-
		// windows/set-up-icloud-drive-icw0144825a5).
		root = filepath.Join(home, "iCloudDrive")
	} else {
		root = filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs")
	}

	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return root, true
	}
	return "", false
}

// ObsidianICloudContainerRoot returns Obsidian's own dedicated iCloud
// container - the location its "iCloud" vault-storage option (available
// when creating a Vault directly in the Obsidian app, including on
// iPhone/iPad) actually uses - distinct from the generic iCloud Drive
// folder ICloudDriveRoot returns. Confirmed against real devices on both
// platforms: a Vault created via Obsidian iOS's "iCloud" toggle lands on
// macOS at ~/Library/Mobile Documents/iCloud~md~obsidian/Documents/<vault
// name> (vaults sit directly under "Documents", no extra "Obsidian"
// subfolder), and on Windows at
// C:\Users\<user>\iCloudDrive\iCloud~md~obsidian\<vault name> (no
// "Documents" subfolder there at all). Both are a sibling of, not nested
// inside, the generic iCloud Drive container. This matters because a Vault
// placed under the generic iCloud Drive's own self-made "Obsidian" folder
// (ICloudDriveRoot's convention, used before this was discovered) does not
// actually show up for opening on iPhone/iPad the way one placed here does.
func ObsidianICloudContainerRoot() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}

	var root string
	if runtime.GOOS == "windows" {
		root = filepath.Join(home, "iCloudDrive", "iCloud~md~obsidian")
	} else {
		root = filepath.Join(home, "Library", "Mobile Documents", "iCloud~md~obsidian", "Documents")
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
	if err := ensureFreshDestination(newPath); err != nil {
		return err
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

// CopyVaultFolder copies the Vault at oldPath to newPath, exactly like
// MoveVaultFolder except oldPath is never touched - used when oldPath is
// itself the iCloud Bridge location and must be left in place (spec
// 1.6.7, vaultMigrationSourceIsBridge's doc comment in cmd/unitevault):
// moving it out and immediately reseeding a fresh copy at the very same
// path is the exact pattern that previously triggered iCloud's own
// conflict handling and produced a duplicate folder.
func CopyVaultFolder(oldPath, newPath string) error {
	if err := ensureFreshDestination(newPath); err != nil {
		return err
	}
	return CopyDirRecursive(oldPath, newPath)
}

// ensureFreshDestination checks that newPath doesn't already exist (so a
// caller never silently merges into or overwrites whatever's already
// there) and creates its parent directory if needed (e.g. ~/Obsidian on a
// fresh machine that's never migrated a Vault before - os.Rename doesn't
// create it, and would otherwise fail even for an ordinary same-volume
// move/copy that should just work).
func ensureFreshDestination(newPath string) error {
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("destination %s already exists", newPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to check destination %s: %w", newPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory for %s: %w", newPath, err)
	}
	return nil
}

// SeedICloudBridge copies src (the just-migrated local Vault) into dst (the
// iCloud Bridge folder, spec 1.6.3), taking care never to touch dst's
// parent directory (normally <iCloud Drive>/Obsidian) when it already
// exists. The common Vault Migration case seeds the Bridge at the very
// same path the Vault was just moved out of a moment earlier by
// MoveVaultFolder - a real, previously-shipped bug came from
// unconditionally recreating that whole path with os.MkdirAll (harmless
// by itself; MkdirAll no-ops on an already-existing directory), but
// observed on a real device to trigger iCloud's own conflict handling and
// leave behind a second, distinct "Obsidian"-named folder - likely a race
// between iCloud's daemon still settling the deletion and this process
// almost immediately recreating the same path. Pre-creating dst's parent
// only if it's actually missing, and dst itself with a single plain
// os.Mkdir (never MkdirAll) rather than folding that into the recursive
// copy below, avoids that pattern.
func SeedICloudBridge(src, dst string) error {
	parent := filepath.Dir(dst)
	if _, err := os.Stat(parent); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to check %s: %w", parent, err)
		}
		if err := os.MkdirAll(parent, 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", parent, err)
		}
	}
	if err := os.Mkdir(dst, 0755); err != nil && !os.IsExist(err) {
		return fmt.Errorf("failed to create %s: %w", dst, err)
	}
	return CopyDirRecursive(src, dst)
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
