import SwiftUI

@main
struct OpenCDXRouterMenuApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @StateObject private var model = HelperModel()

    var body: some Scene {
        MenuBarExtra {
            RouterMenuView(model: model)
                .onAppear { model.refreshStatus() }
        } label: {
            Image(systemName: model.menuIcon)
                .accessibilityLabel("OpenCDX Router: \(model.status.state)")
                .onAppear {
                    appDelegate.openURLHandler = { [weak model] url in model?.handle(url: url) }
                    model.start()
                }
        }
        .menuBarExtraStyle(.window)

        Settings {
            SettingsView(model: model)
        }
    }
}
