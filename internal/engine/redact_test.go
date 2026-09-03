package engine

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/kh813/unitevault/internal/config"
	"github.com/kh813/unitevault/internal/syncdir"
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

// TestRedactErrPath guards a real bug caught by
// engine_test.go's TestSyncEngine_RunCycle_RedactsFilenameInApplyErrorByDefault:
// redacting the %s placeholder next to a %w-wrapped os.* error isn't
// enough on its own - *fs.PathError (what os.Remove/ReadFile/WriteFile/
// MkdirAll all return on failure) embeds the real absolute path in its own
// Error() string, so the wrapped error must be redacted too.
func TestRedactErrPath(t *testing.T) {
	pathErr := &fs.PathError{Op: "remove", Path: "/Users/alice/Vault/secret-plan.md", Err: errors.New("directory not empty")}

	t.Run("flag off redacts the embedded path but keeps the reason", func(t *testing.T) {
		got := redactErrPath(&config.Config{LogIncludeFilenames: false}, pathErr)
		if strings.Contains(got.Error(), "secret-plan.md") || strings.Contains(got.Error(), "alice") {
			t.Errorf("expected the real path to be redacted, got %q", got.Error())
		}
		if !strings.Contains(got.Error(), "directory not empty") {
			t.Errorf("expected the underlying reason to survive redaction, got %q", got.Error())
		}
	})

	t.Run("flag on leaves the error untouched", func(t *testing.T) {
		got := redactErrPath(&config.Config{LogIncludeFilenames: true}, pathErr)
		if got != error(pathErr) {
			t.Errorf("expected the original error unchanged, got %v", got)
		}
	})

	t.Run("nil error stays nil", func(t *testing.T) {
		if got := redactErrPath(nil, nil); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("non-PathError is left as-is", func(t *testing.T) {
		plain := errors.New("some other failure")
		if got := redactErrPath(nil, plain); got != plain {
			t.Errorf("expected a non-PathError to pass through unchanged, got %v", got)
		}
	})
}

// TestDefaultEngineLogPath_NeverInsideVault guards a real, previously-
// shipped bug: NewSyncEngine's default driveRunner once logged to a path
// under vaultPath/.sync, which SyncModeDrive's Primary publish doesn't
// fully exclude - so this device's own local rclone log (local absolute
// paths, rclone remote name) ended up synced to Google Drive and pulled
// onto every other paired device. defaultEngineLogPath must always resolve
// outside the Vault, regardless of vaultPath.
func TestDefaultEngineLogPath_NeverInsideVault(t *testing.T) {
	vaultPath := "/Users/someone/Documents/MyVault"
	got := defaultEngineLogPath(vaultPath)

	if strings.HasPrefix(got, vaultPath) {
		t.Errorf("expected the engine.log path to live outside the Vault, got %q under vault %q", got, vaultPath)
	}
	if strings.Contains(got, syncdir.Name) {
		t.Errorf("expected the engine.log path to never reference the .sync bookkeeping dir, got %q", got)
	}
}
