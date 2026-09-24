import AppKit
import XCTest

@testable import SkillmKit

/// The status item's image: a template without the Badge; with it, a red
/// dot and a glyph that still follows a light or dark menu bar.
@MainActor
final class StatusIconTests: XCTestCase {
    func testPlainIsATemplate() {
        XCTAssertTrue(StatusIcon.plain.isTemplate)
        XCTAssertTrue(StatusIcon.image(badge: false) === StatusIcon.plain)
    }

    func testBadgedIsNotATemplateAndKeepsTheSize() {
        XCTAssertFalse(StatusIcon.badged.isTemplate)
        XCTAssertTrue(StatusIcon.image(badge: true) === StatusIcon.badged)
        XCTAssertEqual(StatusIcon.badged.size, StatusIcon.plain.size)
        XCTAssertGreaterThan(StatusIcon.plain.size.width, 10)
    }

    func testBadgedDrawsARedDotAndAGlyphForEachAppearance() throws {
        let dark = try render(StatusIcon.badged, .darkAqua)
        let light = try render(StatusIcon.badged, .aqua)
        for rep in [dark, light] {
            // The dot's centre: top right, a little inside.
            let d = Int((min(rep.size.width, rep.size.height) * 0.45).rounded()) * scale
            let c = try XCTUnwrap(rep.colorAt(x: rep.pixelsWide - d / 2, y: d / 2)?.usingColorSpace(.sRGB))
            XCTAssertGreaterThan(c.redComponent, 0.8)
            XCTAssertLessThan(c.greenComponent, 0.5)
            XCTAssertGreaterThan(c.alphaComponent, 0.9)
        }
        // The glyph is light on a dark bar and dark on a light one.
        XCTAssertGreaterThan(try glyphBrightness(dark), 0.7)
        XCTAssertLessThan(try glyphBrightness(light), 0.3)
    }

    private let scale = 2

    /// Draws `image` at 2x with `appearance` current, as the menu bar does.
    private func render(_ image: NSImage, _ appearance: NSAppearance.Name) throws -> NSBitmapImageRep {
        let size = image.size
        let rep = try XCTUnwrap(
            NSBitmapImageRep(
                bitmapDataPlanes: nil, pixelsWide: Int(size.width) * scale, pixelsHigh: Int(size.height) * scale,
                bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
                bytesPerRow: 0, bitsPerPixel: 0))
        rep.size = NSSize(width: CGFloat(Int(size.width)), height: CGFloat(Int(size.height)))
        let appearance = try XCTUnwrap(NSAppearance(named: appearance))
        appearance.performAsCurrentDrawingAppearance {
            NSGraphicsContext.saveGraphicsState()
            NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
            image.draw(in: NSRect(origin: .zero, size: rep.size))
            NSGraphicsContext.restoreGraphicsState()
        }
        return rep
    }

    /// The mean brightness of the opaque pixels that are not the red dot.
    private func glyphBrightness(_ rep: NSBitmapImageRep) throws -> CGFloat {
        var total: CGFloat = 0
        var count = 0
        for y in 0..<rep.pixelsHigh {
            for x in 0..<rep.pixelsWide {
                guard let c = rep.colorAt(x: x, y: y)?.usingColorSpace(.sRGB), c.alphaComponent > 0.9 else { continue }
                if c.redComponent - c.greenComponent > 0.3 { continue }
                total += c.brightnessComponent
                count += 1
            }
        }
        XCTAssertGreaterThan(count, 20, "no glyph drawn")
        return count == 0 ? 0 : total / CGFloat(count)
    }
}
