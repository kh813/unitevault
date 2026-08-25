package bootstrap_test

import (
	"runtime"
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
