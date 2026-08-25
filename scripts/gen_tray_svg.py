#!/usr/bin/env python3
"""Generates assets/tray/icon-mono.svg: the same two-arrow circular sync
glyph as gen_icon.swift's "mono" mode, as a real SVG rather than a raster
PNG.

This matters specifically for the macOS menu bar icon: Fyne wraps it in a
theme.ThemedResource so the OS treats it as a native template image
(auto-recolored for light/dark menu bars). ThemedResource.Content() always
tries to parse its source as SVG XML to recolor it - handing it a PNG
"works" (it fails to parse, logs a warning, and falls back to the original
PNG bytes unmodified) but a genuine SVG lets that recoloring succeed
cleanly with no warning, and macOS decodes SVG tray icons natively via
NSImage(data:), so quality is at least as good as the PNG version.

Geometry matches gen_icon.swift's "mono" mode constants exactly (viewBox
100x100 standing in for size=100): radiusFactor=0.34, lineWidthFactor=0.20,
arrowBoost=1.35, arrows spanning 25-155deg and 205-335deg.
"""
import math

CX, CY = 50.0, 50.0
SIZE = 100.0
RADIUS = SIZE * 0.34
LINE_WIDTH = SIZE * 0.20
ARROW_BOOST = 1.35
ARROW_LEN = LINE_WIDTH * 2.0 * ARROW_BOOST
ARROW_WIDTH = LINE_WIDTH * 1.7 * ARROW_BOOST


def polar(deg):
    r = math.radians(deg)
    return CX + RADIUS * math.cos(r), CY + RADIUS * math.sin(r)


def arrow_svg(start_deg, end_deg):
    x1, y1 = polar(start_deg)
    x2, y2 = polar(end_deg)
    arc = (
        f'<path d="M {x1:.2f} {y1:.2f} A {RADIUS:.2f} {RADIUS:.2f} 0 0 1 {x2:.2f} {y2:.2f}" '
        f'stroke="#000000" stroke-width="{LINE_WIDTH:.2f}" stroke-linecap="round" fill="none"/>'
    )

    end_rad = math.radians(end_deg)
    tangent = (-math.sin(end_rad), math.cos(end_rad))
    normal = (-tangent[1], tangent[0])
    # back sits behind the round line cap's bulge (radius LINE_WIDTH * 0.5)
    # so the triangle's flat base fully covers it instead of letting it peek
    # out - keeps the bar-to-head transition crisp at small sizes.
    back = (x2 - tangent[0] * LINE_WIDTH * 0.55, y2 - tangent[1] * LINE_WIDTH * 0.55)
    tip = (back[0] + tangent[0] * ARROW_LEN, back[1] + tangent[1] * ARROW_LEN)
    left = (back[0] + normal[0] * ARROW_WIDTH / 2, back[1] + normal[1] * ARROW_WIDTH / 2)
    right = (back[0] - normal[0] * ARROW_WIDTH / 2, back[1] - normal[1] * ARROW_WIDTH / 2)
    triangle = (
        f'<polygon points="{tip[0]:.2f},{tip[1]:.2f} {left[0]:.2f},{left[1]:.2f} '
        f'{right[0]:.2f},{right[1]:.2f}" fill="#000000"/>'
    )
    return arc + "\n  " + triangle


def main():
    # width/height are required alongside viewBox - some SVG renderers
    # (observed via `qlmanage -t` on macOS) mis-scale a viewBox-only SVG with
    # no intrinsic size, badly distorting the stroke widths.
    svg = f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {SIZE:.0f} {SIZE:.0f}" width="{SIZE:.0f}" height="{SIZE:.0f}">
  {arrow_svg(25, 155)}
  {arrow_svg(205, 335)}
</svg>
'''
    with open("assets/tray/icon-mono.svg", "w") as f:
        f.write(svg)
    print("wrote assets/tray/icon-mono.svg")


if __name__ == "__main__":
    main()
