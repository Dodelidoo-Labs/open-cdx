import AppKit
import Sparkle

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    var openURLHandler: ((URL) -> Void)? {
        didSet { deliverPendingURLs() }
    }

    private var pendingURLs: [URL] = []
    private let updaterController: SPUStandardUpdaterController

    override init() {
        updaterController = SPUStandardUpdaterController(
            startingUpdater: true,
            updaterDelegate: nil,
            userDriverDelegate: nil
        )
        super.init()
    }

    func checkForUpdates() {
        updaterController.checkForUpdates(nil)
    }

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
