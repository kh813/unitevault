// Package syncdir names this app's own bookkeeping directory, created
// inside a Vault (and inside an iCloud Bridge folder, spec 1.6.3): device
// logs, scan state, markers, manifest.
package syncdir

// Name is the bookkeeping directory's name. Dot-prefixed so Obsidian's
// file explorer, search, and file indexing hide it automatically -
// Obsidian ignores any dot-prefixed path by default, the same mechanism
// that already hides ".obsidian" and ".git" - since none of this is meant
// to be user-visible or user-edited.
const Name = ".sync"
