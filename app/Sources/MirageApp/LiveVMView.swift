import SwiftUI
import Virtualization
import UniformTypeIdentifiers

// VMViewHolder keeps a reference to the live AppKit view so the window can grab
// its rendered framebuffer for a PNG export (host-side — no guest capture).
final class VMViewHolder { weak var view: VZVirtualMachineView? }

// LiveVMView bridges AppKit's VZVirtualMachineView into SwiftUI. The view renders
// the guest framebuffer on the host — so what you see is the live screen, with no
// in-guest capture and therefore no screen-recording consent prompt.
struct LiveVMView: NSViewRepresentable {
    let vm: VZVirtualMachine?
    let holder: VMViewHolder

    func makeNSView(context: Context) -> VZVirtualMachineView {
        let v = VZVirtualMachineView()
        v.capturesSystemKeys = true   // route ⌘-tab etc. to the guest when focused
        v.virtualMachine = vm
        holder.view = v
        return v
    }

    func updateNSView(_ v: VZVirtualMachineView, context: Context) {
        v.virtualMachine = vm
        holder.view = v
    }
}

// LiveVMWindow is the content of a per-VM live window: it owns the VMRunner,
// boots the VM on appear, and shows the framebuffer with a small toolbar.
struct LiveVMWindow: View {
    let name: String
    @StateObject private var runner: VMRunner
    @State private var holder = VMViewHolder()

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
                LiveVMView(vm: runner.vm, holder: holder)
                if case .running = runner.status {} else { overlay }
            }
        }
        .onAppear { if case .idle = runner.status { runner.start() } }
        .onDisappear { runner.teardown() }   // never let the VM outlive its window
        .frame(minWidth: 640, minHeight: 480)
    }

    // Capture the host-rendered framebuffer to a PNG — no in-guest screencapture,
    // so no screen-recording consent prompt.
    private func savePNG() {
        guard let view = holder.view, view.bounds.width > 0,
              let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) else { return }
        view.cacheDisplay(in: view.bounds, to: rep)
        guard let data = rep.representation(using: .png, properties: [:]) else { return }
        let panel = NSSavePanel()
        panel.allowedContentTypes = [.png]
        panel.nameFieldStringValue = "\(name).png"
        if panel.runModal() == .OK, let url = panel.url { try? data.write(to: url) }
    }

    private var toolbar: some View {
        HStack(spacing: 10) {
            Circle().fill(running ? .green : .secondary).frame(width: 9, height: 9)
            Text(name).font(.body.weight(.medium))
            Text(statusText).font(.caption).foregroundStyle(.secondary)
            if runner.hasSnapshot {
                Label("snapshot", systemImage: "camera.viewfinder")
                    .font(.caption2).foregroundStyle(.blue)
                    .help("Open restores to this saved desktop instantly")
            }
            Spacer()
            if running {
                Button("Save PNG…") { savePNG() }
                    .help("Save the current screen as a PNG (captured on the host)")
                Button("Snapshot") { runner.snapshot() }
                    .help("Freeze the current desktop so Open restores here instantly")
                if runner.hasSnapshot {
                    Button("Discard Snapshot", role: .destructive) { runner.discardSnapshot() }
                }
                Button("Shut Down") { runner.stop() }
            }
        }
        .padding(8)
    }

    private var overlay: some View {
        VStack(spacing: 8) {
            switch runner.status {
            case .starting: ProgressView(); Text("Booting \(name)…").foregroundStyle(.white)
            case .restoring: ProgressView(); Text("Restoring snapshot…").foregroundStyle(.white)
            case .saving: ProgressView(); Text("Saving snapshot…").foregroundStyle(.white)
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
        case .restoring: return "restoring"
        case .running: return "running (live)"
        case .saving: return "saving snapshot"
        case .stopping: return "stopping"
        case .stopped: return "stopped"
        case .failed: return "failed"
        }
    }
}
