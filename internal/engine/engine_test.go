package engine_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kh813/unitevault/internal/config"
	"github.com/kh813/unitevault/internal/engine"
)

type mockDriveRunner struct{}

func (m *mockDriveRunner) Sync(ctx context.Context, srcPath, remoteTarget string) error {
	return nil
}
func (m *mockDriveRunner) Copy(ctx context.Context, remoteSrc, dstPath string) error {
	return nil
}
func (m *mockDriveRunner) FileExists(ctx context.Context, remoteTargetFile string) (bool, error) {
	return true, nil
}
func (m *mockDriveRunner) DownloadFile(ctx context.Context, remoteSourceFile, localDstFile string) error {
	return nil
}
func (m *mockDriveRunner) UploadFile(ctx context.Context, localSrcFile, remoteTargetFile string) error {
	return nil
}

func TestSyncEngine_RunCycle(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgDir := filepath.Join(tempDir, "config")

	cfgMgr := config.NewConfigManagerWithDir(cfgDir)
	_ = cfgMgr.SaveRole("primary")

	eng := engine.NewSyncEngine(cfgMgr, vaultPath, "mac-test", &mockDriveRunner{})
	ctx := context.Background()

	if err := eng.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle failed: %v", err)
	}
}

// failingDriveRunner is mockDriveRunner with Sync forced to fail, for
// exercising RunCycle's drive-sync-failure path.
type failingDriveRunner struct {
	mockDriveRunner
}

func (m *failingDriveRunner) Sync(ctx context.Context, srcPath, remoteTarget string) error {
	return errors.New("network error")
}

// TestSyncEngine_RunCycle_RecordsDriveSyncStatusOnSuccess guards that a
// Primary device's successful rclone sync is persisted (via
// config.DriveSyncStatus) so the Settings window's "Google Drive sync" row
// can show it without needing a live connection to the running daemon loop.
func TestSyncEngine_RunCycle_RecordsDriveSyncStatusOnSuccess(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgDir := filepath.Join(tempDir, "config")

	cfgMgr := config.NewConfigManagerWithDir(cfgDir)
	_ = cfgMgr.SaveRole("primary")
	_ = cfgMgr.SaveConfig(&config.Config{
		VaultPath:       vaultPath,
		RcloneRemote:    "ObsidianVault",
		RclonePath:      "MyVault",
		IntervalSeconds: 120,
	})

	eng := engine.NewSyncEngine(cfgMgr, vaultPath, "mac-test", &mockDriveRunner{})
	if err := eng.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle failed: %v", err)
	}

	status, err := cfgMgr.LoadDriveSyncStatus()
	if err != nil {
		t.Fatalf("LoadDriveSyncStatus failed: %v", err)
	}
	if status == nil || !status.Success {
		t.Fatalf("expected a recorded successful drive sync status, got %+v", status)
	}
}

// TestSyncEngine_RunCycle_RecordsDriveSyncStatusOnFailure is the failure
// counterpart: RunCycle must still surface the error to its caller *and*
// persist it, so the Settings window can show "Last sync failed: ...".
func TestSyncEngine_RunCycle_RecordsDriveSyncStatusOnFailure(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgDir := filepath.Join(tempDir, "config")

	cfgMgr := config.NewConfigManagerWithDir(cfgDir)
	_ = cfgMgr.SaveRole("primary")
	_ = cfgMgr.SaveConfig(&config.Config{
		VaultPath:       vaultPath,
		RcloneRemote:    "ObsidianVault",
		RclonePath:      "MyVault",
		IntervalSeconds: 120,
	})

	eng := engine.NewSyncEngine(cfgMgr, vaultPath, "mac-test", &failingDriveRunner{})
	if err := eng.RunCycle(context.Background()); err == nil {
		t.Fatal("expected RunCycle to surface the rclone sync error")
	}

	status, err := cfgMgr.LoadDriveSyncStatus()
	if err != nil {
		t.Fatalf("LoadDriveSyncStatus failed: %v", err)
	}
	if status == nil || status.Success || status.Error == "" {
		t.Fatalf("expected a recorded failed drive sync status with an error message, got %+v", status)
	}
}
