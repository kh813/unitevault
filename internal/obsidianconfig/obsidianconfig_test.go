package obsidianconfig_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kh813/unitevault/internal/obsidianconfig"
)

func TestConfigPath(t *testing.T) {
	path, err := obsidianconfig.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath failed: %v", err)
	}
	if path == "" {
		t.Fatal("expected a non-empty path")
	}
	if filepath.Base(path) != "obsidian.json" {
		t.Errorf("expected the path to end in obsidian.json, got %s", path)
	}
}

func TestUpdateVaultPathAt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obsidian.json")
	original := `{
  "vaults": {
    "abc123": {
      "path": "/Users/me/OldVault",
      "ts": 1234567890,
      "open": true
    },
    "def456": {
      "path": "/Users/me/OtherVault",
      "ts": 987654321
    }
  }
}`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("failed to seed obsidian.json: %v", err)
	}

	if err := obsidianconfig.UpdateVaultPathAt(path, "/Users/me/OldVault", "/Users/me/NewVault"); err != nil {
		t.Fatalf("UpdateVaultPathAt failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read updated obsidian.json: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("updated obsidian.json is not valid JSON: %v", err)
	}
	vaults := got["vaults"].(map[string]interface{})

	updated := vaults["abc123"].(map[string]interface{})
	if updated["path"] != "/Users/me/NewVault" {
		t.Errorf("expected abc123's path to be updated, got %+v", updated)
	}
	// ts/open on the updated entry, and the untouched sibling entry, must
	// survive unchanged - this must never touch fields it doesn't own.
	if updated["ts"] != float64(1234567890) || updated["open"] != true {
		t.Errorf("expected abc123's other fields to be preserved untouched, got %+v", updated)
	}
	untouched := vaults["def456"].(map[string]interface{})
	if untouched["path"] != "/Users/me/OtherVault" {
		t.Errorf("expected def456 to be untouched, got %+v", untouched)
	}

	// A backup of the original content must exist.
	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("expected a .bak file to be created: %v", err)
	}
	if string(backup) != original {
		t.Error("expected the backup to contain the original, unmodified content")
	}
}

func TestUpdateVaultPathAt_NoMatchingEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obsidian.json")
	if err := os.WriteFile(path, []byte(`{"vaults": {"abc123": {"path": "/Users/me/SomeVault"}}}`), 0644); err != nil {
		t.Fatalf("failed to seed obsidian.json: %v", err)
	}

	if err := obsidianconfig.UpdateVaultPathAt(path, "/Users/me/DoesNotMatch", "/Users/me/NewVault"); err == nil {
		t.Fatal("expected an error when no vault entry matches oldPath")
	}

	// A failed match must not leave a stray backup file behind.
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Error("expected no .bak file to be created when nothing was updated")
	}
}

func TestUpdateVaultPathAt_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	if err := obsidianconfig.UpdateVaultPathAt(path, "/Users/me/OldVault", "/Users/me/NewVault"); err == nil {
		t.Fatal("expected an error when obsidian.json doesn't exist")
	}
}
