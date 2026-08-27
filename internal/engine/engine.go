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
	"github.com/kh813/unitevault/internal/watch"
)

// watcherFullScanEvery bounds how long SyncEngine can go relying solely on
// a Watcher's hints before falling back to a full ScanVault() (spec 1.6.5)
// - a periodic reconciliation safety net against any watch event the OS
// failed to deliver (dropped, coalesced, or simply unsupported for a given
// filesystem).
const watcherFullScanEvery = 30

type SyncEngine struct {
	cfgMgr    *config.ConfigManager
	scanner   *scan.Scanner
	logMgr    *syncedlog.LogManager
	drive     drive.RcloneRunner
	vaultPath string
	label     string

	watcher    *watch.Watcher
	cycleCount int
	tickIndex  int
}

// externalSyncTask identifies one of the external sync destinations a
// Primary device round-robins across, one per tick (spec 1.6.5). Google
// Drive and the iCloud Bridge are deliberately never both synced within the
// same RunCycle when both are configured - each instead gets roughly every
// other tick, while whichever one is the *only* one configured gets every
// tick (see primaryExternalTasks).
type externalSyncTask int

const (
	taskDrive externalSyncTask = iota
	taskBridge
)

// primaryExternalTasks returns, in a stable order, the external sync tasks
// a Primary device currently has enough configuration to actually run -
// only these ever get a turn in RunCycle's round robin, so an unconfigured
// destination is never selected and simply never runs (matching the old,
// pre-alternation behavior for the single-destination case).
func primaryExternalTasks(cfg *config.Config) []externalSyncTask {
	var tasks []externalSyncTask
	if cfg.RcloneRemote != "" && cfg.RclonePath != "" {
		tasks = append(tasks, taskDrive)
	}
	if cfg.ICloudBridgePath != "" {
		tasks = append(tasks, taskBridge)
	}
	return tasks
}

// SetWatcher attaches an OS-level file watcher (spec 1.6.5) that RunCycle
// will use, when present, to scan only the paths it reports changed rather
// than the whole Vault - purely a performance optimization; a nil watcher
// (the default) makes RunCycle behave exactly as before, always scanning
// the whole Vault. A setter rather than a NewSyncEngine parameter so every
// existing construction call site (tests included) keeps working
// unchanged.
func (e *SyncEngine) SetWatcher(w *watch.Watcher) {
	e.watcher = w
}

// scanStep produces this cycle's raw scan state, preferring a targeted
// rescan of the attached Watcher's drained paths over a full ScanVault()
// when possible (spec 1.6.5). Falls back to a full scan when: no watcher is
// attached, this is the first cycle (cycleCount == 1, so there's no prior
// watcher activity to have accumulated anything meaningful yet), or the
// periodic reconciliation point (watcherFullScanEvery) has come around.
func (e *SyncEngine) scanStep(lastRawState *scan.ScanState) (*scan.ScanState, error) {
	if e.watcher == nil || e.cycleCount <= 1 || e.cycleCount%watcherFullScanEvery == 0 {
		return e.scanner.ScanVault()
	}

	paths := e.watcher.Drain()
	if paths == nil {
		// The watcher observed nothing since the last cycle - carry the
		// previous raw scan forward untouched rather than re-hashing
		// everything for no reason.
		return lastRawState, nil
	}
	return e.scanner.ScanPaths(lastRawState, paths)
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

	// lastRawState is purely the debounce comparison baseline (was this
	// file's hash the same the last time we looked, regardless of whether
	// that was ever confirmed/logged) - never used to decide what changed.
	// It also doubles as ScanPaths' starting point below, since a targeted
	// rescan needs a baseline to carry every unchanged path forward from.
	lastRawState, err := e.scanner.LoadLastScan()
	if err != nil {
		return fmt.Errorf("failed to load last scan: %w", err)
	}

	// 1. Scan Vault - via the OS-level watcher's hints when one's attached
	// and due, otherwise a full scan (spec 1.6.5). See watcherFullScanEvery
	// and Watcher's own doc comment for why a full scan is still forced
	// periodically even with a watcher attached.
	e.cycleCount++
	currState, err := e.scanStep(lastRawState)
	if err != nil {
		return fmt.Errorf("vault scan failed: %w", err)
	}
	stableState := scan.DebounceFilter(lastRawState, currState)

	// confirmedState is what DetectChanges actually compares against - see
	// Scanner.ConfirmedStateFilePath's doc comment for why this must be a
	// separate, independently-advanced baseline from lastRawState above.
	// ReconcileForDetection (not stableState directly) tells apart a
	// genuine deletion from a file simply mid-edit, not yet confirmed
	// stable - see its own doc comment.
	confirmedState, err := e.scanner.LoadConfirmedState()
	if err != nil {
		return fmt.Errorf("failed to load confirmed scan state: %w", err)
	}
	changes := scan.DetectChanges(confirmedState, scan.ReconcileForDetection(confirmedState, currState, stableState))

	if err := e.scanner.SaveScanState(currState); err != nil {
		return fmt.Errorf("failed to save scan state: %w", err)
	}
	if err := e.scanner.SaveConfirmedState(scan.ApplyChangesToState(confirmedState, changes)); err != nil {
		return fmt.Errorf("failed to save confirmed scan state: %w", err)
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

	// Secondary node: push this device's own _sync/ (its log and nothing
	// else - the log entries already carry full content, spec 3.4, so
	// there's no need to push raw Vault files too) to Google Drive via
	// `rclone copy` (additive only, never `sync` - must never delete
	// anything else already there), then pull down whatever Primary has
	// already merged and published (spec 1.6.4). Secondary never merges
	// itself - that's Primary-only.
	//
	// Known limitations (v1, see unitevault-todo.md Phase 16): the pull
	// uses `copy`, not `sync`, so a file Primary has since deleted
	// upstream won't be removed locally here - and in the narrow window
	// where a local edit hasn't stabilized/logged yet, this pull could in
	// principle overwrite it before this device's own next scan ever
	// captures it. Accepted for now in favor of never risking a
	// destructive sync against a Vault folder Obsidian may be actively
	// editing.
	if role == "secondary" {
		secondaryCfg, err := e.cfgMgr.LoadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config for secondary drive sync: %w", err)
		}
		if secondaryCfg.RcloneRemote != "" && secondaryCfg.RclonePath != "" {
			remoteTarget := fmt.Sprintf("%s:%s", secondaryCfg.RcloneRemote, secondaryCfg.RclonePath)
			// state/** (Scanner.ConfirmedStateFilePath's directory) is this
			// device's own private scanner bookkeeping - excluded so
			// pushing it never lets another device's pull silently
			// overwrite that other device's own copy of the same relative
			// path with this device's state instead.
			if err := e.drive.Copy(ctx, filepath.Join(e.vaultPath, "_sync"), remoteTarget+"/_sync", "state/**"); err != nil {
				return fmt.Errorf("failed to push local changes to Google Drive: %w", err)
			}
			// _sync/state/** excluded here so this pull can never
			// overwrite this device's own scanner bookkeeping with
			// Primary's.
			if err := e.drive.Copy(ctx, remoteTarget, e.vaultPath, "/_sync/state/**"); err != nil {
				return fmt.Errorf("failed to pull latest changes from Google Drive: %w", err)
			}

			// `rclone copy` above is additive-only, so a file Primary has
			// since deleted wouldn't otherwise ever be removed here -
			// reconcile against Primary's published manifest instead
			// (spec 1.6.4), best-effort (never fails the cycle over it).
			if manifest, err := LoadManifest(e.vaultPath); err == nil {
				_, _ = ApplyManifestDeletions(e.vaultPath, manifest)
			}
		}
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

	// Primary node: alternate which external sync destination (Google
	// Drive, iCloud Bridge) gets a turn this tick, round-robin across
	// whichever are actually configured (spec 1.6.5) - the local
	// scan/log/merge above and below always runs every tick regardless.
	// Whichever destination is the *only* one configured still gets every
	// tick, exactly like the pre-alternation behavior.
	tasks := primaryExternalTasks(primaryCfg)
	var task externalSyncTask
	runningExternalTask := len(tasks) > 0
	if runningExternalTask {
		task = tasks[e.tickIndex%len(tasks)]
		e.tickIndex++
	}
	runDrive := runningExternalTask && task == taskDrive
	runBridge := runningExternalTask && task == taskBridge

	// Primary node: pull every other device's pushed _sync/ (their logs -
	// spec 1.6.4) from Google Drive before merging, so
	// mergeAndTrackConflicts can see Secondaries' contributions. Scoped to
	// _sync/ only, via `rclone copy` (additive), so this can never
	// overwrite this device's own just-edited Vault content with a stale
	// Drive copy - only the log entries flow in, not raw files.
	if runDrive {
		// state/** excluded so this pull can never overwrite Primary's own
		// scanner bookkeeping with whichever other device pushed last.
		if err := e.drive.Copy(ctx, remoteTarget+"/_sync", filepath.Join(e.vaultPath, "_sync"), "state/**"); err != nil {
			return fmt.Errorf("failed to pull other devices' changes from Google Drive: %w", err)
		}
	}

	// Primary node: pull in changes from the iCloud Bridge folder (spec
	// 1.6.3), on this destination's turn - best-effort: a problem with the
	// (optional) Bridge must never block the Google Drive sync that runs
	// on its own turn.
	if runBridge {
		bridgeDeviceID, err := e.cfgMgr.GetOrCreateBridgeDeviceID()
		if err == nil {
			_, _ = ScanBridgeAndLog(e.vaultPath, primaryCfg.ICloudBridgePath, bridgeDeviceID, "iCloud Bridge")
		}
	}

	// Primary node: Perform 3-way/N-way merges across device logs - always
	// runs every tick, regardless of which (if any) external destination
	// got a turn, using whatever's currently in _sync/ (freshly pulled
	// this tick, or carried over from a previous one).
	latestByPath, err := e.logMgr.LatestEntryByPath()
	if err != nil {
		return fmt.Errorf("failed to get latest log entries: %w", err)
	}
	if err := e.mergeAndTrackConflicts(deviceID, latestByPath); err != nil {
		return err
	}

	// Mirror the merged result back out to the iCloud Bridge folder, so
	// the next iCloud sync carries it to iPhone/iPad - again best-effort,
	// and only on this destination's turn.
	if runBridge {
		_ = MirrorVaultToBridge(e.vaultPath, primaryCfg.ICloudBridgePath)
	}

	// 3. Mirror to Google Drive via rclone sync, only on this
	// destination's turn.
	if runDrive {
		// Publish "what should exist" (spec 1.6.4) before the mirror below,
		// so Secondaries can tell a genuine deletion apart from their own
		// not-yet-merged local creations despite pulling non-destructively.
		// Best-effort - a failed manifest write must never block the
		// actual Drive sync that follows.
		_ = PublishManifest(e.vaultPath)

		// /_sync/state/** excluded so Primary's own private scanner
		// bookkeeping is never published to Drive at all - keeping it
		// local-only is what lets every other device's pull safely
		// exclude the same pattern without missing anything real.
		syncErr := e.drive.Sync(ctx, e.vaultPath, remoteTarget, "/_sync/state/**")

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

// applySingleDeviceChange handles a path exactly one device has ever logged
// a change for - the "1台のみが分岐点から変更している場合は、自動的にそ
// の変更を採用する" rule (spec 3.3). If that lone device is selfDeviceID
// (this Primary), it's a no-op: the Vault file already reflects it. If it's
// any other device (a Secondary or the iCloud Bridge virtual device),
// applies its logged action directly to the Vault - a delete removes the
// file, anything else (create/modify/rename) writes the logged content.
//
// A real, previously-shipped bug came from mergeAndTrackConflicts
// unconditionally skipping every path with fewer than 2 concurrent log
// entries: a file a Secondary alone created or edited, with Primary never
// having touched it, was silently never written into Primary's Vault at
// all - meaning it also never reached Google Drive's published mirror or
// any other Secondary, despite the Secondary's own push having succeeded
// without error.
func (e *SyncEngine) applySingleDeviceChange(relPath string, devEntries map[string]syncedlog.LogEntry, pendingByPath map[string]config.PendingConflict, selfDeviceID string) error {
	var devID string
	var entry syncedlog.LogEntry
	for id, en := range devEntries {
		devID, entry = id, en
	}
	if devID == selfDeviceID {
		return nil
	}

	fullPath := filepath.Join(e.vaultPath, relPath)

	if entry.Action == scan.ActionDelete {
		if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to apply %s's deletion of %s: %w", devID, relPath, err)
		}
		delete(pendingByPath, relPath)
		return nil
	}

	if entry.Diff == "" {
		// No content snapshot to apply (shouldn't normally happen outside
		// of a delete) - nothing safe to do.
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory for %s: %w", relPath, err)
	}
	if err := os.WriteFile(fullPath, []byte(entry.Diff), 0644); err != nil {
		return fmt.Errorf("failed to apply %s's change to %s: %w", devID, relPath, err)
	}
	delete(pendingByPath, relPath)
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
func (e *SyncEngine) mergeAndTrackConflicts(selfDeviceID string, latestByPath map[string]map[string]syncedlog.LogEntry) error {
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
		if len(devEntries) == 0 {
			continue
		}

		if len(devEntries) == 1 {
			// Exactly one device has ever logged a change to this path -
			// no concurrent changes to merge. If that device is this
			// Primary itself, its own Vault file already reflects it, so
			// there's nothing to do. Otherwise (a Secondary, or the
			// iCloud Bridge virtual device), this loop is the only place
			// Primary's Vault - and therefore anything Primary later
			// publishes to Google Drive or mirrors to the iCloud Bridge -
			// ever learns about that change at all (spec 3.3's "1台のみ
			// が分岐点から変更している場合は、自動的にその変更を採用す
			// る" rule), so it must be applied directly here.
			if err := e.applySingleDeviceChange(relPath, devEntries, pendingByPath, selfDeviceID); err != nil {
				return err
			}
			continue
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
