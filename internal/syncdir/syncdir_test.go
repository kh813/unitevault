package syncdir_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kh813/unitevault/internal/syncdir"
)

func TestMigrate_RenamesLegacyDirectory(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "_sync")
	if err := os.MkdirAll(oldPath, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldPath, "log-abc.jsonl"), []byte("{}"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	syncdir.Migrate(root)

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("expected the legacy _sync directory to be gone, stat err = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, syncdir.Name, "log-abc.jsonl"))
	if err != nil {
		t.Fatalf("expected the content to survive under the new name, got: %v", err)
	}
	if string(got) != "{}" {
		t.Errorf("expected content to be preserved, got %q", string(got))
	}
}

func TestMigrate_NoOpWhenNothingToMigrate(t *testing.T) {
	root := t.TempDir()
	// Neither _sync nor .sync exists - a brand new Vault/Bridge folder.
	syncdir.Migrate(root)

	if _, err := os.Stat(filepath.Join(root, syncdir.Name)); !os.IsNotExist(err) {
		t.Errorf("expected no directory to be created out of nothing, stat err = %v", err)
	}
}

func TestMigrate_NoOpWhenAlreadyMigrated(t *testing.T) {
	root := t.TempDir()
	newPath := filepath.Join(root, syncdir.Name)
	if err := os.MkdirAll(newPath, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(newPath, "log-abc.jsonl"), []byte("new"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	// A stray legacy directory also exists (shouldn't normally happen, but
	// guards that an already-migrated device never clobbers its current
	// state by re-migrating over it).
	oldPath := filepath.Join(root, "_sync")
	if err := os.MkdirAll(oldPath, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldPath, "log-abc.jsonl"), []byte("stale"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	syncdir.Migrate(root)

	got, err := os.ReadFile(filepath.Join(newPath, "log-abc.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("expected the already-migrated content to survive untouched, got %q", string(got))
	}
}
