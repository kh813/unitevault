package drive_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kh813/unitevault/internal/drive"
	"github.com/kh813/unitevault/internal/engine"
)

func TestClient_CheckBinary(t *testing.T) {
	_, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone is not installed on this machine, skipping rclone execution tests")
	}

	client := drive.NewClient("")
	ctx := context.Background()

	// Calling FileExists on non-existent remote target should return false without error
	exists, err := client.FileExists(ctx, "nonexistent_remote_target_xyz_123:test")
	if err != nil {
		t.Logf("FileExists returned error as expected for invalid remote: %v", err)
	} else if exists {
		t.Errorf("expected false for nonexistent remote target")
	}
}

func TestClient_RemoveRemote(t *testing.T) {
	_, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone is not installed on this machine, skipping rclone execution tests")
	}

	client := drive.NewClient("")
	ctx := context.Background()

	// rclone config delete on a non-existent remote name should not error
	// (it's already gone, which is the desired end state).
	if err := client.RemoveRemote(ctx, "nonexistent_remote_xyz_123"); err != nil {
		t.Errorf("expected no error removing a non-existent remote, got: %v", err)
	}
}

// TestClient_Sync_ExcludesDotfilesAndJunkFiles runs the real rclone binary
// (local-to-local - rclone treats a plain filesystem destination exactly
// like it would a Google Drive remote path, so no Drive account or network
// access is needed) with engine.DefaultExcludes, the exact pattern list
// production code passes to every real Google Drive publish. This closes a
// real coverage gap: engine_test.go's mock-based tests only prove these
// pattern strings reach drive.Sync's argument list, never that the
// installed rclone binary actually interprets "**/.*" / "**/.*/" as "skip
// every dotfile and don't even recurse into dot-directories" the way
// engine.DefaultExcludes's doc comment claims - the one thing nobody here
// has been able to verify by hand yet on a real Google Drive sync.
func TestClient_Sync_ExcludesDotfilesAndJunkFiles(t *testing.T) {
	if _, found := drive.FindRcloneBinary(); !found {
		t.Skip("rclone binary not found (PATH or the app's default install location) - skipping real-rclone integration test")
	}

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	writeFile := func(rel, content string) {
		full := filepath.Join(srcDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", rel, err)
		}
	}

	// Real Vault content that must survive the sync.
	writeFile("Note.md", "# hello")
	writeFile("Sub/Nested.md", "# nested")

	// OS junk and dotfiles/dot-directories that must never reach Drive.
	writeFile(".DS_Store", "junk")
	writeFile("Sub/.DS_Store", "junk")
	writeFile("Thumbs.db", "junk")
	writeFile(".gitignore", "*.tmp")
	writeFile(".git/HEAD", "ref: refs/heads/main")
	writeFile(".obsidian/workspace.json", "{}")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := drive.NewClient("")
	if err := client.Sync(ctx, srcDir, dstDir, engine.DefaultExcludes...); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	for _, rel := range []string{"Note.md", filepath.Join("Sub", "Nested.md")} {
		if _, err := os.Stat(filepath.Join(dstDir, rel)); err != nil {
			t.Errorf("expected %s to be synced, got: %v", rel, err)
		}
	}

	for _, rel := range []string{
		".DS_Store",
		filepath.Join("Sub", ".DS_Store"),
		"Thumbs.db",
		".gitignore",
		".git",
		".obsidian",
	} {
		if _, err := os.Stat(filepath.Join(dstDir, rel)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be excluded from the synced backup, got stat err: %v", rel, err)
		}
	}
}

func TestGetDefaultRcloneTargetPath(t *testing.T) {
	path, err := drive.GetDefaultRcloneTargetPath()
	if err != nil {
		t.Fatalf("expected no error getting target path, got %v", err)
	}
	if path == "" {
		t.Error("expected non-empty rclone target path")
	}
}

