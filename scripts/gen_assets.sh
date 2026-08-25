#!/usr/bin/env bash
# Regenerates all UniteVault icon assets (macOS AppIcon.icns, tray icons for
# macOS/Windows/Linux) from scripts/gen_icon.swift. Run from the repo root:
#   ./scripts/gen_assets.sh
set -euo pipefail

cd "$(dirname "$0")/.."

ICONSET=assets/icon.iconset
TRAY=assets/tray
EMBED_TRAY=cmd/unitevault/assets/tray

rm -rf "$ICONSET"
mkdir -p "$ICONSET" "$TRAY" "$EMBED_TRAY"

echo "== Rendering app icon (Dock/Finder) iconset =="
declare -a SIZES=(
  "16:icon_16x16.png"
  "32:icon_16x16@2x.png"
  "32:icon_32x32.png"
  "64:icon_32x32@2x.png"
  "128:icon_128x128.png"
  "256:icon_128x128@2x.png"
  "256:icon_256x256.png"
  "512:icon_256x256@2x.png"
  "512:icon_512x512.png"
  "1024:icon_512x512@2x.png"
)
for entry in "${SIZES[@]}"; do
  size="${entry%%:*}"
  name="${entry#*:}"
  swift scripts/gen_icon.swift app "$size" "$ICONSET/$name"
done

echo "== Building AppIcon.icns =="
iconutil -c icns "$ICONSET" -o assets/AppIcon.icns

echo "== Rendering tray icons =="
# Colored (app-style) icon: used as the Fyne app/window icon on all
# platforms, and as the system tray icon on Windows/Linux (no native
# light/dark template mechanism there, so a static colored glyph is more
# reliably visible than a monochrome one).
swift scripts/gen_icon.swift app 32 "$TRAY/icon.png"
swift scripts/gen_icon.swift app 64 "$TRAY/icon@2x.png"

# Monochrome glyph: wrapped as a Fyne ThemedResource and used only for the
# macOS menu bar, where it's rendered as a native template icon that macOS
# automatically recolors for the light/dark menu bar.
swift scripts/gen_icon.swift mono 32 "$TRAY/icon-mono.png"
swift scripts/gen_icon.swift mono 64 "$TRAY/icon-mono@2x.png"

echo "== Building tray icon.ico (multi-resolution, colored) =="
swift scripts/gen_icon.swift app 16 /tmp/unitevault-ico-16.png
swift scripts/gen_icon.swift app 32 /tmp/unitevault-ico-32.png
swift scripts/gen_icon.swift app 48 /tmp/unitevault-ico-48.png
swift scripts/gen_icon.swift app 256 /tmp/unitevault-ico-256.png
python3 scripts/pack_ico.py "$TRAY/icon.ico" \
  /tmp/unitevault-ico-16.png /tmp/unitevault-ico-32.png /tmp/unitevault-ico-48.png /tmp/unitevault-ico-256.png
rm -f /tmp/unitevault-ico-16.png /tmp/unitevault-ico-32.png /tmp/unitevault-ico-48.png /tmp/unitevault-ico-256.png

echo "== Syncing tray assets into cmd/unitevault (go:embed source) =="
cp "$TRAY/icon.png" "$TRAY/icon@2x.png" "$TRAY/icon-mono.png" "$TRAY/icon-mono@2x.png" "$TRAY/icon.ico" "$EMBED_TRAY/"

echo "Done."
