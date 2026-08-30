import SwiftUI

struct RouterMenuView: View {
    @ObservedObject var model: HelperModel

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
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .firstTextBaseline) {
                Text("Accounts")
                    .font(.subheadline.weight(.semibold))
                Spacer(minLength: 10)
                Text("Updates within 30 sec")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }

            if model.status.accounts.isEmpty {
                Text(model.status.connected ? "No OpenAI accounts connected." : "Account allowances are unavailable while disconnected.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                ForEach(Array(model.status.accounts.enumerated()), id: \.offset) { _, account in
                    AccountAllowanceRow(account: account)
                }
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 13)
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
        return model.status.connected ? .green : .red
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
        return model.status.restartRequired ? .orange : .green
    }

    private var activeRouteLabel: String {
        [model.status.provider.capitalized, model.status.model]
            .filter { !$0.isEmpty }
            .joined(separator: " · ")
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

private struct AccountAllowanceRow: View {
    let account: AccountAllowanceStatus

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 8) {
                Text(account.maskedEmail.isEmpty ? "OpenAI account" : account.maskedEmail)
                    .font(.system(size: 12, weight: .medium))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer(minLength: 10)
                Text("\(Int(remaining.rounded()))% left")
                    .font(.system(size: 12, weight: .semibold, design: .rounded))
                    .monospacedDigit()
            }

            ProgressView(value: remaining, total: 100)
                .progressViewStyle(.linear)
                .controlSize(.mini)
                .tint(allowanceColor)
                .accessibilityLabel("\(account.maskedEmail) allowance remaining")
                .accessibilityValue("\(Int(remaining.rounded())) percent")

            HStack(spacing: 8) {
                Text(accountDescription)
                Spacer(minLength: 10)
                if let resetDescription {
                    Text(resetDescription)
                        .monospacedDigit()
                }
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
            .lineLimit(1)
        }
    }

    private var remaining: Double {
        min(max(account.quotaRemaining, 0), 100)
    }

    private var allowanceColor: Color {
        if account.paused { return .secondary }
        if remaining <= 10 { return .red }
        if remaining <= 25 { return .orange }
        return .green
    }

    private var accountDescription: String {
        var parts: [String] = []
        if !account.plan.isEmpty { parts.append(account.plan.localizedCapitalized) }
        if account.primary { parts.append("Primary") }
        if account.paused {
            parts.append("Paused")
        } else if !account.status.isEmpty && account.status != "ready" {
            parts.append(account.status.localizedCapitalized)
        }
        return parts.isEmpty ? "Allowance" : parts.joined(separator: " · ")
    }

    private var resetDescription: String? {
        guard let resetAt = account.quotaResetAt else { return nil }
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
        return "Resets in \(duration)"
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
