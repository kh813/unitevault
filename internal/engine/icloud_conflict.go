package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/kh813/unitevault/internal/config"
	"github.com/kh813/unitevault/internal/merge"
	"github.com/kh813/unitevault/internal/scan"
)

// conflictCopyPattern matches iCloud's own conflict-copy naming convention
// (spec 1.6.10): "Name (suffix).ext" - e.g. "Note (1).md", "Note (Macの
// 競合コピー).md". Deliberately broad about what the parenthesized suffix
// itself contains (it varies by device name/locale/OS version) - what
// actually keeps this from misfiring on a legitimately parenthesized note
// name like "Meeting (draft).md" is requiring an actual "Meeting.md"
// sibling to exist too (see FindICloudConflictCopies), not the pattern
// alone.
var conflictCopyPattern = regexp.MustCompile(`^(.+) \([^()]+\)(\.[^./\\]*)?$`)

// conflictCopyOriginalRelPath returns the Vault-relative path relPath
// would have been bounced from if it matches conflictCopyPattern, or
// ("", false) if it doesn't look like a conflict copy at all.
func conflictCopyOriginalRelPath(relPath string) (string, bool) {
	dir := filepath.Dir(relPath)
	name := filepath.Base(relPath)
	m := conflictCopyPattern.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	originalName := m[1] + m[2]
	if dir == "." {
		return originalName, true
	}
	return dir + "/" + originalName, true
}

// ConflictCopyPair is one detected iCloud conflict copy alongside the
// original file it was bounced from.
type ConflictCopyPair struct {
	OriginalRelPath string
	CopyRelPath     string
}

// FindICloudConflictCopies scans vaultPath for conflictCopyPattern matches
// that actually have their original sibling present too - both files must
// exist side by side, which is what keeps a legitimately parenthesized
// note name from being misflagged (see conflictCopyPattern's own doc
// comment). Reuses scan.Scanner.ScanVault purely for its existing
// directory-walk/ignore-list (this app's own .sync/ bookkeeping) rather
// than duplicating it - the hashes it computes go unused here.
func FindICloudConflictCopies(vaultPath string) ([]ConflictCopyPair, error) {
	state, err := scan.NewScanner(vaultPath).ScanVault()
	if err != nil {
		return nil, fmt.Errorf("failed to scan vault: %w", err)
	}

	var pairs []ConflictCopyPair
	for relPath := range state.Files {
		originalRelPath, ok := conflictCopyOriginalRelPath(relPath)
		if !ok {
			continue
		}
		if _, exists := state.Files[originalRelPath]; !exists {
			continue
		}
		pairs = append(pairs, ConflictCopyPair{OriginalRelPath: originalRelPath, CopyRelPath: relPath})
	}
	return pairs, nil
}

// ICloudConflictCheckResult summarizes
// CheckAndMergeICloudConflictCopies's outcome for the confirmation dialog
// shown after it runs.
type ICloudConflictCheckResult struct {
	// AutoMerged counts pairs whose merge produced no conflict markers -
	// applied automatically, with the conflict-copy file removed.
	AutoMerged int
	// NeedsReview counts pairs whose merge produced conflict markers -
	// written as a new config.PendingConflict, resolvable via the same
	// "Resolve Conflicts..." flow spec 3.3.2 already uses.
	NeedsReview int
	// Failed lists the OriginalRelPath of any pair that errored out
	// (unreadable file, etc.) - best-effort, never blocks the rest.
	Failed []string
}

// CheckAndMergeICloudConflictCopies is spec 1.6.10's manual "Check for
// Conflicts and Merge..." action (Mode A only - Mode D's own conflict-copy
// naming convention hasn't been confirmed yet, so it isn't handled here).
// Deliberately on-demand rather than automatic: a false-positive match
// (see conflictCopyPattern) is at worst a surprising prompt the user can
// decline, never a silent background rewrite.
//
// For each FindICloudConflictCopies pair, attempts a merge between the two
// files. Since iCloud created these independently - Mode A never
// scans/logs Vault content the way Mode B/C's device-log system does, so
// there is no real common ancestor this app ever recorded - the merge
// uses an empty base rather than a true 3-way merge base. git merge-file
// still produces a sensible result either way: a clean combination when
// the two files don't genuinely overlap, or the same
// <<<<<<</=======/>>>>>>>-marked output already used for a genuine
// multi-device conflict (spec 3.3.2) when they do - reusing that exact
// same PendingConflict/"Resolve Conflicts..." machinery rather than
// building a second, parallel conflict-resolution UI.
func CheckAndMergeICloudConflictCopies(cfgMgr *config.ConfigManager, vaultPath string) (ICloudConflictCheckResult, error) {
	pairs, err := FindICloudConflictCopies(vaultPath)
	if err != nil {
		return ICloudConflictCheckResult{}, err
	}

	existing, err := cfgMgr.LoadPendingConflicts()
	if err != nil {
		return ICloudConflictCheckResult{}, err
	}
	pendingByPath := make(map[string]config.PendingConflict, len(existing))
	for _, pc := range existing {
		pendingByPath[pc.RelPath] = pc
	}

	var result ICloudConflictCheckResult
	for _, pair := range pairs {
		originalPath := filepath.Join(vaultPath, pair.OriginalRelPath)
		copyPath := filepath.Join(vaultPath, pair.CopyRelPath)

		originalContent, err := os.ReadFile(originalPath)
		if err != nil {
			result.Failed = append(result.Failed, pair.OriginalRelPath)
			continue
		}
		copyContent, err := os.ReadFile(copyPath)
		if err != nil {
			result.Failed = append(result.Failed, pair.OriginalRelPath)
			continue
		}

		res, err := merge.MergeContents(string(originalContent), "", string(copyContent))
		if err != nil {
			result.Failed = append(result.Failed, pair.OriginalRelPath)
			continue
		}

		if err := os.WriteFile(originalPath, scan.NormalizeLF([]byte(res.MergedContent)), 0644); err != nil {
			result.Failed = append(result.Failed, pair.OriginalRelPath)
			continue
		}

		if !res.HasConflict {
			_ = os.Remove(copyPath)
			delete(pendingByPath, pair.OriginalRelPath)
			result.AutoMerged++
			continue
		}

		writtenHash, err := scan.CalculateNormalizedHash(originalPath)
		if err != nil {
			result.Failed = append(result.Failed, pair.OriginalRelPath)
			continue
		}
		pendingByPath[pair.OriginalRelPath] = config.PendingConflict{
			RelPath:     pair.OriginalRelPath,
			DetectedAt:  time.Now().Format(time.RFC3339),
			WrittenHash: writtenHash,
			Versions: []config.PendingConflictVersion{
				{DeviceID: "icloud_original", Label: "Original", Content: string(originalContent)},
				{DeviceID: "icloud_conflict_copy", Label: filepath.Base(pair.CopyRelPath), Content: string(copyContent)},
			},
			ExtraFileToRemove: pair.CopyRelPath,
		}
		result.NeedsReview++
	}

	pending := make([]config.PendingConflict, 0, len(pendingByPath))
	for _, pc := range pendingByPath {
		pending = append(pending, pc)
	}
	if err := cfgMgr.SavePendingConflicts(pending); err != nil {
		return result, err
	}
	return result, nil
}
