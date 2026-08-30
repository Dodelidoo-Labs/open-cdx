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
                    Text("An administrator must approve this Mac in the dashboard.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Divider()
                Text("Usage History").font(.headline)
                Text("Import aggregate request and token counts from the default Codex home (~/.codex). OpenCDX shows the exact source and routed/native counts before replacement. Conversation content and credentials never leave this Mac.")
                    .font(.callout).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Button("Reconcile Usage History…") { model.requestUsageReconciliation() }
                    .disabled(!model.status.connected || model.usageReconciliationInProgress || model.telemetryResetInProgress)
                Button("Reset Telemetry…", role: .destructive) { model.requestTelemetryReset() }
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
    }
}
