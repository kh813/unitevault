package eventlog_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kh813/unitevault/internal/eventlog"
)

func TestManager_AppendAndRead(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "Vault")
	m := eventlog.NewManager(vaultPath)

	if err := m.Append("dev-a", "mac-mini", eventlog.EventInitializedAsPrimary, nil); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if err := m.Append("dev-a", "mac-mini", eventlog.EventPromotedToPrimary, map[string]string{"reason": "manual"}); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	entries, err := m.ReadDeviceLog("dev-a")
	if err != nil {
		t.Fatalf("ReadDeviceLog failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Event != eventlog.EventInitializedAsPrimary {
		t.Errorf("expected first entry to be %q, got %q", eventlog.EventInitializedAsPrimary, entries[0].Event)
	}
	if entries[1].Details["reason"] != "manual" {
		t.Errorf("expected second entry's details to round-trip, got %+v", entries[1].Details)
	}
}

func TestManager_ReadDeviceLog_MissingFileReturnsNil(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "Vault")
	m := eventlog.NewManager(vaultPath)

	entries, err := m.ReadDeviceLog("never-written")
	if err != nil {
		t.Fatalf("expected no error for a missing log file, got %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil entries for a missing log file, got %+v", entries)
	}
}

// TestManager_PruneOwnEvents_DiscardsOnlyOldEntries guards the core
// retention behavior (spec 3.2.1): entries older than retainDays are
// dropped, entries within the window survive untouched.
func TestManager_PruneOwnEvents_DiscardsOnlyOldEntries(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "Vault")
	m := eventlog.NewManager(vaultPath)

	old := eventlog.EventEntry{
		Device: "dev-a", Label: "mac-mini", Event: eventlog.EventInitializedAsPrimary,
		TS: time.Now().AddDate(0, 0, -400).Format(time.RFC3339),
	}
	recent := eventlog.EventEntry{
		Device: "dev-a", Label: "mac-mini", Event: eventlog.EventPromotedToPrimary,
		TS: time.Now().AddDate(0, 0, -1).Format(time.RFC3339),
	}
	writeRawEntries(t, m, "dev-a", old, recent)

	if err := m.PruneOwnEvents("dev-a", eventlog.DefaultRetentionDays); err != nil {
		t.Fatalf("PruneOwnEvents failed: %v", err)
	}

	entries, err := m.ReadDeviceLog("dev-a")
	if err != nil {
		t.Fatalf("ReadDeviceLog failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 surviving entry, got %d: %+v", len(entries), entries)
	}
	if entries[0].Event != eventlog.EventPromotedToPrimary {
		t.Errorf("expected the recent entry to survive pruning, got %+v", entries[0])
	}
}

// TestManager_PruneOwnEvents_NeverTouchesOtherDevicesLogs guards the
// ownership rule that makes per-device append-only logs conflict-free in
// the first place: pruning device A's log must never write to device B's.
func TestManager_PruneOwnEvents_NeverTouchesOtherDevicesLogs(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "Vault")
	m := eventlog.NewManager(vaultPath)

	old := eventlog.EventEntry{
		Device: "dev-b", Label: "iphone", Event: eventlog.EventInitializedAsSecondary,
		TS: time.Now().AddDate(0, 0, -400).Format(time.RFC3339),
	}
	writeRawEntries(t, m, "dev-b", old)

	if err := m.PruneOwnEvents("dev-a", eventlog.DefaultRetentionDays); err != nil {
		t.Fatalf("PruneOwnEvents(dev-a) failed: %v", err)
	}

	entries, err := m.ReadDeviceLog("dev-b")
	if err != nil {
		t.Fatalf("ReadDeviceLog(dev-b) failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected dev-b's old entry to survive untouched (pruning dev-a must not affect it), got %d entries", len(entries))
	}
}

// TestManager_LatestEventForEachDevice guards the multi-device summary used
// to decide whether other devices still look active (spec 3.6.1.5): one
// entry per device, always the most recent, and devices that never wrote
// anything are simply absent rather than zero-valued.
func TestManager_LatestEventForEachDevice(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "Vault")
	m := eventlog.NewManager(vaultPath)

	if err := m.Append("dev-a", "mac-mini", eventlog.EventInitializedAsPrimary, nil); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if err := m.Append("dev-a", "mac-mini", eventlog.EventDeviceDecommissioned, nil); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if err := m.Append("dev-b", "iphone", eventlog.EventInitializedAsSecondary, nil); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	got, err := m.LatestEventForEachDevice()
	if err != nil {
		t.Fatalf("LatestEventForEachDevice failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected exactly 2 known devices, got %d: %+v", len(got), got)
	}
	if got["dev-a"].Event != eventlog.EventDeviceDecommissioned {
		t.Errorf("expected dev-a's latest event to be the decommission, got %+v", got["dev-a"])
	}
	if got["dev-b"].Event != eventlog.EventInitializedAsSecondary || got["dev-b"].Label != "iphone" {
		t.Errorf("expected dev-b's single entry to come back as-is, got %+v", got["dev-b"])
	}
	if _, ok := got["never-written"]; ok {
		t.Error("expected a device that never wrote an event to be absent from the result")
	}
}

func TestManager_LatestEventForEachDevice_NoEventsAtAll(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "Vault")
	m := eventlog.NewManager(vaultPath)

	got, err := m.LatestEventForEachDevice()
	if err != nil {
		t.Fatalf("expected no error when the sync dir doesn't exist yet, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected an empty result, got %+v", got)
	}
}

func writeRawEntries(t *testing.T, m *eventlog.Manager, deviceID string, entries ...eventlog.EventEntry) {
	t.Helper()
	for _, e := range entries {
		details := e.Details
		if err := m.Append(deviceID, e.Label, e.Event, details); err != nil {
			t.Fatalf("failed to seed entry: %v", err)
		}
	}
	// Append always stamps "now" as TS, so overwrite the file directly with
	// the caller's real (possibly backdated) timestamps.
	overwriteWithTimestamps(t, m.DeviceLogPath(deviceID), entries)
}

func overwriteWithTimestamps(t *testing.T, path string, entries []eventlog.EventEntry) {
	t.Helper()
	var buf []byte
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("failed to marshal seed entry: %v", err)
		}
		buf = append(buf, data...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(path, buf, 0644); err != nil {
		t.Fatalf("failed to write seed event log: %v", err)
	}
}
