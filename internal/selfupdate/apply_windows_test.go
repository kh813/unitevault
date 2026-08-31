//go:build windows

package selfupdate

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
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

// TestCreateNoWindowFlag guards the exact bit values of the two Windows
// process creation flag constants Apply combines when launching the update
// helper. These are magic numbers copied from the Windows API (mirrored by
// hand rather than pulling in golang.org/x/sys/windows), so a typo here
// would silently weaken the flags with no compiler error to catch it - the
// actual console-flash fix now comes from the helper binary itself having
// no console subsystem at all (cmd/unitevault-updatehelper), not from these
// flags, but they cost nothing to keep as a second layer.
func TestCreateNoWindowFlag(t *testing.T) {
	if detachedProcess != 0x00000008 {
		t.Errorf("detachedProcess must mirror windows.DETACHED_PROCESS (0x8), got %#x", detachedProcess)
	}
	if createNoWindow != 0x08000000 {
		t.Errorf("createNoWindow must mirror windows.CREATE_NO_WINDOW (0x08000000), got %#x", createNoWindow)
	}
}
