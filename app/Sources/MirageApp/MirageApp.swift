import SwiftUI

@main
struct MirageApp: App {
    var body: some Scene {
        WindowGroup("Mirage") {
            ContentView()
                .frame(minWidth: 640, minHeight: 420)
        }
    }
}

@MainActor
final class VMStore: ObservableObject {
    @Published var rows: [VMRow] = []
    @Published var busy: String? = nil      // name being acted on
    @Published var error: String? = nil
    @Published var shot: URL? = nil

    func refresh() {
        Task.detached {
            do {
                let rows = try MirageClient.list()
                await MainActor.run { self.rows = rows; self.error = nil }
            } catch {
                await MainActor.run { self.error = error.localizedDescription }
            }
        }
    }

    private func act(_ name: String, _ work: @escaping () throws -> Void) {
        busy = name
        Task.detached {
            do {
                try work()
                await MainActor.run { self.error = nil }
            } catch {
                await MainActor.run { self.error = error.localizedDescription }
            }
            await MainActor.run { self.busy = nil; self.refresh() }
        }
    }

    func start(_ n: String) { act(n) { try MirageClient.start(n) } }
    func stop(_ n: String) { act(n) { try MirageClient.stop(n) } }
    func delete(_ n: String) { act(n) { try MirageClient.delete(n) } }

    func screenshot(_ n: String) {
        busy = n
        Task.detached {
            do {
                let url = try MirageClient.screenshot(n)
                await MainActor.run { self.shot = url; self.error = nil }
            } catch {
                await MainActor.run { self.error = error.localizedDescription }
            }
            await MainActor.run { self.busy = nil }
        }
    }
}

struct ContentView: View {
    @StateObject private var store = VMStore()

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            if store.rows.isEmpty {
                ContentUnavailableLabel
            } else {
                List(store.rows) { row in VMRowView(row: row, store: store) }
            }
            if let e = store.error {
                Text(e).font(.callout).foregroundStyle(.red)
                    .padding(8).frame(maxWidth: .infinity, alignment: .leading)
                    .background(.red.opacity(0.08))
            }
        }
        .onAppear { store.refresh() }
        .sheet(isPresented: Binding(get: { store.shot != nil }, set: { if !$0 { store.shot = nil } })) {
            if let url = store.shot { ScreenshotSheet(url: url) }
        }
    }

    private var header: some View {
        HStack {
            Image(systemName: "macwindow.on.rectangle").font(.title2)
            Text("Mirage").font(.title2.bold())
            Spacer()
            Button { store.refresh() } label: { Label("Refresh", systemImage: "arrow.clockwise") }
        }
        .padding(12)
    }

    private var ContentUnavailableLabel: some View {
        VStack(spacing: 6) {
            Image(systemName: "tray").font(.largeTitle).foregroundStyle(.secondary)
            Text("No images or VMs").foregroundStyle(.secondary)
            Text("Create one with: mirage create <name> --ipsw <path>")
                .font(.caption).foregroundStyle(.tertiary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

struct VMRowView: View {
    let row: VMRow
    @ObservedObject var store: VMStore

    private var running: Bool { row.status == "running" }

    var body: some View {
        HStack(spacing: 12) {
            Circle().fill(running ? .green : .secondary).frame(width: 9, height: 9)
            VStack(alignment: .leading, spacing: 1) {
                Text(row.name).font(.body.weight(.medium))
                Text("\(row.kind) · \(row.os) · \(row.status)").font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            if store.busy == row.name {
                ProgressView().controlSize(.small)
            } else {
                if running {
                    Button("Screenshot") { store.screenshot(row.name) }
                    Button("Stop") { store.stop(row.name) }
                } else {
                    Button("Start") { store.start(row.name) }
                    if row.kind == "vm" {
                        Button(role: .destructive) { store.delete(row.name) } label: { Image(systemName: "trash") }
                    }
                }
            }
        }
        .padding(.vertical, 4)
    }
}

struct ScreenshotSheet: View {
    let url: URL
    @Environment(\.dismiss) private var dismiss
    var body: some View {
        VStack {
            if let img = NSImage(contentsOf: url) {
                Image(nsImage: img).resizable().scaledToFit()
            } else {
                Text("Could not load screenshot").foregroundStyle(.secondary)
            }
            Button("Close") { dismiss() }.padding(.top, 8)
        }
        .padding()
        .frame(width: 720, height: 480)
    }
}
