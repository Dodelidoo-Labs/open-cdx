import SwiftUI

@main
struct OpenCDXRouterMenuApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @StateObject private var model = HelperModel()

    var body: some Scene {
        MenuBarExtra {
            RouterMenuView(model: model) {
                appDelegate.checkForUpdates()
            }
            .onAppear { model.refreshStatus() }
        } label: {
            Image(nsImage: OpenCDXMenuBarIcon.image)
                .renderingMode(.template)
                .frame(width: 18, height: 18)
                .opacity(model.inferenceActive && !model.activityPulsePhase ? 0.55 : 1)
                .accessibilityLabel("OpenCDX Router: \(model.routerStatusLabel)")
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
