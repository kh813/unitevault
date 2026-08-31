package merge_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kh813/unitevault/internal/merge"
	"github.com/kh813/unitevault/internal/syncedlog"
)

func TestMergeContents_CleanMerge(t *testing.T) {
	base := "Line 1\nLine 2\nLine 3\n"
	versionA := "Line 1 (edited by A)\nLine 2\nLine 3\n"
	versionB := "Line 1\nLine 2\nLine 3 (edited by B)\n"

	res, err := merge.MergeContents(versionA, base, versionB)
	if err != nil {
		t.Fatalf("MergeContents returned error: %v", err)
	}

	if res.HasConflict {
		t.Fatalf("expected no conflict in clean merge")
	}

	expected := "Line 1 (edited by A)\nLine 2\nLine 3 (edited by B)\n"
	if res.MergedContent != expected {
		t.Fatalf("expected:\n%q\ngot:\n%q", expected, res.MergedContent)
	}
}

func TestMergeContents_Conflict(t *testing.T) {
	base := "Line 1\nLine 2\nLine 3\n"
	versionA := "Line 1 (edited by A)\nLine 2\nLine 3\n"
	versionB := "Line 1 (edited by B)\nLine 2\nLine 3\n"

	res, err := merge.MergeContents(versionA, base, versionB)
	if err != nil {
		t.Fatalf("MergeContents returned error: %v", err)
	}

	if !res.HasConflict {
		t.Fatalf("expected conflict marker flag to be true")
	}

	if !merge.HasConflictMarkers(res.MergedContent) {
		t.Fatalf("expected conflict markers (<<<<<<<) in merged content")
	}
}

func TestNWayMerge_ThreeDevices(t *testing.T) {
	base := "Title\n\nSection A\n\nSection B\n\nSection C\n"
	devA := merge.DeviceVersion{DeviceID: "devA", Content: "Title\n\nSection A (updated A)\n\nSection B\n\nSection C\n"}
	devB := merge.DeviceVersion{DeviceID: "devB", Content: "Title\n\nSection A\n\nSection B (updated B)\n\nSection C\n"}
	devC := merge.DeviceVersion{DeviceID: "devC", Content: "Title\n\nSection A\n\nSection B\n\nSection C (updated C)\n"}

	versions := []merge.DeviceVersion{devA, devB, devC}
	res, err := merge.NWayMerge(base, versions)
	if err != nil {
		t.Fatalf("NWayMerge returned error: %v", err)
	}

	if res.HasConflict {
		t.Fatalf("expected no conflict in 3-way clean merge")
	}

	expected := "Title\n\nSection A (updated A)\n\nSection B (updated B)\n\nSection C (updated C)\n"
	if res.MergedContent != expected {
		t.Fatalf("expected:\n%q\ngot:\n%q", expected, res.MergedContent)
	}
}

// TestNWayMerge_EmptyBaseFalselyConflictsOnNonOverlappingEdits documents
// exactly why an empty base must never be passed to NWayMerge for real
// devices' content (spec 3.3.1): git merge-file has no way to tell
// non-overlapping edits apart from genuinely conflicting ones without a
// real common ancestor, so it reports a conflict for both. This is a
// property of git merge-file itself (not a bug to fix here) - callers
// (engine.mergeAndTrackConflicts) must always reconstruct and pass the
// real base via FindCommonBaseHash + FindContentByHash instead.
func TestNWayMerge_EmptyBaseFalselyConflictsOnNonOverlappingEdits(t *testing.T) {
	devA := merge.DeviceVersion{DeviceID: "devA", Content: "line1 CHANGED\nline2\nline3\nline4\nline5\n"}
	devB := merge.DeviceVersion{DeviceID: "devB", Content: "line1\nline2\nline3\nline4\nline5 CHANGED\n"}

	res, err := merge.NWayMerge("", []merge.DeviceVersion{devA, devB})
	if err != nil {
		t.Fatalf("NWayMerge returned error: %v", err)
	}
	if !res.HasConflict {
		t.Fatal("expected an empty base to falsely report a conflict for non-overlapping edits")
	}

	realBase := "line1\nline2\nline3\nline4\nline5\n"
	res, err = merge.NWayMerge(realBase, []merge.DeviceVersion{devA, devB})
	if err != nil {
		t.Fatalf("NWayMerge returned error: %v", err)
	}
	if res.HasConflict {
		t.Fatal("expected the real base to auto-merge non-overlapping edits cleanly")
	}
}

// TestNWayMerge_StopsFoldingAfterConflict guards a real, previously-shipped
// bug: once a pairwise merge in the fold produces a genuine conflict, its
// raw conflict-marker text (literal "<<<<<<<"/"======="/">>>>>>>" lines)
// used to get fed into the *next* MergeContents call as if it were
// ordinary file content. Since that "content" differs wildly from the
// unchanged base, git merge-file would wrap it in a second, nested layer
// of conflict markers - corrupting the file with doubled-up garbage
// instead of a single, normal conflict (reported on a real device: a
// three-device N-way merge produced a file with two full sets of nested
// <<<<<<</=======/>>>>>>> markers). The fix stops folding the moment any
// step conflicts, so a third device's content must never appear in the
// result and the marker set must never be doubled.
func TestNWayMerge_StopsFoldingAfterConflict(t *testing.T) {
	base := "line1\nline2\nline3\n"
	// devA and devB both change line1 differently relative to the same
	// base - a genuine, unavoidable conflict on the very first pairwise
	// merge.
	devA := merge.DeviceVersion{DeviceID: "devA", Content: "line1 CHANGED BY A\nline2\nline3\n"}
	devB := merge.DeviceVersion{DeviceID: "devB", Content: "line1 CHANGED BY B\nline2\nline3\n"}
	devC := merge.DeviceVersion{DeviceID: "devC", Content: "line1\nline2\nline3 CHANGED BY C\n"}

	res, err := merge.NWayMerge(base, []merge.DeviceVersion{devA, devB, devC})
	if err != nil {
		t.Fatalf("NWayMerge returned error: %v", err)
	}
	if !res.HasConflict {
		t.Fatalf("expected a genuine conflict between devA and devB, got clean merge:\n%q", res.MergedContent)
	}

	if strings.Contains(res.MergedContent, "CHANGED BY C") {
		t.Errorf("expected devC to never be folded in once devA/devB conflicted, got:\n%s", res.MergedContent)
	}

	for _, marker := range []string{"<<<<<<<", "=======", ">>>>>>>"} {
		if got := strings.Count(res.MergedContent, marker); got != 1 {
			t.Errorf("expected exactly one %q marker (a single, un-nested conflict), got %d in:\n%s", marker, got, res.MergedContent)
		}
	}
}

func TestFindCommonBaseHash(t *testing.T) {
	t.Run("all entries agree", func(t *testing.T) {
		entries := map[string]syncedlog.LogEntry{
			"dev-a": {BaseHash: "hash-base"},
			"dev-b": {BaseHash: "hash-base"},
		}
		if got := merge.FindCommonBaseHash(entries); got != "hash-base" {
			t.Errorf("expected %q, got %q", "hash-base", got)
		}
	})

	t.Run("entries disagree", func(t *testing.T) {
		entries := map[string]syncedlog.LogEntry{
			"dev-a": {BaseHash: "hash-1"},
			"dev-b": {BaseHash: "hash-2"},
		}
		if got := merge.FindCommonBaseHash(entries); got != "" {
			t.Errorf("expected empty string when entries disagree, got %q", got)
		}
	})
}

func TestFindContentByHash(t *testing.T) {
	allLogs := map[string][]syncedlog.LogEntry{
		"dev-a": {
			{ResultHash: "hash-base", Diff: "base content"},
			{ResultHash: "hash-a", Diff: "A's content"},
		},
		"dev-b": {
			{ResultHash: "hash-b", Diff: "B's content"},
		},
	}

	t.Run("found in a different device's log than the one that logged it", func(t *testing.T) {
		content, found := merge.FindContentByHash(allLogs, "hash-base")
		if !found || content != "base content" {
			t.Errorf("expected to find %q, got %q (found=%v)", "base content", content, found)
		}
	})

	t.Run("not found", func(t *testing.T) {
		if _, found := merge.FindContentByHash(allLogs, "hash-does-not-exist"); found {
			t.Error("expected not found for an unknown hash")
		}
	})

	t.Run("empty hash never matches", func(t *testing.T) {
		if _, found := merge.FindContentByHash(allLogs, ""); found {
			t.Error("expected an empty target hash to never match")
		}
	})
}

// TestApplyResolution_WritesRealContentNotPlaceholder guards the original
// bug this replaces: the resolved device's actual content, not a
// placeholder string, must end up in the Vault file and the new log entry.
func TestApplyResolution_WritesRealContentNotPlaceholder(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "Vault")
	lm := syncedlog.NewLogManager(vaultPath)

	if err := merge.ApplyResolution(lm, vaultPath, "Notes/foo.md", "resolved content\n", "primary-device", "mac-test"); err != nil {
		t.Fatalf("ApplyResolution failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(vaultPath, "Notes/foo.md"))
	if err != nil {
		t.Fatalf("failed to read resolved file: %v", err)
	}
	if string(got) != "resolved content\n" {
		t.Errorf("expected the real resolved content written, got %q", string(got))
	}

	entries, err := lm.ReadDeviceLog("primary-device")
	if err != nil {
		t.Fatalf("ReadDeviceLog failed: %v", err)
	}
	if len(entries) != 1 || entries[0].Diff != "resolved content\n" || entries[0].Label != "mac-test" {
		t.Errorf("expected a log entry recording the real resolved content, got %+v", entries)
	}
}
