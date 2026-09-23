import AppKit

/// The status item's image: the app's glyph, with a red dot when the Badge
/// is on.
///
/// Without the dot it is a template image, which the menu bar tints for a
/// light or a dark bar. A template image is drawn in one colour, so the dot
/// needs a non-template image: the glyph is drawn in `labelColor`, resolved
/// each time the image is drawn (`cacheMode = .never`), so it follows the
/// bar's appearance too, and the dot stays red.
@MainActor
public enum StatusIcon {
    /// The SF Symbol drawn as the glyph.
    public static let symbolName = "books.vertical"

    /// The glyph alone, as a template image.
    public static let plain: NSImage = make(badge: false)
    /// The glyph with the red dot at its top right.
    public static let badged: NSImage = make(badge: true)

    public static func image(badge: Bool) -> NSImage { badge ? badged : plain }

    static func make(badge: Bool) -> NSImage {
        let config = NSImage.SymbolConfiguration(pointSize: 15, weight: .regular)
        guard
            let glyph = NSImage(systemSymbolName: symbolName, accessibilityDescription: nil)?
                .withSymbolConfiguration(config)
        else {
            return NSImage(size: NSSize(width: 18, height: 18))
        }
        let description = badge ? "skillm, updates available" : "skillm"
        guard badge else {
            let image = glyph.copy() as! NSImage
            image.isTemplate = true
            image.accessibilityDescription = description
            return image
        }
        let size = glyph.size
        let image = NSImage(size: size, flipped: false) { rect in
            guard let context = NSGraphicsContext.current else { return false }
            let cg = context.cgContext
            // A layer of its own, so the tint and the cut-out below touch
            // only what is drawn here, never what lies under the image.
            cg.beginTransparencyLayer(auxiliaryInfo: nil)
            glyph.draw(in: rect)
            NSColor.labelColor.set()
            rect.fill(using: .sourceAtop)

            let diameter = (min(rect.width, rect.height) * 0.45).rounded()
            let dot = NSRect(x: rect.maxX - diameter, y: rect.maxY - diameter, width: diameter, height: diameter)
            // A clear ring around the dot keeps it apart from the glyph.
            context.compositingOperation = .clear
            NSBezierPath(ovalIn: dot.insetBy(dx: -1.5, dy: -1.5)).fill()
            context.compositingOperation = .sourceOver
            NSColor.systemRed.set()
            NSBezierPath(ovalIn: dot).fill()
            cg.endTransparencyLayer()
            return true
        }
        image.cacheMode = .never
        image.isTemplate = false
        image.accessibilityDescription = description
        return image
    }
}
