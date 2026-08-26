package bootstrap

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestWingetInstallErrorCodeHint_ShellExecFailed guards the specific,
// real-world winget exit code a user hit when "Install iCloud..." failed:
// APPINSTALLER_CLI_ERROR_SHELLEXEC_INSTALL_FAILED (0x8A150006), surfaced by
// Go on Windows as "exit status 0x8a150006" (os.ProcessState.String() hex-
// formats any Windows exit code >= 1<<16). This almost always means the
// classic "Apple.iCloud" winget package's elevation (UAC) prompt was
// dismissed or timed out before being approved - the hint must actually
// mention UAC/administrator so the user knows what to do differently, not
// just repeat the raw code back at them.
func TestWingetInstallErrorCodeHint_ShellExecFailed(t *testing.T) {
	hint := wingetInstallErrorCodeHint(0x8A150006)
	if hint == "" {
		t.Fatal("expected a non-empty hint for APPINSTALLER_CLI_ERROR_SHELLEXEC_INSTALL_FAILED (0x8A150006)")
	}
	if !strings.Contains(hint, "UAC") && !strings.Contains(hint, "administrator") {
		t.Errorf("expected the hint to mention UAC/administrator elevation, got: %q", hint)
	}
}

func TestWingetInstallErrorCodeHint_UnknownCodeIsEmpty(t *testing.T) {
	if hint := wingetInstallErrorCodeHint(1); hint != "" {
		t.Errorf("expected no hint for an unrecognized exit code, got: %q", hint)
	}
}

// TestWingetInstallErrorHint_ExtractsCodeFromExitError guards that the
// hint is actually reachable from a real command failure, not just from
// wingetInstallErrorCodeHint's own literal-int tests above - a plain
// (non-ExitError) failure (e.g. winget itself not found) must produce no
// hint rather than panicking or misbehaving.
func TestWingetInstallErrorHint_ExtractsCodeFromExitError(t *testing.T) {
	if hint := wingetInstallErrorHint(errors.New("some other failure")); hint != "" {
		t.Errorf("expected no hint for a non-ExitError, got: %q", hint)
	}

	// A real *exec.ExitError from a trivial failing command - its exit code
	// (small, platform-dependent) won't match 0x8A150006, so this only
	// guards that errors.As correctly unwraps an *exec.ExitError without
	// panicking, not the specific hint text.
	err := exec.Command("false").Run()
	if err == nil {
		t.Skip("expected `false` to exit non-zero on this platform")
	}
	_ = wingetInstallErrorHint(err)
}
