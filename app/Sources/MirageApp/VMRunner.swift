import Foundation
import Virtualization

// VMRunner owns a live VZVirtualMachine in-process so VZVirtualMachineView can
// render it. It is the Swift counterpart of internal/engine.BuildVM — the device
// set is kept in step with the Go engine so a bundle boots identically either way.
// The GUI runs the VM here (host-side rendering) instead of capturing inside the
// guest, which is why the live view needs no guest screen-recording permission.
//
// It also implements snapshots: a paired freeze of RAM/device state (.vzstate)
// and a CoW clone of the disk, so Open can restore straight to a warm, logged-in
// desktop instead of cold-booting. Restore always resets the disk to the
// snapshot's disk first, so every Open returns to the exact frozen state.
@MainActor
final class VMRunner: ObservableObject {
    enum Status: Equatable { case idle, starting, restoring, running, saving, stopping, stopped, failed(String) }

    @Published private(set) var status: Status = .idle
    @Published private(set) var vm: VZVirtualMachine?
    @Published private(set) var hasSnapshot: Bool

    let name: String
    private let bundle: VMBundle?
    private var delegate: Delegate?

    init(name: String) {
        self.name = name
        self.bundle = VMBundle.find(name)
        self.hasSnapshot = bundle?.hasSnapshot ?? false
    }

    /// Build the configuration from the on-disk bundle. Throws a readable error.
    private func buildConfiguration() throws -> VZVirtualMachineConfiguration {
        guard let bundle else { throw Err("no bundle named \(name)") }
        let c = try bundle.load()
        guard c.os == "macos" else { throw Err("the live view supports macOS guests only") }

        guard let hw = VZMacHardwareModel(dataRepresentation: c.hardwareModel) else {
            throw Err("invalid hardware model in config.json")
        }
        guard let mid = VZMacMachineIdentifier(dataRepresentation: c.machineID) else {
            throw Err("invalid machine identifier in config.json")
        }
        let platform = VZMacPlatformConfiguration()
        platform.hardwareModel = hw
        platform.machineIdentifier = mid
        platform.auxiliaryStorage = VZMacAuxiliaryStorage(url: bundle.auxURL)

        let cfg = VZVirtualMachineConfiguration()
        cfg.platform = platform
        cfg.bootLoader = VZMacOSBootLoader()
        cfg.cpuCount = c.cpu
        cfg.memorySize = c.memoryMB << 20

        // Graphics device + display — present on every boot, matching the engine.
        let gfx = VZMacGraphicsDeviceConfiguration()
        gfx.displays = [VZMacGraphicsDisplayConfiguration(
            widthInPixels: c.display.width, heightInPixels: c.display.height,
            pixelsPerInch: c.display.ppi ?? 80)]  // high ppi → HiDPI/Retina rendering
        cfg.graphicsDevices = [gfx]

        // NAT network with the bundle's pinned MAC (so it matches the agent's view).
        let net = VZVirtioNetworkDeviceConfiguration()
        net.attachment = VZNATNetworkDeviceAttachment()
        if let mac = VZMACAddress(string: c.mac) { net.macAddress = mac }
        cfg.networkDevices = [net]

        // Boot disk (read-write). The GUI only opens stopped VMs, so it is the
        // sole writer — never open a VM here that the supervisor is running.
        let diskAttach = try VZDiskImageStorageDeviceAttachment(url: bundle.diskURL, readOnly: false)
        cfg.storageDevices = [VZVirtioBlockDeviceConfiguration(attachment: diskAttach)]

        cfg.keyboards = [VZMacKeyboardConfiguration()]
        cfg.pointingDevices = [VZMacTrackpadConfiguration()]
        cfg.socketDevices = [VZVirtioSocketDeviceConfiguration()]

        try cfg.validate()
        return cfg
    }

    func start() {
        guard status == .idle || status == .stopped || isFailed else { return }
        // Claim a quota slot (and register this VM in the shared state dir) before
        // booting, so the host-wide 2-VM limit is enforced across CLI and GUI.
        do {
            try SupervisorState.claim(name)
        } catch {
            status = .failed(Self.describe(error))
            return
        }
        do {
            // Restore the paired snapshot if present: reset the disk to the
            // snapshot's disk (instant CoW clone), then restore RAM and resume.
            if let bundle, bundle.hasSnapshot {
                try resetDiskToSnapshot(bundle)
                let cfg = try buildConfiguration()
                let machine = makeMachine(cfg)
                status = .restoring
                machine.restoreMachineStateFrom(url: bundle.snapshotStateURL) { [weak self] (err: Error?) in
                    Task { @MainActor in
                        if let err {
                            self?.status = .failed("restore failed: \((err as NSError).localizedDescription) — Discard Snapshot to cold-boot")
                            self?.releaseSlot()
                            return
                        }
                        self?.resume(machine)
                    }
                }
                return
            }
            let cfg = try buildConfiguration()
            let machine = makeMachine(cfg)
            status = .starting
            machine.start { [weak self] result in
                Task { @MainActor in
                    switch result {
                    case .success: self?.status = .running
                    case .failure(let e): self?.status = .failed(Self.describe(e)); self?.releaseSlot()
                    }
                }
            }
        } catch {
            status = .failed(Self.describe(error))
            releaseSlot()
        }
    }

    private func makeMachine(_ cfg: VZVirtualMachineConfiguration) -> VZVirtualMachine {
        let machine = VZVirtualMachine(configuration: cfg)
        // When the guest stops on its own (or via Shut Down), drop the quota slot.
        let d = Delegate { [weak self] _ in Task { @MainActor in self?.status = .stopped; self?.releaseSlot() } }
        machine.delegate = d
        self.delegate = d
        self.vm = machine
        return machine
    }

    private func releaseSlot() { SupervisorState.release(name) }

    private func resume(_ machine: VZVirtualMachine) {
        machine.resume { [weak self] r in
            Task { @MainActor in
                switch r {
                case .success: self?.status = .running
                case .failure(let e): self?.status = .failed(Self.describe(e))
                }
            }
        }
    }

    /// Take a snapshot: pause → save RAM/device state → stop → clone the now-quiesced
    /// disk. Pairing the disk clone with the saved state guarantees a consistent
    /// restore. The VM stops afterwards (the session ends at the freeze point).
    func snapshot() {
        guard let bundle, let machine = vm, machine.canPause, status == .running else { return }
        status = .saving
        machine.pause { [weak self] r in
            Task { @MainActor in
                guard case .success = r else { self?.status = .failed("pause failed"); return }
                machine.saveMachineStateTo(url: bundle.snapshotStateURL) { (err: Error?) in
                    Task { @MainActor in
                        if let err {
                            self?.status = .failed("snapshot failed: \((err as NSError).localizedDescription)")
                            return
                        }
                        // Still paused (so the disk is quiesced): clone it as the
                        // snapshot's paired disk, then power off.
                        do {
                            try self?.cloneFile(from: bundle.diskURL, to: bundle.snapshotDiskURL)
                        } catch {
                            self?.status = .failed("disk snapshot failed: \(Self.describe(error))")
                            return
                        }
                        machine.stop { _ in
                            withExtendedLifetime(machine) {}
                            Task { @MainActor in
                                self?.vm = nil
                                self?.hasSnapshot = true
                                self?.status = .stopped
                                self?.releaseSlot() // VM is down — free the quota slot
                            }
                        }
                    }
                }
            }
        }
    }

    func discardSnapshot() {
        guard let bundle else { return }
        try? FileManager.default.removeItem(at: bundle.snapshotStateURL)
        try? FileManager.default.removeItem(at: bundle.snapshotDiskURL)
        hasSnapshot = false
    }

    func stop() {
        guard let machine = vm, status == .running else { return }
        status = .stopping
        // requestStop sends a guest power-button event; fall back to forceStop.
        do {
            try machine.requestStop()
        } catch {
            machine.stop { _ in }
        }
    }

    /// Called when the live window closes. The VM must not outlive its window —
    /// otherwise the Virtualization helper keeps holding the disk/aux lock and
    /// blocks every other use of the bundle. Force-stop guarantees the lock is
    /// released immediately (an unclean guest power-off, which APFS tolerates);
    /// the toolbar "Shut Down" remains the graceful path.
    func teardown() {
        defer { releaseSlot() } // always free the quota slot when the window closes
        guard let machine = vm else { return }
        guard machine.canStop else { vm = nil; return }
        status = .stopping
        // Retain `machine` inside the completion so the force-stop runs to
        // completion even though the window (and this StateObject) is being torn
        // down — otherwise the VM object can deallocate mid-stop and the
        // Virtualization helper lingers holding the disk lock.
        machine.stop { _ in withExtendedLifetime(machine) {} }
        vm = nil
        status = .stopped
    }

    // resetDiskToSnapshot replaces the live disk with a fresh CoW clone of the
    // snapshot's disk, so a restore always lands on the exact frozen disk state
    // (any in-session changes from the previous Open are discarded).
    private func resetDiskToSnapshot(_ bundle: VMBundle) throws {
        let tmp = bundle.diskURL.appendingPathExtension("restore")
        try? FileManager.default.removeItem(at: tmp)
        try cloneFile(from: bundle.snapshotDiskURL, to: tmp)
        _ = try FileManager.default.replaceItemAt(bundle.diskURL, withItemAt: tmp)
    }

    // cloneFile makes an APFS copy-on-write clone via `cp -c` (metadata-only,
    // ~instant regardless of size — the same primitive the Go bundle clone uses).
    private func cloneFile(from: URL, to: URL) throws {
        try? FileManager.default.removeItem(at: to)
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/bin/cp")
        p.arguments = ["-c", from.path, to.path]
        try p.run()
        p.waitUntilExit()
        if p.terminationStatus != 0 { throw Err("cp -c failed (status \(p.terminationStatus))") }
    }

    private var isFailed: Bool { if case .failed = status { return true }; return false }
    private static func describe(_ e: Error) -> String {
        (e as? Err)?.message ?? (e as NSError).localizedDescription
    }

    struct Err: Error { let message: String; init(_ m: String) { message = m } }

    private final class Delegate: NSObject, VZVirtualMachineDelegate {
        let onStop: (String?) -> Void
        init(onStop: @escaping (String?) -> Void) { self.onStop = onStop }
        func guestDidStop(_ vm: VZVirtualMachine) { onStop(nil) }
        func virtualMachine(_ vm: VZVirtualMachine, didStopWithError error: Error) {
            onStop(error.localizedDescription)
        }
    }
}
