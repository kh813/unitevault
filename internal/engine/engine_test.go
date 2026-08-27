package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kh813/unitevault/internal/bootstrap"
	"github.com/kh813/unitevault/internal/config"
	"github.com/kh813/unitevault/internal/engine"
	"github.com/kh813/unitevault/internal/scan"
	"github.com/kh813/unitevault/internal/syncedlog"
	"github.com/kh813/unitevault/internal/watch"
)

// mockDriveRunner is an in-memory drive.RcloneRunner: FileExists/
// DownloadFile/UploadFile/DeleteFile operate on a real map so tests can
// seed/inspect remote state (e.g. PRIMARY_MARKER.json,
// PRIMARY_CONFLICT.json) precisely, matching internal/bootstrap's own
// mockDrive test double.
type mockDriveRunner struct {
	remoteFiles  map[string][]byte
	syncCalled   bool
	syncExcludes []string
	copyCalls    []copyCall
}

type copyCall struct {
	Src, Dst string
	Excludes string // strings.Join(excludes, ",") - kept a plain string so copyCall stays comparable with ==
}

func newMockDriveRunner() *mockDriveRunner {
	return &mockDriveRunner{remoteFiles: make(map[string][]byte)}
}

func (m *mockDriveRunner) Sync(ctx context.Context, srcPath, remoteTarget string, excludes ...string) error {
	m.syncCalled = true
	m.syncExcludes = excludes
	return nil
}
func (m *mockDriveRunner) Copy(ctx context.Context, remoteSrc, dstPath string, excludes ...string) error {
	m.copyCalls = append(m.copyCalls, copyCall{Src: remoteSrc, Dst: dstPath, Excludes: strings.Join(excludes, ",")})
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

func (m *failingDriveRunner) Sync(ctx context.Context, srcPath, remoteTarget string, excludes ...string) error {
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

// TestSyncEngine_RunCycle_Primary_PullsSyncFolderBeforePublishing guards
// spec 1.6.4: before merging, Primary must pull other devices' _sync/
// (their pushed logs) from Google Drive via the additive `rclone copy` -
// scoped to _sync/ only, never the whole Vault, so this can never
// overwrite Primary's own just-edited content with a stale Drive copy.
// The existing full-Vault `rclone sync` publish must still happen
// afterward, unchanged.
func TestSyncEngine_RunCycle_Primary_PullsSyncFolderBeforePublishing(t *testing.T) {
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

	wantPull := copyCall{Src: "ObsidianVault:MyVault/_sync", Dst: filepath.Join(vaultPath, "_sync"), Excludes: "state/**"}
	found := false
	for _, c := range mock.copyCalls {
		if c == wantPull {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a Copy pull of %+v, got calls %+v", wantPull, mock.copyCalls)
	}
	if !mock.syncCalled {
		t.Error("expected the existing full-Vault rclone sync publish to still happen")
	}
	if strings.Join(mock.syncExcludes, ",") != "/_sync/state/**" {
		t.Errorf("expected the publish sync to exclude /_sync/state/** (this device's own private scanner bookkeeping), got %v", mock.syncExcludes)
	}
}

// TestSyncEngine_RunCycle_Secondary_PushesAndPullsViaCopy guards spec
// 1.6.4: a Secondary must push its own _sync/ (never the whole Vault) via
// additive `rclone copy`, then pull down whatever Primary already
// published - and must never call the destructive `rclone sync` at all
// (that publish step is Primary-only).
func TestSyncEngine_RunCycle_Secondary_PushesAndPullsViaCopy(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgDir := filepath.Join(tempDir, "config")

	cfgMgr := config.NewConfigManagerWithDir(cfgDir)
	_ = cfgMgr.SaveRole("secondary")
	_ = cfgMgr.SaveConfig(&config.Config{
		VaultPath:       vaultPath,
		RcloneRemote:    "ObsidianVault",
		RclonePath:      "MyVault",
		IntervalSeconds: 120,
	})

	mock := newMockDriveRunner()
	eng := engine.NewSyncEngine(cfgMgr, vaultPath, "windows-test", mock)
	if err := eng.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle failed: %v", err)
	}

	wantPush := copyCall{Src: filepath.Join(vaultPath, "_sync"), Dst: "ObsidianVault:MyVault/_sync", Excludes: "state/**"}
	wantPull := copyCall{Src: "ObsidianVault:MyVault", Dst: vaultPath, Excludes: "/_sync/state/**"}
	var gotPush, gotPull bool
	for _, c := range mock.copyCalls {
		if c == wantPush {
			gotPush = true
		}
		if c == wantPull {
			gotPull = true
		}
	}
	if !gotPush {
		t.Errorf("expected a Copy push of %+v, got calls %+v", wantPush, mock.copyCalls)
	}
	if !gotPull {
		t.Errorf("expected a Copy pull of %+v, got calls %+v", wantPull, mock.copyCalls)
	}
	if mock.syncCalled {
		t.Error("expected a Secondary to never call the destructive rclone sync publish")
	}
}

// TestSyncEngine_RunCycle_Secondary_SkipsDriveWhenRemoteNotConfigured
// guards against calling out to rclone at all before a remote is set up.
func TestSyncEngine_RunCycle_Secondary_SkipsDriveWhenRemoteNotConfigured(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgDir := filepath.Join(tempDir, "config")

	cfgMgr := config.NewConfigManagerWithDir(cfgDir)
	_ = cfgMgr.SaveRole("secondary")
	_ = cfgMgr.SaveConfig(&config.Config{VaultPath: vaultPath, IntervalSeconds: 120})

	mock := newMockDriveRunner()
	eng := engine.NewSyncEngine(cfgMgr, vaultPath, "windows-test", mock)
	if err := eng.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle failed: %v", err)
	}

	if len(mock.copyCalls) != 0 {
		t.Errorf("expected no Copy calls with no remote configured, got %+v", mock.copyCalls)
	}
}

// TestSyncEngine_RunCycle_AutoMergesNonOverlappingEdits is the regression
// test for the core merge bug (spec 3.3.1/3.4): without a real
// reconstructed base, git merge-file reports a conflict for ANY divergent
// content, even edits to completely different lines. Two devices editing
// different lines of the same file, from the same base (e.g. one edited
// offline before syncing - the everyday scenario this app exists for),
// must auto-merge cleanly with no conflict and no pending conflict shown
// to the user.
func TestSyncEngine_RunCycle_AutoMergesNonOverlappingEdits(t *testing.T) {
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
	primaryID, _ := cfgMgr.GetDeviceID()

	mock := newMockDriveRunner()
	seedPrimaryMarker(t, mock, "ObsidianVault:MyVault", primaryID, "mac-test")

	logMgr := syncedlog.NewLogManager(vaultPath)
	base := "line1\nline2\nline3\nline4\nline5\n"
	verA := "line1 CHANGED BY A\nline2\nline3\nline4\nline5\n"
	verB := "line1\nline2\nline3\nline4\nline5 CHANGED BY B\n"

	// Device A created the file, then later edited line 1.
	if err := logMgr.AppendLogEntry(syncedlog.LogEntry{
		Device: "dev-a", Label: "mac-a", Seq: 1, Path: "note.md",
		BaseHash: "", ResultHash: "hash-base", Diff: base, Action: scan.ActionCreate,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if err := logMgr.AppendLogEntry(syncedlog.LogEntry{
		Device: "dev-a", Label: "mac-a", Seq: 2, Path: "note.md",
		BaseHash: "hash-base", ResultHash: "hash-a", Diff: verA, Action: scan.ActionModify,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	// Device B independently edited line 5, from the same base, without
	// having seen A's edit yet (the offline-editing scenario spec 3.3.1
	// describes).
	if err := logMgr.AppendLogEntry(syncedlog.LogEntry{
		Device: "dev-b", Label: "iphone-b", Seq: 1, Path: "note.md",
		BaseHash: "hash-base", ResultHash: "hash-b", Diff: verB, Action: scan.ActionModify,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	eng := engine.NewSyncEngine(cfgMgr, vaultPath, "mac-test", mock)
	if err := eng.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(vaultPath, "note.md"))
	if err != nil {
		t.Fatalf("failed to read merged file: %v", err)
	}
	want := "line1 CHANGED BY A\nline2\nline3\nline4\nline5 CHANGED BY B\n"
	if string(got) != want {
		t.Errorf("expected both non-overlapping edits auto-merged:\nwant: %q\ngot:  %q", want, string(got))
	}

	pending, err := cfgMgr.LoadPendingConflicts()
	if err != nil {
		t.Fatalf("LoadPendingConflicts failed: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected no pending conflicts for a clean auto-merge, got %+v", pending)
	}
}

// TestSyncEngine_RunCycle_RecordsGenuineConflictAsPending is the
// companion case: two devices editing the SAME line from the same base is
// a real conflict. RunCycle must not silently record it as resolved (the
// original bug) - it must show up in pending_conflicts.json with both
// devices' actual content (never a placeholder string), and the Vault
// file should still contain the conflict markers so opening it directly
// in Obsidian shows something meaningful.
func TestSyncEngine_RunCycle_RecordsGenuineConflictAsPending(t *testing.T) {
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
	primaryID, _ := cfgMgr.GetDeviceID()

	mock := newMockDriveRunner()
	seedPrimaryMarker(t, mock, "ObsidianVault:MyVault", primaryID, "mac-test")

	logMgr := syncedlog.NewLogManager(vaultPath)
	base := "line1\nline2\nline3\n"
	verA := "line1 CHANGED BY A\nline2\nline3\n"
	verB := "line1 CHANGED BY B\nline2\nline3\n"

	if err := logMgr.AppendLogEntry(syncedlog.LogEntry{
		Device: "dev-a", Label: "mac-a", Seq: 1, Path: "note.md",
		BaseHash: "", ResultHash: "hash-base", Diff: base, Action: scan.ActionCreate,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if err := logMgr.AppendLogEntry(syncedlog.LogEntry{
		Device: "dev-a", Label: "mac-a", Seq: 2, Path: "note.md",
		BaseHash: "hash-base", ResultHash: "hash-a", Diff: verA, Action: scan.ActionModify,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if err := logMgr.AppendLogEntry(syncedlog.LogEntry{
		Device: "dev-b", Label: "iphone-b", Seq: 1, Path: "note.md",
		BaseHash: "hash-base", ResultHash: "hash-b", Diff: verB, Action: scan.ActionModify,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	eng := engine.NewSyncEngine(cfgMgr, vaultPath, "mac-test", mock)
	if err := eng.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(vaultPath, "note.md"))
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if !strings.Contains(string(got), "<<<<<<<") {
		t.Errorf("expected conflict markers left in the Vault file, got %q", string(got))
	}

	pending, err := cfgMgr.LoadPendingConflicts()
	if err != nil {
		t.Fatalf("LoadPendingConflicts failed: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected exactly 1 pending conflict, got %d: %+v", len(pending), pending)
	}
	pc := pending[0]
	if pc.RelPath != "note.md" {
		t.Errorf("expected pending conflict for note.md, got %q", pc.RelPath)
	}
	versionsByDevice := map[string]string{}
	for _, v := range pc.Versions {
		versionsByDevice[v.DeviceID] = v.Content
	}
	if versionsByDevice["dev-a"] != verA || versionsByDevice["dev-b"] != verB {
		t.Errorf("expected both devices' real content recorded (no placeholder strings), got %+v", versionsByDevice)
	}

	// The critical safety property: neither device's log should have
	// gained a new entry claiming this was resolved.
	aEntries, _ := logMgr.ReadDeviceLog("dev-a")
	bEntries, _ := logMgr.ReadDeviceLog("dev-b")
	if len(aEntries) != 2 {
		t.Errorf("expected dev-a's log to still have exactly 2 entries (no fake resolution appended), got %d", len(aEntries))
	}
	if len(bEntries) != 1 {
		t.Errorf("expected dev-b's log to still have exactly 1 entry (no fake resolution appended), got %d", len(bEntries))
	}
}

// TestSyncEngine_RunCycle_PrunesStalePendingConflict guards the
// self-healing behavior (spec 3.3.2): a previously-recorded conflict whose
// file no longer matches what was written when it was detected (e.g. the
// user manually resolved it in Obsidian) must be dropped on the next
// cycle rather than nagging about a conflict that's already gone.
func TestSyncEngine_RunCycle_PrunesStalePendingConflict(t *testing.T) {
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
	primaryID, _ := cfgMgr.GetDeviceID()

	mock := newMockDriveRunner()
	seedPrimaryMarker(t, mock, "ObsidianVault:MyVault", primaryID, "mac-test")

	if err := cfgMgr.SavePendingConflicts([]config.PendingConflict{
		{
			RelPath:     "already-resolved.md",
			DetectedAt:  "2026-08-27T10:00:00+09:00",
			WrittenHash: "stale-hash-that-wont-match-anything",
			Versions: []config.PendingConflictVersion{
				{DeviceID: "dev-a", Label: "mac-a", Content: "A"},
				{DeviceID: "dev-b", Label: "iphone-b", Content: "B"},
			},
		},
	}); err != nil {
		t.Fatalf("failed to seed stale pending conflict: %v", err)
	}

	eng := engine.NewSyncEngine(cfgMgr, vaultPath, "mac-test", mock)
	if err := eng.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle failed: %v", err)
	}

	pending, err := cfgMgr.LoadPendingConflicts()
	if err != nil {
		t.Fatalf("LoadPendingConflicts failed: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected the stale pending conflict to be pruned, got %+v", pending)
	}
}

// TestResolvePendingConflict guards conflict resolution via the GUI
// device-picker (spec 3.3.2): the chosen device's real content (never a
// placeholder string - the original bug) gets written to the Vault file
// and recorded as a new log entry, and the conflict is removed from the
// pending set.
func TestResolvePendingConflict(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "config"))

	conflict := config.PendingConflict{
		RelPath:     "note.md",
		DetectedAt:  "2026-08-27T10:00:00+09:00",
		WrittenHash: "irrelevant-here",
		Versions: []config.PendingConflictVersion{
			{DeviceID: "dev-a", Label: "mac-a", Content: "A's content\n"},
			{DeviceID: "dev-b", Label: "iphone-b", Content: "B's content\n"},
		},
	}
	if err := cfgMgr.SavePendingConflicts([]config.PendingConflict{conflict}); err != nil {
		t.Fatalf("failed to seed pending conflict: %v", err)
	}

	if err := engine.ResolvePendingConflict(cfgMgr, vaultPath, conflict, "dev-b", "primary-device", "mac-test"); err != nil {
		t.Fatalf("ResolvePendingConflict failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(vaultPath, "note.md"))
	if err != nil {
		t.Fatalf("failed to read resolved file: %v", err)
	}
	if string(got) != "B's content\n" {
		t.Errorf("expected the chosen device's real content written, got %q", string(got))
	}

	pending, err := cfgMgr.LoadPendingConflicts()
	if err != nil {
		t.Fatalf("LoadPendingConflicts failed: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected the resolved conflict to be removed from pending, got %+v", pending)
	}

	entries, err := syncedlog.NewLogManager(vaultPath).ReadDeviceLog("primary-device")
	if err != nil {
		t.Fatalf("ReadDeviceLog failed: %v", err)
	}
	if len(entries) != 1 || entries[0].Diff != "B's content\n" {
		t.Errorf("expected a new log entry recording the resolution with real content, got %+v", entries)
	}
}

// TestResolvePendingConflict_UnknownDevice guards against silently
// accepting a chosen device ID that isn't actually one of the conflict's
// recorded versions (e.g. a stale/mismatched GUI selection).
func TestResolvePendingConflict_UnknownDevice(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "config"))

	conflict := config.PendingConflict{
		RelPath: "note.md",
		Versions: []config.PendingConflictVersion{
			{DeviceID: "dev-a", Label: "mac-a", Content: "A"},
		},
	}
	if err := engine.ResolvePendingConflict(cfgMgr, vaultPath, conflict, "dev-does-not-exist", "primary-device", "mac-test"); err == nil {
		t.Error("expected an error when the chosen device isn't one of the conflict's recorded versions")
	}
}

// TestSyncEngine_RunCycle_WithWatcher_DetectsEditsAcrossCycles guards spec
// 1.6.5: attaching a real watch.Watcher via SetWatcher must not break
// change detection - an edit must still eventually get logged, now via a
// targeted ScanPaths rescan of the watcher's drained paths rather than a
// full ScanVault(), exactly like the watcher-less multi-cycle scan pipeline
// test in internal/scan already guards for the no-watcher case.
func TestSyncEngine_RunCycle_WithWatcher_DetectsEditsAcrossCycles(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgDir := filepath.Join(tempDir, "config")

	cfgMgr := config.NewConfigManagerWithDir(cfgDir)
	_ = cfgMgr.SaveRole("primary")
	_ = cfgMgr.SaveConfig(&config.Config{
		VaultPath:    vaultPath,
		RcloneRemote: "ObsidianVault",
		RclonePath:   "MyVault",
	})
	deviceID, _ := cfgMgr.GetDeviceID()

	if err := os.MkdirAll(vaultPath, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	notePath := filepath.Join(vaultPath, "note.md")
	if err := os.WriteFile(notePath, []byte("version 1\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	w, err := watch.New(vaultPath)
	if err != nil {
		t.Fatalf("watch.New failed: %v", err)
	}
	defer w.Close()

	mock := newMockDriveRunner()
	seedPrimaryMarker(t, mock, "ObsidianVault:MyVault", deviceID, "mac-test")

	eng := engine.NewSyncEngine(cfgMgr, vaultPath, "mac-test", mock)
	eng.SetWatcher(w)
	ctx := context.Background()

	// Two cycles with stable content are needed before version 1 becomes
	// the confirmed baseline, matching
	// TestScanner_MultiCycleIntegration_ModifyIsCorrectlyDetected's own
	// reasoning in internal/scan.
	if err := eng.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle failed: %v", err)
	}
	if err := eng.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle failed: %v", err)
	}

	if err := os.WriteFile(notePath, []byte("version 2\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	// Let the watcher actually observe the edit before the next cycle
	// drains it - fsnotify delivery is asynchronous.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, statErr := scan.CalculateNormalizedHash(notePath); statErr == nil && got != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	var modifyEntries []syncedlog.LogEntry
	for i := 0; i < 5 && len(modifyEntries) == 0; i++ {
		if err := eng.RunCycle(ctx); err != nil {
			t.Fatalf("RunCycle failed: %v", err)
		}
		entries, err := syncedlog.NewLogManager(vaultPath).ReadDeviceLog(deviceID)
		if err != nil {
			t.Fatalf("ReadDeviceLog failed: %v", err)
		}
		for _, e := range entries {
			if e.Path == "note.md" && e.Action == scan.ActionModify {
				modifyEntries = append(modifyEntries, e)
			}
		}
	}

	if len(modifyEntries) != 1 {
		t.Fatalf("expected exactly 1 modify entry for note.md across settling cycles with a watcher attached, got %d", len(modifyEntries))
	}
	if modifyEntries[0].Diff != "version 2\n" {
		t.Errorf("expected the logged content to be the new version, got %q", modifyEntries[0].Diff)
	}
}

// TestSyncEngine_RunCycle_WithWatcher_FirstCycleStillSeesPreexistingFile guards
// against relying on a watcher's hints before it's had any chance to
// observe anything - the very first cycle must behave exactly like the
// no-watcher case (a full ScanVault()), so a file already present when the
// engine starts is never missed just because it predates watcher
// attachment.
func TestSyncEngine_RunCycle_WithWatcher_FirstCycleStillSeesPreexistingFile(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgDir := filepath.Join(tempDir, "config")

	cfgMgr := config.NewConfigManagerWithDir(cfgDir)
	_ = cfgMgr.SaveRole("primary")
	_ = cfgMgr.SaveConfig(&config.Config{
		VaultPath:    vaultPath,
		RcloneRemote: "ObsidianVault",
		RclonePath:   "MyVault",
	})
	deviceID, _ := cfgMgr.GetDeviceID()

	if err := os.MkdirAll(vaultPath, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	// Written *before* the watcher exists, so the watcher itself can never
	// have observed it - only a full first-cycle scan can.
	if err := os.WriteFile(filepath.Join(vaultPath, "note.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	w, err := watch.New(vaultPath)
	if err != nil {
		t.Fatalf("watch.New failed: %v", err)
	}
	defer w.Close()

	mock := newMockDriveRunner()
	seedPrimaryMarker(t, mock, "ObsidianVault:MyVault", deviceID, "mac-test")

	eng := engine.NewSyncEngine(cfgMgr, vaultPath, "mac-test", mock)
	eng.SetWatcher(w)

	// Two settling cycles for debounce+confirmation, matching the same
	// pattern used elsewhere in this file.
	if err := eng.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle failed: %v", err)
	}
	if err := eng.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle failed: %v", err)
	}

	entries, err := syncedlog.NewLogManager(vaultPath).ReadDeviceLog(deviceID)
	if err != nil {
		t.Fatalf("ReadDeviceLog failed: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Path == "note.md" && e.Action == scan.ActionCreate {
			found = true
		}
	}
	if !found {
		t.Errorf("expected note.md to be logged as a create despite predating the watcher, got entries %+v", entries)
	}
}
