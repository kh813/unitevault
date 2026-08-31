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
	"github.com/kh813/unitevault/internal/eventlog"
	"github.com/kh813/unitevault/internal/syncdir"
)

const AppVersion = "0.0.59"
const PrimaryMarkerRelPath = syncdir.Name + "/PRIMARY_MARKER.json"

// ConflictMarkerRelPath is the Google Drive path (not mirrored into the
// local Vault/.sync/ folder like PrimaryMarkerRelPath is - see
// VerifyPrimaryStatus) of the shared record filed when two devices
// disagree about who is Primary (spec section 3.6.1.4). Its mere presence
// pauses Google Drive sync on every device that notices it, whichever side
// of the disagreement they're on, until a human resolves it via
// PromoteToPrimary (Settings > "Promote to Primary...").
const ConflictMarkerRelPath = syncdir.Name + "/PRIMARY_CONFLICT.json"

// PrimaryConflictMarker is the schema of PRIMARY_CONFLICT.json.
type PrimaryConflictMarker struct {
	SchemaVersion          int    `json:"schema_version"`
	DetectedAt             string `json:"detected_at"`
	SupersededDeviceID     string `json:"superseded_device_id"`
	SupersededLabel        string `json:"superseded_label"`
	ClaimedPrimaryDeviceID string `json:"claimed_primary_device_id"`
	ClaimedPrimaryLabel    string `json:"claimed_primary_label"`
}

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
		if err := b.initAsPrimary(ctx, vaultPath, remoteTarget, deviceID, label); err != nil {
			return "", err
		}
		_ = eventlog.NewManager(vaultPath).Append(deviceID, label, eventlog.EventInitializedAsPrimary, nil)
		return "primary", nil
	}

	// Remote marker exists. Check if this device itself is already the
	// primary recorded in the marker. A failure at any step here (download,
	// read, or parse) must NOT silently fall through to Secondary
	// initialization: a transient network error must never look identical to
	// "another device is Primary" and demote a real Primary.
	tempVerifyFile := filepath.Join(os.TempDir(), fmt.Sprintf("check_marker_%d.json", time.Now().UnixNano()))
	defer os.Remove(tempVerifyFile)

	if err := b.drive.DownloadFile(ctx, remoteMarkerFile, tempVerifyFile); err != nil {
		return "", fmt.Errorf("failed to download primary marker for verification: %w", err)
	}

	vData, err := os.ReadFile(tempVerifyFile)
	if err != nil {
		return "", fmt.Errorf("failed to read downloaded primary marker: %w", err)
	}

	var downloadedMarker PrimaryMarker
	if err := json.Unmarshal(vData, &downloadedMarker); err != nil {
		return "", fmt.Errorf("failed to parse downloaded primary marker: %w", err)
	}

	if downloadedMarker.PrimaryDeviceID == deviceID {
		// This device is already the Primary! Restore/ensure primary role.
		if err := b.cfgMgr.SaveRole("primary"); err != nil {
			return "", err
		}
		return "primary", nil
	}

	// Initialize as Secondary
	if err := b.initAsSecondary(vaultPath, deviceID); err != nil {
		return "", err
	}
	_ = eventlog.NewManager(vaultPath).Append(deviceID, label, eventlog.EventInitializedAsSecondary, nil)
	return "secondary", nil
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

	// 6.3: Save local copy in Vault/.sync/PRIMARY_MARKER.json
	localMarkerPath := filepath.Join(vaultPath, syncdir.Name, "PRIMARY_MARKER.json")
	if err := os.MkdirAll(filepath.Dir(localMarkerPath), 0755); err != nil {
		return fmt.Errorf("failed to create local sync dir: %w", err)
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
	localLogPath := filepath.Join(vaultPath, syncdir.Name, fmt.Sprintf("log-%s.jsonl", deviceID))
	if err := os.MkdirAll(filepath.Dir(localLogPath), 0755); err != nil {
		return fmt.Errorf("failed to create sync dir: %w", err)
	}

	if _, err := os.Stat(localLogPath); os.IsNotExist(err) {
		if err := os.WriteFile(localLogPath, []byte{}, 0644); err != nil {
			return fmt.Errorf("failed to create empty device log file: %w", err)
		}
	}

	return b.cfgMgr.SaveRole("secondary")
}

// PromoteToPrimary allows manual transition to primary role, by writing a
// fresh PRIMARY_MARKER.json naming this device (which simply overwrites
// whatever the old one said - no separate delete-then-recreate step is
// needed, see unitevault-spec.md 3.6.1.2). Used both for a proactive
// promotion (Settings > "Promote to Primary...", e.g. because the old
// Primary is unreachable) and to resolve an active multi-Primary conflict
// in this device's favor (spec 3.6.1.4) - both are the same operation from
// this device's perspective, just with a PRIMARY_CONFLICT.json to also
// clear in the conflict case.
func (b *Bootstrapper) PromoteToPrimary(ctx context.Context, vaultPath, remoteTarget, label string) error {
	deviceID, err := b.cfgMgr.GetDeviceID()
	if err != nil {
		return err
	}

	conflictFile := fmt.Sprintf("%s/%s", remoteTarget, ConflictMarkerRelPath)
	hadConflict, err := b.drive.FileExists(ctx, conflictFile)
	if err != nil {
		return fmt.Errorf("failed to check for an existing primary conflict marker: %w", err)
	}

	if err := b.initAsPrimary(ctx, vaultPath, remoteTarget, deviceID, label); err != nil {
		return err
	}

	// initAsPrimary can itself lose a race to another device and convert
	// this one to Secondary instead (its own verify-after-upload step) -
	// that still returns a nil error, so the actual resulting role must be
	// checked before treating this as a successful promotion.
	actualRole, err := b.cfgMgr.LoadRole()
	if err != nil {
		return err
	}
	if actualRole != "primary" {
		return nil
	}

	events := eventlog.NewManager(vaultPath)
	if hadConflict {
		if err := b.drive.DeleteFile(ctx, conflictFile); err != nil {
			return fmt.Errorf("promoted to primary, but failed to clear the resolved conflict marker: %w", err)
		}
		_ = events.Append(deviceID, label, eventlog.EventConflictResolved, nil)
	}
	if err := b.cfgMgr.ClearPrimaryConflict(); err != nil {
		return err
	}
	_ = events.Append(deviceID, label, eventlog.EventPromotedToPrimary, nil)

	return nil
}

// VerifyPrimaryStatus re-confirms, once per sync cycle, that this device is
// still the Primary before merge + Google Drive sync run (spec 3.6.1.4).
// RunCycle's cached role is set once at InitializeNode time and never
// otherwise re-checked, so without this a device superseded by a
// "Promote to Primary" elsewhere would never notice and would keep
// merging + pushing conflicting rclone syncs indefinitely - a real
// split-brain risk this closes.
//
// Returns proceed=true only when it's safe to run this cycle's merge and
// Google Drive sync. A transient error (network, parse failure) returns
// proceed=false and a non-nil error, deliberately WITHOUT touching any
// local role/conflict state - by the same principle as InitializeNode
// (see its own doc comment): an inconclusive check must never look
// identical to "superseded" and demote a real Primary.
func (b *Bootstrapper) VerifyPrimaryStatus(ctx context.Context, vaultPath, remoteTarget, deviceID, label string) (proceed bool, err error) {
	marker, err := b.downloadMarker(ctx, remoteTarget)
	if err != nil {
		return false, err
	}

	conflictFile := fmt.Sprintf("%s/%s", remoteTarget, ConflictMarkerRelPath)
	conflictExists, err := b.drive.FileExists(ctx, conflictFile)
	if err != nil {
		return false, fmt.Errorf("failed to check for a primary conflict marker: %w", err)
	}

	events := eventlog.NewManager(vaultPath)

	if marker.PrimaryDeviceID == deviceID {
		// The marker still agrees this device is Primary.
		if !conflictExists {
			if err := b.cfgMgr.ClearPrimaryConflict(); err != nil {
				return false, err
			}
			return true, nil
		}

		// Another device disagrees. Pause - this device may yet be the one
		// a human Authorizes, so its cached role is left untouched, only
		// the conflict is (re-)recorded so Settings can show it. The
		// conflict marker's own content names the *other* (superseded)
		// device, if it can be read; a read failure here still leaves the
		// conflict correctly recorded, just without that detail for the
		// Settings message.
		conflictDetail, _ := b.downloadConflictMarker(ctx, remoteTarget)
		pc := config.PrimaryConflict{
			DetectedAt: time.Now().Format(time.RFC3339),
			Role:       config.ConflictRoleClaimed,
		}
		if conflictDetail != nil {
			pc.OtherDeviceID = conflictDetail.SupersededDeviceID
			pc.OtherLabel = conflictDetail.SupersededLabel
		}
		if err := b.cfgMgr.SavePrimaryConflict(pc); err != nil {
			return false, err
		}
		return false, nil
	}

	// The marker names a different device: this device has been superseded.
	cached, err := b.cfgMgr.LoadPrimaryConflict()
	if err != nil {
		return false, err
	}

	if cached == nil {
		// Newly discovered - file (or adopt an already-filed) conflict
		// record and stop.
		if !conflictExists {
			cm := PrimaryConflictMarker{
				SchemaVersion:          1,
				DetectedAt:             time.Now().Format(time.RFC3339),
				SupersededDeviceID:     deviceID,
				SupersededLabel:        label,
				ClaimedPrimaryDeviceID: marker.PrimaryDeviceID,
				ClaimedPrimaryLabel:    marker.PrimaryLabel,
			}
			if err := b.uploadConflictMarker(ctx, remoteTarget, cm); err != nil {
				return false, err
			}
		}
		if err := b.cfgMgr.SavePrimaryConflict(config.PrimaryConflict{
			DetectedAt:    time.Now().Format(time.RFC3339),
			Role:          config.ConflictRoleSuperseded,
			OtherDeviceID: marker.PrimaryDeviceID,
			OtherLabel:    marker.PrimaryLabel,
		}); err != nil {
			return false, err
		}
		_ = events.Append(deviceID, label, eventlog.EventConflictDetected, map[string]string{
			"claimed_primary_device_id": marker.PrimaryDeviceID,
			"claimed_primary_label":     marker.PrimaryLabel,
		})
		return false, nil
	}

	// Already known about - see if it's been resolved yet.
	if conflictExists {
		return false, nil // still unresolved
	}

	// Resolved against this device: finalize the demotion.
	if err := b.cfgMgr.SaveRole("secondary"); err != nil {
		return false, err
	}
	if err := b.cfgMgr.ClearPrimaryConflict(); err != nil {
		return false, err
	}
	_ = events.Append(deviceID, label, eventlog.EventDemotedToSecondary, map[string]string{
		"new_primary_device_id": marker.PrimaryDeviceID,
		"new_primary_label":     marker.PrimaryLabel,
	})
	return false, nil
}

// downloadMarker downloads and parses PRIMARY_MARKER.json from
// remoteTarget. Used by VerifyPrimaryStatus; InitializeNode has its own
// inline copy of equivalent logic, deliberately left untouched here rather
// than refactored to share this, to avoid touching its already-hardened
// verification path.
func (b *Bootstrapper) downloadMarker(ctx context.Context, remoteTarget string) (*PrimaryMarker, error) {
	remoteMarkerFile := fmt.Sprintf("%s/%s", remoteTarget, PrimaryMarkerRelPath)
	tempFile := filepath.Join(os.TempDir(), fmt.Sprintf("verify_marker_%d.json", time.Now().UnixNano()))
	defer os.Remove(tempFile)

	if err := b.drive.DownloadFile(ctx, remoteMarkerFile, tempFile); err != nil {
		return nil, fmt.Errorf("failed to download primary marker: %w", err)
	}
	data, err := os.ReadFile(tempFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read downloaded primary marker: %w", err)
	}
	var marker PrimaryMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return nil, fmt.Errorf("failed to parse primary marker: %w", err)
	}
	return &marker, nil
}

// uploadConflictMarker uploads marker as PRIMARY_CONFLICT.json under
// remoteTarget.
func (b *Bootstrapper) uploadConflictMarker(ctx context.Context, remoteTarget string, marker PrimaryConflictMarker) error {
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal primary conflict marker: %w", err)
	}
	tempFile := filepath.Join(os.TempDir(), fmt.Sprintf("conflict_marker_%d.json", time.Now().UnixNano()))
	defer os.Remove(tempFile)
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp conflict marker: %w", err)
	}
	remoteFile := fmt.Sprintf("%s/%s", remoteTarget, ConflictMarkerRelPath)
	if err := b.drive.UploadFile(ctx, tempFile, remoteFile); err != nil {
		return fmt.Errorf("failed to upload primary conflict marker: %w", err)
	}
	return nil
}

// downloadConflictMarker downloads and parses PRIMARY_CONFLICT.json from
// remoteTarget, if present.
func (b *Bootstrapper) downloadConflictMarker(ctx context.Context, remoteTarget string) (*PrimaryConflictMarker, error) {
	remoteFile := fmt.Sprintf("%s/%s", remoteTarget, ConflictMarkerRelPath)
	tempFile := filepath.Join(os.TempDir(), fmt.Sprintf("read_conflict_marker_%d.json", time.Now().UnixNano()))
	defer os.Remove(tempFile)

	if err := b.drive.DownloadFile(ctx, remoteFile, tempFile); err != nil {
		return nil, fmt.Errorf("failed to download primary conflict marker: %w", err)
	}
	data, err := os.ReadFile(tempFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read downloaded primary conflict marker: %w", err)
	}
	var marker PrimaryConflictMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return nil, fmt.Errorf("failed to parse primary conflict marker: %w", err)
	}
	return &marker, nil
}
