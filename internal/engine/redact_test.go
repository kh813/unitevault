package engine

import (
	"testing"

	"github.com/kh813/unitevault/internal/config"
)

// TestRedactRelPath guards a real user request: by default, diagnostic
// merge/apply error messages must not reveal a note's actual name/path -
// only its file extension, useful for bug-report triage ("is this always
// a .md file?") - unless the user has explicitly opted in via Settings >
// Advanced Options > "Include Filenames in Logs"
// (config.Config.LogIncludeFilenames).
func TestRedactRelPath(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *config.Config
		relPath string
		want    string
	}{
		{"nil cfg redacts", nil, "Notes/secret plan.md", "<redacted.md>"},
		{"flag off redacts, keeps extension", &config.Config{LogIncludeFilenames: false}, "Notes/secret plan.md", "<redacted.md>"},
		{"flag on reveals the full relative path", &config.Config{LogIncludeFilenames: true}, "Notes/secret plan.md", "Notes/secret plan.md"},
		{"flag off, no extension", &config.Config{}, "Notes/README", "<redacted>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := redactRelPath(c.cfg, c.relPath); got != c.want {
				t.Errorf("redactRelPath(%+v, %q) = %q, want %q", c.cfg, c.relPath, got, c.want)
			}
		})
	}
}
