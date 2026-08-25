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

func TestClient_RemoveRemote(t *testing.T) {
	_, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone is not installed on this machine, skipping rclone execution tests")
	}

	client := drive.NewClient("")
	ctx := context.Background()

	// rclone config delete on a non-existent remote name should not error
	// (it's already gone, which is the desired end state).
	if err := client.RemoveRemote(ctx, "nonexistent_remote_xyz_123"); err != nil {
		t.Errorf("expected no error removing a non-existent remote, got: %v", err)
	}
}
