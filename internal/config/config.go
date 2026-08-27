package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/uuid"
)

// Config represents the local application configuration (~/.unitevault/config.json)
type Config struct {
	VaultPath       string `json:"vault_path"`
	RcloneRemote    string `json:"rclone_remote"`
	RclonePath      string `json:"rclone_path"`
	IntervalSeconds int    `json:"interval_seconds"`
	// ICloudBridgePath is the iCloud Drive-resident folder Vault Migration
	// (spec 1.6.3) seeded a copy of the Vault into, for iPhone/iPad to keep
	// editing via iCloud. Empty means no bridge was set up (e.g. iCloud
	// Drive wasn't detected at migration time, or the user has no iPhone).
	// This is currently a one-time seed copy only - the ongoing bridge
	// sync/merge described in spec 1.6.3 is still future work (Phase 15,
	// unitevault-todo.md).
	ICloudBridgePath string `json:"icloud_bridge_path,omitempty"`
}

// ConfigManager handles loading and saving settings in the local config directory
type ConfigManager struct {
	configDir string
}

// NewConfigManager initializes a ConfigManager using default OS config directory paths
func NewConfigManager() (*ConfigManager, error) {
	dir, err := GetConfigDir()
	if err != nil {
		return nil, err
	}
	return &ConfigManager{configDir: dir}, nil
}

// NewConfigManagerWithDir initializes a ConfigManager with a custom directory (useful for testing)
func NewConfigManagerWithDir(dir string) *ConfigManager {
	return &ConfigManager{configDir: dir}
}

// GetConfigDir resolves the local settings directory path:
// Mac/Linux: ~/.unitevault/
// Windows: %APPDATA%\unitevault\
func GetConfigDir() (string, error) {
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get home dir: %w", err)
			}
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "unitevault"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home dir: %w", err)
	}
	return filepath.Join(home, ".unitevault"), nil
}

// EnsureDir ensures that the config directory exists
func (cm *ConfigManager) EnsureDir() error {
	return os.MkdirAll(cm.configDir, 0755)
}

// ConfigPath returns the path to config.json
func (cm *ConfigManager) ConfigPath() string {
	return filepath.Join(cm.configDir, "config.json")
}

// DeviceIDPath returns the path to device_id
func (cm *ConfigManager) DeviceIDPath() string {
	return filepath.Join(cm.configDir, "device_id")
}

// BridgeDeviceIDPath returns the path to icloud_bridge_device_id - a
// second, separate UUID (spec 1.6.3) this device uses to log changes
// detected in the iCloud Bridge folder, kept distinct from this device's
// own DeviceIDPath so Bridge-originated entries are clearly
// distinguishable from this device's own real edits in _sync/ logs.
func (cm *ConfigManager) BridgeDeviceIDPath() string {
	return filepath.Join(cm.configDir, "icloud_bridge_device_id")
}

// RolePath returns the path to role
func (cm *ConfigManager) RolePath() string {
	return filepath.Join(cm.configDir, "role")
}

// InstallReminderDismissedPath returns the path to the marker file recording
// that the user opted out of the "Git/rclone missing" startup reminder.
func (cm *ConfigManager) InstallReminderDismissedPath() string {
	return filepath.Join(cm.configDir, "install_reminder_dismissed")
}

// DriveSyncStatus records the outcome of the most recent Google Drive
// backup attempt (the rclone sync step of a Primary device's sync cycle),
// so the Settings window can show it without needing a live connection to
// the running daemon loop (settings_window.go is rebuilt independently by
// reading disk state, not by querying the tray app's in-memory state).
type DriveSyncStatus struct {
	Time    string `json:"time"` // RFC3339
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// DriveSyncStatusPath returns the path to drive_sync_status.json.
func (cm *ConfigManager) DriveSyncStatusPath() string {
	return filepath.Join(cm.configDir, "drive_sync_status.json")
}

// SaveDriveSyncStatus persists the outcome of a Google Drive sync attempt.
func (cm *ConfigManager) SaveDriveSyncStatus(status DriveSyncStatus) error {
	if err := cm.EnsureDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal drive sync status: %w", err)
	}
	return os.WriteFile(cm.DriveSyncStatusPath(), data, 0644)
}

// LoadDriveSyncStatus reads the last-recorded Google Drive sync outcome, or
// returns (nil, nil) if a sync has never been attempted on this device.
func (cm *ConfigManager) LoadDriveSyncStatus() (*DriveSyncStatus, error) {
	data, err := os.ReadFile(cm.DriveSyncStatusPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read drive sync status: %w", err)
	}
	var status DriveSyncStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("failed to parse drive sync status: %w", err)
	}
	return &status, nil
}

// PrimaryConflictRole distinguishes the two sides of an unresolved
// multi-Primary conflict (see internal/bootstrap.Bootstrapper.
// VerifyPrimaryStatus and unitevault-spec.md section 3.6.1.4):
type PrimaryConflictRole string

const (
	// ConflictRoleSuperseded means this device believed it was Primary,
	// but PRIMARY_MARKER.json now names a different device.
	ConflictRoleSuperseded PrimaryConflictRole = "superseded"
	// ConflictRoleClaimed means PRIMARY_MARKER.json still names this
	// device, but a PRIMARY_CONFLICT.json filed by another device shows
	// it disagrees.
	ConflictRoleClaimed PrimaryConflictRole = "claimed"
)

// PrimaryConflict is this device's locally cached view of an unresolved
// multi-Primary conflict, so the Settings window can show a warning
// without a live Google Drive round-trip on every render (matching
// DriveSyncStatus's pattern above) - it's refreshed each cycle by
// VerifyPrimaryStatus and cleared once the conflict resolves.
type PrimaryConflict struct {
	DetectedAt    string              `json:"detected_at"`
	Role          PrimaryConflictRole `json:"role"`
	OtherDeviceID string              `json:"other_device_id"`
	OtherLabel    string              `json:"other_label"`
}

// PrimaryConflictPath returns the path to primary_conflict.json.
func (cm *ConfigManager) PrimaryConflictPath() string {
	return filepath.Join(cm.configDir, "primary_conflict.json")
}

// SavePrimaryConflict persists the currently-detected multi-Primary
// conflict, if any.
func (cm *ConfigManager) SavePrimaryConflict(c PrimaryConflict) error {
	if err := cm.EnsureDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal primary conflict state: %w", err)
	}
	return os.WriteFile(cm.PrimaryConflictPath(), data, 0644)
}

// LoadPrimaryConflict reads the locally cached conflict state, or returns
// (nil, nil) if there is no unresolved conflict recorded.
func (cm *ConfigManager) LoadPrimaryConflict() (*PrimaryConflict, error) {
	data, err := os.ReadFile(cm.PrimaryConflictPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read primary conflict state: %w", err)
	}
	var c PrimaryConflict
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("failed to parse primary conflict state: %w", err)
	}
	return &c, nil
}

// ClearPrimaryConflict removes the locally cached conflict state once
// resolved. Removing an already-absent file is not an error.
func (cm *ConfigManager) ClearPrimaryConflict() error {
	if err := os.Remove(cm.PrimaryConflictPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to clear primary conflict state: %w", err)
	}
	return nil
}

// PendingConflictVersion is one device's content for a file caught in a
// genuine (overlapping-edit) merge conflict.
type PendingConflictVersion struct {
	DeviceID string `json:"device_id"`
	Label    string `json:"label"`
	Content  string `json:"content"`
}

// PendingConflict is an unresolved genuine content conflict (spec 3.3.2):
// git merge-file produced conflict markers for relPath because two or more
// devices changed the same region since their common base. Only ever held
// by the Primary device (merging only ever happens there) - never shared
// or synced with other devices.
type PendingConflict struct {
	RelPath string `json:"rel_path"`
	// DetectedAt is informational only (display), never used to decide
	// anything - matching syncedlog.LogEntry.TS's convention.
	DetectedAt string `json:"detected_at"`
	// WrittenHash is the hash of the conflict-marker-laden content that was
	// written to the Vault file when this conflict was detected - if the
	// file's current hash no longer matches, the user must have resolved
	// it manually in Obsidian, and this entry should be dropped rather
	// than keep nagging about a conflict that no longer exists.
	WrittenHash string                   `json:"written_hash"`
	Versions    []PendingConflictVersion `json:"versions"`
}

// PendingConflictsPath returns the path to pending_conflicts.json.
func (cm *ConfigManager) PendingConflictsPath() string {
	return filepath.Join(cm.configDir, "pending_conflicts.json")
}

// SavePendingConflicts persists the full set of currently-unresolved
// content conflicts. Passing an empty slice still writes an (empty-array)
// file rather than removing it - callers that want "no file at all" should
// use ClearPendingConflicts instead; LoadPendingConflicts treats both the
// same way regardless.
func (cm *ConfigManager) SavePendingConflicts(conflicts []PendingConflict) error {
	if err := cm.EnsureDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(conflicts, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal pending conflicts: %w", err)
	}
	return os.WriteFile(cm.PendingConflictsPath(), data, 0644)
}

// LoadPendingConflicts reads the locally cached set of unresolved content
// conflicts, or returns (nil, nil) if none is recorded.
func (cm *ConfigManager) LoadPendingConflicts() ([]PendingConflict, error) {
	data, err := os.ReadFile(cm.PendingConflictsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read pending conflicts: %w", err)
	}
	var conflicts []PendingConflict
	if err := json.Unmarshal(data, &conflicts); err != nil {
		return nil, fmt.Errorf("failed to parse pending conflicts: %w", err)
	}
	return conflicts, nil
}

// ClearPendingConflicts removes the locally cached conflict set entirely
// (e.g. once every conflict has been resolved). Removing an already-absent
// file is not an error.
func (cm *ConfigManager) ClearPendingConflicts() error {
	if err := os.Remove(cm.PendingConflictsPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to clear pending conflicts: %w", err)
	}
	return nil
}

// IsInstallReminderDismissed reports whether the user previously checked
// "Don't show this again" on the missing-dependency startup reminder.
func (cm *ConfigManager) IsInstallReminderDismissed() bool {
	_, err := os.Stat(cm.InstallReminderDismissedPath())
	return err == nil
}

// SetInstallReminderDismissed persists that the missing-dependency startup
// reminder should no longer be shown.
func (cm *ConfigManager) SetInstallReminderDismissed() error {
	if err := cm.EnsureDir(); err != nil {
		return err
	}
	return os.WriteFile(cm.InstallReminderDismissedPath(), []byte("1"), 0644)
}

// DefaultIntervalSeconds is the Google Drive sync interval used whenever
// nothing else has been configured (spec section 3.4.1). Google Drive
// backup is a background safety net, not the channel that keeps devices in
// sync with each other (iCloud handles device-to-device propagation on its
// own schedule, independent of this interval - spec section 1.3), so a
// wider default cadence than the original 120s trades a little backup
// freshness for meaningfully less background rclone/API traffic - which
// also includes the per-cycle Primary-conflict check added alongside this
// (see bootstrap.Bootstrapper.VerifyPrimaryStatus).
const DefaultIntervalSeconds = 600

// LoadConfig reads config.json if present, or returns default values
func (cm *ConfigManager) LoadConfig() (*Config, error) {
	path := cm.ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{
				IntervalSeconds: DefaultIntervalSeconds,
			}, nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	return &cfg, nil
}

// SaveConfig writes the given Config struct to config.json
func (cm *ConfigManager) SaveConfig(cfg *Config) error {
	if err := cm.EnsureDir(); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(cm.ConfigPath(), data, 0644)
}

// GetDeviceID reads the device_id file or generates a new UUID if non-existent
func (cm *ConfigManager) GetDeviceID() (string, error) {
	path := cm.DeviceIDPath()
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to read device_id: %w", err)
	}

	// Generate new UUID
	if err := cm.EnsureDir(); err != nil {
		return "", err
	}

	newID := uuid.New().String()
	if err := os.WriteFile(path, []byte(newID), 0644); err != nil {
		return "", fmt.Errorf("failed to write device_id: %w", err)
	}

	return newID, nil
}

// GetOrCreateBridgeDeviceID reads icloud_bridge_device_id, generating and
// persisting a new UUID on first use - GetDeviceID's exact behavior,
// mirrored against BridgeDeviceIDPath instead (spec 1.6.3).
func (cm *ConfigManager) GetOrCreateBridgeDeviceID() (string, error) {
	path := cm.BridgeDeviceIDPath()
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to read icloud_bridge_device_id: %w", err)
	}

	if err := cm.EnsureDir(); err != nil {
		return "", err
	}

	newID := uuid.New().String()
	if err := os.WriteFile(path, []byte(newID), 0644); err != nil {
		return "", fmt.Errorf("failed to write icloud_bridge_device_id: %w", err)
	}

	return newID, nil
}

// LoadRole reads the cached role ("primary" or "secondary")
func (cm *ConfigManager) LoadRole() (string, error) {
	data, err := os.ReadFile(cm.RolePath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read role: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// SaveRole saves the cached role ("primary" or "secondary")
func (cm *ConfigManager) SaveRole(role string) error {
	if err := cm.EnsureDir(); err != nil {
		return err
	}
	return os.WriteFile(cm.RolePath(), []byte(role), 0644)
}

// ResetConfig removes config.json and role files to restore initial state
func (cm *ConfigManager) ResetConfig() error {
	_ = os.Remove(cm.ConfigPath())
	_ = os.Remove(cm.RolePath())
	_ = os.Remove(cm.InstallReminderDismissedPath())
	_ = os.Remove(cm.DriveSyncStatusPath())
	_ = os.Remove(cm.PrimaryConflictPath())
	_ = os.Remove(cm.PendingConflictsPath())
	return nil
}
