package gui

import (
	"bytes"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestNotoSansJPFonts_AreValidOpenTypeData guards against a corrupted or
// accidentally-empty embed (e.g. a bad go:embed path, or the extraction
// step that produced these files truncating them): both bundled font
// files must start with the "OTTO" magic header real CFF-flavored
// OpenType fonts (like Noto Sans JP) use.
func TestNotoSansJPFonts_AreValidOpenTypeData(t *testing.T) {
	for name, data := range map[string][]byte{
		"NotoSansJP-Regular.otf": notoSansJPRegular,
		"NotoSansJP-Bold.otf":    notoSansJPBold,
	} {
		if len(data) < 1_000_000 {
			t.Errorf("%s: expected at least ~1MB of embedded font data, got %d bytes", name, len(data))
		}
		if !bytes.HasPrefix(data, []byte("OTTO")) {
			t.Errorf("%s: expected the OpenType/CFF \"OTTO\" magic header, got %q", name, data[:4])
		}
	}
}

// TestUnifiedFontTheme_SelectsBoldOnlyForBoldStyle guards the real,
// user-reported bug this theme fixes (Latin/Japanese text on the same
// line rendered in two different fonts with mismatched baselines on
// Windows): every text style this app actually requests must resolve to
// one of the two bundled Noto Sans JP resources, never falling through to
// Fyne's own default theme's per-script font fallback.
func TestUnifiedFontTheme_SelectsBoldOnlyForBoldStyle(t *testing.T) {
	th := newUnifiedFontTheme()

	cases := []struct {
		name  string
		style fyne.TextStyle
		want  fyne.Resource
	}{
		{name: "regular", style: fyne.TextStyle{}, want: fontResourceRegular},
		{name: "bold", style: fyne.TextStyle{Bold: true}, want: fontResourceBold},
		{name: "italic falls back to regular", style: fyne.TextStyle{Italic: true}, want: fontResourceRegular},
		{name: "monospace falls back to regular", style: fyne.TextStyle{Monospace: true}, want: fontResourceRegular},
		{name: "bold italic falls back to bold", style: fyne.TextStyle{Bold: true, Italic: true}, want: fontResourceBold},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := th.Font(c.style); got != c.want {
				t.Errorf("Font(%+v) = %v, want %v", c.style, got, c.want)
			}
		})
	}
}

// TestUnifiedFontTheme_RendersMixedLatinJapaneseTextWithoutError exercises
// the real rendering pipeline (not just the theme's own Font() method) by
// laying out a label mixing Latin and Japanese text - exactly the
// real-world case (e.g. "「Vault」がrcloneに見つかりません") that surfaced
// the baseline mismatch bug - through Fyne's headless test driver, which
// still loads and shapes the actual font data. A parse failure in either
// bundled font would surface here as an error or panic during layout.
func TestUnifiedFontTheme_RendersMixedLatinJapaneseTextWithoutError(t *testing.T) {
	app := test.NewApp()
	defer test.NewApp() // reset the global test app for later tests
	app.Settings().SetTheme(newUnifiedFontTheme())

	label := widget.NewLabel(`未設定 - リモート「Vault」がrcloneに見つかりません`)
	w := test.NewWindow(label)
	defer w.Close()
	w.Resize(fyne.NewSize(400, 100))

	// MinSize forces Fyne to actually measure the text (shaping it with the
	// bundled font data) rather than just holding a string - a corrupt or
	// unparseable font would panic or return a zero size here.
	if size := label.MinSize(); size.Width <= 0 || size.Height <= 0 {
		t.Errorf("expected a non-zero measured size for the label, got %v", size)
	}
}
