import XCTest
@testable import OpenCDXRouterMenu

final class HelperModelTests: XCTestCase {
    func testConnectedStatusClearsCompletedEnrollmentOperation() {
        var status = HelperStatus()
        status.connected = true

        XCTAssertEqual(operationAfterApplyingStatus("Device approved. Connecting…", status: status), "")
    }

    func testOtherOperationsAndDisconnectedStatusRemainVisible() {
        var connected = HelperStatus()
        connected.connected = true
        XCTAssertEqual(operationAfterApplyingStatus("Catalog is up to date.", status: connected), "Catalog is up to date.")

        let disconnected = HelperStatus()
        XCTAssertEqual(
            operationAfterApplyingStatus("Device approved. Connecting…", status: disconnected),
            "Device approved. Connecting…"
        )
    }

    func testRouterOperationsRequireConfigurationAndConnection() {
        XCTAssertTrue(routerOperationsAvailable(configured: true, connected: true))
        XCTAssertFalse(routerOperationsAvailable(configured: true, connected: false))
        XCTAssertFalse(routerOperationsAvailable(configured: false, connected: true))
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
}
