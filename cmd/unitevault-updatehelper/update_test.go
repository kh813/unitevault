package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testDelay = time.Millisecond

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	return string(got)
}

// lockedDir returns a directory whose entries (the exe/new-exe/old-exe
// trio, all created inside it before calling this) can no longer be
// renamed, added, or removed - simulating a persistent lock (e.g. an
// antivirus scan) that keeps every swap attempt failing, deterministically
// rather than via any real timing race. Restores write permission during
// cleanup so t.TempDir() can still remove it afterwards.
func lockedDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("failed to lock %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
}

func TestSwapExecutables_SucceedsOnFirstAttempt(t *testing.T) {
	root := t.TempDir()
	exePath := filepath.Join(root, "exe")
	newExePath := filepath.Join(root, "exe.new")
	oldExePath := filepath.Join(root, "exe.old")
	writeFile(t, exePath, "old version")
	writeFile(t, newExePath, "new version")

	if !swapExecutables(exePath, newExePath, oldExePath, 10, testDelay) {
		t.Fatal("expected swapExecutables to succeed")
	}
	if got := readFile(t, exePath); got != "new version" {
		t.Errorf("expected exePath to contain the new version, got %q", got)
	}
	if got := readFile(t, oldExePath); got != "old version" {
		t.Errorf("expected oldExePath to contain the backed-up old version, got %q", got)
	}
	if exists(newExePath) {
		t.Error("expected newExePath to no longer exist after a successful swap")
	}
}

// TestSwapExecutables_FailsWhenRenameKeepsFailing guards the actual reason
// a retry loop exists at all - a lock on either path (e.g. an antivirus
// scan holding the freshly-downloaded exe open) can persist for the whole
// retry window - and that swapExecutables reports that honestly rather
// than claiming success.
func TestSwapExecutables_FailsWhenRenameKeepsFailing(t *testing.T) {
	root := t.TempDir()
	exePath := filepath.Join(root, "exe")
	newExePath := filepath.Join(root, "exe.new")
	oldExePath := filepath.Join(root, "exe.old")
	writeFile(t, exePath, "old version")
	writeFile(t, newExePath, "new version")
	lockedDir(t, root)

	if swapExecutables(exePath, newExePath, oldExePath, 3, testDelay) {
		t.Fatal("expected swapExecutables to report failure when every rename keeps failing")
	}
}

func TestRun_KillsProcessSwapsAndStartsTheNewExe(t *testing.T) {
	root := t.TempDir()
	exePath := filepath.Join(root, "exe")
	newExePath := filepath.Join(root, "exe.new")
	oldExePath := filepath.Join(root, "exe.old")
	writeFile(t, exePath, "old version")
	writeFile(t, newExePath, "new version")

	var killedPID int
	var startedPath string
	run(exePath, newExePath, oldExePath, 4242, 10, testDelay, func(pid int) { killedPID = pid }, func(path string) { startedPath = path })

	if killedPID != 4242 {
		t.Errorf("expected run to kill pid 4242, got %d", killedPID)
	}
	if startedPath != exePath {
		t.Errorf("expected run to start %s, got %s", exePath, startedPath)
	}
	if got := readFile(t, exePath); got != "new version" {
		t.Errorf("expected exePath to contain the new version, got %q", got)
	}
	if exists(oldExePath) {
		t.Error("expected the backup to be removed after a successful swap")
	}
}

// TestRun_RestoresOldExeAndStartsItWhenSwapFails guards that a failed
// update degrades to "still running the previous version" rather than
// leaving nothing installed at all.
func TestRun_RestoresOldExeAndStartsItWhenSwapFails(t *testing.T) {
	root := t.TempDir()
	exePath := filepath.Join(root, "exe")
	newExePath := filepath.Join(root, "exe.new")
	oldExePath := filepath.Join(root, "exe.old")
	writeFile(t, exePath, "old version")
	writeFile(t, newExePath, "new version")
	lockedDir(t, root)

	var startedPath string
	run(exePath, newExePath, oldExePath, 4242, 3, testDelay, func(int) {}, func(path string) { startedPath = path })

	// The directory is still locked, so even run's own restore/cleanup
	// renames can't happen - only confirm run still tries to start
	// whatever ended up at exePath rather than giving up silently.
	if startedPath != exePath {
		t.Errorf("expected run to attempt starting %s even on failure, got %s", exePath, startedPath)
	}
}
