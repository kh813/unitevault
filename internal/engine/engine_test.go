package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kh813/unitevault/internal/bootstrap"
	"github.com/kh813/unitevault/internal/config"
	"github.com/kh813/unitevault/internal/engine"
)

// mockDriveRunner is an in-memory drive.RcloneRunner: FileExists/
// DownloadFile/UploadFile/DeleteFile operate on a real map so tests can
// seed/inspect remote state (e.g. PRIMARY_MARKER.json,
// PRIMARY_CONFLICT.json) precisely, matching internal/bootstrap's own
// mockDrive test double.
type mockDriveRunner struct {
	remoteFiles map[string][]byte
	syncCalled  bool
}

func newMockDriveRunner() *mockDriveRunner {
	return &mockDriveRunner{remoteFiles: make(map[string][]byte)}
}

func (m *mockDriveRunner) Sync(ctx context.Context, srcPath, remoteTarget string) error {
	m.syncCalled = true
	return nil
}
func (m *mockDriveRunner) Copy(ctx context.Context, remoteSrc, dstPath string) error {
	return nil
}
func (m *mockDriveRunner) FileExists(ctx context.Context, remoteTargetFile string) (bool, error) {
	_, ok := m.remoteFiles[remoteTargetFile]
	return ok, nil
}
func (m *mockDriveRunner) DownloadFile(ctx context.Context, remoteSourceFile, localDstFile string) error {
	data, ok := m.remoteFiles[remoteSourceFile]
	if !ok {
		return fmt.Errorf("file not found: %s", remoteSourceFile)
	}
	return os.WriteFile(localDstFile, data, 0644)
}
func (m *mockDriveRunner) UploadFile(ctx context.Context, localSrcFile, remoteTargetFile string) error {
	data, err := os.ReadFile(localSrcFile)
	if err != nil {
		return err
	}
	m.remoteFiles[remoteTargetFile] = data
	return nil
}
func (m *mockDriveRunner) DeleteFile(ctx context.Context, remoteTargetFile string) error {
	delete(m.remoteFiles, remoteTargetFile)
	return nil
}

// seedPrimaryMarker writes a PRIMARY_MARKER.json directly into the mock's
// remote store, as if some device (possibly this one) had already
// initialized as Primary - used to set up RunCycle's per-cycle
// marker-reverification check (VerifyPrimaryStatus) without going through
// a full InitializeNode call.
func seedPrimaryMarker(t *testing.T, m *mockDriveRunner, remoteTarget, deviceID, label string) {
	t.Helper()
	marker := bootstrap.PrimaryMarker{
		SchemaVersion:   1,
		PrimaryDeviceID: deviceID,
		PrimaryLabel:    label,
	}
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatalf("failed to marshal seed marker: %v", err)
	}
	m.remoteFiles[remoteTarget+"/"+bootstrap.PrimaryMarkerRelPath] = data
}

func TestSyncEngine_RunCycle(t *testing.T) {
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
	deviceID, _ := cfgMgr.GetDeviceID()

	mock := newMockDriveRunner()
	seedPrimaryMarker(t, mock, "ObsidianVault:MyVault", deviceID, "mac-test")

	eng := engine.NewSyncEngine(cfgMgr, vaultPath, "mac-test", mock)
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
	deviceID, _ := cfgMgr.GetDeviceID()

	mock := newMockDriveRunner()
	seedPrimaryMarker(t, mock, "ObsidianVault:MyVault", deviceID, "mac-test")

	eng := engine.NewSyncEngine(cfgMgr, vaultPath, "mac-test", mock)
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
	deviceID, _ := cfgMgr.GetDeviceID()

	mock := &failingDriveRunner{mockDriveRunner: *newMockDriveRunner()}
	seedPrimaryMarker(t, &mock.mockDriveRunner, "ObsidianVault:MyVault", deviceID, "mac-test")

	eng := engine.NewSyncEngine(cfgMgr, vaultPath, "mac-test", mock)
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

// TestSyncEngine_RunCycle_SkipsSyncWhenSupersededByAnotherPrimary is the
// engine-level guard for the split-brain fix (spec 3.6.1.4): a device
// whose cached role is "primary" must not run merge/Google Drive sync once
// PRIMARY_MARKER.json names a different device, even though nothing else
// about its local state changed.
func TestSyncEngine_RunCycle_SkipsSyncWhenSupersededByAnotherPrimary(t *testing.T) {
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

	mock := newMockDriveRunner()
	seedPrimaryMarker(t, mock, "ObsidianVault:MyVault", "some-other-device-id", "windows-desktop")

	eng := engine.NewSyncEngine(cfgMgr, vaultPath, "mac-test", mock)
	if err := eng.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle failed: %v", err)
	}

	if mock.syncCalled {
		t.Error("expected RunCycle to skip rclone sync once superseded by another device's PRIMARY_MARKER.json")
	}

	status, err := cfgMgr.LoadDriveSyncStatus()
	if err != nil {
		t.Fatalf("LoadDriveSyncStatus failed: %v", err)
	}
	if status != nil {
		t.Errorf("expected no drive sync status to be recorded when the cycle was skipped, got %+v", status)
	}
}
