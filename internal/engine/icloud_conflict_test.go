package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kh813/unitevault/internal/config"
	"github.com/kh813/unitevault/internal/engine"
)

// TestFindICloudConflictCopies_DetectsPairWithSibling guards spec 1.6.10's
// core detection rule: a file matching iCloud's own "Name (suffix).ext"
// conflict-copy convention is only ever reported when its original
// "Name.ext" sibling actually exists alongside it.
func TestFindICloudConflictCopies_DetectsPairWithSibling(t *testing.T) {
	vaultPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(vaultPath, "Note.md"), []byte("original\n"), 0644); err != nil {
		t.Fatalf("failed to seed Note.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vaultPath, "Note (1).md"), []byte("copy\n"), 0644); err != nil {
		t.Fatalf("failed to seed Note (1).md: %v", err)
	}

	pairs, err := engine.FindICloudConflictCopies(vaultPath)
	if err != nil {
		t.Fatalf("FindICloudConflictCopies failed: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected exactly 1 pair, got %+v", pairs)
	}
	if pairs[0].OriginalRelPath != "Note.md" || pairs[0].CopyRelPath != "Note (1).md" {
		t.Errorf("unexpected pair: %+v", pairs[0])
	}
}

// TestFindICloudConflictCopies_LocalizedSuffixAlsoMatches guards that the
// parenthesized suffix itself isn't pattern-matched against a fixed list -
// Apple's actual wording varies by device name/locale (e.g. "Macの競合コ
// ピー"), so any non-empty parenthesized suffix must be accepted as long
// as the original sibling exists.
func TestFindICloudConflictCopies_LocalizedSuffixAlsoMatches(t *testing.T) {
	vaultPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(vaultPath, "Note.md"), []byte("original\n"), 0644); err != nil {
		t.Fatalf("failed to seed Note.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vaultPath, "Note (Macの競合コピー).md"), []byte("copy\n"), 0644); err != nil {
		t.Fatalf("failed to seed conflict copy: %v", err)
	}

	pairs, err := engine.FindICloudConflictCopies(vaultPath)
	if err != nil {
		t.Fatalf("FindICloudConflictCopies failed: %v", err)
	}
	if len(pairs) != 1 || pairs[0].CopyRelPath != "Note (Macの競合コピー).md" {
		t.Fatalf("expected the localized-suffix copy to be detected, got %+v", pairs)
	}
}

// TestFindICloudConflictCopies_IgnoresWhenNoSiblingExists guards the false-
// positive-avoidance rule itself: a parenthesized filename alone, with no
// original sibling present (e.g. it was already cleaned up, or never had
// one), must never be reported - spec 1.6.10 deliberately keys detection
// off the pair actually existing, not the naming pattern alone.
func TestFindICloudConflictCopies_IgnoresWhenNoSiblingExists(t *testing.T) {
	vaultPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(vaultPath, "Note (1).md"), []byte("copy\n"), 0644); err != nil {
		t.Fatalf("failed to seed Note (1).md: %v", err)
	}

	pairs, err := engine.FindICloudConflictCopies(vaultPath)
	if err != nil {
		t.Fatalf("FindICloudConflictCopies failed: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected no pairs when the original sibling doesn't exist, got %+v", pairs)
	}
}

// TestFindICloudConflictCopies_IgnoresLegitimatelyParenthesizedNote guards
// against the exact false-positive scenario this feature exists to avoid:
// a genuinely user-named note that happens to use parentheses (e.g. a
// draft marker) must not be flagged just because it has no sibling.
func TestFindICloudConflictCopies_IgnoresLegitimatelyParenthesizedNote(t *testing.T) {
	vaultPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(vaultPath, "Meeting (draft).md"), []byte("notes\n"), 0644); err != nil {
		t.Fatalf("failed to seed Meeting (draft).md: %v", err)
	}

	pairs, err := engine.FindICloudConflictCopies(vaultPath)
	if err != nil {
		t.Fatalf("FindICloudConflictCopies failed: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected a legitimately-named note without a sibling to never be flagged, got %+v", pairs)
	}
}

// TestFindICloudConflictCopies_IgnoresBareNumberSuffixWithoutParens guards
// that this feature only ever matches the parenthesized convention
// ("Name (1).md"), not the older bare-space-number convention ("Name
// 2.md", Apple TN2336) originally identified as too ambiguous with a
// legitimately-numbered note (e.g. "Chapter 2.md") to detect safely - the
// user's own research confirmed modern iCloud actually uses the
// parenthesized form, so this app deliberately never matches the bare
// form at all, even with a sibling present.
func TestFindICloudConflictCopies_IgnoresBareNumberSuffixWithoutParens(t *testing.T) {
	vaultPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(vaultPath, "Chapter.md"), []byte("original\n"), 0644); err != nil {
		t.Fatalf("failed to seed Chapter.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vaultPath, "Chapter 2.md"), []byte("a different, legitimately-named note\n"), 0644); err != nil {
		t.Fatalf("failed to seed Chapter 2.md: %v", err)
	}

	pairs, err := engine.FindICloudConflictCopies(vaultPath)
	if err != nil {
		t.Fatalf("FindICloudConflictCopies failed: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected the bare 'Name 2.md' form to never be treated as a conflict copy, got %+v", pairs)
	}
}

// TestCheckAndMergeICloudConflictCopies_IdenticalContentAutoMergesAndRemovesCopy
// guards the "clean merge" path: when the original and its conflict copy
// happen to hold identical content (e.g. a transient iCloud timing hiccup
// that resolved itself), the copy is redundant - it should be removed
// automatically, with no PendingConflict raised to bother the user over
// nothing.
func TestCheckAndMergeICloudConflictCopies_IdenticalContentAutoMergesAndRemovesCopy(t *testing.T) {
	vaultPath := t.TempDir()
	content := []byte("same content on both sides\n")
	if err := os.WriteFile(filepath.Join(vaultPath, "Note.md"), content, 0644); err != nil {
		t.Fatalf("failed to seed Note.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vaultPath, "Note (1).md"), content, 0644); err != nil {
		t.Fatalf("failed to seed Note (1).md: %v", err)
	}
	cfgMgr := config.NewConfigManagerWithDir(t.TempDir())

	result, err := engine.CheckAndMergeICloudConflictCopies(cfgMgr, vaultPath)
	if err != nil {
		t.Fatalf("CheckAndMergeICloudConflictCopies failed: %v", err)
	}
	if result.AutoMerged != 1 || result.NeedsReview != 0 || len(result.Failed) != 0 {
		t.Errorf("expected 1 auto-merge and nothing else, got %+v", result)
	}
	if _, err := os.Stat(filepath.Join(vaultPath, "Note (1).md")); !os.IsNotExist(err) {
		t.Error("expected the redundant conflict-copy file to be removed")
	}
	pending, err := cfgMgr.LoadPendingConflicts()
	if err != nil {
		t.Fatalf("LoadPendingConflicts failed: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected no pending conflict for an identical-content pair, got %+v", pending)
	}
}

// TestCheckAndMergeICloudConflictCopies_DifferingContentCreatesPendingConflict
// guards the "needs review" path: genuinely differing content must be
// surfaced through the exact same PendingConflict/"Resolve Conflicts..."
// mechanism spec 3.3.2 already uses for multi-device conflicts, rather
// than a separate UI, and must record ExtraFileToRemove so resolving it
// also cleans up the now-redundant copy file.
func TestCheckAndMergeICloudConflictCopies_DifferingContentCreatesPendingConflict(t *testing.T) {
	vaultPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(vaultPath, "Note.md"), []byte("Mac's version\n"), 0644); err != nil {
		t.Fatalf("failed to seed Note.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vaultPath, "Note (1).md"), []byte("iPhone's version\n"), 0644); err != nil {
		t.Fatalf("failed to seed Note (1).md: %v", err)
	}
	cfgMgr := config.NewConfigManagerWithDir(t.TempDir())

	result, err := engine.CheckAndMergeICloudConflictCopies(cfgMgr, vaultPath)
	if err != nil {
		t.Fatalf("CheckAndMergeICloudConflictCopies failed: %v", err)
	}
	if result.AutoMerged != 0 || result.NeedsReview != 1 || len(result.Failed) != 0 {
		t.Fatalf("expected 1 pending review and nothing else, got %+v", result)
	}

	// The copy file must still exist until the conflict is actually
	// resolved (see TestResolvePendingConflict_RemovesExtraFile) - only
	// the original gets the conflict-marked placeholder content.
	if _, err := os.Stat(filepath.Join(vaultPath, "Note (1).md")); err != nil {
		t.Error("expected the conflict-copy file to remain until the conflict is resolved")
	}
	original, err := os.ReadFile(filepath.Join(vaultPath, "Note.md"))
	if err != nil {
		t.Fatalf("failed to read Note.md: %v", err)
	}
	if !strings.Contains(string(original), "<<<<<<<") {
		t.Errorf("expected the original file to now hold conflict-marked content, got %q", string(original))
	}

	pending, err := cfgMgr.LoadPendingConflicts()
	if err != nil {
		t.Fatalf("LoadPendingConflicts failed: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected exactly 1 pending conflict, got %+v", pending)
	}
	pc := pending[0]
	if pc.RelPath != "Note.md" {
		t.Errorf("expected RelPath 'Note.md', got %q", pc.RelPath)
	}
	if pc.ExtraFileToRemove != "Note (1).md" {
		t.Errorf("expected ExtraFileToRemove 'Note (1).md', got %q", pc.ExtraFileToRemove)
	}
	if len(pc.Versions) != 2 {
		t.Fatalf("expected 2 recorded versions, got %+v", pc.Versions)
	}
}

// TestCheckAndMergeICloudConflictCopies_NoPairsFound guards the trivial
// case: an ordinary Vault with no conflict copies at all must report an
// empty result without error.
func TestCheckAndMergeICloudConflictCopies_NoPairsFound(t *testing.T) {
	vaultPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(vaultPath, "Note.md"), []byte("ordinary note\n"), 0644); err != nil {
		t.Fatalf("failed to seed Note.md: %v", err)
	}
	cfgMgr := config.NewConfigManagerWithDir(t.TempDir())

	result, err := engine.CheckAndMergeICloudConflictCopies(cfgMgr, vaultPath)
	if err != nil {
		t.Fatalf("CheckAndMergeICloudConflictCopies failed: %v", err)
	}
	if result.AutoMerged != 0 || result.NeedsReview != 0 || len(result.Failed) != 0 {
		t.Errorf("expected an empty result, got %+v", result)
	}
}

// TestResolvePendingConflict_RemovesExtraFile guards that resolving a
// conflict whose PendingConflict.ExtraFileToRemove is set (spec 1.6.10's
// iCloud conflict-copy check) also deletes that companion file - without
// this, the now-redundant conflict-copy file would linger in the Vault
// forever even after the user picks a version to keep.
func TestResolvePendingConflict_RemovesExtraFile(t *testing.T) {
	tempDir := t.TempDir()
	vaultPath := filepath.Join(tempDir, "Vault")
	cfgMgr := config.NewConfigManagerWithDir(filepath.Join(tempDir, "config"))

	if err := os.MkdirAll(vaultPath, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vaultPath, "Note (1).md"), []byte("iPhone's version\n"), 0644); err != nil {
		t.Fatalf("failed to seed Note (1).md: %v", err)
	}

	conflict := config.PendingConflict{
		RelPath:     "Note.md",
		WrittenHash: "irrelevant-here",
		Versions: []config.PendingConflictVersion{
			{DeviceID: "icloud_original", Label: "Original", Content: "Mac's version\n"},
			{DeviceID: "icloud_conflict_copy", Label: "Note (1).md", Content: "iPhone's version\n"},
		},
		ExtraFileToRemove: "Note (1).md",
	}
	if err := cfgMgr.SavePendingConflicts([]config.PendingConflict{conflict}); err != nil {
		t.Fatalf("failed to seed pending conflict: %v", err)
	}

	if err := engine.ResolvePendingConflict(cfgMgr, vaultPath, conflict, "icloud_conflict_copy", "primary-device", "mac-test"); err != nil {
		t.Fatalf("ResolvePendingConflict failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(vaultPath, "Note (1).md")); !os.IsNotExist(err) {
		t.Error("expected the conflict-copy companion file to be removed once resolved")
	}
	got, err := os.ReadFile(filepath.Join(vaultPath, "Note.md"))
	if err != nil {
		t.Fatalf("failed to read resolved file: %v", err)
	}
	if string(got) != "iPhone's version\n" {
		t.Errorf("expected the chosen version's content written, got %q", string(got))
	}
}
