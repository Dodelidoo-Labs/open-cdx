import AppKit

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    var openURLHandler: ((URL) -> Void)? {
        didSet { deliverPendingURLs() }
    }

    private var pendingURLs: [URL] = []

    func application(_ application: NSApplication, open urls: [URL]) {
        guard let openURLHandler else {
            pendingURLs.append(contentsOf: urls)
            return
        }
        urls.forEach(openURLHandler)
    }

    private func deliverPendingURLs() {
        guard let openURLHandler, !pendingURLs.isEmpty else { return }
        let urls = pendingURLs
        pendingURLs.removeAll()
        urls.forEach(openURLHandler)
    }
}
