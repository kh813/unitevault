// Package watch provides a best-effort, OS-native hint of which Vault
// files may have changed between sync cycles (spec 1.6.5), layered on top
// of - never replacing - periodic full-scan reconciliation. Watch events
// can be dropped (queue overflow), coalesced, or simply unreliable on some
// filesystems - notably cloud-synced folders, which is why the iCloud
// Bridge (internal/engine/bridge.go) is deliberately left on the polling/
// full-scan model instead of being wired to a Watcher.
package watch

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/kh813/unitevault/internal/syncdir"
)

// Watcher recursively watches a Vault directory (excluding .sync/, this
// app's own bookkeeping) and accumulates the set of relative paths that may
// have changed since the last Drain call.
type Watcher struct {
	root string
	fsw  *fsnotify.Watcher

	mu      sync.Mutex
	changed map[string]bool

	done chan struct{}
}

// New starts watching root and every current subdirectory (except .sync/),
// dynamically extending the watch to new subdirectories as they're
// created.
func New(root string) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		root:    root,
		fsw:     fsw,
		changed: make(map[string]bool),
		done:    make(chan struct{}),
	}

	if err := w.addRecursive(root); err != nil {
		_ = fsw.Close()
		return nil, err
	}

	go w.loop()
	return w, nil
}

func excluded(slashRel string) bool {
	return slashRel == syncdir.Name || strings.HasPrefix(slashRel, syncdir.Name+"/")
}

// addRecursive adds fsnotify watches for dir and every subdirectory beneath
// it, except .sync/. fsnotify.Add is non-recursive by design, so new
// directories must be added explicitly - handled here for the initial
// walk, and again in handle() for directories created afterward.
func (w *Watcher) addRecursive(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// A path can vanish between the walk listing it and this
			// callback running (e.g. an editor's temp file) - best-effort,
			// skip rather than fail the whole watch setup over it.
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(w.root, path)
		if relErr != nil {
			return nil
		}
		slashRel := filepath.ToSlash(rel)
		if slashRel != "." && excluded(slashRel) {
			return filepath.SkipDir
		}
		return w.fsw.Add(path)
	})
}

func (w *Watcher) loop() {
	for {
		select {
		case <-w.done:
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handle(ev)
		case _, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			// Errors are deliberately not surfaced anywhere - this is a
			// best-effort hint, and the periodic full-scan fallback (see
			// SyncEngine.RunCycle) is what actually guarantees correctness.
		}
	}
}

func (w *Watcher) handle(ev fsnotify.Event) {
	rel, err := filepath.Rel(w.root, ev.Name)
	if err != nil {
		return
	}
	slashRel := filepath.ToSlash(rel)
	if excluded(slashRel) {
		return
	}

	w.mu.Lock()
	w.changed[slashRel] = true
	w.mu.Unlock()

	if ev.Has(fsnotify.Create) {
		if info, statErr := os.Stat(ev.Name); statErr == nil && info.IsDir() {
			_ = w.addRecursive(ev.Name)
		}
	}
}

// Drain returns every Vault-relative path (slash-separated) touched since
// the last Drain call (or since New, on the first call), clearing the
// accumulator. A nil return means nothing was observed - callers should
// treat this as "no change detected", not as "the watcher is not running".
func (w *Watcher) Drain() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.changed) == 0 {
		return nil
	}
	paths := make([]string, 0, len(w.changed))
	for p := range w.changed {
		paths = append(paths, p)
	}
	w.changed = make(map[string]bool)
	return paths
}

// Close stops the watcher and releases its OS resources.
func (w *Watcher) Close() error {
	close(w.done)
	return w.fsw.Close()
}
