// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "OpenCDXRouterMenu",
    platforms: [.macOS(.v13)],
    products: [.executable(name: "OpenCDXRouterMenu", targets: ["OpenCDXRouterMenu"])],
    targets: [
        .executableTarget(name: "OpenCDXRouterMenu"),
        .testTarget(name: "OpenCDXRouterMenuTests", dependencies: ["OpenCDXRouterMenu"]),
    ]
)
