import SwiftUI
import Virtualization

// LiveVMView bridges AppKit's VZVirtualMachineView into SwiftUI. The view renders
// the guest framebuffer on the host — so what you see is the live screen, with no
// in-guest capture and therefore no screen-recording consent prompt.
struct LiveVMView: NSViewRepresentable {
    let vm: VZVirtualMachine?

    func makeNSView(context: Context) -> VZVirtualMachineView {
        let v = VZVirtualMachineView()
        v.capturesSystemKeys = true   // route ⌘-tab etc. to the guest when focused
        v.virtualMachine = vm
        return v
    }

    func updateNSView(_ v: VZVirtualMachineView, context: Context) {
        v.virtualMachine = vm
    }
}

// LiveVMWindow is the content of a per-VM live window: it owns the VMRunner,
// boots the VM on appear, and shows the framebuffer with a small toolbar.
struct LiveVMWindow: View {
    let name: String
    @StateObject private var runner: VMRunner

    init(name: String) {
        self.name = name
        _runner = StateObject(wrappedValue: VMRunner(name: name))
    }

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            Divider()
            ZStack {
                Color.black
                LiveVMView(vm: runner.vm)
                if case .running = runner.status {} else { overlay }
            }
        }
        .onAppear { if case .idle = runner.status { runner.start() } }
        .onDisappear { runner.teardown() }   // never let the VM outlive its window
        .frame(minWidth: 640, minHeight: 480)
    }

    private var toolbar: some View {
        HStack(spacing: 10) {
            Circle().fill(running ? .green : .secondary).frame(width: 9, height: 9)
            Text(name).font(.body.weight(.medium))
            Text(statusText).font(.caption).foregroundStyle(.secondary)
            Spacer()
            if running {
                Button("Shut Down") { runner.stop() }
            }
        }
        .padding(8)
    }

    private var overlay: some View {
        VStack(spacing: 8) {
            switch runner.status {
            case .starting: ProgressView(); Text("Booting \(name)…").foregroundStyle(.white)
            case .stopping: ProgressView(); Text("Shutting down…").foregroundStyle(.white)
            case .stopped: Text("VM stopped").foregroundStyle(.white)
            case .failed(let m):
                Image(systemName: "exclamationmark.triangle").foregroundStyle(.yellow)
                Text(m).foregroundStyle(.white).multilineTextAlignment(.center).padding(.horizontal)
            default: EmptyView()
            }
        }
    }

    private var running: Bool { if case .running = runner.status { return true }; return false }
    private var statusText: String {
        switch runner.status {
        case .idle: return "idle"
        case .starting: return "booting"
        case .running: return "running (live)"
        case .stopping: return "stopping"
        case .stopped: return "stopped"
        case .failed: return "failed"
        }
    }
}
