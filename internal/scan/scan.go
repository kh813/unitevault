package scan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileState represents the hash and status of a single file in the Vault.
type FileState struct {
	Hash string `json:"hash"`
}

// ScanState represents the state of all tracked files in the Vault during a scan.
type ScanState struct {
	Files map[string]FileState `json:"files"` // key: slash-separated relative path from Vault root
}

// ActionType represents the type of change detected.
type ActionType string

const (
	ActionCreate ActionType = "create"
	ActionModify ActionType = "modify"
	ActionDelete ActionType = "delete"
	ActionRename ActionType = "rename"
)

// FileChange represents a detected change in a file.
type FileChange struct {
	Action     ActionType `json:"action"`
	Path       string     `json:"path"`
	OldPath    string     `json:"old_path,omitempty"`    // present for rename
	BaseHash   string     `json:"base_hash,omitempty"`   // hash before change
	ResultHash string     `json:"result_hash,omitempty"` // hash after change
}

// NormalizeLF normalizes CRLF and CR line endings to LF.
func NormalizeLF(b []byte) []byte {
	// First replace CRLF with LF
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	// Then replace any remaining isolated CR with LF
	b = bytes.ReplaceAll(b, []byte("\r"), []byte("\n"))
	return b
}

// CalculateNormalizedHash reads a file, normalizes its content line endings to LF, and returns SHA-256 hash.
func CalculateNormalizedHash(filePath string) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	normalized := NormalizeLF(content)
	hasher := sha256.New()
	hasher.Write(normalized)
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// Scanner scans a Vault directory and detects changes.
type Scanner struct {
	vaultPath string
}

// NewScanner creates a new Scanner for the given Vault directory.
func NewScanner(vaultPath string) *Scanner {
	return &Scanner{vaultPath: vaultPath}
}

// ScanResult holds current scan state and stability info for debounce.
type ScanResult struct {
	CurrentState *ScanState
	StableState  *ScanState            // Files that are stable (unchanged across 2 scans)
	Changes      []FileChange          // Changes detected relative to previous stable state
}

// StateFilePath returns the path to _sync/state/last_scan.json
func (s *Scanner) StateFilePath() string {
	return filepath.Join(s.vaultPath, "_sync", "state", "last_scan.json")
}

// LoadLastScan loads the last recorded scan state from disk.
func (s *Scanner) LoadLastScan() (*ScanState, error) {
	path := s.StateFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ScanState{Files: make(map[string]FileState)}, nil
		}
		return nil, fmt.Errorf("failed to read last scan state: %w", err)
	}

	var state ScanState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal last scan state: %w", err)
	}
	if state.Files == nil {
		state.Files = make(map[string]FileState)
	}
	return &state, nil
}

// SaveScanState saves the current scan state to disk.
func (s *Scanner) SaveScanState(state *ScanState) error {
	path := s.StateFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create scan state dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal scan state: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// ScanVault walks the Vault directory and calculates hashes for all files excluding `_sync/`.
func (s *Scanner) ScanVault() (*ScanState, error) {
	state := &ScanState{Files: make(map[string]FileState)}

	err := filepath.Walk(s.vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(s.vaultPath, path)
		if err != nil {
			return err
		}

		if rel == "." {
			return nil
		}

		// Convert path to slash-separated for consistency across OSes
		slashRel := filepath.ToSlash(rel)

		// Ignore _sync directory and hidden files/directories (like .git, .obsidian if needed, though only _sync is specified by spec)
		if slashRel == "_sync" || strings.HasPrefix(slashRel, "_sync/") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		hash, err := CalculateNormalizedHash(path)
		if err != nil {
			return fmt.Errorf("failed to calculate hash for %s: %w", slashRel, err)
		}

		state.Files[slashRel] = FileState{Hash: hash}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return state, nil
}

// DetectChanges compares previous scan state with current scan state and identifies create, delete, modify, and rename actions.
func DetectChanges(prev, curr *ScanState) []FileChange {
	if prev == nil || prev.Files == nil {
		prev = &ScanState{Files: make(map[string]FileState)}
	}
	if curr == nil || curr.Files == nil {
		curr = &ScanState{Files: make(map[string]FileState)}
	}

	var changes []FileChange

	deleted := make(map[string]FileState)
	created := make(map[string]FileState)

	// Identify deleted or modified
	for path, prevFile := range prev.Files {
		currFile, exists := curr.Files[path]
		if !exists {
			deleted[path] = prevFile
		} else if prevFile.Hash != currFile.Hash {
			changes = append(changes, FileChange{
				Action:     ActionModify,
				Path:       path,
				BaseHash:   prevFile.Hash,
				ResultHash: currFile.Hash,
			})
		}
	}

	// Identify created
	for path, currFile := range curr.Files {
		if _, exists := prev.Files[path]; !exists {
			created[path] = currFile
		}
	}

	// Detect renames (hash in deleted matches hash in created)
	matchedCreated := make(map[string]bool)
	matchedDeleted := make(map[string]bool)

	for delPath, delFile := range deleted {
		for crPath, crFile := range created {
			if !matchedCreated[crPath] && delFile.Hash == crFile.Hash {
				changes = append(changes, FileChange{
					Action:     ActionRename,
					Path:       crPath,
					OldPath:    delPath,
					BaseHash:   delFile.Hash,
					ResultHash: crFile.Hash,
				})
				matchedDeleted[delPath] = true
				matchedCreated[crPath] = true
				break
			}
		}
	}

	// Unmatched deleted -> ActionDelete
	for delPath, delFile := range deleted {
		if !matchedDeleted[delPath] {
			changes = append(changes, FileChange{
				Action:     ActionDelete,
				Path:       delPath,
				BaseHash:   delFile.Hash,
				ResultHash: "",
			})
		}
	}

	// Unmatched created -> ActionCreate
	for crPath, crFile := range created {
		if !matchedCreated[crPath] {
			changes = append(changes, FileChange{
				Action:     ActionCreate,
				Path:       crPath,
				BaseHash:   "",
				ResultHash: crFile.Hash,
			})
		}
	}

	return changes
}

// DebounceFilter filters current scan files to only include those that are stable (identical in scan1 and scan2).
func DebounceFilter(scan1, scan2 *ScanState) *ScanState {
	stable := &ScanState{Files: make(map[string]FileState)}
	if scan1 == nil || scan2 == nil {
		return stable
	}

	for path, f2 := range scan2.Files {
		if f1, exists := scan1.Files[path]; exists && f1.Hash == f2.Hash {
			stable.Files[path] = f2
		}
	}

	return stable
}

// Helper io reader copier
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
