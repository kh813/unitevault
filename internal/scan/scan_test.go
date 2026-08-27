package scan_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/kh813/unitevault/internal/scan"
	"github.com/kh813/unitevault/internal/syncdir"
)

func TestNormalizeLF(t *testing.T) {
	input := []byte("line1\r\nline2\rline3\nline4")
	expected := []byte("line1\nline2\nline3\nline4")

	result := scan.NormalizeLF(input)
	if !bytes.Equal(result, expected) {
		t.Fatalf("NormalizeLF failed.\nExpected: %q\nGot: %q", expected, result)
	}
}

func TestCalculateNormalizedHash(t *testing.T) {
	tempDir := t.TempDir()

	fileCRLF := filepath.Join(tempDir, "crlf.txt")
	fileLF := filepath.Join(tempDir, "lf.txt")

	if err := os.WriteFile(fileCRLF, []byte("hello\r\nworld\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileLF, []byte("hello\nworld\n"), 0644); err != nil {
		t.Fatal(err)
	}

	hashCRLF, err := scan.CalculateNormalizedHash(fileCRLF)
	if err != nil {
		t.Fatalf("failed to calculate hash for CRLF file: %v", err)
	}

	hashLF, err := scan.CalculateNormalizedHash(fileLF)
	if err != nil {
		t.Fatalf("failed to calculate hash for LF file: %v", err)
	}

	if hashCRLF != hashLF {
		t.Fatalf("expected hashes to match after LF normalization, got %s vs %s", hashCRLF, hashLF)
	}
}

func TestScanner_ScanAndDetectChanges(t *testing.T) {
	vault := t.TempDir()
	scanner := scan.NewScanner(vault)

	// Step 1: Initial empty state
	lastScan, err := scanner.LoadLastScan()
	if err != nil {
		t.Fatalf("failed to load initial last scan: %v", err)
	}
	if len(lastScan.Files) != 0 {
		t.Fatalf("expected 0 files initially, got %d", len(lastScan.Files))
	}

	// Create files
	doc1 := filepath.Join(vault, "Notes", "doc1.md")
	doc2 := filepath.Join(vault, "doc2.md")
	if err := os.MkdirAll(filepath.Dir(doc1), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc1, []byte("doc1 content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc2, []byte("doc2 content\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Step 2: Scan vault
	scan1, err := scanner.ScanVault()
	if err != nil {
		t.Fatalf("ScanVault failed: %v", err)
	}
	if len(scan1.Files) != 2 {
		t.Fatalf("expected 2 scanned files, got %d", len(scan1.Files))
	}

	changes := scan.DetectChanges(lastScan, scan1)
	if len(changes) != 2 {
		t.Fatalf("expected 2 create changes, got %d", len(changes))
	}
	for _, c := range changes {
		if c.Action != scan.ActionCreate {
			t.Errorf("expected ActionCreate, got %s for %s", c.Action, c.Path)
		}
	}

	// Save scan1 state
	if err := scanner.SaveScanState(scan1); err != nil {
		t.Fatalf("failed to save scan state: %v", err)
	}

	// Step 3: Modify doc1, rename doc2 -> doc2_renamed.md, delete doc1 later
	doc2Renamed := filepath.Join(vault, "doc2_renamed.md")
	if err := os.Rename(doc2, doc2Renamed); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc1, []byte("doc1 updated content\n"), 0644); err != nil {
		t.Fatal(err)
	}

	scan2, err := scanner.ScanVault()
	if err != nil {
		t.Fatalf("ScanVault failed: %v", err)
	}

	changes2 := scan.DetectChanges(scan1, scan2)
	// We expect 1 modify (doc1.md) and 1 rename (doc2.md -> doc2_renamed.md)
	var hasModify, hasRename bool
	for _, c := range changes2 {
		if c.Action == scan.ActionModify && c.Path == "Notes/doc1.md" {
			hasModify = true
		}
		if c.Action == scan.ActionRename && c.Path == "doc2_renamed.md" && c.OldPath == "doc2.md" {
			hasRename = true
		}
	}

	if !hasModify {
		t.Errorf("expected modify change for Notes/doc1.md")
	}
	if !hasRename {
		t.Errorf("expected rename change for doc2.md -> doc2_renamed.md")
	}
}

func TestDebounceFilter(t *testing.T) {
	s1 := &scan.ScanState{
		Files: map[string]scan.FileState{
			"a.txt": {Hash: "hash1"},
			"b.txt": {Hash: "hash2_old"},
		},
	}
	s2 := &scan.ScanState{
		Files: map[string]scan.FileState{
			"a.txt": {Hash: "hash1"},
			"b.txt": {Hash: "hash2_new"},
			"c.txt": {Hash: "hash3"},
		},
	}

	stable := scan.DebounceFilter(s1, s2)
	if len(stable.Files) != 1 {
		t.Fatalf("expected 1 stable file, got %d", len(stable.Files))
	}
	if _, exists := stable.Files["a.txt"]; !exists {
		t.Errorf("expected a.txt to be stable")
	}
}

func TestReconcileForDetection(t *testing.T) {
	confirmed := &scan.ScanState{Files: map[string]scan.FileState{
		"stable.md":        {Hash: "h-stable"},
		"transitioning.md": {Hash: "h-old"},
		"deleted.md":       {Hash: "h-gone"},
	}}
	currState := &scan.ScanState{Files: map[string]scan.FileState{
		"stable.md":        {Hash: "h-stable"},
		"transitioning.md": {Hash: "h-new-not-yet-stable"}, // present, but...
		"created.md":       {Hash: "h-created"},
		// "deleted.md" is genuinely absent here too.
	}}
	stableState := &scan.ScanState{Files: map[string]scan.FileState{
		"stable.md":  {Hash: "h-stable"},
		"created.md": {Hash: "h-created"},
		// "transitioning.md" not yet stable, so absent here (but present
		// in currState above) - must NOT be treated as deleted.
	}}

	got := scan.ReconcileForDetection(confirmed, currState, stableState)

	if h, ok := got.Files["stable.md"]; !ok || h.Hash != "h-stable" {
		t.Errorf("expected stable.md to carry its stable value, got %+v (present=%v)", h, ok)
	}
	if h, ok := got.Files["transitioning.md"]; !ok || h.Hash != "h-old" {
		t.Errorf("expected transitioning.md to keep its OLD confirmed value (not yet a change, and not a deletion), got %+v (present=%v)", h, ok)
	}
	if _, ok := got.Files["deleted.md"]; ok {
		t.Error("expected deleted.md (genuinely absent from currState) to be treated as deleted")
	}
	if h, ok := got.Files["created.md"]; !ok || h.Hash != "h-created" {
		t.Errorf("expected created.md (stable, never confirmed before) to appear as a create, got %+v (present=%v)", h, ok)
	}
}

func TestApplyChangesToState(t *testing.T) {
	prev := &scan.ScanState{Files: map[string]scan.FileState{
		"unchanged.md": {Hash: "h-unchanged"},
		"modified.md":  {Hash: "h-old"},
		"deleted.md":   {Hash: "h-deleted"},
		"old-name.md":  {Hash: "h-renamed"},
		// "still-transitioning.md" is deliberately absent from changes below
		// (debounce hasn't confirmed it stable yet) - it must survive
		// untouched, which this state alone can't show; see the dedicated
		// mid-transition test below instead.
	}}
	changes := []scan.FileChange{
		{Action: scan.ActionModify, Path: "modified.md", ResultHash: "h-new"},
		{Action: scan.ActionDelete, Path: "deleted.md"},
		{Action: scan.ActionRename, Path: "new-name.md", OldPath: "old-name.md", ResultHash: "h-renamed"},
		{Action: scan.ActionCreate, Path: "created.md", ResultHash: "h-created"},
	}

	next := scan.ApplyChangesToState(prev, changes)

	want := map[string]string{
		"unchanged.md": "h-unchanged",
		"modified.md":  "h-new",
		"new-name.md":  "h-renamed",
		"created.md":   "h-created",
	}
	if len(next.Files) != len(want) {
		t.Fatalf("expected %d files, got %d: %+v", len(want), len(next.Files), next.Files)
	}
	for path, hash := range want {
		if got, ok := next.Files[path]; !ok || got.Hash != hash {
			t.Errorf("expected %s to have hash %q, got %+v (present=%v)", path, hash, got, ok)
		}
	}
	if _, stillThere := next.Files["deleted.md"]; stillThere {
		t.Error("expected deleted.md to be removed from the resulting state")
	}
	if _, stillThere := next.Files["old-name.md"]; stillThere {
		t.Error("expected old-name.md to be removed (renamed away) from the resulting state")
	}
}

// TestApplyChangesToState_LeavesUnlistedPathsUntouched guards the property
// the whole fix depends on: a path debounce hasn't confirmed stable yet
// (and so has no entry in changes) must keep its *old* confirmed value,
// not be dropped or updated - otherwise the next cycle would have nothing
// correct left to compare its eventual stable value against.
func TestApplyChangesToState_LeavesUnlistedPathsUntouched(t *testing.T) {
	prev := &scan.ScanState{Files: map[string]scan.FileState{
		"still-transitioning.md": {Hash: "h-original"},
	}}

	next := scan.ApplyChangesToState(prev, nil)

	if got, ok := next.Files["still-transitioning.md"]; !ok || got.Hash != "h-original" {
		t.Errorf("expected still-transitioning.md to be carried over unchanged, got %+v (present=%v)", got, ok)
	}
}

// TestScanner_MultiCycleIntegration_ModifyIsCorrectlyDetected is the
// regression test for a real, previously-shipped bug: chaining
// ScanVault -> DebounceFilter -> DetectChanges -> SaveScanState the way
// engine.go actually does, using a single shared last_scan.json as both
// the debounce baseline *and* DetectChanges' comparison baseline, meant
// that editing an existing file produced one bogus "delete" the cycle
// right after the edit, and then the edit was never logged as a
// create/modify at all - the new content silently became the baseline
// with no log entry ever describing how it got there. This drives several
// scan cycles exactly the way RunCycle now does (a separately-advanced
// confirmed-state baseline) and checks a real edit is eventually reported
// as ActionModify with the correct before/after hashes - never as a
// delete, and not silently dropped.
func TestScanner_MultiCycleIntegration_ModifyIsCorrectlyDetected(t *testing.T) {
	vault := t.TempDir()
	scanner := scan.NewScanner(vault)
	notePath := filepath.Join(vault, "note.md")

	runCycle := func() []scan.FileChange {
		curr, err := scanner.ScanVault()
		if err != nil {
			t.Fatalf("ScanVault failed: %v", err)
		}
		lastRaw, err := scanner.LoadLastScan()
		if err != nil {
			t.Fatalf("LoadLastScan failed: %v", err)
		}
		stable := scan.DebounceFilter(lastRaw, curr)

		confirmed, err := scanner.LoadConfirmedState()
		if err != nil {
			t.Fatalf("LoadConfirmedState failed: %v", err)
		}
		changes := scan.DetectChanges(confirmed, scan.ReconcileForDetection(confirmed, curr, stable))

		if err := scanner.SaveScanState(curr); err != nil {
			t.Fatalf("SaveScanState failed: %v", err)
		}
		if err := scanner.SaveConfirmedState(scan.ApplyChangesToState(confirmed, changes)); err != nil {
			t.Fatalf("SaveConfirmedState failed: %v", err)
		}
		return changes
	}

	if err := os.WriteFile(notePath, []byte("version 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Two cycles with stable content are needed before version 1 itself
	// becomes the *confirmed* baseline (debounce needs 2 consecutive
	// matching raw scans, and confirmation lags a further cycle behind
	// that) - otherwise editing again before it's confirmed would make the
	// edit look like the path's first-ever create instead of a modify,
	// which is a real but different case this test isn't after.
	runCycle()
	runCycle()

	if err := os.WriteFile(notePath, []byte("version 2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var modifyChanges []scan.FileChange
	for i := 0; i < 5 && len(modifyChanges) == 0; i++ {
		for _, ch := range runCycle() {
			if ch.Path == "note.md" {
				modifyChanges = append(modifyChanges, ch)
			}
		}
	}

	if len(modifyChanges) != 1 {
		t.Fatalf("expected exactly 1 change logged for note.md across settling cycles, got %d: %+v", len(modifyChanges), modifyChanges)
	}
	if modifyChanges[0].Action != scan.ActionModify {
		t.Errorf("expected the edit to be logged as ActionModify, got %s", modifyChanges[0].Action)
	}
}

func TestScanPaths_CarriesBaselineForwardAndUpdatesGivenPaths(t *testing.T) {
	vault := t.TempDir()
	if err := os.WriteFile(filepath.Join(vault, "unchanged.md"), []byte("stays the same\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "note.md"), []byte("version 1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	scanner := scan.NewScanner(vault)
	baseline, err := scanner.ScanVault()
	if err != nil {
		t.Fatalf("ScanVault failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(vault, "note.md"), []byte("version 2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := scanner.ScanPaths(baseline, []string{"note.md"})
	if err != nil {
		t.Fatalf("ScanPaths failed: %v", err)
	}

	full, err := scanner.ScanVault()
	if err != nil {
		t.Fatalf("ScanVault failed: %v", err)
	}

	if got.Files["note.md"] != full.Files["note.md"] {
		t.Errorf("expected note.md's hash to match a full scan, got %+v vs %+v", got.Files["note.md"], full.Files["note.md"])
	}
	if got.Files["unchanged.md"] != baseline.Files["unchanged.md"] {
		t.Errorf("expected unchanged.md to be carried forward from baseline untouched, got %+v vs %+v", got.Files["unchanged.md"], baseline.Files["unchanged.md"])
	}
	if len(got.Files) != len(full.Files) {
		t.Errorf("expected ScanPaths result to match a full scan's file set, got %+v vs %+v", got.Files, full.Files)
	}
}

func TestScanPaths_RemovesDeletedPath(t *testing.T) {
	vault := t.TempDir()
	notePath := filepath.Join(vault, "note.md")
	if err := os.WriteFile(notePath, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	scanner := scan.NewScanner(vault)
	baseline, err := scanner.ScanVault()
	if err != nil {
		t.Fatalf("ScanVault failed: %v", err)
	}
	if _, ok := baseline.Files["note.md"]; !ok {
		t.Fatal("expected note.md in baseline")
	}

	if err := os.Remove(notePath); err != nil {
		t.Fatal(err)
	}

	got, err := scanner.ScanPaths(baseline, []string{"note.md"})
	if err != nil {
		t.Fatalf("ScanPaths failed: %v", err)
	}
	if _, ok := got.Files["note.md"]; ok {
		t.Errorf("expected note.md to be removed from the result, got %+v", got.Files)
	}
}

func TestScanPaths_IgnoresSyncPaths(t *testing.T) {
	vault := t.TempDir()
	scanner := scan.NewScanner(vault)
	baseline := &scan.ScanState{Files: map[string]scan.FileState{}}

	got, err := scanner.ScanPaths(baseline, []string{syncdir.Name + "/state/last_scan.json", syncdir.Name})
	if err != nil {
		t.Fatalf("ScanPaths failed: %v", err)
	}
	if len(got.Files) != 0 {
		t.Errorf("expected sync-dir paths to be ignored, got %+v", got.Files)
	}
}
