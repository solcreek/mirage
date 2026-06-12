import Foundation
import Virtualization

// VMRunner owns a live VZVirtualMachine in-process so VZVirtualMachineView can
// render it. It is the Swift counterpart of internal/engine.BuildVM — the device
// set is kept in step with the Go engine so a bundle boots identically either way.
// The GUI runs the VM here (host-side rendering) instead of capturing inside the
// guest, which is why the live view needs no guest screen-recording permission.
@MainActor
final class VMRunner: ObservableObject {
    enum Status: Equatable { case idle, starting, running, stopping, stopped, failed(String) }

    @Published private(set) var status: Status = .idle
    @Published private(set) var vm: VZVirtualMachine?

    let name: String
    private var delegate: Delegate?

    init(name: String) { self.name = name }

    /// Build the configuration from the on-disk bundle. Throws a readable error.
    private func buildConfiguration() throws -> VZVirtualMachineConfiguration {
        guard let bundle = VMBundle.find(name) else {
            throw Err("no bundle named \(name)")
        }
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
            widthInPixels: c.display.width, heightInPixels: c.display.height, pixelsPerInch: 80)]
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
        status = .starting
        do {
            let cfg = try buildConfiguration()
            let machine = VZVirtualMachine(configuration: cfg)
            let d = Delegate { [weak self] reason in
                Task { @MainActor in self?.status = .stopped; self?.note(reason) }
            }
            machine.delegate = d
            self.delegate = d
            self.vm = machine
            machine.start { [weak self] result in
                Task { @MainActor in
                    switch result {
                    case .success: self?.status = .running
                    case .failure(let e): self?.status = .failed(Self.describe(e))
                    }
                }
            }
        } catch {
            status = .failed(Self.describe(error))
        }
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
        guard let machine = vm, machine.canStop else { return }
        machine.stop { _ in }
        vm = nil
        status = .stopped
    }

    private var isFailed: Bool { if case .failed = status { return true }; return false }
    private func note(_ s: String?) {}
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
