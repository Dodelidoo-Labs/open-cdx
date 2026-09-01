import SwiftUI

struct RouterMenuView: View {
    @ObservedObject var model: HelperModel
    let checkForUpdates: () -> Void

    var body: some View {
        VStack(spacing: 0) {
            statusSection

            Divider()
                .padding(.horizontal, 12)

            accountsSection

            Divider()
                .padding(.horizontal, 12)

            VStack(spacing: 2) {
                MenuActionButton("Open Dashboard", systemImage: "rectangle.on.rectangle") {
                    model.openDashboard()
                }
                .disabled(!model.configured)

                MenuActionButton(model.accountLoginInProgress ? "OpenAI Login in Progress…" : "Add OpenAI Account…", systemImage: "person.badge.plus") {
                    model.addOpenAIAccount()
                }
                .disabled(!remoteActionsAvailable || model.accountLoginInProgress)
            }
            .padding(8)

            Divider()
                .padding(.horizontal, 12)

            VStack(spacing: 2) {
                MenuActionButton("Refresh Allowances", systemImage: "arrow.clockwise") {
                    model.refreshQuotas()
                }
                .disabled(!remoteActionsAvailable)

                MenuActionButton("Refresh Model Catalog", systemImage: "square.stack.3d.up") {
                    model.refreshCatalog()
                }
                .disabled(!remoteActionsAvailable)

                if model.configured && !model.status.connected {
                    MenuActionButton("Retry Connection", systemImage: "network") {
                        model.retryConnection()
                    }
                }

                MenuActionButton("Copy Codex Configuration", systemImage: "doc.on.doc") {
                    model.copyConfiguration()
                }
                .disabled(!remoteActionsAvailable)

                MenuActionButton("Reconcile Usage History…", systemImage: "clock.arrow.circlepath") {
                    model.requestUsageReconciliation()
                }
                .disabled(!model.status.connected || model.usageReconciliationInProgress || model.telemetryResetInProgress)

                MenuActionButton("Reset Telemetry…", systemImage: "trash") {
                    model.requestTelemetryReset()
                }
                .disabled(!model.status.connected || model.usageReconciliationInProgress || model.telemetryResetInProgress)
            }
            .padding(8)

            Divider()
                .padding(.horizontal, 12)

            VStack(spacing: 2) {
                if #available(macOS 14.0, *) {
                    SettingsLink {
                        MenuActionLabel("Settings…", systemImage: "gearshape")
                    }
                    .buttonStyle(.plain)
                } else {
                    MenuActionButton("Settings…", systemImage: "gearshape") {
                        NSApp.sendAction(Selector(("showSettingsWindow:")), to: nil, from: nil)
                    }
                }

                MenuActionButton("Check for Updates…", systemImage: "arrow.down.circle") {
                    checkForUpdates()
                }

                HStack(spacing: 0) {
                    MenuIconTitle("Launch at Login", systemImage: "play")
                    Spacer(minLength: 12)
                    Toggle("Launch at Login", isOn: Binding(
                        get: { model.launchAtLogin },
                        set: { model.setLaunchAtLogin($0) }
                    ))
                    .labelsHidden()
                    .toggleStyle(.switch)
                    .controlSize(.mini)
                }
                .padding(.horizontal, 8)
                .frame(maxWidth: .infinity, minHeight: 28, alignment: .leading)

                MenuActionButton("Quit OpenCDX Router", systemImage: "power") {
                    model.quit()
                }
            }
            .padding(8)
        }
        .frame(width: 360)
        .padding(.vertical, 6)
    }

    private var remoteActionsAvailable: Bool {
        routerOperationsAvailable(configured: model.configured, connected: model.status.connected)
    }

    private var statusSection: some View {
        VStack(spacing: 9) {
            StatusSummaryRow(
                title: "Router",
                value: model.routerStatusLabel,
                systemImage: routerStatusIcon,
                color: routerStatusColor
            )

            if model.configured {
                StatusSummaryRow(
                    title: "Catalog",
                    value: catalogStatusLabel,
                    systemImage: catalogStatusIcon,
                    color: catalogStatusColor,
                    actionTitle: model.status.restartRequired ? "Done" : nil,
                    action: model.status.restartRequired ? { model.acknowledgeCodexRestart() } : nil
                )
            }

            if !model.status.model.isEmpty || !model.status.provider.isEmpty {
                HStack(spacing: 8) {
                    Text("Active route")
                        .foregroundStyle(.secondary)
                    Spacer(minLength: 12)
                    Text(activeRouteLabel)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                .font(.caption)
            }

            if !model.status.lastError.isEmpty {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                    Text(model.status.lastError)
                        .foregroundStyle(.secondary)
                }
                .font(.caption)
                .frame(maxWidth: .infinity, alignment: .leading)
                .fixedSize(horizontal: false, vertical: true)
            }

            if !model.operation.isEmpty {
                Text(model.operation)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
    }

    private var accountsSection: some View {
        AccountAllowanceSection(accounts: model.status.accounts, connected: model.status.connected)
    }

    private var routerStatusIcon: String {
        if !model.configured { return "gearshape.fill" }
        if model.inferenceActive { return "waveform.circle.fill" }
        if model.status.connected { return "checkmark.circle.fill" }
        if model.status.state == "connecting" { return "arrow.clockwise.circle" }
        return "exclamationmark.circle.fill"
    }

    private var routerStatusColor: Color {
        if !model.configured || model.status.state == "connecting" { return .secondary }
        return model.status.connected ? .accentColor : .red
    }

    private var catalogStatusLabel: String {
        if !model.status.catalogSynced { return "Pending" }
        return model.status.restartRequired ? "Restart Codex" : "Synchronized"
    }

    private var catalogStatusIcon: String {
        if !model.status.catalogSynced { return "clock" }
        return model.status.restartRequired ? "exclamationmark.triangle.fill" : "checkmark.circle.fill"
    }

    private var catalogStatusColor: Color {
        if !model.status.catalogSynced { return .secondary }
        return model.status.restartRequired ? .orange : .accentColor
    }

    private var activeRouteLabel: String {
        [model.status.provider.capitalized, model.status.model]
            .filter { !$0.isEmpty }
            .joined(separator: " · ")
    }
}

struct AccountAllowanceSection: View {
    let accounts: [AccountAllowanceStatus]
    let connected: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .firstTextBaseline) {
                Text("Accounts")
                    .font(.subheadline.weight(.semibold))
                Spacer(minLength: 10)
                Text("Updates within 30 sec")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }

            if accounts.isEmpty {
                Text(connected ? "No OpenAI accounts connected." : "Account allowances are unavailable while disconnected.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                AccountAllowanceList(accounts: accounts)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 13)
    }
}

private struct StatusSummaryRow: View {
    let title: String
    let value: String
    let systemImage: String
    let color: Color
    let actionTitle: String?
    let action: (() -> Void)?

    init(
        title: String,
        value: String,
        systemImage: String,
        color: Color,
        actionTitle: String? = nil,
        action: (() -> Void)? = nil
    ) {
        self.title = title
        self.value = value
        self.systemImage = systemImage
        self.color = color
        self.actionTitle = actionTitle
        self.action = action
    }

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: systemImage)
                .foregroundStyle(color)
                .frame(width: 16, alignment: .center)
            Text(title)
                .fontWeight(.medium)
            Spacer(minLength: 16)
            Text(value)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            if let actionTitle, let action {
                Button(actionTitle, action: action)
                    .buttonStyle(.bordered)
                    .controlSize(.mini)
                    .help("Use after restarting Codex to clear the catalog reminder.")
            }
        }
        .font(.system(size: 13))
    }
}

struct AccountAllowanceList: View {
    let accounts: [AccountAllowanceStatus]

    var body: some View {
        VStack(spacing: 12) {
            ForEach(Array(accounts.enumerated()), id: \.offset) { _, account in
                AccountAllowanceRow(account: account)
            }
        }
    }
}

struct AccountAllowanceRow: View {
    let account: AccountAllowanceStatus

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 8) {
                Text(account.maskedEmail.isEmpty ? "OpenAI account" : account.maskedEmail)
                    .font(.system(size: 12, weight: .medium))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer(minLength: 10)
                Text(accountTypeDescription)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            if let mainWindow {
                AllowanceWindowRow(
                    window: mainWindow,
                    tint: allowanceColor(for: clampedRemaining(mainWindow)),
                    height: 1,
                    resetDescription: resetDescription(for: mainWindow.resetAt),
                    accessibilityName: "\(account.maskedEmail) \(mainWindow.label) allowance"
                )

                ForEach(Array(secondaryWindows.enumerated()), id: \.offset) { _, window in
                    AllowanceWindowRow(
                        window: window,
                        tint: allowanceColor(for: clampedRemaining(window)),
                        height: 1,
                        resetDescription: resetDescription(for: window.resetAt),
                        accessibilityName: "\(account.maskedEmail) \(window.label) allowance"
                    )
                }
            } else {
                Text("Allowance unavailable")
                .font(.caption2)
                .foregroundStyle(.secondary)
            }
        }
    }

    private var displayedWindows: [AccountQuotaWindowStatus] {
        if !account.quotaWindows.isEmpty { return account.quotaWindows }
        guard let resetAt = account.quotaResetAt else { return [] }
        return [AccountQuotaWindowStatus(label: "Allowance", remaining: account.quotaRemaining, resetAt: resetAt)]
    }

    private var mainWindow: AccountQuotaWindowStatus? { displayedWindows.first }
    private var secondaryWindows: ArraySlice<AccountQuotaWindowStatus> { displayedWindows.dropFirst() }

    private func clampedRemaining(_ window: AccountQuotaWindowStatus) -> Double {
        min(max(window.remaining, 0), 100)
    }

    private func allowanceColor(for remaining: Double) -> Color {
        if account.paused { return .secondary }
        if remaining <= 10 { return .red }
        if remaining <= 25 { return .orange }
        return .accentColor
    }

    private var accountTypeDescription: String {
        var parts: [String] = []
        if !account.plan.isEmpty { parts.append(account.plan.uppercased()) }
        if account.primary { parts.append("PRIMARY") }
        if account.paused {
            parts.append("PAUSED")
        } else if !account.status.isEmpty && account.status != "ready" {
            parts.append(account.status.uppercased())
        }
        return parts.joined(separator: " · ")
    }

    private func resetDescription(for resetAt: Date?) -> String? {
        guard let resetAt else { return nil }
        let interval = resetAt.timeIntervalSinceNow
        guard interval > 0 else { return "Reset pending" }

        let formatter = DateComponentsFormatter()
        formatter.unitsStyle = .abbreviated
        formatter.maximumUnitCount = 2
        formatter.zeroFormattingBehavior = .dropAll
        if interval >= 24 * 60 * 60 {
            formatter.allowedUnits = [.day, .hour]
        } else if interval >= 60 * 60 {
            formatter.allowedUnits = [.hour, .minute]
        } else {
            formatter.allowedUnits = [.minute]
        }
        guard let duration = formatter.string(from: max(interval, 60)) else { return nil }
        return "Reset \(duration)"
    }
}

private struct AllowanceWindowRow: View {
    let window: AccountQuotaWindowStatus
    let tint: Color
    let height: CGFloat
    let resetDescription: String?
    let accessibilityName: String

    var body: some View {
        VStack(spacing: 4) {
            HStack(spacing: 8) {
                Text(displayLabel)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Text("\(Int(clampedRemaining.rounded()))% available")
                    .monospacedDigit()
                    .frame(maxWidth: .infinity, alignment: .center)
                Text(resetDescription ?? "")
                    .monospacedDigit()
                    .frame(maxWidth: .infinity, alignment: .trailing)
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
            .lineLimit(1)

            AllowanceProgressBar(
                window: window,
                tint: tint,
                height: height,
                accessibilityName: accessibilityName
            )
        }
    }

    private var clampedRemaining: Double {
        min(max(window.remaining, 0), 100)
    }

    private var displayLabel: String {
        switch window.label.lowercased() {
        case "5 hours", "5 hour", "5-hour": return "5-HOUR"
        default: return window.label.uppercased()
        }
    }
}

private struct AllowanceProgressBar: View {
    let window: AccountQuotaWindowStatus
    let tint: Color
    let height: CGFloat
    let accessibilityName: String

    var body: some View {
        GeometryReader { proxy in
            let width = max(proxy.size.width, 0)
            ZStack(alignment: .leading) {
                Rectangle().fill(Color.secondary.opacity(0.22))
                Rectangle()
                    .fill(tint)
                    .frame(width: width * CGFloat(clamped(window.remaining)) / 100)
            }
            .frame(height: height)
            .overlay(alignment: .leading) {
                if !window.paceStatus.isEmpty, let marker = window.paceMarkerPercent {
                    Rectangle()
                        .fill(Color.primary.opacity(0.9))
                        .frame(width: markerWidth, height: max(height + 5, 7))
                        .offset(x: markerOffset(marker, width: width))
                        .accessibilityHidden(true)
                }
            }
        }
        .frame(height: height)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityName)
        .accessibilityValue(accessibilityValue)
    }

    private var accessibilityValue: String {
        var value = "\(Int(clamped(window.remaining).rounded())) percent remaining"
        if window.paceStatus == "on_pace" { value += ", on pace" }
        if window.paceStatus == "too_fast" { value += ", going fast" }
        return value
    }

    private func markerOffset(_ marker: Double, width: CGFloat) -> CGFloat {
        guard width > markerWidth else { return 0 }
        let position = width * CGFloat(clamped(marker)) / 100
        return min(max(position - markerWidth / 2, 0), width - markerWidth)
    }

    private var markerWidth: CGFloat { 1.5 }

    private func clamped(_ value: Double) -> Double {
        min(max(value, 0), 100)
    }
}

private struct MenuActionButton: View {
    let title: String
    let systemImage: String
    let action: () -> Void

    init(_ title: String, systemImage: String, action: @escaping () -> Void) {
        self.title = title
        self.systemImage = systemImage
        self.action = action
    }

    var body: some View {
        Button(action: action) {
            MenuActionLabel(title, systemImage: systemImage)
        }
        .buttonStyle(.plain)
    }
}

private struct MenuActionLabel: View {
    let title: String
    let systemImage: String

    @Environment(\.isEnabled) private var isEnabled
    @State private var isHovering = false

    init(_ title: String, systemImage: String) {
        self.title = title
        self.systemImage = systemImage
    }

    var body: some View {
        HStack(spacing: 0) {
            MenuIconTitle(title, systemImage: systemImage)
            Spacer(minLength: 8)
        }
        .foregroundStyle(Color.primary)
        .padding(.horizontal, 8)
        .frame(maxWidth: .infinity, minHeight: 28, alignment: .leading)
        .background {
            RoundedRectangle(cornerRadius: 6, style: .continuous)
                .fill(isHovering && isEnabled ? Color.primary.opacity(0.08) : .clear)
        }
        .contentShape(Rectangle())
        .opacity(isEnabled ? 1 : 0.42)
        .onHover { isHovering = $0 }
    }
}

private struct MenuIconTitle: View {
    let title: String
    let systemImage: String

    init(_ title: String, systemImage: String) {
        self.title = title
        self.systemImage = systemImage
    }

    var body: some View {
        HStack(spacing: 9) {
            Image(systemName: systemImage)
                .font(.system(size: 15, weight: .regular))
                .symbolRenderingMode(.monochrome)
                .frame(width: 18, height: 18, alignment: .center)
            Text(title)
        }
    }
}

#if DEBUG
@MainActor
private struct RouterMenuViewPreviews: PreviewProvider {
    static var previews: some View {
        Group {
            RouterMenuView(model: mixedWindowsModel, checkForUpdates: {})
                .previewDisplayName("Weekly-only Pro and two-window Plus")
            RouterMenuView(model: edgeCasesModel, checkForUpdates: {})
                .previewDisplayName("Going fast and unavailable")
        }
    }

    private static var mixedWindowsModel: HelperModel {
        let now = Date()
        let model = HelperModel()
        var status = previewStatus()
        status.accounts = [
            previewAccount(
                email: "h***o@t***.com",
                plan: "pro",
                primary: true,
                windows: [
                    AccountQuotaWindowStatus(
                        label: "Weekly", remaining: 97, durationMinutes: 10_080,
                        resetAt: now.addingTimeInterval(6 * 24 * 60 * 60 + 5 * 60 * 60),
                        paceStatus: "on_pace", paceMarkerPercent: 88.7, paceBufferPercent: 8.3
                    )
                ]
            ),
            previewAccount(
                email: "s***a@q***.com",
                plan: "plus",
                windows: [
                    AccountQuotaWindowStatus(
                        label: "Weekly", remaining: 100, durationMinutes: 10_080,
                        resetAt: now.addingTimeInterval(6 * 24 * 60 * 60 + 5 * 60 * 60),
                        paceStatus: "on_pace", paceMarkerPercent: 88.7, paceBufferPercent: 11.3
                    ),
                    AccountQuotaWindowStatus(
                        label: "5 hours", remaining: 64, durationMinutes: 300,
                        resetAt: now.addingTimeInterval(2 * 60 * 60 + 13 * 60),
                        paceStatus: "on_pace", paceMarkerPercent: 44.3, paceBufferPercent: 19.7
                    )
                ]
            )
        ]
        model.applyPreviewStatus(status)
        return model
    }

    private static var edgeCasesModel: HelperModel {
        let now = Date()
        let model = HelperModel()
        var status = previewStatus()
        status.accounts = [
            previewAccount(
                email: "long-account-name@example.com",
                plan: "plus",
                primary: true,
                windows: [
                    AccountQuotaWindowStatus(
                        label: "Weekly", remaining: 61, durationMinutes: 10_080,
                        resetAt: now.addingTimeInterval(5 * 24 * 60 * 60),
                        paceStatus: "too_fast", paceMarkerPercent: 71.4, paceBufferPercent: -10.4
                    ),
                    AccountQuotaWindowStatus(label: "5 hours", remaining: 82)
                ]
            ),
            previewAccount(email: "no-windows@example.com", plan: "pro", windows: [])
        ]
        model.applyPreviewStatus(status)
        return model
    }

    private static func previewStatus() -> HelperStatus {
        var status = HelperStatus()
        status.state = "connected"
        status.connected = true
        status.catalogSynced = true
        status.provider = "openai"
        status.model = "gpt-5.6-terra"
        return status
    }

    private static func previewAccount(
        email: String,
        plan: String,
        primary: Bool = false,
        windows: [AccountQuotaWindowStatus]
    ) -> AccountAllowanceStatus {
        var account = AccountAllowanceStatus()
        account.maskedEmail = email
        account.plan = plan
        account.status = "ready"
        account.primary = primary
        account.quotaWindows = windows
        return account
    }
}
#endif
