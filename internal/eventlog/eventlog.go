// Package eventlog records application-level lifecycle events (role
// changes, primary/secondary conflicts, ...) separately from
// internal/syncedlog's per-file content diff logs (spec section 3.2.1).
// Like those content logs, each device only ever writes to its own
// events-<device-uuid>.jsonl file inside Vault/_sync/, so files never need
// merging and never conflict - reads may look at any device's file, but
// writes only ever touch the caller's own.
package eventlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EventType enumerates the recognized values of EventEntry.Event.
type EventType string

const (
	// EventInitializedAsPrimary is recorded when InitializeNode first
	// establishes this device as Primary (no PRIMARY_MARKER.json existed
	// yet on Google Drive).
	EventInitializedAsPrimary EventType = "initialized_as_primary"
	// EventInitializedAsSecondary is recorded when InitializeNode first
	// establishes this device as Secondary.
	EventInitializedAsSecondary EventType = "initialized_as_secondary"
	// EventPromotedToPrimary is recorded whenever PromoteToPrimary
	// succeeds, whether triggered proactively (Settings > "Promote to
	// Primary...") or to resolve an active multi-Primary conflict.
	EventPromotedToPrimary EventType = "promoted_to_primary"
	// EventConflictDetected is recorded by a device that discovers
	// PRIMARY_MARKER.json no longer names it as Primary, despite believing
	// it still was (see bootstrap.Bootstrapper.VerifyPrimaryStatus).
	EventConflictDetected EventType = "conflict_detected"
	// EventDemotedToSecondary is recorded once a device that detected a
	// conflict (EventConflictDetected) confirms the conflict resolved
	// against it - i.e. PRIMARY_CONFLICT.json was cleared while
	// PRIMARY_MARKER.json still names a different device.
	EventDemotedToSecondary EventType = "demoted_to_secondary"
	// EventConflictResolved is recorded by whichever device's
	// PromoteToPrimary call cleared an active PRIMARY_CONFLICT.json.
	EventConflictResolved EventType = "conflict_resolved"
	// EventDeviceDecommissioned is recorded by a device, just before Reset
	// Configuration clears its local role/config, to tell every other
	// device it's deliberately leaving this Vault for good - as opposed to
	// simply going quiet (e.g. powered off, uninstalled without resetting
	// first), which leaves no such signal. LatestEventForEachDevice treats
	// this as a device's terminal state unless a later event supersedes it
	// (e.g. the same device ID re-initializing after being reset and set
	// up again).
	EventDeviceDecommissioned EventType = "device_decommissioned"
)

// EventEntry represents a single line in events-<device-uuid>.jsonl.
type EventEntry struct {
	Device  string            `json:"device"`
	Label   string            `json:"label"`
	Event   EventType         `json:"event"`
	TS      string            `json:"ts"`
	Details map[string]string `json:"details,omitempty"`
}

// DefaultRetentionDays is how long an event log entry is kept before
// PruneOwnEvents discards it (spec section 3.2.1) - lifecycle events are
// small and infrequent, so a generous default costs little.
const DefaultRetentionDays = 365

// Manager handles reading and writing device event log files in
// Vault/_sync/.
type Manager struct {
	vaultPath string
}

// NewManager creates a new Manager for the given Vault path.
func NewManager(vaultPath string) *Manager {
	return &Manager{vaultPath: vaultPath}
}

// SyncDir returns the path to Vault/_sync/.
func (m *Manager) SyncDir() string {
	return filepath.Join(m.vaultPath, "_sync")
}

// DeviceLogPath returns the path to events-<deviceID>.jsonl.
func (m *Manager) DeviceLogPath(deviceID string) string {
	return filepath.Join(m.SyncDir(), fmt.Sprintf("events-%s.jsonl", deviceID))
}

// Append records a new event for deviceID/label, filling in TS if empty.
func (m *Manager) Append(deviceID, label string, event EventType, details map[string]string) error {
	if err := os.MkdirAll(m.SyncDir(), 0755); err != nil {
		return fmt.Errorf("failed to create _sync dir: %w", err)
	}

	entry := EventEntry{
		Device:  deviceID,
		Label:   label,
		Event:   event,
		TS:      time.Now().Format(time.RFC3339),
		Details: details,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal event entry: %w", err)
	}

	f, err := os.OpenFile(m.DeviceLogPath(deviceID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open event log file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write event entry: %w", err)
	}

	return nil
}

// ReadDeviceLog reads all entries from events-<deviceID>.jsonl, oldest
// first. Returns (nil, nil) if the device has no event log yet.
func (m *Manager) ReadDeviceLog(deviceID string) ([]EventEntry, error) {
	f, err := os.Open(m.DeviceLogPath(deviceID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to open event log file: %w", err)
	}
	defer f.Close()

	var entries []EventEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry EventEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("failed to unmarshal event entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanner error: %w", err)
	}

	return entries, nil
}

// LatestEventForEachDevice returns, for every device that has ever written
// to this Vault's event log, its single most recent entry - the label
// (hostname), event type, and timestamp of the last thing it reported
// doing (initializing, promoting, decommissioning, ...). Callers use this
// to answer "which other devices does this Vault know about, and does any
// of them still look active" (spec 3.6.1.5): a device whose latest entry
// is EventDeviceDecommissioned is treated as having explicitly left,
// distinct from one that's merely offline (which keeps whatever its last
// real event was). A device that has never written an event at all isn't
// included - there's nothing to read.
func (m *Manager) LatestEventForEachDevice() (map[string]EventEntry, error) {
	matches, err := filepath.Glob(filepath.Join(m.SyncDir(), "events-*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("failed to list event log files: %w", err)
	}

	result := make(map[string]EventEntry, len(matches))
	for _, path := range matches {
		deviceID := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "events-"), ".jsonl")
		entries, err := m.ReadDeviceLog(deviceID)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			continue
		}
		result[deviceID] = entries[len(entries)-1]
	}
	return result, nil
}

// PruneOwnEvents rewrites deviceID's own event log, discarding entries
// older than retainDays (spec section 3.2.1). A device must only ever
// prune its own file - pruning another device's would be a write to a
// file this device doesn't own, exactly what the per-device log split is
// meant to avoid. A missing log file is a silent no-op.
func (m *Manager) PruneOwnEvents(deviceID string, retainDays int) error {
	entries, err := m.ReadDeviceLog(deviceID)
	if err != nil {
		return err
	}
	if entries == nil {
		return nil
	}

	cutoff := time.Now().AddDate(0, 0, -retainDays)
	kept := entries[:0]
	for _, e := range entries {
		ts, err := time.Parse(time.RFC3339, e.TS)
		if err != nil || !ts.Before(cutoff) {
			kept = append(kept, e)
		}
	}
	if len(kept) == len(entries) {
		return nil // nothing to prune, avoid a pointless rewrite
	}

	path := m.DeviceLogPath(deviceID)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create pruned event log: %w", err)
	}
	for _, e := range kept {
		data, err := json.Marshal(e)
		if err != nil {
			f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("failed to marshal event entry: %w", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("failed to write pruned event log: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to close pruned event log: %w", err)
	}

	return os.Rename(tmp, path)
}
