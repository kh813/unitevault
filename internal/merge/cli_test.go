package merge_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kh813/unitevault/internal/merge"
)

func TestConflictResolver(t *testing.T) {
	input := "1\n"
	reader := strings.NewReader(input)
	var writer bytes.Buffer

	resolver := merge.NewConflictResolver(reader, &writer)
	deviceLabels := map[string]string{
		"dev1": "mac-mini",
	}

	result, err := resolver.ResolveInteractive("doc.md", "<<<<<<<\nconflict\n>>>>>>>", deviceLabels)
	if err != nil {
		t.Fatalf("ResolveInteractive error: %v", err)
	}

	if result != "SELECTED_DEVICE:dev1" {
		t.Fatalf("expected SELECTED_DEVICE:dev1, got %s", result)
	}
}
