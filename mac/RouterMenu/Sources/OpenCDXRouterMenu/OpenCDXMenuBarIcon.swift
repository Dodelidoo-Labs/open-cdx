import AppKit

enum OpenCDXMenuBarIcon {
    static let image: NSImage = {
        let image: NSImage
        if let url = Bundle.main.url(forResource: "OpenCDXMenuBarTemplate", withExtension: "png"),
           let bundledImage = NSImage(contentsOf: url) {
            image = bundledImage
        } else {
            image = NSImage(
                systemSymbolName: "arrow.triangle.branch",
                accessibilityDescription: "OpenCDX Router"
            ) ?? NSImage(size: NSSize(width: 18, height: 18))
        }
        image.size = NSSize(width: 18, height: 18)
        image.isTemplate = true
        return image
    }()
}
