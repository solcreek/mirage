// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "MirageApp",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(name: "MirageApp", path: "Sources/MirageApp")
    ]
)
