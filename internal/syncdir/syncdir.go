// Package syncdir names this app's own bookkeeping directory, created
// inside a Vault (and inside an iCloud Bridge folder, spec 1.6.3): device
// logs, scan state, markers, manifest.
package syncdir

import "strings"

// Name is the bookkeeping directory's name. Dot-prefixed so Obsidian's
// file explorer, search, and file indexing hide it automatically -
// Obsidian ignores any dot-prefixed path by default, the same mechanism
// that already hides ".obsidian" and ".git" - since none of this is meant
// to be user-visible or user-edited.
const Name = ".sync"

// LegacyName was this directory's name before the rename to Name. Kept
// only so this app can permanently ignore it wherever it treats Name as
// bookkeeping rather than real Vault content (see IsBookkeeping and
// internal/engine's rclone exclude patterns) - a Google Drive remote
// populated by an app version built before the rename can still have this
// folder sitting there, and a plain rclone copy/sync (which mirrors
// whatever it finds, with no awareness of "old" vs "new" content) would
// otherwise keep re-downloading it to any device that later joins or
// re-syncs against that same remote (a real, reported case: a freshly
// created local Vault picked up a stray top-level "_sync" folder from a
// Google Drive remote that had been used for testing before the rename).
const LegacyName = "_sync"

// IsBookkeeping reports whether slashRel (a Vault-relative, slash-
// separated path) is this app's own bookkeeping directory (or something
// inside it) rather than real Vault content - either under the current
// Name or the LegacyName a pre-rename remote can still be carrying.
func IsBookkeeping(slashRel string) bool {
	return isNameOrUnder(slashRel, Name) || isNameOrUnder(slashRel, LegacyName)
}

func isNameOrUnder(slashRel, name string) bool {
	return slashRel == name || strings.HasPrefix(slashRel, name+"/")
}
