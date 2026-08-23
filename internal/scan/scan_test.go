package scan_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/kh813/unitevault/internal/scan"
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
