//go:build windows

package selfupdate

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTestExeZip builds an in-memory zip mirroring the real release layout
// (a single top-level .exe, matching what release.yml's
// UniteVault-windows-amd64.zip actually contains), so extractExe can be
// tested without a real release download.
func buildTestExeZip(t *testing.T, content string) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)

	fw, err := w.Create("UniteVault.exe")
	if err != nil {
		t.Fatalf("failed to add UniteVault.exe to test zip: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("failed to write UniteVault.exe to test zip: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("failed to close test zip: %v", err)
	}
	return buf.Bytes()
}

func TestExtractExe(t *testing.T) {
	zipData := buildTestExeZip(t, "fake exe bytes")
	destPath := filepath.Join(t.TempDir(), "UniteVault.exe.new")

	if err := extractExe(zipData, destPath); err != nil {
		t.Fatalf("extractExe returned error: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("expected extracted exe at %s: %v", destPath, err)
	}
	if string(got) != "fake exe bytes" {
		t.Errorf("expected extracted content %q, got %q", "fake exe bytes", got)
	}
}

func TestExtractExe_NoExeInArchive(t *testing.T) {
	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)
	fw, err := w.Create("readme.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("no exe here"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	destPath := filepath.Join(t.TempDir(), "UniteVault.exe.new")
	if err := extractExe(buf.Bytes(), destPath); err == nil {
		t.Fatal("expected an error when the archive contains no .exe")
	}
}

// TestUpdateHelperScript_NeverDeletesBackupInsideRetryLoop guards against a
// real, previously-shipped bug: the retry loop used to delete OLDEXE (the
// backup of the still-working previous exe) at the top of *every* iteration,
// not just once before the loop starts. That meant the very first successful
// EXE->OLDEXE rename got wiped out one iteration later if the second rename
// (NEWEXE->EXE) kept failing (e.g. an antivirus scan holding the freshly
// downloaded exe open past the retry window) - by the time all attempts were
// exhausted, neither EXE nor OLDEXE existed any more and nothing could be
// relaunched, silently uninstalling the app with no way to recover.
//
// This can't be executed here (cmd.exe/batch semantics only exist on
// Windows), so it's a structural guard on the script text: the retry loop's
// body - the parenthesized block driven by `for /L` - must never contain a
// deletion of %OLDEXE%.
func TestUpdateHelperScript_NeverDeletesBackupInsideRetryLoop(t *testing.T) {
	const forMarker = "for /L"
	loopStart := strings.Index(updateHelperScript, forMarker)
	if loopStart == -1 {
		t.Fatal("expected the update helper script to contain a `for /L` retry loop")
	}

	openParen := strings.Index(updateHelperScript[loopStart:], "(")
	if openParen == -1 {
		t.Fatal("expected the retry loop to open with '('")
	}
	openParen += loopStart

	closeParen := strings.Index(updateHelperScript[openParen:], "\n)")
	if closeParen == -1 {
		t.Fatal("expected the retry loop to close with ')' on its own line")
	}
	closeParen += openParen

	loopBody := updateHelperScript[openParen:closeParen]
	if strings.Contains(loopBody, `del`) && strings.Contains(loopBody, `%OLDEXE%`) {
		t.Errorf("the retry loop body must never delete %%OLDEXE%% - doing so destroys the backup before a swap is confirmed to have succeeded. Loop body:\n%s", loopBody)
	}
}

// TestUpdateHelperScript_RestoresBackupOnTotalFailure guards the flip side
// of the same bug: if every retry attempt fails, the script must restore
// EXE from OLDEXE (and relaunch it) instead of leaving the app uninstalled
// with the new exe never having been placed and the old one gone.
func TestUpdateHelperScript_RestoresBackupOnTotalFailure(t *testing.T) {
	if !strings.Contains(updateHelperScript, `move /y "%OLDEXE%" "%EXE%"`) {
		t.Error("expected the script to restore EXE from OLDEXE when every retry attempt fails")
	}
}

// TestCreateNoWindowFlag guards the exact bit values of the two Windows
// process creation flag constants Apply combines to keep the update helper
// (and the console commands it runs internally, like ping) from ever
// flashing a visible cmd.exe window - a real user-reported bug caused by
// DETACHED_PROCESS alone not being enough to suppress it. These are magic
// numbers copied from the Windows API (mirrored by hand rather than
// pulling in golang.org/x/sys/windows), so a typo here would silently
// reintroduce the flash without any compiler error to catch it.
func TestCreateNoWindowFlag(t *testing.T) {
	if detachedProcess != 0x00000008 {
		t.Errorf("detachedProcess must mirror windows.DETACHED_PROCESS (0x8), got %#x", detachedProcess)
	}
	if createNoWindow != 0x08000000 {
		t.Errorf("createNoWindow must mirror windows.CREATE_NO_WINDOW (0x08000000), got %#x", createNoWindow)
	}
}
