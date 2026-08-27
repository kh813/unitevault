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
	"github.com/kh813/unitevault/internal/eventlog"
)

type mockDrive struct {
	remoteFiles map[string][]byte
	copyCalled  bool
	// downloadErr, if set, makes DownloadFile fail every call instead of
	// serving remoteFiles - used to simulate a transient network error
	// during the InitializeNode's primary-identity verification download.
	downloadErr error
	// raceOverride, if set for a given remote path, is served by the next
	// DownloadFile call for that path instead of remoteFiles' real content,
	// then cleared - simulating another device's upload landing in between
	// this call's own UploadFile and its immediately-following verification
	// DownloadFile (initAsPrimary's race-condition check).
	raceOverride map[string][]byte
}

func newMockDrive() *mockDrive {
	return &mockDrive{remoteFiles: make(map[string][]byte)}
}

func (m *mockDrive) Sync(ctx context.Context, srcPath, remoteTarget string, excludes ...string) error {
	return nil
}

func (m *mockDrive) Copy(ctx context.Context, remoteSrc, dstPath string, excludes ...string) error {
	m.copyCalled = true
	return nil
}

func (m *mockDrive) FileExists(ctx context.Context, remoteTargetFile string) (bool, error) {
	_, ok := m.remoteFiles[remoteTargetFile]
	return ok, nil
}

func (m *mockDrive) DownloadFile(ctx context.Context, remoteSourceFile, localDstFile string) error {
	if m.downloadErr != nil {
		return m.downloadErr
	}
	if override, ok := m.raceOverride[remoteSourceFile]; ok {
		delete(m.raceOverride, remoteSourceFile)
		return os.WriteFile(localDstFile, override, 0644)
	}
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

func (m *mockDrive) DeleteFile(ctx context.Context, remoteTargetFile string) error {
	delete(m.remoteFiles, remoteTargetFile)
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

// TestBootstrap_PrimaryVerificationDownloadFailure_DoesNotDemoteToSecondary
// guards against a bug where a transient network error while re-verifying
// whether this device is the recorded Primary (InitializeNode downloading
// PRIMARY_MARKER.json back to check its PrimaryDeviceID) was silently
// treated the same as "another device is Primary", falling through to
// initAsSecondary and demoting a real Primary device with no error surfaced
// at all. A failed verification download must return an error instead.
func TestBootstrap_PrimaryVerificationDownloadFailure_DoesNotDemoteToSecondary(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgDir := filepath.Join(tempDir, "config")

	cfgMgr := config.NewConfigManagerWithDir(cfgDir)
	deviceID, _ := cfgMgr.GetDeviceID()
	mock := newMockDrive()

	// Remote marker exists (so InitializeNode takes the verification path)
	// and belongs to THIS device, but the verification download itself
	// fails transiently.
	remoteMarkerFile := "gdrive:Backup/_sync/PRIMARY_MARKER.json"
	existingMarker := bootstrap.PrimaryMarker{
		SchemaVersion:   1,
		PrimaryDeviceID: deviceID,
		PrimaryLabel:    "my-mac",
	}
	markerBytes, _ := json.Marshal(existingMarker)
	mock.remoteFiles[remoteMarkerFile] = markerBytes
	mock.downloadErr = fmt.Errorf("simulated transient network error")

	bootstrapper := bootstrap.NewBootstrapper(cfgMgr, mock)
	ctx := context.Background()

	role, err := bootstrapper.InitializeNode(ctx, vaultPath, "gdrive:Backup", "my-mac")
	if err == nil {
		t.Fatalf("expected an error when the verification download fails, got role=%q, err=nil", role)
	}
	if role == "secondary" {
		t.Fatalf("must not silently demote to secondary on a verification download failure, got role=%q", role)
	}

	// The role must not have been overwritten to "secondary" in local config
	// either, since initAsSecondary() must never have run.
	if cachedRole, err := cfgMgr.LoadRole(); err == nil && cachedRole == "secondary" {
		t.Fatalf("expected cached role to remain unset/unchanged, got 'secondary'")
	}
}

// --- VerifyPrimaryStatus / multi-Primary conflict tests (spec 3.6.1.4) ---
//
// These guard the split-brain-prevention mechanism directly: a device
// whose cached role is "primary" must re-confirm that against
// PRIMARY_MARKER.json every cycle rather than trusting the cache forever,
// and two devices that each believe they're Primary must both pause
// Google Drive sync until a human resolves it via PromoteToPrimary.

const testRemoteTarget = "gdrive:Backup"

func markerPath() string {
	return testRemoteTarget + "/" + bootstrap.PrimaryMarkerRelPath
}

func conflictPath() string {
	return testRemoteTarget + "/" + bootstrap.ConflictMarkerRelPath
}

func seedMarker(t *testing.T, mock *mockDrive, primaryDeviceID, primaryLabel string) {
	t.Helper()
	data, err := json.Marshal(bootstrap.PrimaryMarker{SchemaVersion: 1, PrimaryDeviceID: primaryDeviceID, PrimaryLabel: primaryLabel})
	if err != nil {
		t.Fatalf("failed to marshal seed marker: %v", err)
	}
	mock.remoteFiles[markerPath()] = data
}

// TestVerifyPrimaryStatus_StillPrimary_NoConflict_Proceeds is the common
// case: nothing has changed, merge + Google Drive sync should run.
func TestVerifyPrimaryStatus_StillPrimary_NoConflict_Proceeds(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "config"))
	deviceID, _ := cfgMgr.GetDeviceID()
	_ = cfgMgr.SaveRole("primary")

	mock := newMockDrive()
	seedMarker(t, mock, deviceID, "mac-mini")

	proceed, err := bootstrap.NewBootstrapper(cfgMgr, mock).VerifyPrimaryStatus(context.Background(), vaultPath, testRemoteTarget, deviceID, "mac-mini")
	if err != nil {
		t.Fatalf("VerifyPrimaryStatus failed: %v", err)
	}
	if !proceed {
		t.Error("expected proceed=true when the marker still names this device and there is no conflict")
	}
}

// TestVerifyPrimaryStatus_Superseded_FirstDetection_FilesConflictAndPauses
// is the core split-brain guard: a device that still believes it's Primary
// but has been superseded must file PRIMARY_CONFLICT.json, cache the
// conflict locally (for the Settings warning banner), log it, and refuse
// to proceed - all without touching its cached role yet, since a human may
// still Authorize this same device.
func TestVerifyPrimaryStatus_Superseded_FirstDetection_FilesConflictAndPauses(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "config"))
	deviceID, _ := cfgMgr.GetDeviceID()
	_ = cfgMgr.SaveRole("primary")

	mock := newMockDrive()
	seedMarker(t, mock, "other-device-id", "windows-desktop")

	proceed, err := bootstrap.NewBootstrapper(cfgMgr, mock).VerifyPrimaryStatus(context.Background(), vaultPath, testRemoteTarget, deviceID, "mac-mini")
	if err != nil {
		t.Fatalf("VerifyPrimaryStatus failed: %v", err)
	}
	if proceed {
		t.Fatal("expected proceed=false when this device has been superseded")
	}

	if _, ok := mock.remoteFiles[conflictPath()]; !ok {
		t.Error("expected PRIMARY_CONFLICT.json to have been filed on Google Drive")
	}

	cached, err := cfgMgr.LoadPrimaryConflict()
	if err != nil {
		t.Fatalf("LoadPrimaryConflict failed: %v", err)
	}
	if cached == nil {
		t.Fatal("expected a locally cached conflict record")
	}
	if cached.Role != config.ConflictRoleSuperseded {
		t.Errorf("expected role=superseded, got %q", cached.Role)
	}
	if cached.OtherDeviceID != "other-device-id" {
		t.Errorf("expected OtherDeviceID=other-device-id, got %q", cached.OtherDeviceID)
	}

	if role, _ := cfgMgr.LoadRole(); role != "primary" {
		t.Errorf("expected the cached role to remain 'primary' while the conflict is still pending (a human may Authorize this device), got %q", role)
	}

	events, err := eventlog.NewManager(vaultPath).ReadDeviceLog(deviceID)
	if err != nil {
		t.Fatalf("ReadDeviceLog failed: %v", err)
	}
	if len(events) != 1 || events[0].Event != eventlog.EventConflictDetected {
		t.Errorf("expected a single conflict_detected event, got %+v", events)
	}
}

// TestVerifyPrimaryStatus_Superseded_StillPending_NoDuplicateConflictWrite
// guards that a device already tracking a conflict doesn't keep re-filing
// it (or otherwise misbehave) on every subsequent cycle while it's still
// unresolved.
func TestVerifyPrimaryStatus_Superseded_StillPending_NoDuplicateConflictWrite(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "config"))
	deviceID, _ := cfgMgr.GetDeviceID()
	_ = cfgMgr.SaveRole("primary")

	mock := newMockDrive()
	seedMarker(t, mock, "other-device-id", "windows-desktop")
	b := bootstrap.NewBootstrapper(cfgMgr, mock)

	// First cycle files the conflict.
	if _, err := b.VerifyPrimaryStatus(context.Background(), vaultPath, testRemoteTarget, deviceID, "mac-mini"); err != nil {
		t.Fatalf("first VerifyPrimaryStatus failed: %v", err)
	}

	// Second cycle: conflict is still open on Drive - must still pause,
	// role must still be untouched, and no second conflict_detected event.
	proceed, err := b.VerifyPrimaryStatus(context.Background(), vaultPath, testRemoteTarget, deviceID, "mac-mini")
	if err != nil {
		t.Fatalf("second VerifyPrimaryStatus failed: %v", err)
	}
	if proceed {
		t.Error("expected proceed=false while the conflict is still open")
	}
	if role, _ := cfgMgr.LoadRole(); role != "primary" {
		t.Errorf("expected role to remain 'primary' while unresolved, got %q", role)
	}

	events, _ := eventlog.NewManager(vaultPath).ReadDeviceLog(deviceID)
	count := 0
	for _, e := range events {
		if e.Event == eventlog.EventConflictDetected {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 conflict_detected event across two cycles, got %d", count)
	}
}

// TestVerifyPrimaryStatus_Superseded_ResolvedAgainstSelf_Demotes covers the
// case where a human Authorized the *other* device: once
// PRIMARY_CONFLICT.json is cleared while the marker still names someone
// else, this device must finalize its own demotion.
func TestVerifyPrimaryStatus_Superseded_ResolvedAgainstSelf_Demotes(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "config"))
	deviceID, _ := cfgMgr.GetDeviceID()
	_ = cfgMgr.SaveRole("primary")

	mock := newMockDrive()
	seedMarker(t, mock, "other-device-id", "windows-desktop")
	b := bootstrap.NewBootstrapper(cfgMgr, mock)

	if _, err := b.VerifyPrimaryStatus(context.Background(), vaultPath, testRemoteTarget, deviceID, "mac-mini"); err != nil {
		t.Fatalf("first VerifyPrimaryStatus failed: %v", err)
	}

	// The other device's PromoteToPrimary clears the conflict marker.
	delete(mock.remoteFiles, conflictPath())

	proceed, err := b.VerifyPrimaryStatus(context.Background(), vaultPath, testRemoteTarget, deviceID, "mac-mini")
	if err != nil {
		t.Fatalf("second VerifyPrimaryStatus failed: %v", err)
	}
	if proceed {
		t.Error("expected proceed=false - this device is now Secondary, not Primary")
	}
	if role, _ := cfgMgr.LoadRole(); role != "secondary" {
		t.Errorf("expected role=secondary once the conflict resolved against this device, got %q", role)
	}
	if cached, _ := cfgMgr.LoadPrimaryConflict(); cached != nil {
		t.Errorf("expected the local conflict cache to be cleared, got %+v", cached)
	}

	events, _ := eventlog.NewManager(vaultPath).ReadDeviceLog(deviceID)
	found := false
	for _, e := range events {
		if e.Event == eventlog.EventDemotedToSecondary {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a demoted_to_secondary event, got %+v", events)
	}
}

// TestVerifyPrimaryStatus_Claimed_ConflictFiledByOther_Pauses covers the
// other side of the conflict: the marker still names this device, but
// another device has filed PRIMARY_CONFLICT.json against it - Google
// Drive sync must pause here too (never just on the superseded side),
// otherwise this device's own rclone sync could still clobber whatever
// the superseded device wrote about the conflict.
func TestVerifyPrimaryStatus_Claimed_ConflictFiledByOther_Pauses(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "config"))
	deviceID, _ := cfgMgr.GetDeviceID()
	_ = cfgMgr.SaveRole("primary")

	mock := newMockDrive()
	seedMarker(t, mock, deviceID, "mac-mini") // marker still names THIS device
	mock.remoteFiles[conflictPath()] = []byte(`{"schema_version":1}`)

	proceed, err := bootstrap.NewBootstrapper(cfgMgr, mock).VerifyPrimaryStatus(context.Background(), vaultPath, testRemoteTarget, deviceID, "mac-mini")
	if err != nil {
		t.Fatalf("VerifyPrimaryStatus failed: %v", err)
	}
	if proceed {
		t.Error("expected proceed=false while a conflict marker exists, even though this device is still the recognized Primary")
	}
	if role, _ := cfgMgr.LoadRole(); role != "primary" {
		t.Errorf("expected role to remain 'primary', got %q", role)
	}

	cached, _ := cfgMgr.LoadPrimaryConflict()
	if cached == nil || cached.Role != config.ConflictRoleClaimed {
		t.Errorf("expected a locally cached conflict with role=claimed, got %+v", cached)
	}
}

// TestVerifyPrimaryStatus_DownloadFailure_LeavesStateUntouched guards the
// same fail-safe principle InitializeNode already follows: a transient
// error must never be treated the same as "superseded" and must never
// silently demote a real Primary.
func TestVerifyPrimaryStatus_DownloadFailure_LeavesStateUntouched(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "config"))
	deviceID, _ := cfgMgr.GetDeviceID()
	_ = cfgMgr.SaveRole("primary")

	mock := newMockDrive()
	seedMarker(t, mock, deviceID, "mac-mini")
	mock.downloadErr = fmt.Errorf("simulated transient network error")

	proceed, err := bootstrap.NewBootstrapper(cfgMgr, mock).VerifyPrimaryStatus(context.Background(), vaultPath, testRemoteTarget, deviceID, "mac-mini")
	if err == nil {
		t.Fatal("expected an error when the marker download fails")
	}
	if proceed {
		t.Error("expected proceed=false on a download failure")
	}
	if role, _ := cfgMgr.LoadRole(); role != "primary" {
		t.Errorf("expected role to remain 'primary' on a transient failure, got %q", role)
	}
	if cached, _ := cfgMgr.LoadPrimaryConflict(); cached != nil {
		t.Errorf("expected no conflict to be recorded from an inconclusive check, got %+v", cached)
	}
}

// --- PromoteToPrimary conflict-resolution tests ---

// TestPromoteToPrimary_ClearsExistingConflictAndLogsEvents covers the
// "Authorize" path: promoting while a conflict is open must clear
// PRIMARY_CONFLICT.json (so the other device can quietly settle into
// Secondary) and log both events for the audit trail.
func TestPromoteToPrimary_ClearsExistingConflictAndLogsEvents(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "config"))
	deviceID, _ := cfgMgr.GetDeviceID()

	mock := newMockDrive()
	seedMarker(t, mock, "other-device-id", "windows-desktop")
	mock.remoteFiles[conflictPath()] = []byte(`{"schema_version":1}`)
	_ = cfgMgr.SavePrimaryConflict(config.PrimaryConflict{Role: config.ConflictRoleSuperseded, OtherDeviceID: "other-device-id"})

	if err := bootstrap.NewBootstrapper(cfgMgr, mock).PromoteToPrimary(context.Background(), vaultPath, testRemoteTarget, "mac-mini"); err != nil {
		t.Fatalf("PromoteToPrimary failed: %v", err)
	}

	if role, _ := cfgMgr.LoadRole(); role != "primary" {
		t.Errorf("expected role=primary after promotion, got %q", role)
	}
	if _, ok := mock.remoteFiles[conflictPath()]; ok {
		t.Error("expected PRIMARY_CONFLICT.json to be cleared from Google Drive")
	}
	if cached, _ := cfgMgr.LoadPrimaryConflict(); cached != nil {
		t.Errorf("expected the local conflict cache to be cleared, got %+v", cached)
	}

	events, _ := eventlog.NewManager(vaultPath).ReadDeviceLog(deviceID)
	var gotResolved, gotPromoted bool
	for _, e := range events {
		switch e.Event {
		case eventlog.EventConflictResolved:
			gotResolved = true
		case eventlog.EventPromotedToPrimary:
			gotPromoted = true
		}
	}
	if !gotResolved || !gotPromoted {
		t.Errorf("expected both conflict_resolved and promoted_to_primary events, got %+v", events)
	}
}

// TestPromoteToPrimary_LosesInitAsPrimaryRace_DoesNotLogPromotion guards a
// subtle correctness edge: initAsPrimary can itself lose a race to another
// device (its own upload-then-verify step) and convert this device to
// Secondary instead, while still returning a nil error. PromoteToPrimary
// must check the *actual* resulting role before treating this as a
// successful promotion - otherwise it would wrongly clear a real conflict
// marker and log a promotion that never actually happened.
func TestPromoteToPrimary_LosesInitAsPrimaryRace_DoesNotLogPromotion(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "config"))
	deviceID, _ := cfgMgr.GetDeviceID()

	mock := newMockDrive()
	// Simulate another device's marker winning the race: whatever this
	// call uploads, the verification download right after it sees a
	// different device's marker instead.
	raceWinner, err := json.Marshal(bootstrap.PrimaryMarker{SchemaVersion: 1, PrimaryDeviceID: "race-winner-id", PrimaryLabel: "other-mac"})
	if err != nil {
		t.Fatalf("failed to marshal race winner marker: %v", err)
	}
	mock.raceOverride = map[string][]byte{markerPath(): raceWinner}

	if err := bootstrap.NewBootstrapper(cfgMgr, mock).PromoteToPrimary(context.Background(), vaultPath, testRemoteTarget, "mac-mini"); err != nil {
		t.Fatalf("PromoteToPrimary failed: %v", err)
	}

	if role, _ := cfgMgr.LoadRole(); role != "secondary" {
		t.Fatalf("expected this device to end up Secondary after losing the race, got %q", role)
	}

	events, _ := eventlog.NewManager(vaultPath).ReadDeviceLog(deviceID)
	for _, e := range events {
		if e.Event == eventlog.EventPromotedToPrimary {
			t.Errorf("must not log promoted_to_primary when initAsPrimary actually lost its race and this device became Secondary instead")
		}
	}
}
