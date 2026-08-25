package bootstrap_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kh813/unitevault/internal/bootstrap"
	"github.com/kh813/unitevault/internal/config"
)

type mockDrive struct {
	remoteFiles map[string][]byte
	copyCalled  bool
}

func newMockDrive() *mockDrive {
	return &mockDrive{remoteFiles: make(map[string][]byte)}
}

func (m *mockDrive) Sync(ctx context.Context, srcPath, remoteTarget string) error {
	return nil
}

func (m *mockDrive) Copy(ctx context.Context, remoteSrc, dstPath string) error {
	m.copyCalled = true
	return nil
}

func (m *mockDrive) FileExists(ctx context.Context, remoteTargetFile string) (bool, error) {
	_, ok := m.remoteFiles[remoteTargetFile]
	return ok, nil
}

func (m *mockDrive) DownloadFile(ctx context.Context, remoteSourceFile, localDstFile string) error {
	data, ok := m.remoteFiles[remoteSourceFile]
	if !ok {
		return fmt.Errorf("file not found: %s", remoteSourceFile)
	}
	return os.WriteFile(localDstFile, data, 0644)
}

func (m *mockDrive) UploadFile(ctx context.Context, localSrcFile, remoteTargetFile string) error {
	data, err := os.ReadFile(localSrcFile)
	if err != nil {
		return err
	}
	m.remoteFiles[remoteTargetFile] = data
	return nil
}

func TestBootstrap_PrimaryInitialization(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgDir := filepath.Join(tempDir, "config")

	cfgMgr := config.NewConfigManagerWithDir(cfgDir)
	mock := newMockDrive()

	bootstrapper := bootstrap.NewBootstrapper(cfgMgr, mock)
	ctx := context.Background()

	role, err := bootstrapper.InitializeNode(ctx, vaultPath, "gdrive:Backup", "mac-test")
	if err != nil {
		t.Fatalf("expected no error initializing node, got %v", err)
	}
	if role != "primary" {
		t.Fatalf("expected role primary, got %s", role)
	}

	// Verify local marker exists
	localMarkerPath := filepath.Join(vaultPath, "_sync", "PRIMARY_MARKER.json")
	if _, err := os.Stat(localMarkerPath); os.IsNotExist(err) {
		t.Fatalf("expected local PRIMARY_MARKER.json to exist")
	}

	// Verify remote marker exists in mock drive
	remoteMarkerFile := "gdrive:Backup/_sync/PRIMARY_MARKER.json"
	if _, ok := mock.remoteFiles[remoteMarkerFile]; !ok {
		t.Fatalf("expected remote PRIMARY_MARKER.json in mock drive")
	}

	// Verify role cached as primary
	cachedRole, err := cfgMgr.LoadRole()
	if err != nil || cachedRole != "primary" {
		t.Fatalf("expected cached role primary, got %s", cachedRole)
	}
}

func TestBootstrap_SecondaryInitialization(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgDir := filepath.Join(tempDir, "config")

	cfgMgr := config.NewConfigManagerWithDir(cfgDir)
	mock := newMockDrive()

	// Simulate existing primary marker on remote drive
	remoteMarkerFile := "gdrive:Backup/_sync/PRIMARY_MARKER.json"
	existingMarker := bootstrap.PrimaryMarker{
		SchemaVersion:   1,
		PrimaryDeviceID: "other-primary-uuid",
		PrimaryLabel:    "other-mac",
	}
	markerBytes, _ := json.Marshal(existingMarker)
	mock.remoteFiles[remoteMarkerFile] = markerBytes

	bootstrapper := bootstrap.NewBootstrapper(cfgMgr, mock)
	ctx := context.Background()

	role, err := bootstrapper.InitializeNode(ctx, vaultPath, "gdrive:Backup", "win-secondary")
	if err != nil {
		t.Fatalf("expected no error initializing secondary node, got %v", err)
	}
	if role != "secondary" {
		t.Fatalf("expected role secondary, got %s", role)
	}

	deviceID, _ := cfgMgr.GetDeviceID()
	localLogPath := filepath.Join(vaultPath, "_sync", fmt.Sprintf("log-%s.jsonl", deviceID))
	if _, err := os.Stat(localLogPath); os.IsNotExist(err) {
		t.Fatalf("expected local empty device log file to exist at %s", localLogPath)
	}

	cachedRole, _ := cfgMgr.LoadRole()
	if cachedRole != "secondary" {
		t.Fatalf("expected cached role secondary, got %s", cachedRole)
	}
}

// TestBootstrap_SecondaryInitialization_DoesNotCopyFromDrive guards against
// a real reported bug: a Secondary device's Vault folder already has the
// current content via iCloud Drive by the time this runs (see spec 1.3 -
// iCloud, not rclone, distributes Vault content between devices), and its
// own sync cycle never reads other devices' logs either (only Primary's
// merge phase does). An earlier version still did an unconditional
// `rclone copy` from Google Drive here "just in case", which meant writing
// Google Drive's backup on top of a folder iCloud had already populated -
// on Windows this surfaced as file conflicts during Secondary setup. A
// pre-existing local file must survive Secondary initialization untouched.
func TestBootstrap_SecondaryInitialization_DoesNotCopyFromDrive(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgDir := filepath.Join(tempDir, "config")

	// Simulate iCloud having already populated the Vault folder on this
	// device before UniteVault ever runs.
	if err := os.MkdirAll(vaultPath, 0755); err != nil {
		t.Fatalf("failed to create vault dir: %v", err)
	}
	preExistingContent := []byte("already synced via iCloud")
	preExistingFile := filepath.Join(vaultPath, "Note.md")
	if err := os.WriteFile(preExistingFile, preExistingContent, 0644); err != nil {
		t.Fatalf("failed to seed pre-existing vault file: %v", err)
	}

	cfgMgr := config.NewConfigManagerWithDir(cfgDir)
	mock := newMockDrive()

	remoteMarkerFile := "gdrive:Backup/_sync/PRIMARY_MARKER.json"
	existingMarker := bootstrap.PrimaryMarker{SchemaVersion: 1, PrimaryDeviceID: "other-primary-uuid", PrimaryLabel: "other-mac"}
	markerBytes, _ := json.Marshal(existingMarker)
	mock.remoteFiles[remoteMarkerFile] = markerBytes

	bootstrapper := bootstrap.NewBootstrapper(cfgMgr, mock)
	if _, err := bootstrapper.InitializeNode(context.Background(), vaultPath, "gdrive:Backup", "win-secondary"); err != nil {
		t.Fatalf("expected no error initializing secondary node, got %v", err)
	}

	if mock.copyCalled {
		t.Error("expected Secondary initialization to never call drive.Copy - content distribution is iCloud's job")
	}

	got, err := os.ReadFile(preExistingFile)
	if err != nil {
		t.Fatalf("expected pre-existing vault file to survive, got error reading it: %v", err)
	}
	if string(got) != string(preExistingContent) {
		t.Errorf("expected pre-existing vault file content to be untouched, got %q", got)
	}
}

// TestBootstrap_PrimaryReinitializationPreservesPrimaryRole guards against a bug where
// resetting local config and re-saving on a Primary device erroneously demoted the device
// to Secondary because PRIMARY_MARKER.json already existed on Google Drive.
func TestBootstrap_PrimaryReinitializationPreservesPrimaryRole(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgDir := filepath.Join(tempDir, "config")

	cfgMgr := config.NewConfigManagerWithDir(cfgDir)
	deviceID, _ := cfgMgr.GetDeviceID()
	mock := newMockDrive()

	// Remote marker exists and belongs to THIS device
	remoteMarkerFile := "gdrive:Backup/_sync/PRIMARY_MARKER.json"
	existingMarker := bootstrap.PrimaryMarker{
		SchemaVersion:   1,
		PrimaryDeviceID: deviceID,
		PrimaryLabel:    "my-mac",
	}
	markerBytes, _ := json.Marshal(existingMarker)
	mock.remoteFiles[remoteMarkerFile] = markerBytes

	bootstrapper := bootstrap.NewBootstrapper(cfgMgr, mock)
	ctx := context.Background()

	role, err := bootstrapper.InitializeNode(ctx, vaultPath, "gdrive:Backup", "my-mac")
	if err != nil {
		t.Fatalf("expected no error re-initializing primary node, got %v", err)
	}
	if role != "primary" {
		t.Fatalf("expected role 'primary', got %s", role)
	}

	cachedRole, _ := cfgMgr.LoadRole()
	if cachedRole != "primary" {
		t.Fatalf("expected cached role 'primary', got %s", cachedRole)
	}
}

