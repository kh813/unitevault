// Package syncdir names and manages this app's own bookkeeping directory,
// created inside a Vault (and inside an iCloud Bridge folder, spec 1.6.3):
// device logs, scan state, markers, manifest.
package syncdir

import (
	"os"
	"path/filepath"
)

// Name is the bookkeeping directory's name. Dot-prefixed so Obsidian's
// file explorer, search, and file indexing hide it automatically -
// Obsidian ignores any dot-prefixed path by default, the same mechanism
// that already hides ".obsidian" and ".git" - since none of this is meant
// to be user-visible or user-edited.
const Name = ".sync"

// legacyName was this directory's name before the switch to the
// dot-prefixed Name, which made it visible (and editable) in Obsidian's
// file explorer - exactly what motivated the rename.
const legacyName = "_sync"

// Migrate renames a pre-existing legacyName directory under root to Name,
// if one exists and Name doesn't already - a one-time, best-effort
// upgrade step for a device that ran an older version of this app before
// the rename, so its existing device logs/scan state/markers carry
// forward instead of the device looking uninitialized. Safe to call every
// cycle: a no-op once migrated, and a no-op for a brand new Vault/Bridge
// folder that never had either directory.
func Migrate(root string) {
	newPath := filepath.Join(root, Name)
	if _, err := os.Stat(newPath); err == nil {
		return
	}
	oldPath := filepath.Join(root, legacyName)
	if _, err := os.Stat(oldPath); err != nil {
		return
	}
	_ = os.Rename(oldPath, newPath)
}
