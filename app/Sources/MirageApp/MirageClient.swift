import Foundation

// MirageClient is a thin wrapper over the `mirage` CLI's --json interface — the
// GUI is a client of the same API surface as the CLI (control-plane only; live
// VM display comes later via the per-VM supervisor/ownership handoff).
struct MirageError: LocalizedError { let message: String; var errorDescription: String? { message } }

struct Envelope<T: Decodable>: Decodable {
    let ok: Bool
    let data: T?
    let error: EnvError?
}
struct EnvError: Decodable { let code: String; let message: String; let hint: String? }

struct VMRow: Decodable, Identifiable {
    var id: String { name }
    let name: String
    let kind: String
    let os: String
    let status: String
}
struct VMList: Decodable { let bundles: [VMRow]? }
struct ScreenshotResult: Decodable { let name: String; let path: String; let bytes: Int }

enum MirageClient {
    /// Resolve the `mirage` binary: $MIRAGE_BIN, else the repo's bin/mirage
    /// derived from this source file's path (a dev convenience).
    static var binary: String = {
        if let p = ProcessInfo.processInfo.environment["MIRAGE_BIN"], !p.isEmpty { return p }
        // .../app/Sources/MirageApp/MirageClient.swift → repo root is 4 levels up.
        let here = URL(fileURLWithPath: #filePath)
        let repo = here.deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent()
        return repo.appendingPathComponent("bin/mirage").path
    }()

    /// Run `mirage --json <args>` and decode the envelope's data as T.
    static func run<T: Decodable>(_ args: [String], as: T.Type) throws -> T {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: binary)
        p.arguments = ["--json"] + args
        let out = Pipe()
        p.standardOutput = out
        p.standardError = Pipe()
        try p.run()
        let data = out.fileHandleForReading.readDataToEndOfFile()
        p.waitUntilExit()
        let env = try JSONDecoder().decode(Envelope<T>.self, from: data)
        if !env.ok {
            throw MirageError(message: env.error.map { "\($0.code): \($0.message)" } ?? "command failed")
        }
        guard let d = env.data else { throw MirageError(message: "empty response") }
        return d
    }

    static func list() throws -> [VMRow] { (try run(["ls"], as: VMList.self)).bundles ?? [] }
    static func start(_ name: String) throws { _ = try run(["start", name], as: Empty.self) }
    static func stop(_ name: String) throws { _ = try run(["stop", name], as: Empty.self) }
    static func delete(_ name: String) throws { _ = try run(["rm", name], as: Empty.self) }
    static func clone(_ src: String, _ dst: String) throws { _ = try run(["clone", src, dst], as: Empty.self) }
    static func screenshot(_ name: String) throws -> URL {
        let tmp = NSTemporaryDirectory() + "mirage-\(name)-\(Int(Date().timeIntervalSince1970)).png"
        _ = try run(["screenshot", name, "-o", tmp], as: ScreenshotResult.self)
        return URL(fileURLWithPath: tmp)
    }
}

// Empty decodes any object we don't need to read (start/stop/etc. return a map).
struct Empty: Decodable {}
