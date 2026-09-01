import AppKit
import Combine
import Foundation
import ServiceManagement

let openCDXApplicationIdentifier = "com.dodelidoo.opencdx"

func isOpenCDXOAuthURL(_ url: URL) -> Bool {
    url.scheme == openCDXApplicationIdentifier && url.host == "oauth" && url.path == "/openai/start"
}

struct AccountQuotaWindowStatus: Codable {
    var label = "Allowance"
    var remaining = 0.0
    var durationMinutes = 0
    var resetAt: Date?
    var paceStatus = ""
    var paceMarkerPercent: Double?
    var paceBufferPercent = 0.0

    enum CodingKeys: String, CodingKey {
        case label, remaining
        case durationMinutes = "duration_minutes"
        case resetAt = "reset_at"
        case paceStatus = "pace_status"
        case paceMarkerPercent = "pace_marker_percent"
        case paceBufferPercent = "pace_buffer_percent"
    }

    init(
        label: String = "Allowance",
        remaining: Double = 0,
        durationMinutes: Int = 0,
        resetAt: Date? = nil,
        paceStatus: String = "",
        paceMarkerPercent: Double? = nil,
        paceBufferPercent: Double = 0
    ) {
        self.label = label
        self.remaining = remaining
        self.durationMinutes = durationMinutes
        self.resetAt = resetAt
        self.paceStatus = paceStatus
        self.paceMarkerPercent = paceMarkerPercent
        self.paceBufferPercent = paceBufferPercent
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        label = try container.decodeIfPresent(String.self, forKey: .label) ?? "Allowance"
        remaining = try container.decodeIfPresent(Double.self, forKey: .remaining) ?? 0
        durationMinutes = try container.decodeIfPresent(Int.self, forKey: .durationMinutes) ?? 0
        resetAt = try container.decodeIfPresent(Date.self, forKey: .resetAt)
        paceStatus = try container.decodeIfPresent(String.self, forKey: .paceStatus) ?? ""
        paceMarkerPercent = try container.decodeIfPresent(Double.self, forKey: .paceMarkerPercent)
        paceBufferPercent = try container.decodeIfPresent(Double.self, forKey: .paceBufferPercent) ?? 0
    }
}

struct AccountAllowanceStatus: Codable {
    var maskedEmail = ""
    var plan = ""
    var status = ""
    var paused = false
    var primary = false
    var quotaRemaining = 0.0
    var quotaResetAt: Date?
    var quotaWindows: [AccountQuotaWindowStatus] = []
    var resetCredits = 0

    enum CodingKeys: String, CodingKey {
        case plan, status, paused, primary
        case maskedEmail = "masked_email"
        case quotaRemaining = "quota_remaining"
        case quotaResetAt = "quota_reset_at"
        case quotaWindows = "quota_windows"
        case resetCredits = "reset_credits"
    }

    init() {}

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        maskedEmail = try container.decodeIfPresent(String.self, forKey: .maskedEmail) ?? ""
        plan = try container.decodeIfPresent(String.self, forKey: .plan) ?? ""
        status = try container.decodeIfPresent(String.self, forKey: .status) ?? ""
        paused = try container.decodeIfPresent(Bool.self, forKey: .paused) ?? false
        primary = try container.decodeIfPresent(Bool.self, forKey: .primary) ?? false
        quotaRemaining = try container.decodeIfPresent(Double.self, forKey: .quotaRemaining) ?? 0
        quotaResetAt = try container.decodeIfPresent(Date.self, forKey: .quotaResetAt)
        quotaWindows = try container.decodeIfPresent([AccountQuotaWindowStatus].self, forKey: .quotaWindows) ?? []
        resetCredits = try container.decodeIfPresent(Int.self, forKey: .resetCredits) ?? 0
    }
}

struct HelperStatus: Codable {
    var state = "disconnected"
    var connected = false
    var activeRequests = 0
    var routerURL = ""
    var deviceName = ""
    var accounts: [AccountAllowanceStatus] = []
    var provider = ""
    var model = ""
    var account = ""
    var quotaRemaining = 0.0
    var quotaResetAt: Date?
    var catalogSynced = false
    var catalogUpdated: Date?
    var restartRequired = false
    var lastRequestAt: Date?
    var lastError = ""

    enum CodingKeys: String, CodingKey {
        case state, connected, accounts, provider, model, account
        case activeRequests = "active_requests"
        case routerURL = "router_url"
        case deviceName = "device_name"
        case quotaRemaining = "quota_remaining"
        case quotaResetAt = "quota_reset_at"
        case catalogSynced = "catalog_synced"
        case catalogUpdated = "catalog_updated_at"
        case restartRequired = "codex_restart_required"
        case lastRequestAt = "last_request_at"
        case lastError = "last_error"
    }

    init() {}

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        state = try container.decodeIfPresent(String.self, forKey: .state) ?? "disconnected"
        connected = try container.decodeIfPresent(Bool.self, forKey: .connected) ?? false
        activeRequests = try container.decodeIfPresent(Int.self, forKey: .activeRequests) ?? 0
        routerURL = try container.decodeIfPresent(String.self, forKey: .routerURL) ?? ""
        deviceName = try container.decodeIfPresent(String.self, forKey: .deviceName) ?? ""
        accounts = try container.decodeIfPresent([AccountAllowanceStatus].self, forKey: .accounts) ?? []
        provider = try container.decodeIfPresent(String.self, forKey: .provider) ?? ""
        model = try container.decodeIfPresent(String.self, forKey: .model) ?? ""
        account = try container.decodeIfPresent(String.self, forKey: .account) ?? ""
        quotaRemaining = try container.decodeIfPresent(Double.self, forKey: .quotaRemaining) ?? 0
        quotaResetAt = try container.decodeIfPresent(Date.self, forKey: .quotaResetAt)
        catalogSynced = try container.decodeIfPresent(Bool.self, forKey: .catalogSynced) ?? false
        catalogUpdated = try container.decodeIfPresent(Date.self, forKey: .catalogUpdated)
        restartRequired = try container.decodeIfPresent(Bool.self, forKey: .restartRequired) ?? false
        lastRequestAt = try container.decodeIfPresent(Date.self, forKey: .lastRequestAt)
        lastError = try container.decodeIfPresent(String.self, forKey: .lastError) ?? ""
    }
}

func operationAfterApplyingStatus(_ operation: String, status: HelperStatus) -> String {
    if status.connected && operation == "Device approved. Connecting…" {
        return ""
    }
    return operation
}

func routerOperationsAvailable(configured: Bool, connected: Bool) -> Bool {
    configured && connected
}

struct UsageHistoryPreview: Decodable, Equatable {
    let filesScanned: Int
    let eventsImported: Int
    let rowsFound: Int
    let routedRequests: Int64
    let nativeRequests: Int64
    let duplicateEvents: Int
    let malformedLines: Int

    enum CodingKeys: String, CodingKey {
        case filesScanned = "files_scanned"
        case eventsImported = "events_imported"
        case rowsFound = "rows_found"
        case routedRequests = "routed_requests"
        case nativeRequests = "native_requests"
        case duplicateEvents = "duplicate_events_skipped"
        case malformedLines = "malformed_lines_skipped"
    }
}

func defaultCodexHomePath(homeDirectory: URL = FileManager.default.homeDirectoryForCurrentUser) -> String {
    homeDirectory.appendingPathComponent(".codex", isDirectory: true).standardizedFileURL.path
}

func usageHistoryHelperArguments(codexHome: String, preview: Bool) -> [String] {
    var arguments = ["reconcile-usage", "--codex-home", codexHome]
    if preview { arguments.append("--preview-json") }
    return arguments
}

func usageHistoryPreviewMessage(_ preview: UsageHistoryPreview, codexHome: String) -> String {
    var message = """
    Source: \(codexHome)

    Scanned \(preview.filesScanned.formatted()) rollout files and found \(preview.eventsImported.formatted()) usage events across \(preview.rowsFound.formatted()) daily model/routing rows:
    • \(preview.routedRequests.formatted()) routed
    • \(preview.nativeRequests.formatted()) native (not routed)
    """
    if preview.duplicateEvents > 0 || preview.malformedLines > 0 {
        message += "\n\nSkipped copied events: \(preview.duplicateEvents.formatted()) · malformed records: \(preview.malformedLines.formatted())."
    }
    message += "\n\nOnly aggregate dates, providers, models, routing classifications, request counts, and token counters will be sent. Existing router telemetry will be replaced; prompts, responses, paths, credentials, and account identifiers are never imported."
    return message
}

@MainActor
final class HelperModel: ObservableObject {
    @Published var status = HelperStatus()
    @Published var operation = ""
    @Published var routerURL = UserDefaults.standard.string(forKey: "routerURL") ?? "https://router.example.com"
    @Published var deviceName = UserDefaults.standard.string(forKey: "deviceName") ?? (Host.current().localizedName ?? "Codex Mac")
    @Published var insecureDevelopment = UserDefaults.standard.bool(forKey: "insecureDevelopment")
    @Published var launchAtLogin = SMAppService.mainApp.status == .enabled
    @Published private(set) var configured = false
    @Published private(set) var accountLoginInProgress = false
    @Published private(set) var activityPulsePhase = false
    @Published private(set) var usageReconciliationInProgress = false
    @Published private(set) var telemetryResetInProgress = false

    private var timer: AnyCancellable?
    private var activityTimer: AnyCancellable?
    private var activityRequestInFlight = false
    private var helperHealthURL: URL?
    private var daemonProcess: Process?
    private var pairing = UserDefaults.standard.bool(forKey: "pairingPending")
    private var restartingDaemon = false
    private var started = false
    private var historyPromptVisible = false
    private let historyImportDecisionKey = "usageHistoryImportDecisionMade"

    var inferenceActive: Bool { status.activeRequests > 0 }

    var routerStatusLabel: String {
        if !configured { return "Setup Required" }
        if inferenceActive { return "Responding" }
        return status.connected ? "Connected" : status.state.capitalized
    }

    func start() {
        guard !started else { return }
        started = true
        configured = helperConfigurationExists
        updateHelperHealthURL()
        if configured {
            startDaemon()
            status.state = "connecting"
            status.lastError = ""
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { [weak self] in
                self?.refreshStatus()
            }
        } else {
            markSetupRequired()
        }
        timer = Timer.publish(every: 8, on: .main, in: .common)
            .autoconnect()
            .sink { [weak self] _ in self?.tick() }
        activityTimer = Timer.publish(every: 0.75, on: .main, in: .common)
            .autoconnect()
            .sink { [weak self] _ in self?.activityTick() }
    }

#if DEBUG
    func applyPreviewStatus(_ previewStatus: HelperStatus) {
        configured = true
        status = previewStatus
        operation = ""
    }
#endif

    func requestEnrollment() {
        let trimmed = routerURL.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            operation = "Enter the remote router URL."
            return
        }
        UserDefaults.standard.set(trimmed, forKey: "routerURL")
        UserDefaults.standard.set(deviceName, forKey: "deviceName")
        UserDefaults.standard.set(insecureDevelopment, forKey: "insecureDevelopment")
        var arguments = ["enroll", "--router", trimmed, "--name", deviceName, "--no-wait"]
        if insecureDevelopment { arguments.append("--insecure-dev") }
        operation = "Requesting enrollment…"
        runHelper(arguments) { [weak self] result in
            guard let self else { return }
            if result.success {
                self.configured = true
                self.updateHelperHealthURL()
                self.pairing = true
                UserDefaults.standard.set(true, forKey: "pairingPending")
                self.status.state = "connecting"
                self.status.lastError = ""
                self.operation = "Pending administrator approval in the dashboard."
            } else {
                if self.insecureDevelopment && result.error.contains("remote router is unreachable") {
                    self.operation = "Router unreachable. Allow OpenCDX Router in System Settings → Privacy & Security → Local Network, then retry."
                } else {
                    self.operation = result.error
                }
            }
        }
    }

    func addOpenAIAccount() {
        guard !accountLoginInProgress else {
            operation = "An OpenAI login is already open in your browser."
            return
        }
        let previousAccountCount = status.accounts.count
        accountLoginInProgress = true
        operation = "Opening an explicit OpenAI login…"
        runHelper(["login-openai"], timeout: 6 * 60) { [weak self] result in
            guard let self else { return }
            if result.success {
                self.updateAfterAccountLogin()
            } else if result.error.localizedCaseInsensitiveContains("login expired before the callback completed") {
                // A second browser/app handoff can complete an account while an
                // older local callback is still waiting. Confirm remote state
                // before surfacing a stale timeout as a failure.
                self.resolveExpiredAccountLogin(previousAccountCount: previousAccountCount)
            } else {
                self.accountLoginInProgress = false
                self.operation = result.error
                self.refreshStatus()
            }
        }
    }

    func openDashboard() { runHelper(["open-dashboard"]) { [weak self] result in if !result.success { self?.operation = result.error } } }
    func refreshQuotas() { operation = "Refreshing quotas…"; runHelper(["refresh-quotas"]) { [weak self] result in self?.operation = result.success ? "Quotas refreshed." : result.error; self?.refreshStatus() } }
    func refreshCatalog() {
        operation = "Refreshing catalog…"
        runHelper(["refresh-catalog"]) { [weak self] result in
            guard let self else { return }
            if result.success {
                _ = self.applyStatusOutput(result.output)
                self.operation = self.status.restartRequired ? "Catalog refreshed. Restart Codex to load changes." : "Catalog is up to date."
            } else {
                self.operation = result.error
            }
            self.refreshStatus()
        }
    }

    func acknowledgeCodexRestart() {
        operation = "Confirming the catalog restart…"
        runHelper(["acknowledge-restart"]) { [weak self] result in
            guard let self else { return }
            if result.success {
                _ = self.applyStatusOutput(result.output)
                self.operation = "Catalog restart reminder cleared."
            } else {
                self.operation = result.error
            }
            self.refreshStatus()
        }
    }
    func retryConnection() { operation = "Checking router connection…"; runHelper(["reconnect"]) { [weak self] result in self?.operation = result.success ? "Router connection restored." : result.error; self?.refreshStatus() } }

    func requestUsageReconciliation() {
        guard status.connected else {
            operation = "Connect and approve this Mac before reconciling usage history."
            return
        }
        guard !usageReconciliationInProgress && !telemetryResetInProgress else {
            operation = "Another telemetry change is already in progress."
            return
        }
        previewUsageHistory()
    }

    func requestTelemetryReset() {
        guard status.connected else {
            operation = "Connect and approve this Mac before resetting telemetry."
            return
        }
        guard !telemetryResetInProgress && !usageReconciliationInProgress else {
            operation = "Another telemetry change is already in progress."
            return
        }
        let alert = NSAlert()
        alert.messageText = "Reset all router telemetry?"
        alert.informativeText = "This permanently deletes aggregate request and token history stored by OpenCDX, including reconciled history. Providers, devices, accounts, and every file in ~/.codex remain unchanged. New routed usage starts again from zero, and you can reconcile local history later."
        alert.alertStyle = .warning
        alert.addButton(withTitle: "Reset Telemetry")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else {
            operation = "Telemetry was not changed."
            return
        }
        telemetryResetInProgress = true
        operation = "Resetting router telemetry…"
        runHelper(["reset-telemetry"], timeout: 60) { [weak self] result in
            guard let self else { return }
            self.telemetryResetInProgress = false
            self.operation = result.success
                ? "Telemetry reset. Local Codex history was not changed."
                : result.error
        }
    }

    func copyConfiguration() {
        runHelper(["config"]) { [weak self] result in
            guard result.success else { self?.operation = result.error; return }
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(result.output, forType: .string)
            self?.operation = "Codex configuration copied. Paste it into config.toml manually."
        }
    }

    func setLaunchAtLogin(_ enabled: Bool) {
        do {
            if enabled { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
            launchAtLogin = enabled
        } catch {
            launchAtLogin = SMAppService.mainApp.status == .enabled
            operation = "Login item could not be updated: \(error.localizedDescription)"
        }
    }

    func quit() {
        runHelper(["quit"]) { _ in NSApp.terminate(nil) }
    }

    func handle(url: URL) {
        guard isOpenCDXOAuthURL(url) else { return }
        addOpenAIAccount()
    }

    func refreshStatus() {
        configured = helperConfigurationExists
        guard configured else {
            markSetupRequired()
            return
        }
        runHelper(["status"], timeout: 10) { [weak self] result in
            guard let self else { return }
            guard result.success, let data = result.output.data(using: .utf8) else {
                self.status.connected = false
                self.status.activeRequests = 0
                self.status.state = self.pairing ? "connecting" : "disconnected"
                self.status.lastError = self.pairing ? "" : "Helper daemon is not running."
                return
            }
            guard self.applyStatusData(data) else {
                self.status.connected = false
                self.status.state = "degraded"
                self.status.lastError = "Helper returned an unreadable status response."
                return
            }
        }
    }

    private func updateAfterAccountLogin() {
        operation = "Account connected. Updating the catalog…"
        runHelper(["refresh-catalog"], timeout: 5 * 60) { [weak self] result in
            guard let self else { return }
            self.accountLoginInProgress = false
            if result.success {
                _ = self.applyStatusOutput(result.output)
                self.operation = "Account connected."
            } else {
                self.operation = "Account connected. Catalog refresh is pending."
            }
            self.refreshStatus()
        }
    }

    private func resolveExpiredAccountLogin(previousAccountCount: Int) {
        runHelper(["reconnect"], timeout: 30) { [weak self] result in
            guard let self else { return }
            let remoteChanged = result.success
                && self.applyStatusOutput(result.output)
                && self.status.accounts.count > previousAccountCount
            if remoteChanged {
                self.updateAfterAccountLogin()
            } else {
                self.accountLoginInProgress = false
                self.operation = "OpenAI login expired. Try again."
                self.refreshStatus()
            }
        }
    }

    @discardableResult
    private func applyStatusOutput(_ output: String) -> Bool {
        guard let data = output.data(using: .utf8) else { return false }
        return applyStatusData(data)
    }

    @discardableResult
    private func applyStatusData(_ data: Data) -> Bool {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let container = try decoder.singleValueContainer()
            let value = try container.decode(String.self)
            let fractional = ISO8601DateFormatter()
            fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let date = fractional.date(from: value) { return date }
            let standard = ISO8601DateFormatter()
            standard.formatOptions = [.withInternetDateTime]
            if let date = standard.date(from: value) { return date }
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "Invalid RFC 3339 timestamp")
        }
        guard let decoded = try? decoder.decode(HelperStatus.self, from: data) else { return false }
        status = decoded
        operation = operationAfterApplyingStatus(operation, status: decoded)
        offerInitialHistoryImportIfNeeded()
        return true
    }

    private func tick() {
        configured = helperConfigurationExists
        if !configured {
            markSetupRequired()
            return
        }
        if pairing {
            runHelper(["pair", "--timeout", "1s"], timeout: 4) { [weak self] result in
                guard let self, result.success else { return }
                self.pairing = false
                UserDefaults.standard.set(false, forKey: "pairingPending")
                self.operation = "Device approved. Restarting helper…"
                self.restartDaemon()
            }
        } else if !restartingDaemon && daemonProcess?.isRunning != true && !status.connected {
            startDaemon()
        }
        refreshStatus()
    }

    private func restartDaemon() {
        guard !restartingDaemon else { return }
        restartingDaemon = true
        let previousProcess = daemonProcess

        // Enrollment replaces the device credential in Keychain. A running
        // daemon keeps the old credential in memory, so stop it before starting
        // a new instance that can load the newly approved credential.
        runHelper(["quit"], timeout: 5) { [weak self] _ in
            guard let self else { return }
            DispatchQueue.global(qos: .userInitiated).async {
                if let previousProcess, previousProcess.isRunning {
                    previousProcess.terminate()
                    previousProcess.waitUntilExit()
                } else {
                    // The daemon may have been started by an earlier app
                    // instance, so allow its loopback listener time to close.
                    Thread.sleep(forTimeInterval: 0.25)
                }
                DispatchQueue.main.async {
                    self.daemonProcess = nil
                    self.restartingDaemon = false
                    self.status.connected = false
                    self.status.state = "connecting"
                    self.status.lastError = ""
                    self.operation = "Device approved. Connecting…"
                    self.startDaemon()
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.75) {
                        self.refreshStatus()
                    }
                }
            }
        }
    }

    private func offerInitialHistoryImportIfNeeded() {
        guard status.connected,
              !historyPromptVisible,
              !UserDefaults.standard.bool(forKey: historyImportDecisionKey) else { return }
        historyPromptVisible = true
        let codexHome = defaultCodexHomePath()
        let alert = NSAlert()
        alert.messageText = "Import existing Codex usage history?"
        alert.informativeText = "OpenCDX can review aggregate usage from the default Codex home:\n\n\(codexHome)\n\nYou will see the file and routed/native request counts before anything is replaced. Prompts, responses, paths, credentials, and account identifiers are never imported."
        alert.alertStyle = .informational
        alert.addButton(withTitle: "Review Import")
        alert.addButton(withTitle: "Skip")
        let response = alert.runModal()
        UserDefaults.standard.set(true, forKey: historyImportDecisionKey)
        historyPromptVisible = false
        if response == .alertFirstButtonReturn {
            previewUsageHistory(codexHome: codexHome)
        }
    }

    private func previewUsageHistory(codexHome: String = defaultCodexHomePath()) {
        guard !usageReconciliationInProgress else { return }
        usageReconciliationInProgress = true
        operation = "Scanning \(codexHome) for a reconciliation preview…"
        runHelper(usageHistoryHelperArguments(codexHome: codexHome, preview: true), timeout: 10 * 60) { [weak self] result in
            guard let self else { return }
            guard result.success, let data = result.output.data(using: .utf8) else {
                self.usageReconciliationInProgress = false
                self.operation = result.error
                return
            }
            guard let preview = try? JSONDecoder().decode(UsageHistoryPreview.self, from: data) else {
                self.usageReconciliationInProgress = false
                self.operation = "Helper returned an unreadable usage history preview. Existing telemetry was left unchanged."
                return
            }
            guard self.status.connected else {
                self.usageReconciliationInProgress = false
                self.operation = "Router disconnected during the history scan. Existing telemetry was left unchanged."
                return
            }
            let alert = NSAlert()
            alert.messageText = "Replace telemetry with this Codex usage history?"
            alert.informativeText = usageHistoryPreviewMessage(preview, codexHome: codexHome)
            alert.alertStyle = .warning
            alert.addButton(withTitle: "Replace Telemetry")
            alert.addButton(withTitle: "Cancel")
            guard alert.runModal() == .alertFirstButtonReturn else {
                self.usageReconciliationInProgress = false
                self.operation = "Usage history was not changed."
                return
            }
            self.reconcileUsageHistory(codexHome: codexHome)
        }
    }

    private func reconcileUsageHistory(codexHome: String) {
        operation = "Reconciling usage history from \(codexHome)…"
        runHelper(usageHistoryHelperArguments(codexHome: codexHome, preview: false), timeout: 10 * 60) { [weak self] result in
            guard let self else { return }
            self.usageReconciliationInProgress = false
            if result.success {
                let summary = result.output.trimmingCharacters(in: .whitespacesAndNewlines)
                self.operation = summary.isEmpty ? "Usage history reconciled." : summary
            } else {
                self.operation = result.error
            }
        }
    }

    private func startDaemon() {
        guard helperConfigurationExists else {
            configured = false
            markSetupRequired()
            return
        }
        guard daemonProcess?.isRunning != true else { return }
        guard let executable = helperExecutable else {
            operation = "Bundled router-helper was not found."
            return
        }
        let process = Process()
        process.executableURL = executable
        process.arguments = ["daemon"]
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        do {
            try process.run()
            daemonProcess = process
        } catch {
            operation = "Helper could not start: \(error.localizedDescription)"
        }
    }

    private func activityTick() {
        let nextPhase = inferenceActive ? !activityPulsePhase : false
        if nextPhase != activityPulsePhase {
            activityPulsePhase = nextPhase
        }
        refreshInferenceActivity()
    }

    private func refreshInferenceActivity() {
        guard configured, !activityRequestInFlight else { return }
        if helperHealthURL == nil { updateHelperHealthURL() }
        guard let helperHealthURL else { return }
        activityRequestInFlight = true
        var request = URLRequest(url: helperHealthURL, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 0.6)
        request.httpMethod = "GET"
        URLSession.shared.dataTask(with: request) { [weak self] data, _, _ in
            let activeRequests = data.flatMap { try? JSONDecoder().decode(HelperHealthStatus.self, from: $0).activeRequests }
            DispatchQueue.main.async {
                guard let self else { return }
                self.activityRequestInFlight = false
                guard let activeRequests, activeRequests != self.status.activeRequests else { return }
                var updated = self.status
                updated.activeRequests = max(0, activeRequests)
                self.status = updated
            }
        }.resume()
    }

    private func updateHelperHealthURL() {
        guard let configURL = helperConfigurationURL,
              let data = try? Data(contentsOf: configURL),
              let config = try? JSONDecoder().decode(HelperRuntimeConfiguration.self, from: data) else {
            helperHealthURL = nil
            return
        }
        let port = config.listenPort == 0 ? 17464 : config.listenPort
        helperHealthURL = URL(string: "http://127.0.0.1:\(port)/healthz")
    }

    private var helperConfigurationURL: URL? {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first?
            .appendingPathComponent(openCDXApplicationIdentifier, isDirectory: true)
            .appendingPathComponent("helper.json", isDirectory: false)
    }

    private var helperConfigurationExists: Bool {
        guard let helperConfigurationURL else { return false }
        return FileManager.default.isReadableFile(atPath: helperConfigurationURL.path)
    }

    private func markSetupRequired() {
        status = HelperStatus()
        status.state = "setup"
        if operation.isEmpty || operation == "Helper daemon is not running." {
            operation = "Open Settings to connect and enroll this Mac."
        }
    }

    private var helperExecutable: URL? {
        if let resource = Bundle.main.resourceURL?.appendingPathComponent("router-helper"), FileManager.default.isExecutableFile(atPath: resource.path) { return resource }
        let sibling = Bundle.main.executableURL?.deletingLastPathComponent().appendingPathComponent("router-helper")
        if let sibling, FileManager.default.isExecutableFile(atPath: sibling.path) { return sibling }
        return nil
    }

    private func runHelper(_ arguments: [String], timeout: TimeInterval = 30, completion: @escaping (CommandResult) -> Void) {
        guard let executable = helperExecutable else {
            completion(CommandResult(success: false, output: "", error: "Bundled router-helper was not found."))
            return
        }
        DispatchQueue.global(qos: .userInitiated).async {
            let process = Process()
            let stdout = Pipe(), stderr = Pipe()
            process.executableURL = executable
            process.arguments = arguments
            process.standardOutput = stdout
            process.standardError = stderr
            do {
                try process.run()
            } catch {
                DispatchQueue.main.async { completion(CommandResult(success: false, output: "", error: error.localizedDescription)) }
                return
            }
            let deadline = Date().addingTimeInterval(timeout)
            while process.isRunning && Date() < deadline { Thread.sleep(forTimeInterval: 0.05) }
            if process.isRunning { process.terminate() }
            process.waitUntilExit()
            let output = String(data: stdout.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
            let error = String(data: stderr.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            let prefix = "router-helper: "
            let displayError = error.hasPrefix(prefix) ? String(error.dropFirst(prefix.count)) : error
            let result = CommandResult(success: process.terminationStatus == 0, output: output, error: displayError.isEmpty ? "Helper command failed." : displayError)
            DispatchQueue.main.async { completion(result) }
        }
    }
}

private struct HelperRuntimeConfiguration: Decodable {
    let listenPort: Int

    enum CodingKeys: String, CodingKey {
        case listenPort = "listen_port"
    }
}

private struct HelperHealthStatus: Decodable {
    let activeRequests: Int

    enum CodingKeys: String, CodingKey {
        case activeRequests = "active_requests"
    }
}

struct CommandResult { let success: Bool; let output: String; let error: String }
