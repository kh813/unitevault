package gui

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"fyne.io/fyne/v2/lang"
)

// TestTranslationsFS_LoadsWithoutError guards that the embedded ja.json
// bundle actually parses under go-i18n's real loader (reserved top-level
// keys like "id"/"description"/"one"/"other" etc. mixed with unreserved
// ones, malformed JSON, ...) - a corrupt or malformed translation file
// would otherwise only surface as a silently-ignored fyne.LogError at
// runtime (see InitApp), never as a build or test failure.
func TestTranslationsFS_LoadsWithoutError(t *testing.T) {
	if err := lang.AddTranslationsFS(translationFiles, "translations"); err != nil {
		t.Fatalf("failed to load embedded translations: %v", err)
	}
}

// wantIndirectKeys lists translation keys that reach lang.L(...) only
// through a variable (so extractLangLKeys, which only resolves string-
// literal arguments, can't find them) - the values SettingsFormData's
// GitStatus/RcloneStatus/ICloudStatus/DeviceRole fields actually hold, set
// in cmd/unitevault/main.go's buildFormData/reopenSettingsGUI and displayed
// via lang.L(orDefault(data.XStatus, ...)) / lang.L(data.ICloudStatus) /
// lang.L(orDefault(data.DeviceRole, "N/A")) in settings_window.go.
var wantIndirectKeys = []string{
	"Installed",
	"Not Found",
	"Not Found (requires an administrator account to install)",
	"Unknown",
	"primary",
	"secondary",
	"N/A",
}

// TestJaTranslations_CoversEveryLangLString is a source-level regression
// guard against silently shipping an untranslated UI string: it parses
// this package's own .go files plus cmd/unitevault/main.go (the other
// place that builds user-facing dialogs/labels, per spec section 8.5.2)
// looking for every lang.L(literal) call - including ones built from
// adjacent "a" + "b" string-literal concatenation - and fails if any
// resolved literal, or any of wantIndirectKeys above, is missing from
// translations/ja.json. It can't verify a translation actually *renders*
// correctly (Fyne's lang package exposes no public way to force the active
// locale for testing - only OS locale auto-detection), but it does
// guarantee every English string this app can show has *a* Japanese entry
// to fall back to.
func TestJaTranslations_CoversEveryLangLString(t *testing.T) {
	entries := loadJaTranslationKeys(t)

	for _, f := range []string{"app.go", "dialogs.go", "settings_window.go"} {
		requireLangLKeysCovered(t, f, entries)
	}

	mainGoPath := filepath.Join("..", "..", "cmd", "unitevault", "main.go")
	if _, err := os.Stat(mainGoPath); err != nil {
		t.Fatalf("expected to find cmd/unitevault/main.go at %s: %v", mainGoPath, err)
	}
	requireLangLKeysCovered(t, mainGoPath, entries)

	for _, key := range wantIndirectKeys {
		if _, ok := entries[key]; !ok {
			t.Errorf("translations/ja.json is missing an entry for %q (reached via a variable, not a lang.L(literal) call - see wantIndirectKeys)", key)
		}
	}
}

func loadJaTranslationKeys(t *testing.T) map[string]bool {
	t.Helper()
	data, err := translationFiles.ReadFile("translations/ja.json")
	if err != nil {
		t.Fatalf("failed to read translations/ja.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to parse translations/ja.json: %v", err)
	}
	keys := make(map[string]bool, len(m))
	for k := range m {
		keys[k] = true
	}
	return keys
}

func requireLangLKeysCovered(t *testing.T, path string, entries map[string]bool) {
	t.Helper()
	for _, key := range extractLangLKeys(t, path) {
		if !entries[key] {
			t.Errorf("%s: translations/ja.json is missing an entry for lang.L(%q)", path, key)
		}
	}
}

// extractLangLKeys parses the Go source file at path and returns the
// resolved string value of every lang.L(...) call's first argument that is
// statically a string literal (optionally "a" + "b" concatenated) - it
// skips calls whose first argument is anything else (a variable, a call
// like orDefault(...), etc.), since those can't be resolved without
// running the program.
func extractLangLKeys(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("failed to parse %s: %v", path, err)
	}

	var keys []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "L" {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "lang" {
			return true
		}
		if key, ok := resolveStringLiteral(call.Args[0]); ok {
			keys = append(keys, key)
		}
		return true
	})
	return keys
}

// resolveStringLiteral evaluates expr as a constant string expression built
// only from string literals and "+" concatenation, returning its Go string
// value. Returns ok=false for anything else (identifiers, function calls, ...).
func resolveStringLiteral(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(e.Value)
		if err != nil {
			return "", false
		}
		return v, true
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, ok := resolveStringLiteral(e.X)
		if !ok {
			return "", false
		}
		right, ok := resolveStringLiteral(e.Y)
		if !ok {
			return "", false
		}
		return left + right, true
	default:
		return "", false
	}
}
