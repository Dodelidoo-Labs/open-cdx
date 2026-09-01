import AppKit
import SwiftUI
import XCTest
@testable import OpenCDXRouterMenu

final class HelperModelTests: XCTestCase {
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
        XCTAssertTrue(message.contains("3 daily model/routing rows"))
        XCTAssertTrue(message.contains("5 routed"))
        XCTAssertTrue(message.contains("7 native (not routed)"))
        XCTAssertTrue(message.contains("Skipped copied events: 2 · malformed records: 1"))
        XCTAssertTrue(message.contains("Existing router telemetry will be replaced"))
    }

    @MainActor
    func testAllowanceFixtureRenders() throws {
        var pro = AccountAllowanceStatus()
        pro.maskedEmail = "h***o@t***.com"
        pro.plan = "pro"
        pro.status = "ready"
        pro.primary = true
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
        ]

        var plus = AccountAllowanceStatus()
        plus.maskedEmail = "s***a@g***.com"
        plus.plan = "plus"
        plus.status = "ready"
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

        let fixture = VStack(spacing: 0) {
            Divider().padding(.horizontal, 12)
            AccountAllowanceSection(accounts: [pro, plus], connected: true)
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
}
