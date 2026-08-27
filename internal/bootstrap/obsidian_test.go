package bootstrap_test

import (
	"context"
	"testing"
	"time"

	"github.com/kh813/unitevault/internal/bootstrap"
)

// TestQuitObsidian_ReturnsPromptlyWhenNotRunning guards against QuitObsidian
// hanging (or otherwise misbehaving) when Obsidian isn't running - the
// common case, and always true in CI, where Obsidian is never installed.
// Exercises the real process-check path rather than a mock, since
// QuitObsidian has no injectable seam - a deliberate design choice
// (Vault Migration, spec 1.6.7) given it must never fail or block the
// caller on Obsidian's own behavior.
func TestQuitObsidian_ReturnsPromptlyWhenNotRunning(t *testing.T) {
	done := make(chan struct{})
	go func() {
		bootstrap.QuitObsidian(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("QuitObsidian did not return promptly when Obsidian isn't running")
	}
}
