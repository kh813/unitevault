package watch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kh813/unitevault/internal/syncdir"
	"github.com/kh813/unitevault/internal/watch"
)

// waitForDrain polls Drain until it reports want or the timeout elapses,
// since fsnotify delivers events asynchronously.
func waitForDrain(t *testing.T, w *watch.Watcher, want string, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if paths := w.Drain(); paths != nil {
			for _, p := range paths {
				if p == want {
					return paths
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for watcher to report %q", want)
	return nil
}

func TestWatcher_DetectsNewFile(t *testing.T) {
	root := t.TempDir()
	w, err := watch.New(root)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer w.Close()

	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	waitForDrain(t, w, "note.md", 2*time.Second)
}

func TestWatcher_DetectsModifiedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.md")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	w, err := watch.New(root)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer w.Close()

	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	waitForDrain(t, w, "note.md", 2*time.Second)
}

func TestWatcher_DetectsFileInNewSubdirectory(t *testing.T) {
	root := t.TempDir()
	w, err := watch.New(root)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer w.Close()

	subdir := filepath.Join(root, "Sub")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}
	// Give the watcher a moment to notice the new directory and register a
	// watch on it before a file is created inside it.
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(subdir, "nested.md"), []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	waitForDrain(t, w, "Sub/nested.md", 2*time.Second)
}

func TestWatcher_IgnoresSyncDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, syncdir.Name, "state"), 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	w, err := watch.New(root)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer w.Close()

	if err := os.WriteFile(filepath.Join(root, syncdir.Name, "state", "last_scan.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	// Also touch a real Vault file so there's something to positively wait
	// for - proving the watcher loop is alive and processing events, not
	// just silent because nothing happened yet.
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	got := waitForDrain(t, w, "note.md", 2*time.Second)
	for _, p := range got {
		if p == syncdir.Name || strings.HasPrefix(p, syncdir.Name+"/") {
			t.Errorf("expected sync dir to be ignored, got path %q in %v", p, got)
		}
	}
}

func TestWatcher_DrainClearsAccumulator(t *testing.T) {
	root := t.TempDir()
	w, err := watch.New(root)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer w.Close()

	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	waitForDrain(t, w, "note.md", 2*time.Second)

	// Nothing changed since the previous Drain - a second, immediate Drain
	// must report no paths.
	if got := w.Drain(); got != nil {
		t.Errorf("expected Drain to report nothing after already draining, got %v", got)
	}
}
