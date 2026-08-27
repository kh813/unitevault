package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kh813/unitevault/internal/bootstrap"
	"github.com/kh813/unitevault/internal/config"
	"github.com/kh813/unitevault/internal/drive"
	"github.com/kh813/unitevault/internal/eventlog"
	"github.com/kh813/unitevault/internal/merge"
	"github.com/kh813/unitevault/internal/scan"
	"github.com/kh813/unitevault/internal/syncedlog"
)

type SyncEngine struct {
	cfgMgr    *config.ConfigManager
	scanner   *scan.Scanner
	logMgr    *syncedlog.LogManager
	drive     drive.RcloneRunner
	vaultPath string
	label     string
}

func NewSyncEngine(cfgMgr *config.ConfigManager, vaultPath string, label string, driveRunner drive.RcloneRunner) *SyncEngine {
	if driveRunner == nil {
		driveRunner = drive.NewClient(filepath.Join(vaultPath, "_sync", "engine.log"))
	}
	return &SyncEngine{
		cfgMgr:    cfgMgr,
		scanner:   scan.NewScanner(vaultPath),
		logMgr:    syncedlog.NewLogManager(vaultPath),
		drive:     driveRunner,
		vaultPath: vaultPath,
		label:     label,
	}
}

// RunCycle executes a single sync iteration (scan -> log -> merge -> sync drive)
func (e *SyncEngine) RunCycle(ctx context.Context) error {
	deviceID, err := e.cfgMgr.GetDeviceID()
	if err != nil {
		return fmt.Errorf("failed to get device ID: %w", err)
	}

	// Prune this device's own old application-event-log entries (spec
	// 3.2.1) - purely local housekeeping on a file only this device ever
	// writes to, so it's safe regardless of role and doesn't need to block
	// the rest of the cycle on failure.
	_ = eventlog.NewManager(e.vaultPath).PruneOwnEvents(deviceID, eventlog.DefaultRetentionDays)

	role, err := e.cfgMgr.LoadRole()
	if err != nil || role == "" {
		cfg, err := e.cfgMgr.LoadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		remoteTarget := fmt.Sprintf("%s:%s", cfg.RcloneRemote, cfg.RclonePath)
		bootstrapper := bootstrap.NewBootstrapper(e.cfgMgr, e.drive)
		role, err = bootstrapper.InitializeNode(ctx, e.vaultPath, remoteTarget, e.label)
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
	}

	if err := os.MkdirAll(e.vaultPath, 0755); err != nil {
		return fmt.Errorf("failed to create vault dir: %w", err)
	}

	// 1. Scan Vault
	currState, err := e.scanner.ScanVault()
	if err != nil {
		return fmt.Errorf("vault scan failed: %w", err)
	}

	lastState, err := e.scanner.LoadLastScan()
	if err != nil {
		return fmt.Errorf("failed to load last scan: %w", err)
	}

	// Debounce check
	stableState := scan.DebounceFilter(lastState, currState)
	changes := scan.DetectChanges(lastState, stableState)

	// Save scan state
	if err := e.scanner.SaveScanState(currState); err != nil {
		return fmt.Errorf("failed to save scan state: %w", err)
	}

	// 2. Log changes for this device
	for _, ch := range changes {
		seq, err := e.logMgr.GetNextSeq(deviceID)
		if err != nil {
			return fmt.Errorf("failed to get next seq: %w", err)
		}

		// Stash this change's resulting content (empty for a delete, which
		// has none) in the log entry itself (spec 3.4) - a later merge
		// needs to look up "what did the content look like at hash X"
		// across every device's log to reconstruct a real 3-way merge base,
		// and this is the only place that content is ever recorded. A
		// failed read (e.g. the file was removed again before this line
		// runs) just leaves it empty rather than failing the whole cycle -
		// base reconstruction already treats a missing snapshot as "fall
		// back to a real conflict" (safe default, see mergeAndTrackConflicts).
		var content string
		if ch.Action != scan.ActionDelete {
			if data, err := os.ReadFile(filepath.Join(e.vaultPath, ch.Path)); err == nil {
				content = string(scan.NormalizeLF(data))
			}
		}

		entry := syncedlog.CreateLogEntryFromChange(deviceID, e.label, seq, ch, content)
		if err := e.logMgr.AppendLogEntry(entry); err != nil {
			return fmt.Errorf("failed to append log entry: %w", err)
		}
	}

	// Secondary node stops here (only records local log)
	if role == "secondary" {
		return nil
	}

	// Primary node: re-confirm every cycle that this device is still the
	// Primary PRIMARY_MARKER.json actually names, and that no unresolved
	// multi-Primary conflict is open (spec 3.6.1.4) - role above is only
	// ever set once (at InitializeNode/PromoteToPrimary time) and never
	// otherwise re-checked, so without this a device superseded elsewhere
	// (e.g. via Settings > "Promote to Primary...") would keep merging and
	// pushing conflicting rclone syncs indefinitely.
	primaryCfg, err := e.cfgMgr.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config for primary status check: %w", err)
	}
	remoteTarget := fmt.Sprintf("%s:%s", primaryCfg.RcloneRemote, primaryCfg.RclonePath)
	bootstrapper := bootstrap.NewBootstrapper(e.cfgMgr, e.drive)
	proceed, err := bootstrapper.VerifyPrimaryStatus(ctx, e.vaultPath, remoteTarget, deviceID, e.label)
	if err != nil {
		return fmt.Errorf("primary status check failed: %w", err)
	}
	if !proceed {
		return nil
	}

	// Primary node: Perform 3-way/N-way merges across device logs
	latestByPath, err := e.logMgr.LatestEntryByPath()
	if err != nil {
		return fmt.Errorf("failed to get latest log entries: %w", err)
	}
	if err := e.mergeAndTrackConflicts(latestByPath); err != nil {
		return err
	}

	// 3. Mirror to Google Drive via rclone sync
	cfg, err := e.cfgMgr.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config for drive sync: %w", err)
	}

	if cfg.RcloneRemote != "" && cfg.RclonePath != "" {
		remoteTarget := fmt.Sprintf("%s:%s", cfg.RcloneRemote, cfg.RclonePath)
		syncErr := e.drive.Sync(ctx, e.vaultPath, remoteTarget)

		// Recorded regardless of outcome so the Settings window can surface
		// "last synced" / "last sync failed" without needing a live
		// connection to this (possibly not currently running) daemon loop.
		status := config.DriveSyncStatus{Time: time.Now().Format(time.RFC3339), Success: syncErr == nil}
		if syncErr != nil {
			status.Error = syncErr.Error()
		}
		_ = e.cfgMgr.SaveDriveSyncStatus(status)

		if syncErr != nil {
			return fmt.Errorf("rclone sync failed: %w", syncErr)
		}
	}

	return nil
}

// mergeAndTrackConflicts runs N-way merges (spec 3.3) for every path with
// concurrent changes across devices, and reconciles the set of unresolved
// genuine conflicts (spec 3.3.2). Only ever called for the Primary device -
// merging never runs on a Secondary.
//
// Each device's own content at the time of its change is read from its log
// entry (the Diff field - spec 3.4), never from the current Vault file:
// the Vault file holds exactly one state at a time, so re-reading it once
// per device would hand every "version" the same content and make N-way
// merge meaningless. The merge base is reconstructed the same way, via
// FindCommonBaseHash + a lookup across every device's log for a matching
// result_hash - a real base (rather than empty) is essential, since
// git merge-file falsely reports a conflict for non-overlapping edits when
// given no base at all.
func (e *SyncEngine) mergeAndTrackConflicts(latestByPath map[string]map[string]syncedlog.LogEntry) error {
	// Drop any previously-pending conflict whose file has since changed
	// (the recorded hash no longer matches what's on disk) - the user
	// resolved it, manually in Obsidian or otherwise, so it shouldn't keep
	// being surfaced (spec 3.3.2).
	existing, err := e.cfgMgr.LoadPendingConflicts()
	if err != nil {
		return fmt.Errorf("failed to load pending conflicts: %w", err)
	}
	pendingByPath := make(map[string]config.PendingConflict, len(existing))
	for _, pc := range existing {
		fullPath := filepath.Join(e.vaultPath, pc.RelPath)
		if currentHash, err := scan.CalculateNormalizedHash(fullPath); err == nil && currentHash == pc.WrittenHash {
			pendingByPath[pc.RelPath] = pc
		}
	}

	allDeviceLogs, err := e.logMgr.ReadAllDeviceLogs()
	if err != nil {
		return fmt.Errorf("failed to read device logs for merge base lookup: %w", err)
	}

	for relPath, devEntries := range latestByPath {
		if len(devEntries) <= 1 {
			continue // No concurrent changes across multiple devices
		}

		var versions []merge.DeviceVersion
		for devID, entry := range devEntries {
			if entry.Diff == "" {
				continue // no content snapshot for this entry (e.g. its latest action was a delete) - can't include it in the merge
			}
			versions = append(versions, merge.DeviceVersion{
				DeviceID: devID,
				Content:  entry.Diff,
				BaseHash: entry.BaseHash,
			})
		}
		if len(versions) <= 1 {
			continue
		}

		baseContent := ""
		if baseHash := merge.FindCommonBaseHash(devEntries); baseHash != "" {
			if content, found := merge.FindContentByHash(allDeviceLogs, baseHash); found {
				baseContent = content
			}
		}

		res, err := merge.NWayMerge(baseContent, versions)
		if err != nil {
			return fmt.Errorf("merge error for %s: %w", relPath, err)
		}

		fullPath := filepath.Join(e.vaultPath, relPath)
		if err := os.WriteFile(fullPath, []byte(res.MergedContent), 0644); err != nil {
			return fmt.Errorf("failed to write merged file: %w", err)
		}

		if !res.HasConflict {
			// Clean auto-merge - any earlier conflict recorded for this
			// path has just been superseded.
			delete(pendingByPath, relPath)
			continue
		}

		writtenHash, err := scan.CalculateNormalizedHash(fullPath)
		if err != nil {
			return fmt.Errorf("failed to hash conflict-marked file %s: %w", relPath, err)
		}
		conflictVersions := make([]config.PendingConflictVersion, 0, len(versions))
		for _, v := range versions {
			conflictVersions = append(conflictVersions, config.PendingConflictVersion{
				DeviceID: v.DeviceID,
				Label:    devEntries[v.DeviceID].Label,
				Content:  v.Content,
			})
		}
		pendingByPath[relPath] = config.PendingConflict{
			RelPath:     relPath,
			DetectedAt:  time.Now().Format(time.RFC3339),
			WrittenHash: writtenHash,
			Versions:    conflictVersions,
		}
	}

	pending := make([]config.PendingConflict, 0, len(pendingByPath))
	for _, pc := range pendingByPath {
		pending = append(pending, pc)
	}
	return e.cfgMgr.SavePendingConflicts(pending)
}

// ResolvePendingConflict applies chosenDeviceID's recorded content for
// conflict (spec 3.3.2), recording the resolution under
// resolverDeviceID/resolverLabel (normally the Primary device's own
// identity, since that's the only device that ever holds pending
// conflicts), and removes conflict from the locally-tracked pending set.
// A package-level function rather than a SyncEngine method since the GUI
// resolves conflicts outside of any RunCycle invocation.
func ResolvePendingConflict(cfgMgr *config.ConfigManager, vaultPath string, conflict config.PendingConflict, chosenDeviceID, resolverDeviceID, resolverLabel string) error {
	var content string
	found := false
	for _, v := range conflict.Versions {
		if v.DeviceID == chosenDeviceID {
			content = v.Content
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("device %q is not one of the recorded versions for %s", chosenDeviceID, conflict.RelPath)
	}

	if err := merge.ApplyResolution(syncedlog.NewLogManager(vaultPath), vaultPath, conflict.RelPath, content, resolverDeviceID, resolverLabel); err != nil {
		return err
	}

	remaining, err := cfgMgr.LoadPendingConflicts()
	if err != nil {
		return err
	}
	kept := remaining[:0]
	for _, pc := range remaining {
		if pc.RelPath != conflict.RelPath {
			kept = append(kept, pc)
		}
	}
	return cfgMgr.SavePendingConflicts(kept)
}

// RunDaemon runs RunCycle in a continuous loop with the specified interval until ctx is canceled.
func (e *SyncEngine) RunDaemon(ctx context.Context, interval int) error {
	if interval <= 0 {
		interval = config.DefaultIntervalSeconds
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	fmt.Printf("[%s] Starting UniteVault daemon (interval: %ds)...\n", time.Now().Format("15:04:05"), interval)

	// Run first cycle immediately
	if err := e.RunCycle(ctx); err != nil {
		fmt.Printf("[%s] Cycle error: %v\n", time.Now().Format("15:04:05"), err)
	}

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("[%s] Stopping UniteVault daemon...\n", time.Now().Format("15:04:05"))
			return ctx.Err()
		case <-ticker.C:
			if err := e.RunCycle(ctx); err != nil {
				fmt.Printf("[%s] Cycle error: %v\n", time.Now().Format("15:04:05"), err)
			}
		}
	}
}
