package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kh813/unitevault/internal/engine"
	"github.com/kh813/unitevault/internal/scan"
)

func TestPublishManifest_And_LoadManifest(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "Vault")
	writeFile(t, filepath.Join(vaultPath, "note.md"), "hello")
	writeFile(t, filepath.Join(vaultPath, "Sub", "nested.md"), "nested")

	if err := engine.PublishManifest(vaultPath); err != nil {
		t.Fatalf("PublishManifest failed: %v", err)
	}

	manifest, err := engine.LoadManifest(vaultPath)
	if err != nil {
		t.Fatalf("LoadManifest failed: %v", err)
	}
	if manifest == nil {
		t.Fatal("expected a non-nil manifest")
	}
	if _, ok := manifest.Files["note.md"]; !ok {
		t.Errorf("expected note.md in the manifest, got %+v", manifest.Files)
	}
	if _, ok := manifest.Files["Sub/nested.md"]; !ok {
		t.Errorf("expected Sub/nested.md in the manifest, got %+v", manifest.Files)
	}
}

func TestLoadManifest_MissingReturnsNilNoError(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "Vault")

	manifest, err := engine.LoadManifest(vaultPath)
	if err != nil {
		t.Fatalf("expected no error when no manifest has been published, got %v", err)
	}
	if manifest != nil {
		t.Errorf("expected a nil manifest, got %+v", manifest)
	}
}

func TestApplyManifestDeletions_NilManifestIsNoOp(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "Vault")
	writeFile(t, filepath.Join(vaultPath, "note.md"), "hello")

	n, err := engine.ApplyManifestDeletions(vaultPath, nil)
	if err != nil {
		t.Fatalf("ApplyManifestDeletions failed: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 deletions with a nil manifest, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(vaultPath, "note.md")); err != nil {
		t.Errorf("expected note.md to survive untouched, got %v", err)
	}
}

// TestApplyManifestDeletions_RemovesConfirmedFileAbsentFromManifest is the
// core contract (spec 1.6.4): a file this device has *confirmed* (its own
// scanner has logged it, meaning it wasn't just pulled/created this very
// cycle) but that Primary's manifest no longer lists must be removed -
// this is how a Secondary, whose own pull is non-destructive `rclone
// copy`, still ends up reflecting deletions made elsewhere.
func TestApplyManifestDeletions_RemovesConfirmedFileAbsentFromManifest(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "Vault")
	stalePath := filepath.Join(vaultPath, "stale.md")
	writeFile(t, stalePath, "no longer wanted")

	scanner := scan.NewScanner(vaultPath)
	curr, err := scanner.ScanVault()
	if err != nil {
		t.Fatalf("ScanVault failed: %v", err)
	}
	// Confirm it directly (bypassing the multi-cycle debounce dance -
	// ApplyManifestDeletions only cares that it's *in* the confirmed
	// state, not how it got there).
	if err := scanner.SaveConfirmedState(curr); err != nil {
		t.Fatalf("SaveConfirmedState failed: %v", err)
	}

	manifest := &scan.ScanState{Files: map[string]scan.FileState{}} // stale.md absent - Primary no longer has it

	n, err := engine.ApplyManifestDeletions(vaultPath, manifest)
	if err != nil {
		t.Fatalf("ApplyManifestDeletions failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 deletion, got %d", n)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Errorf("expected stale.md to be removed, stat err = %v", err)
	}
}

// TestApplyManifestDeletions_KeepsUnconfirmedNewFile guards the safety
// property that makes this heuristic acceptable (spec 1.6.4): a file not
// yet in this device's own *confirmed* state (e.g. just created locally,
// or just pulled and not yet scanned) must never be deleted just because
// it's also absent from Primary's manifest - it may simply not have had
// time to round-trip through a merge yet.
func TestApplyManifestDeletions_KeepsUnconfirmedNewFile(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "Vault")
	newPath := filepath.Join(vaultPath, "brand-new.md")
	writeFile(t, newPath, "just created, not yet confirmed")
	// Deliberately never call SaveConfirmedState - this file has no
	// confirmed-state entry at all yet.

	manifest := &scan.ScanState{Files: map[string]scan.FileState{}}

	n, err := engine.ApplyManifestDeletions(vaultPath, manifest)
	if err != nil {
		t.Fatalf("ApplyManifestDeletions failed: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 deletions, got %d", n)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("expected brand-new.md to survive untouched, got %v", err)
	}
}

// TestApplyManifestDeletions_NeverTouchesSyncDir guards against ever
// deleting this app's own bookkeeping (_sync/), even if a caller somehow
// managed to get a _sync-rooted path into the confirmed state.
func TestApplyManifestDeletions_NeverTouchesSyncDir(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "Vault")
	logPath := filepath.Join(vaultPath, "_sync", "log-some-device.jsonl")
	writeFile(t, logPath, `{"device":"some-device"}`)

	scanner := scan.NewScanner(vaultPath)
	// Manually construct a confirmed state that (incorrectly, as a
	// worst-case test) includes a _sync/ path, to prove the guard inside
	// ApplyManifestDeletions itself is what protects it - not just that
	// ScanVault never reports _sync/ paths in the first place.
	if err := scanner.SaveConfirmedState(&scan.ScanState{Files: map[string]scan.FileState{
		"_sync/log-some-device.jsonl": {Hash: "irrelevant"},
	}}); err != nil {
		t.Fatalf("SaveConfirmedState failed: %v", err)
	}

	manifest := &scan.ScanState{Files: map[string]scan.FileState{}}
	if _, err := engine.ApplyManifestDeletions(vaultPath, manifest); err != nil {
		t.Fatalf("ApplyManifestDeletions failed: %v", err)
	}

	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("expected _sync/ content to survive untouched, got %v", err)
	}
}
