import AppKit
import SwiftUI
import XCTest
@testable import OpenCDXRouterMenu

final class HelperModelTests: XCTestCase {
    func testEnrollmentStaysDisabledForAnEnrolledServerDuringOutages() {
        var status = HelperStatus()
        status.routerURL = "https://router.example.com"
        status.connected = true
        func allowed(_ url: String, busy: Bool = false) -> Bool {
            enrollmentRequestAllowed(routerURL: url, configuredRouterURL: "https://router.example.com",
                                     deviceID: "device", status: status, inProgress: busy)
        }
        XCTAssertFalse(allowed(" HTTPS://ROUTER.EXAMPLE.COM:443/ "))
        status.connected = false
        status.lastError = "remote router is unreachable"
        XCTAssertFalse(allowed("https://router.example.com"))
        XCTAssertTrue(allowed("https://other.example.com"))
        XCTAssertTrue(allowed("https://router.example.com/other"))
        XCTAssertFalse(allowed("https://other.example.com", busy: true))
        XCTAssertFalse(allowed("invalid"))
        status.enrollmentRequired = true
        XCTAssertTrue(allowed("https://router.example.com"))
        status.routerURL = "https://old.example.com"
        XCTAssertFalse(allowed("https://router.example.com"))
    }

    func testEnrollmentDoesNotRequireAConnectionWhenNeverEnrolled() {
        XCTAssertTrue(enrollmentRequestAllowed(routerURL: "https://router.example.com",
                                               configuredRouterURL: "", deviceID: "",
                                               status: HelperStatus(), inProgress: false))
        XCTAssertFalse(enrollmentRequestAllowed(routerURL: "https://router.example.com",
                                                configuredRouterURL: "https://router.example.com", deviceID: "device",
                                                status: HelperStatus(), inProgress: false))
    }

    func testEnrollmentRequiredStatusIsBackwardCompatible() throws {
        let old = try JSONDecoder().decode(HelperStatus.self, from: Data(#"{"connected":false}"#.utf8))
        XCTAssertFalse(old.enrollmentRequired)
        let revoked = try JSONDecoder().decode(HelperStatus.self, from: Data(#"{"connected":false,"enrollment_required":true}"#.utf8))
        XCTAssertTrue(revoked.enrollmentRequired)
    }

    func testProductIdentityAndOAuthURLUseDodelidooNamespace() throws {
        XCTAssertEqual(openCDXApplicationIdentifier, "com.dodelidoo.opencdx")
        XCTAssertTrue(isOpenCDXOAuthURL(try XCTUnwrap(URL(string: "com.dodelidoo.opencdx://oauth/openai/start"))))
        XCTAssertFalse(isOpenCDXOAuthURL(try XCTUnwrap(URL(string: "opencdx://oauth/openai/start"))))
        XCTAssertFalse(isOpenCDXOAuthURL(try XCTUnwrap(URL(string: "com.dodelidoo.opencdx://oauth/openai/other"))))
    }

    func testConnectedStatusClearsCompletedEnrollmentOperation() {
        var status = HelperStatus()
        status.connected = true

        XCTAssertEqual(operationAfterApplyingStatus("Device approved. Connecting…", status: status), "")
    }

    func testOtherOperationsAndDisconnectedStatusRemainVisible() {
        var connected = HelperStatus()
        connected.connected = true
        XCTAssertEqual(operationAfterApplyingStatus("Catalog refreshed; no changes found.", status: connected), "Catalog refreshed; no changes found.")

        let disconnected = HelperStatus()
        XCTAssertEqual(
            operationAfterApplyingStatus("Device approved. Connecting…", status: disconnected),
            "Device approved. Connecting…"
        )
    }

    func testCatalogRefreshMessagesDescribeThisRefresh() {
        XCTAssertEqual(
            catalogRefreshMessage(changed: false, restartRequired: false),
            "Catalog refreshed; no changes found."
        )
        XCTAssertEqual(
            catalogRefreshMessage(changed: true, restartRequired: true),
            "Catalog refreshed; changes found. Restart Codex to load them."
        )
        XCTAssertEqual(
            catalogRefreshMessage(changed: false, restartRequired: true),
            "Catalog refreshed; no new changes found. Restart Codex to load pending changes."
        )
    }

    @MainActor
    func testTerminalOperationClearsAfterDelay() async throws {
        let model = HelperModel()
        model.setOperation("Finished.", clearsAfter: 0.02)

        XCTAssertEqual(model.operation, "Finished.")
        try await Task.sleep(nanoseconds: 80_000_000)
        XCTAssertEqual(model.operation, "")
    }

    @MainActor
    func testOlderDismissalCannotClearNewOperation() async throws {
        let model = HelperModel()
        model.setOperation("First", clearsAfter: 0.02)
        model.setOperation("Second")

        try await Task.sleep(nanoseconds: 80_000_000)
        XCTAssertEqual(model.operation, "Second")
    }

    func testRouterOperationsRequireConfigurationAndConnection() {
        XCTAssertTrue(routerOperationsAvailable(configured: true, connected: true))
        XCTAssertFalse(routerOperationsAvailable(configured: true, connected: false))
        XCTAssertFalse(routerOperationsAvailable(configured: false, connected: true))
    }

    func testAccountAllowanceDecodesSparseQuotaWindows() throws {
        let data = Data(#"""
        {
            "masked_email":"a***@example.com","plan":"pro","status":"ready",
            "quota_windows":[{
                "label":"Weekly","remaining":97,"duration_minutes":10080,
                "pace_status":"on_pace","pace_marker_percent":88.7,"pace_buffer_percent":8.3
            }]
        }
        """#.utf8)
        let account = try JSONDecoder().decode(AccountAllowanceStatus.self, from: data)

        XCTAssertEqual(account.quotaWindows.count, 1)
        XCTAssertEqual(account.quotaWindows[0].label, "Weekly")
        XCTAssertEqual(account.quotaWindows[0].remaining, 97)
        XCTAssertEqual(account.quotaWindows[0].durationMinutes, 10_080)
        XCTAssertEqual(account.quotaWindows[0].paceStatus, "on_pace")
        XCTAssertEqual(account.quotaWindows[0].paceMarkerPercent, 88.7)
        XCTAssertEqual(account.quotaWindows[0].paceBufferPercent, 8.3)
    }

    func testSparkAllowanceDecodesAlongsideNormalWindows() throws {
        let data = Data(#"""
        {"quota_windows":[
            {"label":"Weekly","remaining":97},
            {"label":"Spark · 5 hours","remaining":75,"duration_minutes":300,
             "reset_at":"2030-01-02T03:04:05Z","pace_status":"on_pace",
             "pace_marker_percent":60,"pace_buffer_percent":15},
            {"label":"Spark · Allowance","remaining":90}
        ]}
        """#.utf8)
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let account = try decoder.decode(AccountAllowanceStatus.self, from: data)
        XCTAssertEqual(account.quotaWindows.count, 3)
        let spark = account.quotaWindows[1]
        XCTAssertEqual(spark.label, "Spark · 5 hours")
        XCTAssertEqual(spark.remaining, 75)
        XCTAssertEqual(spark.durationMinutes, 300)
        XCTAssertNotNil(spark.resetAt)
        XCTAssertEqual(spark.paceMarkerPercent, 60)
        XCTAssertEqual(spark.paceBufferPercent, 15)
        XCTAssertTrue(account.quotaWindows[2].paceStatus.isEmpty)
        XCTAssertNil(account.quotaWindows[2].paceMarkerPercent)
    }

    func testAccountAllowanceAcceptsMissingQuotaWindows() throws {
        let data = Data(#"{"masked_email":"a***@example.com","plan":"pro","status":"ready"}"#.utf8)
        let account = try JSONDecoder().decode(AccountAllowanceStatus.self, from: data)

        XCTAssertTrue(account.quotaWindows.isEmpty)
        XCTAssertNil(account.quotaResetAt)
    }

    func testUsageHistoryPreviewDecodesCompactHelperSummary() throws {
        let data = Data(#"{"files_scanned":7,"events_imported":12,"rows_found":3,"routed_requests":5,"native_requests":7,"duplicate_events_skipped":2,"malformed_lines_skipped":1}"#.utf8)
        let preview = try JSONDecoder().decode(UsageHistoryPreview.self, from: data)

        XCTAssertEqual(preview.filesScanned, 7)
        XCTAssertEqual(preview.eventsImported, 12)
        XCTAssertEqual(preview.rowsFound, 3)
        XCTAssertEqual(preview.routedRequests, 5)
        XCTAssertEqual(preview.nativeRequests, 7)
        XCTAssertEqual(preview.duplicateEvents, 2)
        XCTAssertEqual(preview.malformedLines, 1)
    }

    func testUsageHistoryFlowUsesOneExplicitCodexHome() {
        let home = URL(fileURLWithPath: "/Users/tester", isDirectory: true)
        let codexHome = defaultCodexHomePath(homeDirectory: home)

        XCTAssertEqual(codexHome, "/Users/tester/.codex")
        XCTAssertEqual(
            usageHistoryHelperArguments(codexHome: codexHome, preview: true),
            ["reconcile-usage", "--codex-home", "/Users/tester/.codex", "--preview-json"]
        )
        XCTAssertEqual(
            usageHistoryHelperArguments(codexHome: codexHome, preview: false),
            ["reconcile-usage", "--codex-home", "/Users/tester/.codex"]
        )
    }

    func testUsageHistoryConfirmationNamesSourceAndRoutingCounts() {
        let preview = UsageHistoryPreview(
            filesScanned: 7,
            eventsImported: 12,
            rowsFound: 3,
            routedRequests: 5,
            nativeRequests: 7,
            duplicateEvents: 2,
            malformedLines: 1
        )

        let message = usageHistoryPreviewMessage(preview, codexHome: "/Users/tester/.codex")
        XCTAssertTrue(message.contains("Source: /Users/tester/.codex"))
        XCTAssertTrue(message.contains("3 usage rows"))
        XCTAssertTrue(message.contains("5 routed"))
        XCTAssertTrue(message.contains("7 native (not routed)"))
        XCTAssertTrue(message.contains("Skipped copied events: 2 · malformed records: 1"))
        XCTAssertTrue(message.contains("Only this Mac’s router telemetry will be replaced"))
        XCTAssertTrue(message.contains("Other machines’ history will be preserved"))
    }

    func testResetTicketsDecodeAndDisappearAtExpiration() throws {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let account = try decoder.decode(AccountAllowanceStatus.self, from: Data(#"{"id":"account-b","reset_credits":3,"reset_tickets":[{"id":"expires","expires_at":"2026-09-10T12:00:00Z"},{"id":"stays"},{}]}"#.utf8))
        let boundary = try XCTUnwrap(ISO8601DateFormatter().date(from: "2026-09-10T12:00:00Z"))
        XCTAssertEqual(account.id, "account-b")
        XCTAssertEqual(account.availableResetTickets(at: boundary.addingTimeInterval(-1)).count, 3)
        XCTAssertEqual(account.availableResetTickets(at: boundary).count, 2)
        XCTAssertEqual(account.availableResetTickets(at: boundary).first?.id, "stays")
        let absent = try decoder.decode(AccountAllowanceStatus.self, from: Data(#"{"reset_credits":0}"#.utf8))
        XCTAssertTrue(absent.availableResetTickets(at: boundary).isEmpty)
        let emptyDetails = try decoder.decode(AccountAllowanceStatus.self, from: Data(#"{"reset_credits":2,"reset_tickets":[]}"#.utf8))
        XCTAssertTrue(emptyDetails.availableResetTickets(at: boundary).isEmpty)
    }

    func testCreditsDecodeAndLabel() throws {
        let decoder = JSONDecoder()
        let balance = try decoder.decode(AccountAllowanceStatus.self, from: Data(#"{"id":"a","credits":{"balance":"43"}}"#.utf8))
        XCTAssertEqual(balance.credits?.label, "43 Codex credits")
        let unlimited = try decoder.decode(AccountAllowanceStatus.self, from: Data(#"{"credits":{"unlimited":true}}"#.utf8))
        XCTAssertEqual(unlimited.credits?.label, "Unlimited Codex credits")
        XCTAssertEqual(AccountCredits(balance: "1").label, "1 Codex credit")
        XCTAssertEqual(AccountCredits(balance: "<1").label, "<1 Codex credit")
        XCTAssertEqual(AccountCredits().label, "Codex credits available")
        XCTAssertEqual(AccountCredits(balance: "62500").amount, 62_500.formatted(.number))
        XCTAssertEqual(AccountCredits(balance: "<1").amount, "<1")
        XCTAssertEqual(AccountCredits(unlimited: true).amount, "∞")
        XCTAssertNil(AccountCredits().amount)
        let none = try decoder.decode(AccountAllowanceStatus.self, from: Data(#"{"credits":null}"#.utf8))
        XCTAssertNil(none.credits)
    }

    @MainActor
    func testAllowanceFixtureRenders() throws {
        var pro = AccountAllowanceStatus()
        pro.maskedEmail = "h***o@t***.com"
        pro.plan = "pro"
        pro.status = "ready"
        pro.primary = true
        pro.id = "preview-pro"
        pro.resetCredits = 2
        pro.resetTickets = [AccountResetTicket(id: "one"), AccountResetTicket(id: "two")]
        pro.credits = AccountCredits(balance: "62500")
        pro.quotaWindows = [
            AccountQuotaWindowStatus(
                label: "Weekly",
                remaining: 61,
                durationMinutes: 10_080,
                resetAt: Date().addingTimeInterval(5 * 24 * 60 * 60),
                paceStatus: "too_fast",
                paceMarkerPercent: 71.4,
                paceBufferPercent: -10.4
            ),
            AccountQuotaWindowStatus(
                label: "Spark · Weekly", remaining: 75, durationMinutes: 10_080,
                resetAt: Date().addingTimeInterval(4 * 24 * 60 * 60),
                paceStatus: "on_pace", paceMarkerPercent: 57.1, paceBufferPercent: 17.9
            ),
            AccountQuotaWindowStatus(
                label: "Spark · 5 hours", remaining: 64, durationMinutes: 300,
                resetAt: Date().addingTimeInterval(2 * 60 * 60),
                paceStatus: "on_pace", paceMarkerPercent: 40, paceBufferPercent: 24
            ),
        ]

        var plus = AccountAllowanceStatus()
        plus.maskedEmail = "s***a@g***.com"
        plus.plan = "plus"
        plus.status = "ready"
        plus.resetCredits = 3
        plus.resetTickets = [AccountResetTicket(id: "a"), AccountResetTicket(id: "b"), AccountResetTicket(id: "c")]
        plus.quotaWindows = [
            AccountQuotaWindowStatus(
                label: "Weekly",
                remaining: 96,
                durationMinutes: 10_080,
                resetAt: Date().addingTimeInterval(6 * 24 * 60 * 60),
                paceStatus: "on_pace",
                paceMarkerPercent: 86.8,
                paceBufferPercent: 9.2
            ),
            AccountQuotaWindowStatus(
                label: "5 hours",
                remaining: 64,
                durationMinutes: 300,
                resetAt: Date().addingTimeInterval(2 * 60 * 60),
                paceStatus: "on_pace",
                paceMarkerPercent: 44.3,
                paceBufferPercent: 19.7
            ),
        ]

        var creditsOnly = plus
        creditsOnly.id = "preview-credits"
        creditsOnly.plan = "business"
        creditsOnly.resetTickets = []
        creditsOnly.resetCredits = 0
        creditsOnly.credits = AccountCredits(unlimited: true)

        let fixture = VStack(spacing: 0) {
            Divider().padding(.horizontal, 12)
            AccountAllowanceSection(accounts: [pro, plus, creditsOnly], connected: true, onReset: { _, _ in })
            Divider().padding(.horizontal, 12)
            Text("Open Dashboard")
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(16)
        }
        .frame(width: 360)
        .background(Color(red: 0.075, green: 0.09, blue: 0.11))
        .environment(\.colorScheme, .dark)
        .accentColor(.purple)

        let renderer = ImageRenderer(content: fixture)
        renderer.scale = 2
        let image = try XCTUnwrap(renderer.nsImage)
        let bitmap = try XCTUnwrap(image.tiffRepresentation.flatMap(NSBitmapImageRep.init(data:)))
        let png = try XCTUnwrap(bitmap.representation(using: .png, properties: [:]))
        XCTAssertGreaterThan(png.count, 1_000)

        if let output = ProcessInfo.processInfo.environment["OPENCODEX_FIXTURE_OUTPUT"], !output.isEmpty {
            try png.write(to: URL(fileURLWithPath: output), options: .atomic)
        }
    }

    private func claudeStatusDecoder() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return decoder
    }

    func testClaudeAccountsAndReportingStatusDecode() throws {
        let data = Data(#"""
        {"connected":true,"claude_accounts":[{"id":"c1","provider":"claude","masked_email":"b***s@g***.com","status":"ready",
        "observed_at":"2030-01-02T03:04:05Z","quota_windows":[{"label":"Weekly","remaining":36,"duration_minutes":10080,
        "reset_at":"2030-01-05T00:00:00Z","pace_status":"too_fast","pace_marker_percent":54,"pace_buffer_percent":-18}]}],
        "claude_code":{"last_telemetry_at":"2030-01-02T03:04:00Z","pending_requests":2,"last_error":"router request failed"}}
        """#.utf8)
        let status = try claudeStatusDecoder().decode(HelperStatus.self, from: data)
        XCTAssertEqual(status.claudeAccounts.count, 1)
        XCTAssertEqual(status.claudeAccounts[0].quotaWindows.first?.remaining, 36)
        XCTAssertEqual(status.claudeAccounts[0].accountProvider, .claude)
        let legacy = try JSONDecoder().decode(AccountAllowanceStatus.self, from: Data(#"{"masked_email":"a***@e***.com"}"#.utf8))
        XCTAssertEqual(legacy.accountProvider, .openAI)
        XCTAssertNotNil(status.claudeAccounts[0].observedAt)
        XCTAssertEqual(status.claudeCode.pendingRequests, 2)

        let old = try claudeStatusDecoder().decode(HelperStatus.self, from: Data(#"{"connected":true}"#.utf8))
        XCTAssertTrue(old.claudeAccounts.isEmpty)
        XCTAssertNil(old.claudeCode.lastActivityAt)
    }

    func testClaudeReportingStates() {
        let now = Date(timeIntervalSince1970: 2_000_000_000)
        var status = HelperStatus()
        XCTAssertEqual(claudeCodeReporting(installed: false, status: status, now: now), .notConnected)
        XCTAssertEqual(claudeCodeReporting(installed: true, status: status, now: now), .waiting)
        status.claudeCode.lastStatusLineAt = now.addingTimeInterval(-60)
        XCTAssertEqual(claudeCodeReporting(installed: true, status: status, now: now), .reporting)
        status.claudeCode.lastStatusLineAt = now.addingTimeInterval(-20 * 60)
        XCTAssertEqual(claudeCodeReporting(installed: true, status: status, now: now), .waiting)
        status.claudeCode.lastError = "remote router is unreachable"
        status.claudeCode.pendingRequests = 3
        XCTAssertEqual(claudeCodeReporting(installed: true, status: status, now: now), .uploadPending)
        XCTAssertEqual(ClaudeCodeReporting.uploadPending.label, "Upload Pending")
    }

    func testObservationAgeLabels() {
        let now = Date(timeIntervalSince1970: 2_000_000_000)
        XCTAssertEqual(observationAge(since: now.addingTimeInterval(-10), now: now), "NOW")
        XCTAssertEqual(observationAge(since: now.addingTimeInterval(-12 * 60), now: now), "12M AGO")
        XCTAssertEqual(observationAge(since: now.addingTimeInterval(-3 * 3600), now: now), "3H AGO")
        XCTAssertEqual(observationAge(since: now.addingTimeInterval(-2 * 86400), now: now), "2D AGO")
    }

    func testClaudeSetupPreviewMessagesExplainScope() throws {
        let data = Data(#"""
        {"settings_path":"/Users/me/.claude/settings.json","action":"install","installed":false,"already_applied":false,
        "changes":["Wrap the existing status line; its output is unchanged"],"wraps_status_line":"bash statusline.sh",
        "restart_reminder":"Start a new Claude Code session to begin exporting usage.","helper_executable":"/x"}
        """#.utf8)
        let preview = try JSONDecoder().decode(ClaudeSetupPreview.self, from: data)
        XCTAssertTrue(preview.conflicts.isEmpty)
        let message = claudeSetupConfirmationMessage(preview)
        XCTAssertTrue(message.contains("/Users/me/.claude/settings.json"))
        XCTAssertTrue(message.contains("• Wrap the existing status line"))
        XCTAssertTrue(message.contains("keeps showing exactly as before"))
        XCTAssertTrue(message.contains("Prompts, responses, file paths, and Claude credentials are never read or sent"))

        let history = try JSONDecoder().decode(ClaudeHistoryPreview.self, from: Data(#"""
        {"home":"/Users/me/.claude","files_scanned":118,"models":4,"input_tokens":10,"output_tokens":2,"requests":12728,"malformed_records_skipped":0}
        """#.utf8))
        let historyMessage = claudeHistoryPreviewMessage(history)
        XCTAssertTrue(historyMessage.contains("\(12_728.formatted()) Claude Code requests"))
        XCTAssertTrue(historyMessage.contains("never double-counts"))
    }

    @MainActor
    func testClaudeAllowanceRowsRender() throws {
        var openAI = AccountAllowanceStatus()
        openAI.maskedEmail = "h***o@t***.com"
        openAI.plan = "pro"
        openAI.status = "ready"
        openAI.primary = true
        openAI.quotaWindows = [AccountQuotaWindowStatus(label: "Weekly", remaining: 97, durationMinutes: 10_080,
                                                        resetAt: Date().addingTimeInterval(6 * 86400), paceStatus: "on_pace", paceMarkerPercent: 88, paceBufferPercent: 9)]
        var claude = AccountAllowanceStatus()
        claude.maskedEmail = "b***s@g***.com"
        claude.provider = AccountProvider.claude.rawValue
        claude.status = "ready"
        claude.observedAt = Date().addingTimeInterval(-4 * 60)
        claude.quotaWindows = [
            AccountQuotaWindowStatus(label: "Weekly", remaining: 36, durationMinutes: 10_080, resetAt: Date().addingTimeInterval(4 * 86400),
                                     paceStatus: "too_fast", paceMarkerPercent: 54, paceBufferPercent: -18),
            AccountQuotaWindowStatus(label: "5 hours", remaining: 91, durationMinutes: 300, resetAt: Date().addingTimeInterval(4 * 3600),
                                     paceStatus: "on_pace", paceMarkerPercent: 83.7, paceBufferPercent: 7.3),
        ]
        let fixture = AccountAllowanceSection(accounts: [openAI], claudeAccounts: [claude], connected: true)
            .frame(width: 360)
            .background(Color(red: 0.075, green: 0.09, blue: 0.11))
            .environment(\.colorScheme, .dark)
            .accentColor(.green)
        let renderer = ImageRenderer(content: fixture)
        renderer.scale = 2
        let image = try XCTUnwrap(renderer.nsImage)
        let bitmap = try XCTUnwrap(image.tiffRepresentation.flatMap(NSBitmapImageRep.init(data:)))
        let png = try XCTUnwrap(bitmap.representation(using: .png, properties: [:]))
        XCTAssertGreaterThan(png.count, 1_000)
        if let output = ProcessInfo.processInfo.environment["OPENCODEX_CLAUDE_FIXTURE_OUTPUT"], !output.isEmpty {
            try png.write(to: URL(fileURLWithPath: output), options: .atomic)
        }
    }

    func testDaemonFromAnEarlierAppVersionIsStale() {
        XCTAssertTrue(helperDaemonIsStale(runningBuild: nil, bundledBuild: "1.7.1 (abc)"))
        XCTAssertTrue(helperDaemonIsStale(runningBuild: "1.7.0 (def)", bundledBuild: "1.7.1 (abc)"))
        XCTAssertFalse(helperDaemonIsStale(runningBuild: "1.7.1 (abc)", bundledBuild: "1.7.1 (abc)"))
        XCTAssertFalse(helperDaemonIsStale(runningBuild: nil, bundledBuild: nil))
        XCTAssertFalse(helperDaemonIsStale(runningBuild: "1.7.0 (def)", bundledBuild: ""))
    }
}
