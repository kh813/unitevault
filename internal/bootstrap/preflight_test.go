package bootstrap_test

import (
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
