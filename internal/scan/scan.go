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

	"github.com/kh813/unitevault/internal/syncdir"
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
	StableState  *ScanState   // Files that are stable (unchanged across 2 scans)
	Changes      []FileChange // Changes detected relative to previous stable state
}

// StateFilePath returns the path to .sync/state/last_scan.json - the raw
// scan from the previous cycle, used only as DebounceFilter's comparison
// baseline (see LoadLastScan/SaveScanState). Deliberately distinct from
// ConfirmedStateFilePath - see that method's doc comment for why a single
// state can't serve both roles.
func (s *Scanner) StateFilePath() string {
	return filepath.Join(s.vaultPath, syncdir.Name, "state", "last_scan.json")
}

// ConfirmedStateFilePath returns the path to
// .sync/state/last_confirmed_scan.json - the state DetectChanges compares
// against to decide what actually changed (see LoadConfirmedState/
// SaveConfirmedState/ApplyChangesToState).
//
// This must be a *different* file from StateFilePath/last_scan.json, and
// must only ever be advanced for paths ApplyChangesToState has just
// logged - never wholesale-replaced with a raw scan. A real, previously-
// shipped bug came from doing exactly that (SaveScanState(currState) was
// also being used as DetectChanges' comparison baseline): the cycle right
// after an edit compares the stale confirmed baseline against a not-yet-
// debounce-stable current scan, which correctly logs the edit as
// (spuriously) deleted - deleted because the edited path is genuinely
// absent from the *stable* set that cycle, not because it was actually
// removed. But the very next cycle, once the edit *is* stable, the
// baseline being compared against is by then the raw scan saved during
// that transitional cycle - which already reflects the new content - so
// DetectChanges sees no difference and the edit is never logged as a
// create/modify at all. Net effect: every edit to an existing file
// produced one bogus delete entry and then vanished from the log
// entirely, forever, with the real new content never recorded anywhere -
// undermining the entire cross-device merge system (spec 3.3/3.4), which
// depends on log entries actually existing to reconstruct anything.
func (s *Scanner) ConfirmedStateFilePath() string {
	return filepath.Join(s.vaultPath, syncdir.Name, "state", "last_confirmed_scan.json")
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

// LoadConfirmedState loads DetectChanges' comparison baseline (see
// ConfirmedStateFilePath), or an empty state if none has been recorded
// yet (first run, or upgrading from before this file existed).
func (s *Scanner) LoadConfirmedState() (*ScanState, error) {
	path := s.ConfirmedStateFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ScanState{Files: make(map[string]FileState)}, nil
		}
		return nil, fmt.Errorf("failed to read confirmed scan state: %w", err)
	}

	var state ScanState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal confirmed scan state: %w", err)
	}
	if state.Files == nil {
		state.Files = make(map[string]FileState)
	}
	return &state, nil
}

// SaveConfirmedState persists DetectChanges' comparison baseline (see
// ConfirmedStateFilePath). Callers should pass the result of
// ApplyChangesToState, never a raw scan.
func (s *Scanner) SaveConfirmedState(state *ScanState) error {
	path := s.ConfirmedStateFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create confirmed scan state dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal confirmed scan state: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// ReconcileForDetection builds the "curr" argument DetectChanges should
// compare against confirmed - never stableState directly. stableState
// alone can't distinguish "genuinely deleted" from "present, but debounce
// hasn't confirmed it stable yet" (both are simply absent from it), which
// on its own would make DetectChanges log a spurious delete for any file
// mid-edit, immediately followed by the real new content never being
// logged at all (see ConfirmedStateFilePath's doc comment for the full
// history). Resolving that needs currState too: a confirmed path missing
// from stableState is only a real deletion if it's *also* missing from
// currState (the raw scan) - otherwise it's just not stable yet, and must
// be carried over at its old confirmed value so DetectChanges reports no
// change for it this cycle (rather than a delete now and total silence
// once it finally does stabilize).
func ReconcileForDetection(confirmed, currState, stableState *ScanState) *ScanState {
	result := &ScanState{Files: make(map[string]FileState, len(confirmed.Files))}
	for path, f := range confirmed.Files {
		if sf, ok := stableState.Files[path]; ok {
			result.Files[path] = sf // freshly confirmed stable - use the new value
			continue
		}
		if _, stillPresent := currState.Files[path]; stillPresent {
			result.Files[path] = f // mid-transition - not a change yet, keep the old value
		}
		// else: genuinely gone from the current raw scan - a real
		// deletion, omitted so DetectChanges reports it as such.
	}
	// Paths stable *and* not yet confirmed at all are brand new creates.
	for path, sf := range stableState.Files {
		if _, already := confirmed.Files[path]; !already {
			result.Files[path] = sf
		}
	}
	return result
}

// ApplyChangesToState returns a new ScanState with exactly the paths in
// changes updated against prev - everything else in prev (both genuinely
// unchanged files and files mid-transition that debounce hasn't confirmed
// stable yet, and so aren't in changes at all) is carried over untouched.
// This is how the confirmed-state baseline (see ConfirmedStateFilePath)
// must always be advanced - never by replacing it outright with a raw or
// debounced scan, which is what caused the bug that comment documents.
func ApplyChangesToState(prev *ScanState, changes []FileChange) *ScanState {
	next := &ScanState{Files: make(map[string]FileState, len(prev.Files))}
	for path, f := range prev.Files {
		next.Files[path] = f
	}

	for _, ch := range changes {
		switch ch.Action {
		case ActionDelete:
			delete(next.Files, ch.Path)
		case ActionRename:
			delete(next.Files, ch.OldPath)
			next.Files[ch.Path] = FileState{Hash: ch.ResultHash}
		default: // ActionCreate, ActionModify
			next.Files[ch.Path] = FileState{Hash: ch.ResultHash}
		}
	}

	return next
}

// ScanPaths recomputes only the given Vault-relative paths against
// baseline, carrying every other path in baseline forward unchanged. It
// produces a result equivalent to ScanVault() when paths covers every path
// that could actually have changed since baseline was captured - which is
// exactly what a watch.Watcher's Drain() is meant to provide (spec 1.6.5) -
// but without re-hashing the entire Vault. This is never a substitute for
// ScanVault() on its own: callers remain responsible for periodically
// falling back to a full scan, since a watch event the OS failed to
// deliver would otherwise never be corrected.
func (s *Scanner) ScanPaths(baseline *ScanState, paths []string) (*ScanState, error) {
	next := &ScanState{Files: make(map[string]FileState, len(baseline.Files))}
	for path, f := range baseline.Files {
		next.Files[path] = f
	}

	for _, rel := range paths {
		slashRel := filepath.ToSlash(rel)
		if syncdir.IsBookkeeping(slashRel) {
			continue
		}

		fullPath := filepath.Join(s.vaultPath, filepath.FromSlash(rel))
		info, err := os.Stat(fullPath)
		if err != nil || info.IsDir() {
			delete(next.Files, slashRel)
			continue
		}

		hash, err := CalculateNormalizedHash(fullPath)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate hash for %s: %w", slashRel, err)
		}
		next.Files[slashRel] = FileState{Hash: hash}
	}

	return next, nil
}

// ScanVault walks the Vault directory and calculates hashes for all files excluding `.sync/`.
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

		// Ignore this app's own bookkeeping directory and hidden files/directories (like .git, .obsidian)
		if syncdir.IsBookkeeping(slashRel) {
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
