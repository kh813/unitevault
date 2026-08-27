package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kh813/unitevault/internal/engine"
	"github.com/kh813/unitevault/internal/syncedlog"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create dir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

// TestScanBridgeAndLog_LogsAStableChangeUnderTheBridgeDeviceID guards the
// core contract (spec 1.6.3): a change detected in the Bridge folder ends
// up in the *main Vault's* _sync/ log, under the given bridge device ID -
// not the Bridge folder's own log - so mergeAndTrackConflicts picks it up
// with no special-casing. Debounce (spec 3.4.1) means this can take a few
// scan cycles to settle, so this drives several before asserting.
func TestScanBridgeAndLog_LogsAStableChangeUnderTheBridgeDeviceID(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	bridgePath := filepath.Join(tempDir, "Bridge")
	if err := os.MkdirAll(vaultPath, 0755); err != nil {
		t.Fatalf("failed to create vault dir: %v", err)
	}
	if err := os.MkdirAll(bridgePath, 0755); err != nil {
		t.Fatalf("failed to create bridge dir: %v", err)
	}

	notePath := filepath.Join(bridgePath, "note.md")
	if err := os.WriteFile(notePath, []byte("from iphone, edit 1\n"), 0644); err != nil {
		t.Fatalf("failed to seed bridge file: %v", err)
	}

	// Cycle 1 establishes the baseline (existing content is never logged
	// as a "create" - see scan.DetectChanges).
	if _, err := engine.ScanBridgeAndLog(vaultPath, bridgePath, "bridge-id", "iCloud Bridge"); err != nil {
		t.Fatalf("ScanBridgeAndLog (cycle 1) failed: %v", err)
	}

	// A genuine edit after the baseline, needing a couple of stable scans
	// to be picked up (debounce).
	if err := os.WriteFile(notePath, []byte("from iphone, edit 2\n"), 0644); err != nil {
		t.Fatalf("failed to edit bridge file: %v", err)
	}
	for i := 0; i < 4; i++ {
		if _, err := engine.ScanBridgeAndLog(vaultPath, bridgePath, "bridge-id", "iCloud Bridge"); err != nil {
			t.Fatalf("ScanBridgeAndLog (settling cycle %d) failed: %v", i, err)
		}
	}

	entries, err := syncedlog.NewLogManager(vaultPath).ReadDeviceLog("bridge-id")
	if err != nil {
		t.Fatalf("ReadDeviceLog failed: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Path == "note.md" && e.Diff == "from iphone, edit 2\n" && e.Label == "iCloud Bridge" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a log entry for the bridge's edit under device bridge-id in the main Vault's log, got %+v", entries)
	}

	// Nothing should have been logged into the bridge folder's own _sync/
	// under this device ID - only the main Vault's.
	bridgeOwnEntries, err := syncedlog.NewLogManager(bridgePath).ReadDeviceLog("bridge-id")
	if err != nil {
		t.Fatalf("ReadDeviceLog (bridge's own _sync) failed: %v", err)
	}
	if len(bridgeOwnEntries) != 0 {
		t.Errorf("expected no log entries in the bridge folder's own _sync/, got %+v", bridgeOwnEntries)
	}
}

func TestMirrorVaultToBridge_CopiesNewAndChangedFiles(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	bridgePath := filepath.Join(tempDir, "Bridge")

	writeFile(t, filepath.Join(vaultPath, "note.md"), "hello")
	writeFile(t, filepath.Join(vaultPath, "Sub", "nested.md"), "nested")

	if err := engine.MirrorVaultToBridge(vaultPath, bridgePath); err != nil {
		t.Fatalf("MirrorVaultToBridge failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(bridgePath, "note.md"))
	if err != nil || string(got) != "hello" {
		t.Errorf("expected note.md mirrored with content %q, got %q (err %v)", "hello", got, err)
	}
	got2, err := os.ReadFile(filepath.Join(bridgePath, "Sub", "nested.md"))
	if err != nil || string(got2) != "nested" {
		t.Errorf("expected Sub/nested.md mirrored with content %q, got %q (err %v)", "nested", got2, err)
	}
}

func TestMirrorVaultToBridge_RemovesFilesDeletedFromVault(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	bridgePath := filepath.Join(tempDir, "Bridge")

	writeFile(t, filepath.Join(vaultPath, "keep.md"), "keep me")
	if err := engine.MirrorVaultToBridge(vaultPath, bridgePath); err != nil {
		t.Fatalf("MirrorVaultToBridge (first pass) failed: %v", err)
	}
	writeFile(t, filepath.Join(bridgePath, "stale.md"), "should be removed")
	// stale.md exists only in the bridge (imagine it was removed from the
	// Vault after an earlier mirror) - the next mirror must remove it.

	if err := engine.MirrorVaultToBridge(vaultPath, bridgePath); err != nil {
		t.Fatalf("MirrorVaultToBridge (second pass) failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(bridgePath, "stale.md")); !os.IsNotExist(err) {
		t.Errorf("expected stale.md to be removed from the bridge, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(bridgePath, "keep.md")); err != nil {
		t.Errorf("expected keep.md to still be present, got %v", err)
	}
}

// TestMirrorVaultToBridge_NeverTouchesBridgesOwnSyncDir guards against the
// mirror clobbering the Bridge folder's own independent scan-state
// bookkeeping (its own _sync/state/last_scan.json, written by
// ScanBridgeAndLog) - only the Vault's _sync/ is excluded from *what gets
// copied*, but the Bridge's own _sync/ must also survive the "remove
// stale files" pass untouched.
func TestMirrorVaultToBridge_NeverTouchesBridgesOwnSyncDir(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	bridgePath := filepath.Join(tempDir, "Bridge")

	writeFile(t, filepath.Join(vaultPath, "note.md"), "hello")
	bridgeOwnState := filepath.Join(bridgePath, "_sync", "state", "last_scan.json")
	writeFile(t, bridgeOwnState, `{"files":{}}`)

	if err := engine.MirrorVaultToBridge(vaultPath, bridgePath); err != nil {
		t.Fatalf("MirrorVaultToBridge failed: %v", err)
	}

	if _, err := os.Stat(bridgeOwnState); err != nil {
		t.Errorf("expected the bridge's own _sync/ state file to survive untouched, got %v", err)
	}
}
