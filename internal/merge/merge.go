package merge

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kh813/unitevault/internal/scan"
	"github.com/kh813/unitevault/internal/syncedlog"
	"github.com/kh813/unitevault/internal/winexec"
)

// MergeResult holds the output of a 3-way merge operation.
type MergeResult struct {
	MergedContent string
	HasConflict   bool
}

// GitMergeFile executes `git merge-file -p <fileA> <baseFile> <fileB>`
// Returns merged content string and boolean indicating if conflict markers are present.
func GitMergeFile(fileA, baseFile, fileB string) (MergeResult, error) {
	cmd := exec.Command("git", "merge-file", "-p", fileA, baseFile, fileB)
	winexec.HideWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	// git merge-file exits with status 0 (clean merge) or status >0 (conflict).
	// An exit status >0 is not necessarily a command failure if stderr is empty and output contains conflict markers.
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Exit code > 0 occurs when there are conflicts
			if exitErr.ExitCode() < 0 {
				return MergeResult{}, fmt.Errorf("git merge-file error: %w (stderr: %s)", err, stderr.String())
			}
		} else {
			return MergeResult{}, fmt.Errorf("git merge-file execution error: %w", err)
		}
	}

	mergedBytes := scan.NormalizeLF(stdout.Bytes())
	mergedStr := string(mergedBytes)
	hasConflict := HasConflictMarkers(mergedStr)

	return MergeResult{
		MergedContent: mergedStr,
		HasConflict:   hasConflict,
	}, nil
}

// HasConflictMarkers checks if content contains standard git conflict markers (<<<<<<<)
func HasConflictMarkers(content string) bool {
	return strings.Contains(content, "<<<<<<<")
}

// MergeContents performs 3-way merge on raw string contents using temporary files
func MergeContents(contentA, baseContent, contentB string) (MergeResult, error) {
	tmpDir, err := os.MkdirTemp("", "unitevault_merge_*")
	if err != nil {
		return MergeResult{}, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fileA := fmt.Sprintf("%s/fileA.txt", tmpDir)
	fileBase := fmt.Sprintf("%s/fileBase.txt", tmpDir)
	fileB := fmt.Sprintf("%s/fileB.txt", tmpDir)

	if err := os.WriteFile(fileA, scan.NormalizeLF([]byte(contentA)), 0644); err != nil {
		return MergeResult{}, err
	}
	if err := os.WriteFile(fileBase, scan.NormalizeLF([]byte(baseContent)), 0644); err != nil {
		return MergeResult{}, err
	}
	if err := os.WriteFile(fileB, scan.NormalizeLF([]byte(contentB)), 0644); err != nil {
		return MergeResult{}, err
	}

	return GitMergeFile(fileA, fileBase, fileB)
}

// DeviceVersion represents a single device's version of a file along with its base hash
type DeviceVersion struct {
	DeviceID string
	Content  string
	BaseHash string
}

// NWayMerge sequentially merges multiple device versions against a base version (Octopus merge concept)
func NWayMerge(baseContent string, versions []DeviceVersion) (MergeResult, error) {
	if len(versions) == 0 {
		return MergeResult{MergedContent: baseContent, HasConflict: false}, nil
	}
	if len(versions) == 1 {
		return MergeResult{MergedContent: versions[0].Content, HasConflict: false}, nil
	}

	currentResult := MergeResult{MergedContent: baseContent, HasConflict: false}

	// Merge first version against base
	res, err := MergeContents(versions[0].Content, baseContent, versions[1].Content)
	if err != nil {
		return MergeResult{}, fmt.Errorf("merge failed between %s and %s: %w", versions[0].DeviceID, versions[1].DeviceID, err)
	}

	currentResult = res

	// Sequentially fold remaining versions
	for i := 2; i < len(versions); i++ {
		nextRes, err := MergeContents(currentResult.MergedContent, baseContent, versions[i].Content)
		if err != nil {
			return MergeResult{}, fmt.Errorf("merge failed incorporating %s: %w", versions[i].DeviceID, err)
		}
		currentResult.MergedContent = nextRes.MergedContent
		if nextRes.HasConflict {
			currentResult.HasConflict = true
		}
	}

	return currentResult, nil
}

// ApplyResolution writes resolvedContent to the Vault file at relPath and
// records it as a new log entry under resolverDeviceID/resolverLabel (spec
// 3.3.2) - used once a genuine conflict has been resolved, e.g. by a user
// picking one device's version in the GUI. The new entry's Diff carries
// the resolved content itself (spec 3.4), same as any ordinary change, so
// it's usable as a merge base for future edits too.
func ApplyResolution(lm *syncedlog.LogManager, vaultPath, relPath, resolvedContent, resolverDeviceID, resolverLabel string) error {
	fullPath := filepath.Join(vaultPath, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory for resolved file: %w", err)
	}
	normalized := scan.NormalizeLF([]byte(resolvedContent))
	if err := os.WriteFile(fullPath, normalized, 0644); err != nil {
		return fmt.Errorf("failed to write resolved content to file: %w", err)
	}

	hash, err := scan.CalculateNormalizedHash(fullPath)
	if err != nil {
		return fmt.Errorf("failed to hash resolved file: %w", err)
	}

	seq, err := lm.GetNextSeq(resolverDeviceID)
	if err != nil {
		return err
	}

	entry := syncedlog.LogEntry{
		Device:     resolverDeviceID,
		Label:      resolverLabel,
		Seq:        seq,
		Path:       relPath,
		ResultHash: hash,
		Diff:       string(normalized),
		Action:     scan.ActionModify,
	}

	return lm.AppendLogEntry(entry)
}

// FindContentByHash searches every device's log for an entry whose
// ResultHash equals targetHash, returning its stored content (the Diff
// field - spec 3.4 stores full content there, not a true diff, as a v1
// simplification) and whether a match was found. Which device originally
// logged it doesn't matter - every device's log is readable by any other
// (spec 3.2). Used to reconstruct the real merge base from a common
// base_hash (see mergeAndTrackConflicts).
func FindContentByHash(allDeviceLogs map[string][]syncedlog.LogEntry, targetHash string) (string, bool) {
	if targetHash == "" {
		return "", false
	}
	for _, entries := range allDeviceLogs {
		for _, e := range entries {
			if e.ResultHash == targetHash {
				return e.Diff, true
			}
		}
	}
	return "", false
}

// FindCommonBaseHash reports the base_hash shared by every device's latest
// entry for a path, or "" if they disagree (see mergeAndTrackConflicts).
func FindCommonBaseHash(latestEntries map[string]syncedlog.LogEntry) string {
	var firstHash string
	for _, entry := range latestEntries {
		if firstHash == "" {
			firstHash = entry.BaseHash
		} else if firstHash != entry.BaseHash {
			return "" // Hashes do not match across all devices
		}
	}
	return firstHash
}
