import SwiftUI

struct SettingsView: View {
    @ObservedObject var model: HelperModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                Text("Router Connection").font(.title2).bold()
                Text("Production router addresses must use HTTPS. Plain HTTP on a LAN is available only when insecure development mode is explicitly enabled.")
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                TextField("https://router.example.com", text: $model.routerURL)
                    .textFieldStyle(.roundedBorder)
                TextField("Device name", text: $model.deviceName)
                    .textFieldStyle(.roundedBorder)
                Toggle("Allow plaintext LAN router for development", isOn: $model.insecureDevelopment)
                HStack {
                    Button("Request Enrollment") { model.requestEnrollment() }
                        .buttonStyle(.borderedProminent)
                        .disabled(!model.canRequestEnrollment)
                    Text("An administrator must approve this Mac in the dashboard.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Divider()
                Text("ChatGPT App").font(.headline)
                Text("Use ChatGPT’s native connection for dots and other account features while CLI and IDE clients use the router. Quit ChatGPT, then open it here each time. The app uses a separate local history and settings; sign in to ChatGPT on first use. App requests go directly to OpenAI and do not use the routing pool.")
                    .font(.callout).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Button("Open ChatGPT Without Routing") { model.openChatGPTWithoutRouting() }
                Divider()
                Text("Codex").font(.headline)
                Text("Replace only this Mac’s history on the connected server using its local Codex history (~/.codex). Other machines’ history is preserved. You can review the import before replacing anything.")
                    .font(.callout).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Button("Import Codex History…") { model.requestUsageReconciliation() }
                    .disabled(!model.status.connected || model.usageReconciliationInProgress || model.telemetryResetInProgress)
                Divider()
                Text("Claude Code").font(.headline)
                Text("Show your Claude plan allowance in the menu and Claude Code usage on the dashboard. OpenCDX adds a status line wrapper and local usage export to ~/.claude/settings.json. Claude Code keeps signing in and talking to Anthropic directly; prompts, responses, and credentials never reach OpenCDX.")
                    .font(.callout).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                if let setup = model.claudeSetup {
                    Text(setup.installed ? "Status: \(model.claudeReporting.label)" : "Status: Not connected")
                        .font(.callout)
                    if setup.installed {
                        ClaudeAllowanceStatusView(claudeCode: model.status.claudeCode, claudeAccounts: model.status.claudeAccounts)
                    }
                }
                HStack {
                    if model.claudeSetup?.installed == true {
                        Button("Disconnect Claude Code…") { model.disconnectClaudeCode() }
                            .disabled(model.claudeOperationInProgress)
                    } else {
                        Button("Connect Claude Code…") { model.connectClaudeCode() }
                            .disabled(!model.status.connected || model.claudeOperationInProgress)
                    }
                    Button("Import Claude Code History…") { model.importClaudeHistory() }
                        .disabled(!model.status.connected || model.claudeOperationInProgress)
                    if model.claudeSetup?.installed == true {
                        Button("Check Now") { model.refreshQuotas() }
                            .disabled(!model.status.connected)
                            .help("Read the Claude allowance now instead of at the next 5-minute check.")
                    }
                }
                Divider()
                Label("Delete Server History", systemImage: "exclamationmark.triangle.fill")
                    .font(.headline).foregroundStyle(.red)
                Text("Permanently deletes telemetry for every machine on the connected server, including imported usage and allowance history. This cannot be undone. Local Codex and Claude Code files are preserved.")
                    .font(.callout).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Button("Delete All Server History…", role: .destructive) { model.requestTelemetryReset() }
                    .disabled(!model.status.connected || model.usageReconciliationInProgress || model.telemetryResetInProgress)
                if !model.operation.isEmpty {
                    Text(model.operation)
                        .font(.callout)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .fixedSize(horizontal: false, vertical: true)
                        .textSelection(.enabled)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(22)
        }
        .frame(width: 500)
        .frame(minHeight: 460)
        .onAppear { model.refreshClaudeSetup() }
    }
}

/// The Claude allowance line in Settings: when the last reading arrived, or
/// why it stopped updating and how to fix it.
struct ClaudeAllowanceStatusView: View {
    let claudeCode: ClaudeCodeReportingStatus
    let claudeAccounts: [AccountAllowanceStatus]

    var body: some View {
        let observedAt = claudeAccounts.compactMap(\.observedAt).max()
        if let problem = claudeAllowanceProblem(claudeCode: claudeCode, observedAt: observedAt) {
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
                VStack(alignment: .leading, spacing: 4) {
                    Text(observedAt.map { "Allowance: Not updating since \($0.formatted(date: .abbreviated, time: .shortened))" } ?? "Allowance: No reading yet")
                    Text(LocalizedStringKey(problem.settingsText))
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                    if let lastCheck = claudeCode.lastUsageCheckAt {
                        Text("Last check: \(lastCheck.formatted(date: .omitted, time: .shortened))")
                            .foregroundStyle(.secondary)
                    }
                }
                .fixedSize(horizontal: false, vertical: true)
            }
            .font(.callout)
        } else if let observedAt {
            Text("Allowance: Updated \(observedAt.formatted(date: .omitted, time: .shortened))")
                .font(.callout)
        } else {
            Text("Allowance: Waiting for the first reading")
                .font(.callout)
        }
    }
}
