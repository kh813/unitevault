//go:build darwin

package selfupdate

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// buildTestAppZip builds an in-memory zip mirroring the real release
// layout (a top-level *.app directory containing an executable), so
// extractAppBundle can be tested without touching this test binary's own
// running .app bundle.
func buildTestAppZip(t *testing.T) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)

	writeFile := func(name string, mode os.FileMode, content string) {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.SetMode(mode)
		fw, err := w.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("failed to add %s to test zip: %v", name, err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write %s to test zip: %v", name, err)
		}
	}

	writeFile("UniteVault-mac-arm64.app/Contents/Info.plist", 0644, "<plist/>")
	writeFile("UniteVault-mac-arm64.app/Contents/MacOS/UniteVault", 0755, "#!/bin/sh\necho fake binary\n")

	if err := w.Close(); err != nil {
		t.Fatalf("failed to close test zip: %v", err)
	}
	return buf.Bytes()
}

func TestExtractAppBundle(t *testing.T) {
	zipData := buildTestAppZip(t)
	destDir := t.TempDir()

	appPath, err := extractAppBundle(zipData, destDir)
	if err != nil {
		t.Fatalf("extractAppBundle returned error: %v", err)
	}

	wantAppPath := filepath.Join(destDir, "UniteVault-mac-arm64.app")
	if appPath != wantAppPath {
		t.Errorf("expected app path %q, got %q", wantAppPath, appPath)
	}

	binPath := filepath.Join(appPath, "Contents", "MacOS", "UniteVault")
	info, err := os.Stat(binPath)
	if err != nil {
		t.Fatalf("expected extracted binary at %s: %v", binPath, err)
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("expected extracted binary to preserve its executable bit, got mode %v", info.Mode())
	}

	plistPath := filepath.Join(appPath, "Contents", "Info.plist")
	if _, err := os.Stat(plistPath); err != nil {
		t.Errorf("expected extracted Info.plist at %s: %v", plistPath, err)
	}
}

func TestBundlePathFromExecutable(t *testing.T) {
	cases := []struct {
		name    string
		exe     string
		want    string
		wantErr bool
	}{
		{
			name: "installed in /Applications",
			exe:  "/Applications/UniteVault.app/Contents/MacOS/UniteVault",
			want: "/Applications/UniteVault.app",
		},
		{
			name: "installed in a path with spaces",
			exe:  "/Users/me/My Apps/UniteVault.app/Contents/MacOS/UniteVault",
			want: "/Users/me/My Apps/UniteVault.app",
		},
		{
			name:    "raw dev binary, not inside any .app bundle",
			exe:     "/tmp/unitevault-devbuild",
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := bundlePathFromExecutable(c.exe)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q, got path %q", c.exe, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", c.exe, err)
			}
			if got != c.want {
				t.Errorf("bundlePathFromExecutable(%q) = %q, want %q", c.exe, got, c.want)
			}
		})
	}
}

func TestExtractAppBundle_NoAppInArchive(t *testing.T) {
	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)
	fw, err := w.Create("readme.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("no app bundle here"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := extractAppBundle(buf.Bytes(), t.TempDir()); err == nil {
		t.Fatal("expected an error when the archive contains no .app bundle")
	}
}
