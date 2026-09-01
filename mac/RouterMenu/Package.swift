// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "OpenCDXRouterMenu",
    platforms: [.macOS(.v13)],
    products: [.executable(name: "OpenCDXRouterMenu", targets: ["OpenCDXRouterMenu"])],
    dependencies: [
        .package(url: "https://github.com/sparkle-project/Sparkle", exact: "2.9.6"),
    ],
    targets: [
        .executableTarget(
            name: "OpenCDXRouterMenu",
            dependencies: [
                .product(name: "Sparkle", package: "Sparkle"),
            ],
            swiftSettings: [
                .define("DEBUG", .when(configuration: .debug)),
            ],
            linkerSettings: [
                .unsafeFlags(["-Xlinker", "-rpath", "-Xlinker", "@loader_path/../Frameworks"]),
            ]
        ),
        .testTarget(name: "OpenCDXRouterMenuTests", dependencies: ["OpenCDXRouterMenu"]),
    ]
)
