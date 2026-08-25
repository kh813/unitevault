// Renders UniteVault's icon artwork (a two-arrow circular sync glyph) to PNG
// using CoreGraphics, at whatever size/color mode is requested. Used to
// regenerate assets/icon.iconset, assets/AppIcon.icns and the tray icon PNGs
// without depending on any SVG rasterizer.
//
// Usage: swift gen_icon.swift <mode> <size> <outPath>
//   mode: "app"   - purple rounded-square background + white glyph (Dock/Finder icon, Windows/Linux tray, colored icon uses)
//         "mono"  - transparent background + solid black glyph (macOS menu bar template icon)
import Cocoa

func hexColor(_ r: CGFloat, _ g: CGFloat, _ b: CGFloat) -> NSColor {
    NSColor(calibratedRed: r / 255, green: g / 255, blue: b / 255, alpha: 1)
}

let purple = hexColor(0x7C, 0x3A, 0xED)
let white = NSColor.white
let black = NSColor.black

func drawArrow(_ cg: CGContext, center: CGPoint, radius: CGFloat, lineWidth: CGFloat,
               startDeg: CGFloat, endDeg: CGFloat, color: NSColor, arrowBoost: CGFloat = 1.0) {
    let startRad = startDeg * .pi / 180
    let endRad = endDeg * .pi / 180

    let path = CGMutablePath()
    path.addArc(center: center, radius: radius, startAngle: startRad, endAngle: endRad, clockwise: false)
    cg.addPath(path)
    cg.setLineWidth(lineWidth)
    cg.setLineCap(.round)
    cg.setStrokeColor(color.cgColor)
    cg.strokePath()

    // Arrowhead at the end point, tangent to counter-clockwise travel direction.
    // The triangle's base sits right at the stroke's end (slightly behind it,
    // so the round line cap doesn't peek out past the base) and its tip
    // extends further along the tangent direction.
    let endPoint = CGPoint(x: center.x + radius * cos(endRad), y: center.y + radius * sin(endRad))
    let tangent = CGPoint(x: -sin(endRad), y: cos(endRad))
    let normal = CGPoint(x: -tangent.y, y: tangent.x)

    let arrowLen = lineWidth * 1.5 * arrowBoost
    let arrowWidth = lineWidth * 1.35 * arrowBoost
    let back = CGPoint(x: endPoint.x - tangent.x * lineWidth * 0.2, y: endPoint.y - tangent.y * lineWidth * 0.2)
    let tip = CGPoint(x: back.x + tangent.x * arrowLen, y: back.y + tangent.y * arrowLen)
    let left = CGPoint(x: back.x + normal.x * arrowWidth / 2, y: back.y + normal.y * arrowWidth / 2)
    let right = CGPoint(x: back.x - normal.x * arrowWidth / 2, y: back.y - normal.y * arrowWidth / 2)

    let tri = CGMutablePath()
    tri.move(to: tip)
    tri.addLine(to: left)
    tri.addLine(to: right)
    tri.closeSubpath()
    cg.addPath(tri)
    cg.setFillColor(color.cgColor)
    cg.fillPath()
}

func render(mode: String, size: Int) -> NSBitmapImageRep {
    let s = CGFloat(size)
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size,
                                bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    rep.size = NSSize(width: s, height: s)

    NSGraphicsContext.saveGraphicsState()
    let ctx = NSGraphicsContext(bitmapImageRep: rep)!
    NSGraphicsContext.current = ctx
    let cg = ctx.cgContext
    cg.clear(CGRect(x: 0, y: 0, width: s, height: s))

    var glyphColor = white
    var radiusFactor: CGFloat = 0.30
    var lineWidthFactor: CGFloat = 0.135
    var arrowBoost: CGFloat = 1.0

    switch mode {
    case "app":
        let corner = s * 0.225
        let rect = CGRect(x: 0, y: 0, width: s, height: s)
        cg.addPath(CGPath(roundedRect: rect, cornerWidth: corner, cornerHeight: corner, transform: nil))
        cg.setFillColor(purple.cgColor)
        cg.fillPath()
        glyphColor = white
    case "mono":
        glyphColor = black
        // Bolder/larger so the glyph still reads at tiny menu-bar sizes
        // (as small as ~18-22pt), where fine detail disappears.
        radiusFactor = 0.34
        lineWidthFactor = 0.20
        arrowBoost = 1.35
    default:
        fatalError("unknown mode \(mode)")
    }

    let center = CGPoint(x: s / 2, y: s / 2)
    let radius = s * radiusFactor
    let lineWidth = s * lineWidthFactor

    drawArrow(cg, center: center, radius: radius, lineWidth: lineWidth, startDeg: 25, endDeg: 155, color: glyphColor, arrowBoost: arrowBoost)
    drawArrow(cg, center: center, radius: radius, lineWidth: lineWidth, startDeg: 205, endDeg: 335, color: glyphColor, arrowBoost: arrowBoost)

    NSGraphicsContext.restoreGraphicsState()
    return rep
}

let args = CommandLine.arguments
guard args.count == 4, let size = Int(args[2]) else {
    FileHandle.standardError.write("Usage: swift gen_icon.swift <app|mono> <size> <outPath>\n".data(using: .utf8)!)
    exit(1)
}
let mode = args[1]
let outPath = args[3]

let rep = render(mode: mode, size: size)
guard let data = rep.representation(using: .png, properties: [:]) else {
    FileHandle.standardError.write("failed to encode PNG\n".data(using: .utf8)!)
    exit(1)
}
try! data.write(to: URL(fileURLWithPath: outPath))
print("wrote \(outPath) (\(size)x\(size), mode=\(mode))")
