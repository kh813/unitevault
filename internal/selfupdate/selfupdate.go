// Package selfupdate checks GitHub Releases for a newer UniteVault version
// and, on supported platforms, downloads and applies it in place (see
// apply_darwin.go / apply_windows.go). Platform-specific Apply()
// implementations always hand off the actual file replacement to a detached
// helper process/script: the calling process's own executable cannot safely
// replace itself while running, especially on Windows where the running
// .exe is locked.
package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
)

// releasesAPIURL is a var (not const) so tests can point it at an
// httptest.Server instead of the real GitHub API.
var releasesAPIURL = "https://api.github.com/repos/kh813/unitevault/releases/latest"

// ReleaseInfo describes the latest published GitHub release relevant to
// this platform.
type ReleaseInfo struct {
	Version   string // e.g. "0.0.32" (the tag's leading "v" stripped)
	TagName   string // e.g. "v0.0.32"
	HTMLURL   string // the release's web page, used as a manual-download fallback
	AssetName string // "" if this platform has no matching asset in the release
	AssetURL  string // direct download URL for AssetName, "" if none found
}

// CheckLatest queries GitHub for the most recently published release and
// picks out the asset matching the current OS, if any.
func CheckLatest(ctx context.Context) (*ReleaseInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesAPIURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GitHub returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to parse release info: %w", err)
	}

	info := &ReleaseInfo{
		TagName: payload.TagName,
		Version: strings.TrimPrefix(payload.TagName, "v"),
		HTMLURL: payload.HTMLURL,
	}

	wantName := PlatformAssetName()
	for _, a := range payload.Assets {
		if wantName != "" && a.Name == wantName {
			info.AssetName = a.Name
			info.AssetURL = a.BrowserDownloadURL
			break
		}
	}

	return info, nil
}

// PlatformAssetName returns the release asset filename expected for the
// current OS (matching .github/workflows/release.yml's output names), or ""
// if this OS has no distributed build.
func PlatformAssetName() string {
	switch runtime.GOOS {
	case "darwin":
		return "UniteVault-mac-arm64.app.zip"
	case "windows":
		return "UniteVault-windows-amd64.zip"
	default:
		return ""
	}
}

// IsNewer reports whether latest is a strictly greater dotted-numeric
// version than current (e.g. "0.0.31" -> "0.0.32" is newer; "0.0.31" ->
// "0.0.5" is not, unlike a naive string comparison). Falls back to a plain
// inequality check if either version doesn't parse as dotted numbers, so an
// unexpected tag format still surfaces as "there's something to look at"
// rather than silently comparing as equal.
func IsNewer(current, latest string) bool {
	c := parseVersion(current)
	l := parseVersion(latest)
	if c == nil || l == nil {
		return latest != "" && current != latest
	}
	for i := 0; i < len(c) || i < len(l); i++ {
		var cv, lv int
		if i < len(c) {
			cv = c[i]
		}
		if i < len(l) {
			lv = l[i]
		}
		if lv != cv {
			return lv > cv
		}
	}
	return false
}

func parseVersion(v string) []int {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) == 0 {
		return nil
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		nums[i] = n
	}
	return nums
}

func downloadFile(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download update: status %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}
