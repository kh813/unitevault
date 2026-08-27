// Package obsidianconfig edits Obsidian's own global vault list
// (obsidian.json) so a Vault Migration (moving the Vault folder to a new
// location) doesn't leave Obsidian itself pointed at the old, now-empty
// spot. Obsidian, not this app, owns this file's location and format;
// every operation here is best-effort and conservative about it.
package obsidianconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ConfigPath returns the path to Obsidian's global obsidian.json, per OS.
func ConfigPath() (string, error) {
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get home dir: %w", err)
			}
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "Obsidian", "obsidian.json"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home dir: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", "obsidian", "obsidian.json"), nil
}

// UpdateVaultPath rewrites the vault entry whose "path" is exactly oldPath
// so it points at newPath instead, so Obsidian itself opens the moved
// folder next time rather than the now-empty spot the Vault used to
// occupy (spec: Vault Migration).
//
// Backs up the original file first (obsidian.json.bak, overwriting any
// previous backup) and is deliberately conservative: any unexpected
// structure (missing file, unparseable JSON, no vault entry matching
// oldPath) returns a descriptive error instead of guessing - callers
// should treat this as best-effort and tell the user to update Obsidian's
// vault list by hand rather than fail the whole migration over it.
func UpdateVaultPath(oldPath, newPath string) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	return UpdateVaultPathAt(path, oldPath, newPath)
}

// UpdateVaultPathAt is UpdateVaultPath against an explicit obsidian.json
// path rather than the real, OS-resolved one - split out purely so tests
// can exercise the actual editing logic against a temp file instead of
// duplicating it.
func UpdateVaultPathAt(path, oldPath, newPath string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("could not read Obsidian's vault list at %s: %w", path, err)
	}

	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("could not parse Obsidian's vault list at %s: %w", path, err)
	}

	vaults, ok := root["vaults"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("unexpected format in %s: no \"vaults\" object found", path)
	}

	found := false
	for _, v := range vaults {
		entry, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if p, ok := entry["path"].(string); ok && p == oldPath {
			entry["path"] = newPath
			found = true
		}
	}
	if !found {
		return fmt.Errorf("no vault entry in %s has path %q", path, oldPath)
	}

	if err := os.WriteFile(path+".bak", data, 0644); err != nil {
		return fmt.Errorf("failed to back up %s before editing: %w", path, err)
	}

	updated, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize the updated vault list: %w", err)
	}
	if err := os.WriteFile(path, updated, 0644); err != nil {
		return fmt.Errorf("failed to write the updated vault list to %s: %w", path, err)
	}
	return nil
}
