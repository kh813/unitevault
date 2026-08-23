package engine_test

import (
	"context"
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
