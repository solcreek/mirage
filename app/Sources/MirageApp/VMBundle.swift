import Foundation

// VMBundle mirrors internal/bundle (store.go + bundle.go): the on-disk bundle
// layout and config.json schema, so the GUI can boot a VM in-process for live
// display. The Go engine remains the source of truth for the format; this is a
// faithful reader, not a second writer.
struct VMConfig: Decodable {
    let schemaVersion: Int
    let os: String
    let cpu: Int
    let memoryMB: UInt64
    let mac: String
    let hardwareModel: Data   // base64 in JSON → Data (VZMacHardwareModel)
    let machineID: Data       // base64 in JSON → Data (VZMacMachineIdentifier)
    let display: Display

    struct Display: Decodable {
        let width: Int; let height: Int; let ppi: Int?
    }

    enum CodingKeys: String, CodingKey {
        case schemaVersion = "schema_version"
        case os, cpu, mac, display
        case memoryMB = "memory_mb"
        case hardwareModel = "hardware_model"
        case machineID = "machine_id"
    }
}

struct VMBundle {
    let name: String
    let dir: URL

    var configURL: URL { dir.appendingPathComponent("config.json") }
    var diskURL: URL { dir.appendingPathComponent("disk.img") }
    var auxURL: URL { dir.appendingPathComponent("aux.img") }

    // A snapshot is a paired freeze: the saved RAM/device state plus a CoW clone
    // of the disk taken at the same paused moment. Both are needed for a
    // consistent restore (RAM state alone would mismatch a diverged disk).
    var snapshotStateURL: URL { dir.appendingPathComponent("snapshot.vzstate") }
    var snapshotDiskURL: URL { dir.appendingPathComponent("snapshot-disk.img") }
    var hasSnapshot: Bool {
        let fm = FileManager.default
        return fm.fileExists(atPath: snapshotStateURL.path) && fm.fileExists(atPath: snapshotDiskURL.path)
    }

    func load() throws -> VMConfig {
        let data = try Data(contentsOf: configURL)
        // Go encodes []byte as base64 strings; JSONDecoder's default Data
        // strategy is base64, so hardware_model / machine_id decode directly.
        return try JSONDecoder().decode(VMConfig.self, from: data)
    }

    // dataHome → $XDG_DATA_HOME/mirage or ~/.local/share/mirage (matches store.go).
    private static var dataHome: URL {
        if let x = ProcessInfo.processInfo.environment["XDG_DATA_HOME"], !x.isEmpty {
            return URL(fileURLWithPath: x).appendingPathComponent("mirage")
        }
        let home = FileManager.default.homeDirectoryForCurrentUser
        return home.appendingPathComponent(".local/share/mirage")
    }
    private static var imagesDir: URL { dataHome.appendingPathComponent("images") }
    private static var vmsDir: URL { dataHome.appendingPathComponent("vms") }

    /// Locate a bundle by name, checking VMs first then images (matches Find).
    static func find(_ name: String) -> VMBundle? {
        for base in [vmsDir, imagesDir] {
            let dir = base.appendingPathComponent("\(name).mirage")
            if FileManager.default.fileExists(atPath: dir.appendingPathComponent("config.json").path) {
                return VMBundle(name: name, dir: dir)
            }
        }
        return nil
    }
}
