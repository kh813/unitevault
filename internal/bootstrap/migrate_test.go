package bootstrap_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kh813/unitevault/internal/bootstrap"
)

func TestICloudDriveRoot(t *testing.T) {
	// Should return a boolean without crashing on any OS, and never claim
	// success while returning an empty path.
	path, ok := bootstrap.ICloudDriveRoot()
	if ok && path == "" {
		t.Error("expected a non-empty path whenever ok is true")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create dir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

func TestCopyDirRecursive(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	writeFile(t, filepath.Join(src, "top.md"), "top level")
	writeFile(t, filepath.Join(src, "Notes", "nested.md"), "nested content")
	writeFile(t, filepath.Join(src, "Notes", "Deep", "deeper.md"), "deep content")

	dst := filepath.Join(t.TempDir(), "dst")
	if err := bootstrap.CopyDirRecursive(src, dst); err != nil {
		t.Fatalf("CopyDirRecursive failed: %v", err)
	}

	for relPath, want := range map[string]string{
		"top.md":               "top level",
		"Notes/nested.md":      "nested content",
		"Notes/Deep/deeper.md": "deep content",
	} {
		got, err := os.ReadFile(filepath.Join(dst, relPath))
		if err != nil {
			t.Fatalf("failed to read copied %s: %v", relPath, err)
		}
		if string(got) != want {
			t.Errorf("expected %s to contain %q, got %q", relPath, want, string(got))
		}
	}

	// The source must be untouched by a copy (as opposed to a move).
	if _, err := os.Stat(filepath.Join(src, "top.md")); err != nil {
		t.Errorf("expected the source file to still exist after a copy, got %v", err)
	}
}

func TestMoveVaultFolder_MovesContentAndRemovesSource(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "OldVault")
	writeFile(t, filepath.Join(oldPath, "note.md"), "hello")
	writeFile(t, filepath.Join(oldPath, "Sub", "note2.md"), "world")

	newPath := filepath.Join(root, "NewVault")
	if err := bootstrap.MoveVaultFolder(oldPath, newPath); err != nil {
		t.Fatalf("MoveVaultFolder failed: %v", err)
	}

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("expected the old path to no longer exist, stat err = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(newPath, "note.md"))
	if err != nil || string(got) != "hello" {
		t.Errorf("expected note.md to have moved with its content intact, got %q, err %v", got, err)
	}
	got2, err := os.ReadFile(filepath.Join(newPath, "Sub", "note2.md"))
	if err != nil || string(got2) != "world" {
		t.Errorf("expected Sub/note2.md to have moved with its content intact, got %q, err %v", got2, err)
	}
}

// TestMoveVaultFolder_RefusesToOverwriteExistingDestination guards against
// silently merging into or clobbering whatever's already at newPath.
func TestMoveVaultFolder_RefusesToOverwriteExistingDestination(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "OldVault")
	writeFile(t, filepath.Join(oldPath, "note.md"), "hello")

	newPath := filepath.Join(root, "NewVault")
	writeFile(t, filepath.Join(newPath, "existing.md"), "already here")

	if err := bootstrap.MoveVaultFolder(oldPath, newPath); err == nil {
		t.Fatal("expected an error when the destination already exists")
	}

	// Nothing should have been touched on failure.
	if _, err := os.Stat(oldPath); err != nil {
		t.Errorf("expected the old path to still exist after a refused move, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(newPath, "existing.md")); err != nil {
		t.Errorf("expected the pre-existing destination content to be untouched, got %v", err)
	}
}
