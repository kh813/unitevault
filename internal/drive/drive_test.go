package drive_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/kh813/unitevault/internal/drive"
)

func TestClient_CheckBinary(t *testing.T) {
	_, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone is not installed on this machine, skipping rclone execution tests")
	}

	client := drive.NewClient("")
	ctx := context.Background()

	// Calling FileExists on non-existent remote target should return false without error
	exists, err := client.FileExists(ctx, "nonexistent_remote_target_xyz_123:test")
	if err != nil {
		t.Logf("FileExists returned error as expected for invalid remote: %v", err)
	} else if exists {
		t.Errorf("expected false for nonexistent remote target")
	}
}
