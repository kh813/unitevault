package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kh813/unitevault/internal/scan"
	"github.com/kh813/unitevault/internal/syncedlog"
)

// bridgeExcluded reports whether slashRel (a Vault-relative, slash-
// separated path) is this app's own bookkeeping rather than real Vault
// content - mirrors scan.Scanner.ScanVault's own _sync/ exclusion exactly,
// so the Bridge folder never receives (or has overwritten in it) files
// that are private to whichever scanner/log owns them.
func bridgeExcluded(slashRel string) bool {
	return slashRel == "_sync" || strings.HasPrefix(slashRel, "_sync/")
}

// ScanBridgeAndLog scans the iCloud Bridge folder (spec 1.6.3) for changes
// and logs them into the main Vault's _sync/ under bridgeDeviceID, exactly
// as if it were another real device - so mergeAndTrackConflicts picks
// them up with no special-casing. Call once per cycle, before the merge
// step, whenever a bridge path is configured. Returns how many changes
// were logged (informational only).
func ScanBridgeAndLog(mainVaultPath, bridgePath, bridgeDeviceID, bridgeLabel string) (int, error) {
	bridgeScanner := scan.NewScanner(bridgePath)

	currState, err := bridgeScanner.ScanVault()
	if err != nil {
		return 0, fmt.Errorf("bridge scan failed: %w", err)
	}
	lastRawState, err := bridgeScanner.LoadLastScan()
	if err != nil {
		return 0, fmt.Errorf("failed to load bridge last scan state: %w", err)
	}
	stableState := scan.DebounceFilter(lastRawState, currState)

	// See Scanner.ConfirmedStateFilePath's doc comment: DetectChanges must
	// compare against this separately-advanced baseline, never against
	// lastRawState/a raw scan. ReconcileForDetection (not stableState
	// directly) tells a genuine deletion apart from a file simply mid-
	// edit, not yet confirmed stable.
	confirmedState, err := bridgeScanner.LoadConfirmedState()
	if err != nil {
		return 0, fmt.Errorf("failed to load bridge confirmed scan state: %w", err)
	}
	changes := scan.DetectChanges(confirmedState, scan.ReconcileForDetection(confirmedState, currState, stableState))

	if err := bridgeScanner.SaveScanState(currState); err != nil {
		return 0, fmt.Errorf("failed to save bridge scan state: %w", err)
	}
	if err := bridgeScanner.SaveConfirmedState(scan.ApplyChangesToState(confirmedState, changes)); err != nil {
		return 0, fmt.Errorf("failed to save bridge confirmed scan state: %w", err)
	}
	if len(changes) == 0 {
		return 0, nil
	}

	logMgr := syncedlog.NewLogManager(mainVaultPath)
	for _, ch := range changes {
		seq, err := logMgr.GetNextSeq(bridgeDeviceID)
		if err != nil {
			return 0, fmt.Errorf("failed to get next seq for bridge device: %w", err)
		}

		var content string
		if ch.Action != scan.ActionDelete {
			if data, err := os.ReadFile(filepath.Join(bridgePath, ch.Path)); err == nil {
				content = string(scan.NormalizeLF(data))
			}
		}

		entry := syncedlog.CreateLogEntryFromChange(bridgeDeviceID, bridgeLabel, seq, ch, content)
		if err := logMgr.AppendLogEntry(entry); err != nil {
			return 0, fmt.Errorf("failed to append bridge log entry: %w", err)
		}
	}
	return len(changes), nil
}

// MirrorVaultToBridge mirrors the current Vault content into the iCloud
// Bridge folder (spec 1.6.3), so the next iCloud sync carries the merged
// result out to iPhone/iPad: files present in the Vault are written or
// updated in the Bridge folder, and files in the Bridge folder that no
// longer exist in the Vault are removed. This app's own bookkeeping
// (_sync/, on both sides - see bridgeExcluded) is never touched by this
// mirror, since the Bridge folder's own _sync/ holds ScanBridgeAndLog's
// independent scan state, not a copy of the Vault's.
//
// A deletion is only ever applied to a bridge-side path that
// ScanBridgeAndLog's own confirmed scan state (Scanner.
// ConfirmedStateFilePath, loaded fresh here) already knows about - never
// merely "absent from the Vault". A real, previously-shipped bug came
// from deleting on that weaker condition alone: a brand new file an
// iPhone had just created lands in the Bridge folder before Primary's
// Vault has ever heard of it, and ScanBridgeAndLog needs its own debounce
// (spec 3.4.1) - normally another whole external-sync-task round-robin
// turn (spec 1.6.5) - before it's confirmed and merged in. Without this
// guard, MirrorVaultToBridge ran on that very same still-debouncing turn
// and deleted the brand new file right back out of the Bridge folder
// (since it wasn't in the Vault yet either), so it could never stabilize,
// never get logged, and never actually reach the Vault at all.
func MirrorVaultToBridge(vaultPath, bridgePath string) error {
	if err := os.MkdirAll(bridgePath, 0755); err != nil {
		return fmt.Errorf("failed to create bridge folder: %w", err)
	}

	bridgeConfirmed, err := scan.NewScanner(bridgePath).LoadConfirmedState()
	if err != nil {
		return fmt.Errorf("failed to load bridge confirmed scan state: %w", err)
	}

	present := make(map[string]bool)
	err = filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(vaultPath, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		slashRel := filepath.ToSlash(rel)
		if bridgeExcluded(slashRel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		dst := filepath.Join(bridgePath, rel)
		if info.IsDir() {
			present[slashRel] = true
			return os.MkdirAll(dst, info.Mode())
		}

		present[slashRel] = true
		return mirrorFileIfChanged(path, dst, info)
	})
	if err != nil {
		return fmt.Errorf("failed to mirror Vault into bridge folder: %w", err)
	}

	return filepath.Walk(bridgePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(bridgePath, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		slashRel := filepath.ToSlash(rel)
		if bridgeExcluded(slashRel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if present[slashRel] {
			return nil
		}
		if info.IsDir() {
			// Directories are never tracked in a ScanState (only files
			// are - see scan.Scanner.ScanVault), so the confirmed-state
			// guard below can't apply to them directly. Recursing into a
			// not-yet-vault-known directory and letting the guard protect
			// each individual file underneath is the safe option; an
			// empty directory left behind afterward is harmless clutter,
			// never lost data.
			return nil
		}
		if _, confirmed := bridgeConfirmed.Files[slashRel]; !confirmed {
			// The bridge's own scanner has never stably observed this
			// path existing - it may be brand new content still mid-
			// debounce (see this function's doc comment), so it must
			// never be deleted on the strength of "absent from the
			// Vault" alone. Once ScanBridgeAndLog does confirm it, this
			// guard no longer applies.
			return nil
		}
		// Confirmed as existing before, and no longer in the Vault -
		// remove it from the bridge too (mirror semantics, matching how
		// rclone sync treats the Vault as the source of truth for Google
		// Drive).
		return os.Remove(path)
	})
}

// mirrorFileIfChanged copies src to dst only if dst is missing or its
// content actually differs, to avoid rewriting (and needlessly re-
// triggering iCloud's own sync of) every file on every cycle.
func mirrorFileIfChanged(src, dst string, srcInfo os.FileInfo) error {
	if dstInfo, err := os.Stat(dst); err == nil && !dstInfo.IsDir() && dstInfo.Size() == srcInfo.Size() {
		srcHash, err1 := scan.CalculateNormalizedHash(src)
		dstHash, err2 := scan.CalculateNormalizedHash(dst)
		if err1 == nil && err2 == nil && srcHash == dstHash {
			return nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
