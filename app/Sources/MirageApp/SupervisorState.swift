import Foundation

// SupervisorState lets the GUI participate in the same per-VM state files and
// host-wide 2-VM quota as the CLI/supervisor (internal/supervisor). When the
// GUI boots a VM in-process it writes a state file (owner "gui") under the
// shared .quota.lock, so `mirage ls` sees it and the macOS-VM limit is enforced
// consistently whether VMs are started from the CLI or the GUI.
enum SupervisorState {
    struct QuotaError: LocalizedError { let message: String; var errorDescription: String? { message } }

    // Mirrors internal/supervisor.State (snake_case JSON keys).
    private struct Record: Codable {
        let name: String, pid: Int32, os: String, socket: String
        let status: String, startedAt: String, owner: String
        enum CodingKeys: String, CodingKey {
            case name, pid, os, socket, status, owner
            case startedAt = "started_at"
        }
    }

    // Mirrors bundle.StateVMsDir(): $XDG_STATE_HOME/mirage/vms or ~/.local/state/mirage/vms.
    private static var dir: URL {
        if let x = ProcessInfo.processInfo.environment["XDG_STATE_HOME"], !x.isEmpty {
            return URL(fileURLWithPath: x).appendingPathComponent("mirage/vms")
        }
        return FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".local/state/mirage/vms")
    }
    private static func statePath(_ name: String) -> URL { dir.appendingPathComponent("\(name).json") }

    /// Claim a quota slot and write this VM's state, refusing if two macOS VMs
    /// already run (CLI or GUI). Call before booting; throws QuotaError if full.
    static func claim(_ name: String) throws {
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let lock = openLock()
        defer { unlock(lock) }

        var names: [String] = []
        for rec in liveRecords() where rec.os == "macos" && rec.name != name {
            names.append(rec.name)
        }
        if names.count >= 2 {
            throw QuotaError(message: "the host limit of 2 running macOS VMs is reached (running: \(names.joined(separator: ", "))) — stop one first")
        }
        let rec = Record(name: name, pid: getpid(), os: "macos", socket: "",
                         status: "running", startedAt: ISO8601DateFormatter().string(from: Date()), owner: "gui")
        let enc = JSONEncoder()
        enc.outputFormatting = .prettyPrinted
        try enc.encode(rec).write(to: statePath(name), options: .atomic)
    }

    /// Release the slot (remove the state file). Idempotent.
    static func release(_ name: String) {
        try? FileManager.default.removeItem(at: statePath(name))
    }

    private static func liveRecords() -> [Record] {
        guard let entries = try? FileManager.default.contentsOfDirectory(
            at: dir, includingPropertiesForKeys: nil) else { return [] }
        var out: [Record] = []
        for url in entries where url.pathExtension == "json" {
            guard let data = try? Data(contentsOf: url),
                  let rec = try? JSONDecoder().decode(Record.self, from: data) else { continue }
            if kill(pid_t(rec.pid), 0) == 0 { out.append(rec) }  // process still alive
        }
        return out
    }

    private static func openLock() -> Int32 {
        let fd = open(dir.appendingPathComponent(".quota.lock").path, O_CREAT | O_RDWR, 0o600)
        if fd >= 0 { flock(fd, LOCK_EX) }
        return fd
    }
    private static func unlock(_ fd: Int32) {
        if fd >= 0 { flock(fd, LOCK_UN); close(fd) }
    }
}
