import AppKit
import Combine
import Foundation
import ServiceManagement

struct AccountAllowanceStatus: Codable {
    var maskedEmail = ""
    var plan = ""
    var status = ""
    var paused = false
    var primary = false
    var quotaRemaining = 0.0
    var quotaResetAt: Date?
    var resetCredits = 0

    enum CodingKeys: String, CodingKey {
        case plan, status, paused, primary
        case maskedEmail = "masked_email"
        case quotaRemaining = "quota_remaining"
        case quotaResetAt = "quota_reset_at"
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
        resetCredits = try container.decodeIfPresent(Int.self, forKey: .resetCredits) ?? 0
    }
}

struct HelperStatus: Codable {
    var state = "disconnected"
    var connected = false
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

    private var timer: AnyCancellable?
    private var daemonProcess: Process?
    private var pairing = UserDefaults.standard.bool(forKey: "pairingPending")
    private var restartingDaemon = false
    private var started = false
    private var historyPromptVisible = false
    private let historyImportDecisionKey = "usageHistoryImportDecisionMade"

    var menuIcon: String {
        if !configured { return "gearshape.fill" }
        switch status.state {
        case "connected": return "arrow.triangle.branch"
        case "connecting": return "arrow.clockwise.circle"
        case "degraded": return "exclamationmark.triangle.fill"
        default: return "exclamationmark.circle"
        }
    }

    var routerStatusLabel: String {
        if !configured { return "Setup Required" }
        return status.connected ? "Connected" : status.state.capitalized
    }

    func start() {
        guard !started else { return }
        started = true
        configured = helperConfigurationExists
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
    }

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
        let alert = NSAlert()
        alert.messageText = "Replace telemetry with Codex usage history?"
        alert.informativeText = "OpenCDX will scan Codex rollout files on this Mac and send only dates, providers, models, request counts, and token counters. Prompts, responses, paths, credentials, and account identifiers are never imported. Existing router telemetry will be replaced, then new proxied usage will continue accumulating."
        alert.alertStyle = .warning
        alert.addButton(withTitle: "Reconcile Usage")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        reconcileUsageHistory()
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
        guard url.scheme == "opencdx", url.host == "oauth", url.path == "/openai/start" else { return }
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
        let alert = NSAlert()
        alert.messageText = "Import existing Codex usage history?"
        alert.informativeText = "This optional one-time import runs locally and uploads only daily model/provider request and token totals. It never imports prompts, responses, paths, credentials, or account identifiers. You can run it later from the Router menu."
        alert.alertStyle = .informational
        alert.addButton(withTitle: "Import History")
        alert.addButton(withTitle: "Skip")
        let response = alert.runModal()
        UserDefaults.standard.set(true, forKey: historyImportDecisionKey)
        historyPromptVisible = false
        if response == .alertFirstButtonReturn {
            reconcileUsageHistory()
        }
    }

    private func reconcileUsageHistory() {
        operation = "Scanning local Codex usage history…"
        runHelper(["reconcile-usage"], timeout: 10 * 60) { [weak self] result in
            guard let self else { return }
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

    private var helperConfigurationExists: Bool {
        guard let applicationSupport = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first else { return false }
        let config = applicationSupport
            .appendingPathComponent("OpenCDX Router", isDirectory: true)
            .appendingPathComponent("helper.json", isDirectory: false)
        return FileManager.default.isReadableFile(atPath: config.path)
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

struct CommandResult { let success: Bool; let output: String; let error: String }
