package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kh813/unitevault/internal/engine"
	"github.com/kh813/unitevault/internal/scan"
	"github.com/kh813/unitevault/internal/syncdir"
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
// up in the *main Vault's* .sync/ log, under the given bridge device ID -
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

	// Nothing should have been logged into the bridge folder's own .sync/
	// under this device ID - only the main Vault's.
	bridgeOwnEntries, err := syncedlog.NewLogManager(bridgePath).ReadDeviceLog("bridge-id")
	if err != nil {
		t.Fatalf("ReadDeviceLog (bridge's own sync dir) failed: %v", err)
	}
	if len(bridgeOwnEntries) != 0 {
		t.Errorf("expected no log entries in the bridge folder's own .sync/, got %+v", bridgeOwnEntries)
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

// TestMirrorVaultToBridge_RemovesFilesDeletedFromVault guards the
// "propagate a Vault-side deletion out to the Bridge" case - but only once
// the Bridge's own scanner has actually confirmed the file existed there
// (matching production: ScanBridgeAndLog always runs before
// MirrorVaultToBridge in a real cycle, so by the time a deletion would
// legitimately propagate, the file has necessarily gone through a
// confirmed scan on some earlier cycle). Confirmed directly here
// (bypassing the debounce dance), matching the same pattern used by
// ApplyManifestDeletions' own tests.
func TestMirrorVaultToBridge_RemovesFilesDeletedFromVault(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	bridgePath := filepath.Join(tempDir, "Bridge")

	writeFile(t, filepath.Join(vaultPath, "keep.md"), "keep me")
	if err := engine.MirrorVaultToBridge(vaultPath, bridgePath); err != nil {
		t.Fatalf("MirrorVaultToBridge (first pass) failed: %v", err)
	}

	stalePath := filepath.Join(bridgePath, "stale.md")
	writeFile(t, stalePath, "should be removed")
	// stale.md exists only in the bridge (imagine it was mirrored there on
	// an earlier cycle, then removed from the Vault by another device) -
	// confirm it as the bridge scanner would have, then the next mirror
	// must remove it.
	bridgeScanner := scan.NewScanner(bridgePath)
	confirmed, err := bridgeScanner.ScanVault()
	if err != nil {
		t.Fatalf("ScanVault failed: %v", err)
	}
	if err := bridgeScanner.SaveConfirmedState(confirmed); err != nil {
		t.Fatalf("SaveConfirmedState failed: %v", err)
	}

	if err := engine.MirrorVaultToBridge(vaultPath, bridgePath); err != nil {
		t.Fatalf("MirrorVaultToBridge (second pass) failed: %v", err)
	}

	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Errorf("expected stale.md to be removed from the bridge, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(bridgePath, "keep.md")); err != nil {
		t.Errorf("expected keep.md to still be present, got %v", err)
	}
}

// TestMirrorVaultToBridge_NeverDeletesAnUnconfirmedNewBridgeFile guards a
// real, previously-shipped bug: a brand new file appearing in the Bridge
// folder (as if iCloud had just delivered an iPhone edit) that
// ScanBridgeAndLog's own debounce hasn't confirmed yet must never be
// deleted just because the Vault doesn't have it yet either - it hasn't
// had a chance to be merged in. Deleting it here meant it could never
// stabilize, never get logged, and never reach the Vault at all.
func TestMirrorVaultToBridge_NeverDeletesAnUnconfirmedNewBridgeFile(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	bridgePath := filepath.Join(tempDir, "Bridge")
	if err := os.MkdirAll(vaultPath, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	newPath := filepath.Join(bridgePath, "from-iphone.md")
	writeFile(t, newPath, "brand new, not yet confirmed")
	// Deliberately never confirm it (no ScanBridgeAndLog call) - this is
	// the "just appeared this cycle, still debouncing" state.

	if err := engine.MirrorVaultToBridge(vaultPath, bridgePath); err != nil {
		t.Fatalf("MirrorVaultToBridge failed: %v", err)
	}

	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("expected the unconfirmed new file to survive untouched, got %v", err)
	}
}

// TestMirrorVaultToBridge_NeverTouchesBridgesOwnSyncDir guards against the
// mirror clobbering the Bridge folder's own independent scan-state
// bookkeeping (its own .sync/state/last_scan.json, written by
// ScanBridgeAndLog) - only the Vault's .sync/ is excluded from *what gets
// copied*, but the Bridge's own .sync/ must also survive the "remove
// stale files" pass untouched.
func TestMirrorVaultToBridge_NeverTouchesBridgesOwnSyncDir(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	bridgePath := filepath.Join(tempDir, "Bridge")

	writeFile(t, filepath.Join(vaultPath, "note.md"), "hello")
	bridgeOwnState := filepath.Join(bridgePath, syncdir.Name, "state", "last_scan.json")
	writeFile(t, bridgeOwnState, `{"files":{}}`)

	if err := engine.MirrorVaultToBridge(vaultPath, bridgePath); err != nil {
		t.Fatalf("MirrorVaultToBridge failed: %v", err)
	}

	if _, err := os.Stat(bridgeOwnState); err != nil {
		t.Errorf("expected the bridge's own .sync/ state file to survive untouched, got %v", err)
	}
}
