package gui

import (
	_ "embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Bundled here (rather than relying on Fyne's own font fallback - see
// unifiedFontTheme's doc comment) specifically because Noto Sans JP's
// Latin glyphs share the same metrics/baseline as its Japanese glyphs -
// unlike Fyne's bundled Latin font ("Inter") paired with whatever CJK
// system font a given OS happens to fall back to. Only Regular and Bold
// are bundled since this app never requests Italic or Monospace text
// (fontThemeFont falls back to Regular/Bold for those anyway, rather than
// letting Fyne's own per-style fallback reintroduce mixed fonts).
//
//go:embed fonts/NotoSansJP-Regular.otf
var notoSansJPRegular []byte

//go:embed fonts/NotoSansJP-Bold.otf
var notoSansJPBold []byte

var (
	fontResourceRegular = fyne.NewStaticResource("NotoSansJP-Regular.otf", notoSansJPRegular)
	fontResourceBold    = fyne.NewStaticResource("NotoSansJP-Bold.otf", notoSansJPBold)
)

// unifiedFontTheme wraps Fyne's default theme, overriding only its font
// selection. A real, user-reported bug on Windows: Fyne renders Latin
// characters in its own bundled font ("Inter") but falls back to whatever
// CJK font Windows has installed for Japanese characters - since CJK
// glyphs are designed centered within their em-box rather than sitting on
// a Latin-style baseline, pairing two unrelated font families on the same
// line of mixed English/Japanese text (e.g. this app's own "Vault"/
// "rclone" mixed into otherwise-Japanese UI strings) can visibly misalign
// vertically. This wasn't noticeable on macOS, where the OS's own CJK
// fallback font happens to line up reasonably well with Inter, but was on
// Windows. Using one font family for everything sidesteps the mismatch by
// construction instead of depending on whichever fallback font a given OS
// happens to pick.
type unifiedFontTheme struct {
	fyne.Theme
}

func newUnifiedFontTheme() fyne.Theme {
	return unifiedFontTheme{Theme: theme.DefaultTheme()}
}

func (unifiedFontTheme) Font(style fyne.TextStyle) fyne.Resource {
	if style.Bold {
		return fontResourceBold
	}
	return fontResourceRegular
}
