package drive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRotateLogIfOversized guards a real risk: engine.log grows without
// bound during an extended rclone outage (every failed retry appends
// another entry), so once it exceeds maxLogFileSize the oldest half must
// be dropped, cutting at a line boundary so the kept tail is never a
// truncated mid-entry.
func TestRotateLogIfOversized(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "engine.log")

	var sb strings.Builder
	line := strings.Repeat("x", 100) + "\n"
	for sb.Len() < maxLogFileSize+1000 {
		sb.WriteString(line)
	}
	sb.WriteString("LAST-LINE-MARKER\n")
	if err := os.WriteFile(logPath, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("failed to seed log file: %v", err)
	}

	rotateLogIfOversized(logPath)

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read rotated log: %v", err)
	}
	if len(got) >= maxLogFileSize {
		t.Errorf("expected rotation to shrink the log well under %d bytes, got %d", maxLogFileSize, len(got))
	}
	if !strings.Contains(string(got), "LAST-LINE-MARKER") {
		t.Error("expected the most recent content to survive rotation")
	}
	if !strings.Contains(string(got), "truncated") {
		t.Error("expected a truncation notice to be written")
	}
	// Every kept line must be either the full filler line or the final
	// marker line - if the cut point had landed mid-entry, at least one
	// line would come out shorter than the filler line's full length.
	for i, l := range strings.Split(strings.TrimRight(string(got), "\n"), "\n")[1:] {
		if l != strings.Repeat("x", 100) && l != "LAST-LINE-MARKER" {
			t.Errorf("line %d looks like a mid-entry cut: %q", i, l)
		}
	}
}

func TestRotateLogIfOversized_NoOpUnderCap(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "engine.log")
	want := "small log content\n"
	if err := os.WriteFile(logPath, []byte(want), 0644); err != nil {
		t.Fatalf("failed to seed log file: %v", err)
	}

	rotateLogIfOversized(logPath)

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	if string(got) != want {
		t.Errorf("expected an under-cap log to be left untouched, got %q", got)
	}
}
