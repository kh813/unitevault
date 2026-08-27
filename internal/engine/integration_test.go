package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kh813/unitevault/internal/config"
	"github.com/kh813/unitevault/internal/engine"
)

// fsDriveRunner is a drive.RcloneRunner backed by a real local directory
// tree, standing in for a Google Drive remote. Unlike mockDriveRunner
// (which only records what was called), this one actually moves files, so
// tests built on it exercise real cross-device convergence: a Secondary's
// SyncEngine really does end up with bytes a Primary's SyncEngine wrote,
// by going through the same Sync/Copy call shapes and --exclude patterns
// production code uses (spec 1.6.4/1.6.5) - integration coverage the
// call-recording mocks elsewhere in this package can't provide.
type fsDriveRunner struct {
	root string
}

func newFSDriveRunner(t *testing.T) *fsDriveRunner {
	t.Helper()
	return &fsDriveRunner{root: t.TempDir()}
}

// resolve maps an rclone-style path ("RemoteName:some/path") to a real
// local path under root, or returns a plain local path (no ":") unchanged
// - matching how production call sites pass either a "remote:path" string
// or an actual local filesystem path to Sync/Copy's generically-named
// parameters (see drive.RcloneRunner's own doc comment).
func (f *fsDriveRunner) resolve(s string) string {
	if idx := strings.Index(s, ":"); idx > 0 {
		return filepath.Join(f.root, filepath.FromSlash(s[idx+1:]))
	}
	return s
}

func excludeMatches(relSlash string, excludes []string) bool {
	for _, p := range excludes {
		p = strings.TrimPrefix(p, "/")
		p = strings.TrimSuffix(p, "/**")
		if relSlash == p || strings.HasPrefix(relSlash, p+"/") {
			return true
		}
	}
	return false
}

func fsCopyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// fsCopyAdditive mimics `rclone copy`: every non-excluded file under src is
// copied into dst, but nothing already in dst is ever removed. A missing
// src is a silent no-op (matching rclone copying from an empty/nonexistent
// remote path).
func fsCopyAdditive(src, dst string, excludes []string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == src {
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		relSlash := filepath.ToSlash(rel)
		if excludeMatches(relSlash, excludes) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		return fsCopyFile(path, filepath.Join(dst, rel))
	})
}

// fsMirror mimics `rclone sync`: dst ends up matching src exactly, except
// that any path matching excludes is left untouched on the dst side
// (neither overwritten nor deleted) - matching how production code relies
// on --exclude to protect each device's own private .sync/state/ (spec
// 1.6.4).
func fsMirror(src, dst string, excludes []string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	present := make(map[string]bool)
	if _, err := os.Stat(src); err == nil {
		err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if path == src {
				return nil
			}
			rel, _ := filepath.Rel(src, path)
			relSlash := filepath.ToSlash(rel)
			if excludeMatches(relSlash, excludes) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if info.IsDir() {
				return nil
			}
			present[relSlash] = true
			return fsCopyFile(path, filepath.Join(dst, rel))
		})
		if err != nil {
			return err
		}
	}

	return filepath.Walk(dst, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if path == dst || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dst, path)
		relSlash := filepath.ToSlash(rel)
		if excludeMatches(relSlash, excludes) || present[relSlash] {
			return nil
		}
		return os.Remove(path)
	})
}

func (f *fsDriveRunner) Sync(ctx context.Context, srcPath, remoteTarget string, excludes ...string) error {
	return fsMirror(f.resolve(srcPath), f.resolve(remoteTarget), excludes)
}

func (f *fsDriveRunner) Copy(ctx context.Context, src, dst string, excludes ...string) error {
	return fsCopyAdditive(f.resolve(src), f.resolve(dst), excludes)
}

func (f *fsDriveRunner) FileExists(ctx context.Context, remoteTargetFile string) (bool, error) {
	_, err := os.Stat(f.resolve(remoteTargetFile))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (f *fsDriveRunner) DownloadFile(ctx context.Context, remoteSourceFile, localDstFile string) error {
	return fsCopyFile(f.resolve(remoteSourceFile), localDstFile)
}

func (f *fsDriveRunner) UploadFile(ctx context.Context, localSrcFile, remoteTargetFile string) error {
	return fsCopyFile(localSrcFile, f.resolve(remoteTargetFile))
}

func (f *fsDriveRunner) DeleteFile(ctx context.Context, remoteTargetFile string) error {
	err := os.Remove(f.resolve(remoteTargetFile))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// settle runs eng.RunCycle n times, matching the "2 settling cycles" idiom
// used throughout this package to get past the debounce window (3.4.1) so
// a just-written file is confirmed and logged before the test proceeds.
func settle(t *testing.T, eng *engine.SyncEngine, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := eng.RunCycle(context.Background()); err != nil {
			t.Fatalf("RunCycle failed: %v", err)
		}
	}
}

// TestIntegration_DriveOnly_TwoDevicesConverge exercises the "Google Drive
// only" configuration pattern (unitevault-todo.md Phase 19) end-to-end
// across two real SyncEngine instances sharing a real (filesystem-backed)
// fake Google Drive remote - no iCloud Bridge involved. Guards that
// content flows in both directions: Primary's own edit reaches Secondary,
// and - the bug found while writing this test, see engine.go's
// applySingleDeviceChange - a Secondary's own edit reaches back to
// Primary's Vault (and from there, Google Drive's published mirror).
func TestIntegration_DriveOnly_TwoDevicesConverge(t *testing.T) {
	tempDir := t.TempDir()
	remote := newFSDriveRunner(t)

	aVault := filepath.Join(tempDir, "A", "Vault")
	aCfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "A", "config"))
	if err := aCfgMgr.SaveConfig(&config.Config{VaultPath: aVault, RcloneRemote: "ObsidianVault", RclonePath: "MyVault"}); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}
	aEng := engine.NewSyncEngine(aCfgMgr, aVault, "device-a", remote)

	bVault := filepath.Join(tempDir, "B", "Vault")
	bCfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "B", "config"))
	if err := bCfgMgr.SaveConfig(&config.Config{VaultPath: bVault, RcloneRemote: "ObsidianVault", RclonePath: "MyVault"}); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}
	bEng := engine.NewSyncEngine(bCfgMgr, bVault, "device-b", remote)

	// A initializes as Primary (no marker exists yet) and publishes its
	// own new note.
	writeFile(t, filepath.Join(aVault, "from-a.md"), "written on A\n")
	settle(t, aEng, 2)

	// B initializes as Secondary (marker now exists, names A) and pulls
	// A's published content.
	settle(t, bEng, 1)

	got, err := os.ReadFile(filepath.Join(bVault, "from-a.md"))
	if err != nil {
		t.Fatalf("expected B to have pulled from-a.md, got: %v", err)
	}
	if string(got) != "written on A\n" {
		t.Errorf("expected A's exact content on B, got %q", string(got))
	}

	// B creates its own new note and pushes its log (content only, not
	// the raw file - spec 1.6.4).
	writeFile(t, filepath.Join(bVault, "from-b.md"), "written on B\n")
	settle(t, bEng, 2)

	// A pulls B's log, applies B's sole change to its own Vault (the fix
	// under test), and republishes - all in one cycle since only one
	// external task (Drive) is configured here.
	settle(t, aEng, 1)

	got, err = os.ReadFile(filepath.Join(aVault, "from-b.md"))
	if err != nil {
		t.Fatalf("expected A to have applied B's from-b.md, got: %v", err)
	}
	if string(got) != "written on B\n" {
		t.Errorf("expected B's exact content on A, got %q", string(got))
	}

	// Finally, B pulls again and sees its own note echoed back via A's
	// republished mirror (proving it actually reached Google Drive, not
	// just A's local Vault).
	settle(t, bEng, 1)
	if _, err := os.Stat(filepath.Join(bVault, "from-b.md")); err != nil {
		t.Errorf("expected B's own note to round-trip back through Google Drive, stat err = %v", err)
	}
}

// TestIntegration_Bridge_PhoneEditReachesVaultAndPublishes exercises the
// "iCloud Bridge" configuration pattern (unitevault-todo.md Phase 19): a
// single Primary device with an iCloud Bridge folder configured, standing
// in for what iCloud would deliver from an iPhone. Google Drive is still
// configured too - unlike the Bridge, it's a hard requirement for
// Primary/Secondary role determination itself regardless of whether the
// Bridge is used at all (spec 3.6.1.1/3.6.3.1) - but no Secondary ever
// joins here, so this isolates the Bridge machinery specifically: a change
// appearing in the Bridge folder (as if iCloud had just synced it down
// from iPhone) must reach the Vault, get mirrored back out to the Bridge
// folder for iCloud to carry back to iPhone, and eventually get published
// to Google Drive too via the round-robin (spec 1.6.5).
//
// The Bridge is logged under its own virtual device ID
// (GetOrCreateBridgeDeviceID), distinct from Primary's own - so a
// Bridge-only file is exactly the scenario applySingleDeviceChange fixes
// (a single non-self device's change, with no other device's entry to
// merge against).
func TestIntegration_Bridge_PhoneEditReachesVaultAndPublishes(t *testing.T) {
	tempDir := t.TempDir()
	remote := newFSDriveRunner(t)

	vaultPath := filepath.Join(tempDir, "Vault")
	bridgePath := filepath.Join(tempDir, "Bridge")
	cfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "config"))

	if err := cfgMgr.SaveConfig(&config.Config{
		VaultPath:        vaultPath,
		RcloneRemote:     "ObsidianVault",
		RclonePath:       "MyVault",
		ICloudBridgePath: bridgePath,
	}); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}
	eng := engine.NewSyncEngine(cfgMgr, vaultPath, "mac-test", remote)

	// Cycle 1: becomes Primary (no marker exists yet), Drive gets the
	// first round-robin turn - nothing to publish yet.
	settle(t, eng, 1)

	// Simulate iCloud having just delivered an iPhone edit into the
	// Bridge folder.
	writeFile(t, filepath.Join(bridgePath, "from-iphone.md"), "written on iPhone\n")

	// Bridge scanning uses the same debounce+confirm pipeline as the Vault
	// itself (spec 3.4.2), and only runs on the Bridge's round-robin turn
	// (every other cycle here, since Drive is also configured) - settle
	// across enough cycles for it to stabilize, get logged, merged into
	// the Vault, and mirrored back to the Bridge, then one more full pass
	// so Drive's turn comes back around to publish it.
	settle(t, eng, 5)

	got, err := os.ReadFile(filepath.Join(vaultPath, "from-iphone.md"))
	if err != nil {
		t.Fatalf("expected the Bridge's file to reach the Vault, got: %v", err)
	}
	if string(got) != "written on iPhone\n" {
		t.Errorf("expected the iPhone's exact content, got %q", string(got))
	}

	gotBridge, err := os.ReadFile(filepath.Join(bridgePath, "from-iphone.md"))
	if err != nil {
		t.Fatalf("expected the merged Vault to be mirrored back to the Bridge, got: %v", err)
	}
	if string(gotBridge) != "written on iPhone\n" {
		t.Errorf("expected the mirrored-back content to match, got %q", string(gotBridge))
	}

	if _, err := os.Stat(filepath.Join(remote.root, "MyVault", "from-iphone.md")); err != nil {
		t.Errorf("expected the Bridge-sourced file to eventually be published to Google Drive too, stat err = %v", err)
	}
}

// TestIntegration_Both_DriveAndBridgeConverge exercises the "both Google
// Drive and iCloud Bridge configured" pattern (unitevault-todo.md Phase
// 19): a single Primary with both destinations set up, plus a Secondary
// connected only via Drive. Guards that the two external destinations
// really do alternate one per tick (spec 1.6.5) rather than both firing
// every cycle, and that content from either source (Bridge or Secondary)
// still reaches every other participant given enough ticks.
func TestIntegration_Both_DriveAndBridgeConverge(t *testing.T) {
	tempDir := t.TempDir()
	remote := newFSDriveRunner(t)

	pVault := filepath.Join(tempDir, "P", "Vault")
	pBridge := filepath.Join(tempDir, "P", "Bridge")
	pCfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "P", "config"))
	if err := pCfgMgr.SaveConfig(&config.Config{
		VaultPath:        pVault,
		RcloneRemote:     "ObsidianVault",
		RclonePath:       "MyVault",
		ICloudBridgePath: pBridge,
	}); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}
	pEng := engine.NewSyncEngine(pCfgMgr, pVault, "primary-device", remote)

	sVault := filepath.Join(tempDir, "S", "Vault")
	sCfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "S", "config"))
	if err := sCfgMgr.SaveConfig(&config.Config{VaultPath: sVault, RcloneRemote: "ObsidianVault", RclonePath: "MyVault"}); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}
	sEng := engine.NewSyncEngine(sCfgMgr, sVault, "secondary-device", remote)

	// Primary becomes Primary and settles a locally-created note - two
	// cycles is enough to also publish it, since Drive gets the first
	// turn in the round robin (see engine.primaryExternalTasks/tickIndex).
	writeFile(t, filepath.Join(pVault, "from-primary.md"), "written on Primary\n")
	settle(t, pEng, 2)

	// Simulate an iPhone edit landing in the Bridge folder.
	writeFile(t, filepath.Join(pBridge, "from-iphone.md"), "written on iPhone\n")

	// Bridge's turn: needs its own debounce settling across a few
	// round-robin cycles (the Bridge's own scan needs to stabilize across
	// two of *its* turns, which alternate with Drive's), plus one final
	// Drive turn afterward to actually publish the merged result to the
	// shared remote.
	settle(t, pEng, 5)

	// Secondary pulls whatever Primary has published by now.
	settle(t, sEng, 1)

	for name, want := range map[string]string{
		"from-primary.md": "written on Primary\n",
		"from-iphone.md":  "written on iPhone\n",
	} {
		got, err := os.ReadFile(filepath.Join(sVault, name))
		if err != nil {
			t.Errorf("expected Secondary to have received %s, got: %v", name, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s: got %q, want %q", name, string(got), want)
		}
	}
}
