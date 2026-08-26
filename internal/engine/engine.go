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
	cfgMgr   *config.ConfigManager
	scanner  *scan.Scanner
	logMgr   *syncedlog.LogManager
	drive    drive.RcloneRunner
	vaultPath string
	label    string
}

func NewSyncEngine(cfgMgr *config.ConfigManager, vaultPath string, label string, driveRunner drive.RcloneRunner) *SyncEngine {
	if driveRunner == nil {
		driveRunner = drive.NewClient(filepath.Join(vaultPath, "_sync", "engine.log"))
	}
	return &SyncEngine{
		cfgMgr:   cfgMgr,
		scanner:  scan.NewScanner(vaultPath),
		logMgr:   syncedlog.NewLogManager(vaultPath),
		drive:    driveRunner,
		vaultPath: vaultPath,
		label:    label,
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
		entry := syncedlog.CreateLogEntryFromChange(deviceID, e.label, seq, ch, "")
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

	for relPath, devEntries := range latestByPath {
		if len(devEntries) <= 1 {
			continue // No concurrent changes across multiple devices
		}

		// Perform N-way merge if multiple devices have modified relPath
		var versions []merge.DeviceVersion
		for devID, entry := range devEntries {
			// Read content from Vault or file
			fullPath := filepath.Join(e.vaultPath, relPath)
			content, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}
			versions = append(versions, merge.DeviceVersion{
				DeviceID: devID,
				Content:  string(scan.NormalizeLF(content)),
				BaseHash: entry.BaseHash,
			})
		}

		if len(versions) > 1 {
			// Assume common base or empty for initial
			res, err := merge.NWayMerge("", versions)
			if err != nil {
				return fmt.Errorf("merge error for %s: %w", relPath, err)
			}

			fullPath := filepath.Join(e.vaultPath, relPath)
			if err := os.WriteFile(fullPath, []byte(res.MergedContent), 0644); err != nil {
				return fmt.Errorf("failed to write merged file: %w", err)
			}

			if res.HasConflict {
				resolver := merge.NewConflictResolver(os.Stdin, os.Stdout)
				labels := make(map[string]string)
				for dID, entry := range devEntries {
					labels[dID] = entry.Label
				}
				resolved, err := resolver.ResolveInteractive(relPath, res.MergedContent, labels)
				if err == nil && resolved != "" {
					_ = merge.ResolveAndRecord(e.logMgr, e.vaultPath, relPath, resolved, deviceID, e.label)
				}
			}
		}
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
