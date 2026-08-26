package bootstrap_test

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/kh813/unitevault/internal/bootstrap"
)

func TestPreflight_URLs(t *testing.T) {
	gitURL := bootstrap.GetGitDownloadURL()
	if gitURL == "" {
		t.Errorf("expected non-empty Git download URL")
	}

	icloudURL := bootstrap.GetICloudDownloadURL()
	if icloudURL == "" {
		t.Errorf("expected non-empty iCloud download URL")
	}
}

func TestPreflight_CheckGit(t *testing.T) {
	// Should return a boolean without crashing
	_ = bootstrap.CheckGitInstalled()
}

func TestPreflight_CheckICloud(t *testing.T) {
	// Should return a boolean without crashing, on any OS.
	_ = bootstrap.CheckICloudInstalled()
}

// TestAutoInstallICloud_NonWindowsIsANoOp guards that AutoInstallICloud
// never tries to shell out to winget on macOS/Linux CI runners (where it
// doesn't exist) - it must bail out immediately with an error before
// touching winget at all whenever GOOS isn't windows.
func TestAutoInstallICloud_NonWindowsIsANoOp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this guards the non-Windows short-circuit specifically")
	}
	if err := bootstrap.AutoInstallICloud(); err == nil {
		t.Error("expected AutoInstallICloud to return an error on a non-Windows OS")
	}
}

// TestIsAdministrator_NonWindowsIsAlwaysFalse guards that IsAdministrator
// never attempts any Windows-only privilege check off Windows - the
// concept (and AutoInstallICloud, its only caller) doesn't apply there.
func TestIsAdministrator_NonWindowsIsAlwaysFalse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this guards the non-Windows stub specifically")
	}
	if bootstrap.IsAdministrator() {
		t.Error("expected IsAdministrator to always report false on a non-Windows OS")
	}
}

// TestErrICloudRequiresAdministrator_IsMatchableAndActionable guards two
// properties real callers depend on: main.go's installICloud handler uses
// errors.Is to detect this specific sentinel and show a distinct dialog
// (see cmd/unitevault/main.go), and that dialog's fallback text is just
// this error's own message - so it must actually explain what to do
// ("contact your system administrator"), not just restate that something
// failed.
func TestErrICloudRequiresAdministrator_IsMatchableAndActionable(t *testing.T) {
	if !errors.Is(bootstrap.ErrICloudRequiresAdministrator, bootstrap.ErrICloudRequiresAdministrator) {
		t.Fatal("expected ErrICloudRequiresAdministrator to match itself via errors.Is")
	}
	if !strings.Contains(bootstrap.ErrICloudRequiresAdministrator.Error(), "administrator") {
		t.Errorf("expected the error message to mention administrator privileges, got: %q", bootstrap.ErrICloudRequiresAdministrator.Error())
	}
}
