package syncedlog_test

import (
	"path/filepath"
	"testing"

	"github.com/kh813/unitevault/internal/scan"
	"github.com/kh813/unitevault/internal/syncedlog"
)

func TestLogManager_AppendAndRead(t *testing.T) {
	vault := t.TempDir()
	lm := syncedlog.NewLogManager(vault)

	deviceID := "dev-uuid-1"
	label := "macbook"

	// Get initial seq
	seq, err := lm.GetNextSeq(deviceID)
	if err != nil {
		t.Fatalf("failed to get initial seq: %v", err)
	}
	if seq != 1 {
		t.Fatalf("expected initial seq 1, got %d", seq)
	}

	// Append entry 1
	change1 := scan.FileChange{
		Action:     scan.ActionCreate,
		Path:       "test.md",
		BaseHash:   "",
		ResultHash: "hash123",
	}
	entry1 := syncedlog.CreateLogEntryFromChange(deviceID, label, seq, change1, "diff1")
	if err := lm.AppendLogEntry(entry1); err != nil {
		t.Fatalf("failed to append entry 1: %v", err)
	}

	// Get next seq
	seq2, err := lm.GetNextSeq(deviceID)
	if err != nil {
		t.Fatalf("failed to get next seq: %v", err)
	}
	if seq2 != 2 {
		t.Fatalf("expected next seq 2, got %d", seq2)
	}

	// Append entry 2
	change2 := scan.FileChange{
		Action:     scan.ActionModify,
		Path:       "test.md",
		BaseHash:   "hash123",
		ResultHash: "hash456",
	}
	entry2 := syncedlog.CreateLogEntryFromChange(deviceID, label, seq2, change2, "diff2\r\nline2")
	if err := lm.AppendLogEntry(entry2); err != nil {
		t.Fatalf("failed to append entry 2: %v", err)
	}

	// Read device log
	entries, err := lm.ReadDeviceLog(deviceID)
	if err != nil {
		t.Fatalf("failed to read device log: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(entries))
	}
	if entries[1].Diff != "diff2\nline2" {
		t.Fatalf("expected normalized diff LF, got %q", entries[1].Diff)
	}
}

func TestLogManager_MultipleDevicesLatestEntry(t *testing.T) {
	vault := t.TempDir()
	lm := syncedlog.NewLogManager(vault)

	devA := "device-A"
	devB := "device-B"

	// Device A modifies doc.md
	entryA := syncedlog.LogEntry{
		Device:     devA,
		Label:      "mac-mini",
		Seq:        1,
		Path:       "doc.md",
		BaseHash:   "base1",
		ResultHash: "hashA",
		Action:     scan.ActionModify,
	}
	if err := lm.AppendLogEntry(entryA); err != nil {
		t.Fatal(err)
	}

	// Device B modifies doc.md
	entryB := syncedlog.LogEntry{
		Device:     devB,
		Label:      "win-pc",
		Seq:        1,
		Path:       "doc.md",
		BaseHash:   "base1",
		ResultHash: "hashB",
		Action:     scan.ActionModify,
	}
	if err := lm.AppendLogEntry(entryB); err != nil {
		t.Fatal(err)
	}

	// Read all logs and verify latest entry per path per device
	latestByPath, err := lm.LatestEntryByPath()
	if err != nil {
		t.Fatalf("LatestEntryByPath failed: %v", err)
	}

	docEntries, exists := latestByPath["doc.md"]
	if !exists {
		t.Fatalf("expected doc.md in latest map")
	}
	if len(docEntries) != 2 {
		t.Fatalf("expected 2 devices for doc.md, got %d", len(docEntries))
	}

	if docEntries[devA].ResultHash != "hashA" {
		t.Errorf("expected devA result hash 'hashA', got '%s'", docEntries[devA].ResultHash)
	}
	if docEntries[devB].ResultHash != "hashB" {
		t.Errorf("expected devB result hash 'hashB', got '%s'", docEntries[devB].ResultHash)
	}
}

func TestDeviceLogPath(t *testing.T) {
	vault := "/tmp/vault"
	lm := syncedlog.NewLogManager(vault)
	expected := filepath.Join("/tmp/vault", "_sync", "log-uuid123.jsonl")
	if lm.DeviceLogPath("uuid123") != expected {
		t.Fatalf("expected %s, got %s", expected, lm.DeviceLogPath("uuid123"))
	}
}
