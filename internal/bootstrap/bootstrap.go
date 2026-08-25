package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kh813/unitevault/internal/config"
	"github.com/kh813/unitevault/internal/drive"
)

const AppVersion = "0.0.35"
const PrimaryMarkerRelPath = "_sync/PRIMARY_MARKER.json"

// PrimaryMarker represents the PRIMARY_MARKER.json structure specified in spec section 3.6.1.1
type PrimaryMarker struct {
	SchemaVersion   int    `json:"schema_version"`
	PrimaryDeviceID string `json:"primary_device_id"`
	PrimaryLabel    string `json:"primary_label"`
	InitializedAt   string `json:"initialized_at"`
	VaultRootHash   string `json:"vault_root_hash"`
	AppVersion      string `json:"app_version"`
}

// Bootstrapper handles primary/secondary role auto-detection and initialization
type Bootstrapper struct {
	cfgMgr *config.ConfigManager
	drive  drive.RcloneRunner
}

// NewBootstrapper creates a new Bootstrapper instance
func NewBootstrapper(cfgMgr *config.ConfigManager, runner drive.RcloneRunner) *Bootstrapper {
	return &Bootstrapper{
		cfgMgr: cfgMgr,
		drive:  runner,
	}
}

// InitializeNode checks Google Drive for PRIMARY_MARKER.json and sets up primary or secondary role.
func (b *Bootstrapper) InitializeNode(ctx context.Context, vaultPath, remoteTarget, label string) (string, error) {
	deviceID, err := b.cfgMgr.GetDeviceID()
	if err != nil {
		return "", fmt.Errorf("failed to get device ID: %w", err)
	}

	remoteMarkerFile := fmt.Sprintf("%s/%s", remoteTarget, PrimaryMarkerRelPath)
	exists, err := b.drive.FileExists(ctx, remoteMarkerFile)
	if err != nil {
		return "", fmt.Errorf("failed to check remote primary marker: %w", err)
	}

	if !exists {
		// Initialize as Primary
		return "primary", b.initAsPrimary(ctx, vaultPath, remoteTarget, deviceID, label)
	}

	// Initialize as Secondary
	return "secondary", b.initAsSecondary(vaultPath, deviceID)
}

func (b *Bootstrapper) initAsPrimary(ctx context.Context, vaultPath, remoteTarget, deviceID, label string) error {
	marker := PrimaryMarker{
		SchemaVersion:   1,
		PrimaryDeviceID: deviceID,
		PrimaryLabel:    label,
		InitializedAt:   time.Now().Format(time.RFC3339),
		VaultRootHash:   "", // Optional initial hash
		AppVersion:      AppVersion,
	}

	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal primary marker: %w", err)
	}

	// 6.3: Save local copy in Vault/_sync/PRIMARY_MARKER.json
	localMarkerPath := filepath.Join(vaultPath, "_sync", "PRIMARY_MARKER.json")
	if err := os.MkdirAll(filepath.Dir(localMarkerPath), 0755); err != nil {
		return fmt.Errorf("failed to create local _sync dir: %w", err)
	}
	if err := os.WriteFile(localMarkerPath, data, 0644); err != nil {
		return fmt.Errorf("failed to save local primary marker: %w", err)
	}

	// Upload marker to Google Drive
	remoteMarkerFile := fmt.Sprintf("%s/%s", remoteTarget, PrimaryMarkerRelPath)
	if err := b.drive.UploadFile(ctx, localMarkerPath, remoteMarkerFile); err != nil {
		return fmt.Errorf("failed to upload primary marker: %w", err)
	}

	// Verification step: download back and verify device ID to prevent race conditions
	tempVerifyFile := filepath.Join(os.TempDir(), fmt.Sprintf("verify_marker_%d.json", time.Now().UnixNano()))
	defer os.Remove(tempVerifyFile)

	if err := b.drive.DownloadFile(ctx, remoteMarkerFile, tempVerifyFile); err != nil {
		return fmt.Errorf("failed to download marker for verification: %w", err)
	}

	vData, err := os.ReadFile(tempVerifyFile)
	if err != nil {
		return fmt.Errorf("failed to read downloaded verification marker: %w", err)
	}

	var downloadedMarker PrimaryMarker
	if err := json.Unmarshal(vData, &downloadedMarker); err != nil {
		return fmt.Errorf("failed to parse verification marker: %w", err)
	}

	if downloadedMarker.PrimaryDeviceID != deviceID {
		// Race condition detected! Another node became primary first. Convert to secondary.
		_ = b.cfgMgr.SaveRole("secondary")
		return b.initAsSecondary(vaultPath, deviceID)
	}

	if err := b.cfgMgr.SaveRole("primary"); err != nil {
		return err
	}

	return nil
}

// initAsSecondary does *not* pull the Vault or other devices' logs from
// Google Drive: per spec 1.3, device-to-device content distribution is
// iCloud Drive's job, not rclone's - a Secondary's Vault folder already has
// the current content via iCloud by the time this runs, and RunCycle's
// Secondary path never reads other devices' logs anyway (only Primary does,
// during its merge phase). An earlier version did an unconditional
// `rclone copy` here "to be safe", but that meant copying Google Drive's
// backup on top of a folder iCloud had already populated - a redundant
// transfer that could actively conflict with it (e.g. mid-download iCloud
// placeholder files colliding with the incoming copy) instead of ever being
// needed for anything this device's own sync cycle actually uses.
func (b *Bootstrapper) initAsSecondary(vaultPath, deviceID string) error {
	// Create empty log file for this device if not exists
	localLogPath := filepath.Join(vaultPath, "_sync", fmt.Sprintf("log-%s.jsonl", deviceID))
	if err := os.MkdirAll(filepath.Dir(localLogPath), 0755); err != nil {
		return fmt.Errorf("failed to create _sync dir: %w", err)
	}

	if _, err := os.Stat(localLogPath); os.IsNotExist(err) {
		if err := os.WriteFile(localLogPath, []byte{}, 0644); err != nil {
			return fmt.Errorf("failed to create empty device log file: %w", err)
		}
	}

	return b.cfgMgr.SaveRole("secondary")
}

// PromoteToPrimary allows manual transition to primary role by clearing remote marker and re-initializing
func (b *Bootstrapper) PromoteToPrimary(ctx context.Context, vaultPath, remoteTarget, label string) error {
	deviceID, err := b.cfgMgr.GetDeviceID()
	if err != nil {
		return err
	}
	return b.initAsPrimary(ctx, vaultPath, remoteTarget, deviceID, label)
}
