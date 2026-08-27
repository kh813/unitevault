package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kh813/unitevault/internal/scan"
	"github.com/kh813/unitevault/internal/syncdir"
)

// manifestPath returns the path to .sync/vault_manifest.json - Primary's
// published "what should exist" file list (spec 1.6.4), used by Secondary
// to safely propagate deletions despite pulling via non-destructive
// `rclone copy`. Deliberately a separate, shared-on-purpose file from
// Scanner.ConfirmedStateFilePath's state/ directory (which is per-device
// private and excluded from every Sync/Copy call) - this one is meant to
// be published and pulled.
func manifestPath(vaultPath string) string {
	return filepath.Join(vaultPath, syncdir.Name, "vault_manifest.json")
}

// PublishManifest writes a fresh scan of vaultPath's current content to
// .sync/vault_manifest.json (spec 1.6.4). Called by Primary right before
// publishing to Google Drive, so the manifest matches what's about to be
// published - a fresh scan (not the confirmed-state baseline) is used
// deliberately, since the merge step just before this writes files
// directly, bypassing the scan/debounce/log pipeline that baseline
// tracks.
func PublishManifest(vaultPath string) error {
	state, err := scan.NewScanner(vaultPath).ScanVault()
	if err != nil {
		return fmt.Errorf("failed to scan vault for manifest: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	path := manifestPath(vaultPath)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create sync dir for manifest: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// LoadManifest reads .sync/vault_manifest.json, returning (nil, nil) if
// Primary hasn't published one yet (e.g. this Secondary hasn't completed
// its first pull, or Primary predates this feature).
func LoadManifest(vaultPath string) (*scan.ScanState, error) {
	data, err := os.ReadFile(manifestPath(vaultPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	var state scan.ScanState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}
	if state.Files == nil {
		state.Files = make(map[string]scan.FileState)
	}
	return &state, nil
}

// ApplyManifestDeletions removes local Vault files absent from manifest,
// except ones this device's own confirmed scan state doesn't (yet) know
// about - protecting a just-created local file from being deleted before
// it's had a chance to round-trip through Primary's merge and appear in a
// future manifest (spec 1.6.4). Returns how many files were removed.
//
// This is a heuristic, not a guarantee: a very fast deletion elsewhere
// combined with a very slow round-trip could in principle still race it -
// the same class of accepted v1 tradeoff as the rest of this section (see
// spec 1.6.4's other known limitations).
func ApplyManifestDeletions(vaultPath string, manifest *scan.ScanState) (int, error) {
	if manifest == nil {
		return 0, nil
	}

	confirmed, err := scan.NewScanner(vaultPath).LoadConfirmedState()
	if err != nil {
		return 0, fmt.Errorf("failed to load confirmed state for manifest reconciliation: %w", err)
	}

	deleted := 0
	for relPath := range confirmed.Files {
		if bridgeExcluded(relPath) {
			continue // never touch this app's own bookkeeping
		}
		if _, inManifest := manifest.Files[relPath]; inManifest {
			continue
		}
		fullPath := filepath.Join(vaultPath, filepath.FromSlash(relPath))
		if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
			return deleted, fmt.Errorf("failed to remove %s (absent from Primary's manifest): %w", relPath, err)
		}
		deleted++
	}
	return deleted, nil
}
