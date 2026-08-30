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
}
