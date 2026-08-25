package selfupdate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.0.31", "0.0.32", true},
		{"0.0.31", "0.0.31", false},
		{"0.0.9", "0.0.10", true},  // numeric, not lexicographic, comparison
		{"0.0.10", "0.0.9", false}, // guards against a naive string compare
		{"0.9.0", "0.10.0", true},  // same, one level up
		{"1.0.0", "0.9.9", false},
		{"v0.0.31", "v0.0.32", true}, // tolerates a leading "v"
		{"0.0.31", "0.0.31.1", true}, // extra trailing component counts as newer
	}
	for _, c := range cases {
		if got := IsNewer(c.current, c.latest); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestIsNewer_UnparseableFallsBackToInequality(t *testing.T) {
	if !IsNewer("0.0.31", "not-a-version") {
		t.Error("expected an unparseable but different latest version to be treated as newer")
	}
	if IsNewer("same-tag", "same-tag") {
		t.Error("expected identical unparseable versions to not be treated as newer")
	}
	if IsNewer("0.0.31", "") {
		t.Error("expected an empty latest version to never be treated as newer")
	}
}

func TestCheckLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v9.9.9",
			"html_url": "https://github.com/kh813/unitevault/releases/tag/v9.9.9",
			"assets": []map[string]string{
				{"name": "UniteVault-mac-arm64.app.zip", "browser_download_url": "https://example.com/mac.zip"},
				{"name": "UniteVault-windows-amd64.zip", "browser_download_url": "https://example.com/win.zip"},
			},
		})
	}))
	defer server.Close()

	original := releasesAPIURL
	releasesAPIURL = server.URL
	defer func() { releasesAPIURL = original }()

	info, err := CheckLatest(context.Background())
	if err != nil {
		t.Fatalf("CheckLatest returned error: %v", err)
	}
	if info.TagName != "v9.9.9" {
		t.Errorf("expected TagName v9.9.9, got %q", info.TagName)
	}
	if info.Version != "9.9.9" {
		t.Errorf("expected Version 9.9.9 (v-prefix stripped), got %q", info.Version)
	}
	if info.HTMLURL == "" {
		t.Error("expected a non-empty HTMLURL")
	}

	wantAsset := PlatformAssetName()
	if wantAsset != "" {
		if info.AssetName != wantAsset {
			t.Errorf("expected AssetName %q for this platform, got %q", wantAsset, info.AssetName)
		}
		if info.AssetURL == "" {
			t.Error("expected a non-empty AssetURL when a matching asset exists")
		}
	}
}

func TestCheckLatest_NoMatchingAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v9.9.9",
			"html_url": "https://example.com/release",
			"assets": []map[string]string{
				{"name": "some-other-file.zip", "browser_download_url": "https://example.com/other.zip"},
			},
		})
	}))
	defer server.Close()

	original := releasesAPIURL
	releasesAPIURL = server.URL
	defer func() { releasesAPIURL = original }()

	info, err := CheckLatest(context.Background())
	if err != nil {
		t.Fatalf("CheckLatest returned error: %v", err)
	}
	if info.AssetURL != "" || info.AssetName != "" {
		t.Errorf("expected no asset match, got AssetName=%q AssetURL=%q", info.AssetName, info.AssetURL)
	}
}

func TestCheckLatest_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer server.Close()

	original := releasesAPIURL
	releasesAPIURL = server.URL
	defer func() { releasesAPIURL = original }()

	if _, err := CheckLatest(context.Background()); err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}
