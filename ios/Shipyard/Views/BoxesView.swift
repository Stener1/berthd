import SwiftUI

struct BoxesView: View {
    @Environment(BoxStore.self) private var boxes
    @Environment(BoardModel.self) private var board
    @State private var renaming: Box?
    @State private var newName = ""
    @State private var removing: Box?
    @State private var pairing = false

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(boxes.boxes) { box in
                        HStack(spacing: 10) {
                            Circle().fill(color(board.status[box.id])).frame(width: 9, height: 9)
                            VStack(alignment: .leading, spacing: 2) {
                                Text(box.name).font(.headline)
                                Text(statusText(board.status[box.id])).font(.footnote).foregroundStyle(Theme.muted)
                                Text(box.url.host() ?? "").font(.caption.monospaced()).foregroundStyle(Theme.muted)
                            }
                            Spacer()
                            if board.status[box.id] == .needsRepair {
                                Button("Re-pair") { pairing = true }.buttonStyle(.bordered)
                            }
                        }
                        .swipeActions {
                            Button("Remove", role: .destructive) { removing = box }
                            Button("Rename") { newName = box.name; renaming = box }.tint(.blue)
                        }
                        .contextMenu {
                            Button("Rename", systemImage: "pencil") { newName = box.name; renaming = box }
                            Button("Remove", systemImage: "trash", role: .destructive) { removing = box }
                        }
                        .accessibilityElement(children: .combine)
                    }
                } footer: {
                    Text("Removing a box forgets its token on this phone. Nothing changes on the box.")
                }
                .listRowBackground(Theme.card)
            }
            .scrollContentBackground(.hidden)
            .background(Theme.background)
            .navigationTitle("Boxes")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button { pairing = true } label: { Image(systemName: "plus") }.accessibilityLabel("Add a box")
                }
            }
            .sheet(isPresented: $pairing) { NavigationStack { PairView(firstRun: false) } }
            .alert("Rename box", isPresented: Binding(get: { renaming != nil }, set: { if !$0 { renaming = nil } })) {
                TextField("Name", text: $newName)
                Button("Save") { if let b = renaming { boxes.rename(b, to: newName) }; renaming = nil }
                Button("Cancel", role: .cancel) { renaming = nil }
            }
            .confirmationDialog("Remove \(removing?.name ?? "this box")?", isPresented: Binding(get: { removing != nil }, set: { if !$0 { removing = nil } }), titleVisibility: .visible) {
                Button("Remove", role: .destructive) { if let b = removing { boxes.remove(b) }; removing = nil }
            }
        }
    }

    private func color(_ s: BoxStatus?) -> Color {
        switch s {
        case .online: Theme.done
        case .offline, .needsRepair, .failed: Theme.bad
        default: Theme.idle
        }
    }

    private func statusText(_ s: BoxStatus?) -> String {
        switch s {
        case .online: "Online"
        case .offline: "Offline"
        case .needsRepair: "Re-pair this box"
        case .failed(let m): m
        default: "Checking…"
        }
    }
}

struct PairView: View {
    @Environment(BoxStore.self) private var boxes
    @Environment(\.dismiss) private var dismiss
    var firstRun: Bool

    @State private var link = ""
    @State private var error: String?
    @State private var scanning = false

    var body: some View {
        Form {
            if firstRun {
                Section {
                    Text("See which agents need you and answer them, across all your boxes.").font(.body)
                }
                .listRowBackground(Color.clear)
            }
            Section {
                Button { scanning = true } label: { Label("Scan the pairing code", systemImage: "qrcode.viewfinder") }
                    .accessibilityIdentifier("scan-code")
            } footer: {
                Text("In Shipyard on your computer: Settings → Phone → turn on phone access for each box. Your phone needs Tailscale, signed in to the same tailnet.")
            }
            .listRowBackground(Theme.card)
            Section("Or paste a pairing link") {
                TextField("http://…#pair=…", text: $link, axis: .vertical)
                    .lineLimit(1...4)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .keyboardType(.URL)
                    .accessibilityIdentifier("pair-link")
                Button("Pair") { pair(link) }
                    .disabled(link.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    .accessibilityIdentifier("pair-button")
                if let error { Text(error).font(.footnote).foregroundStyle(Theme.bad) }
            }
            .listRowBackground(Theme.card)
        }
        .scrollContentBackground(.hidden)
        .background(Theme.background)
        .navigationTitle(firstRun ? "Shipyard" : "Add a box")
        .toolbar {
            if !firstRun {
                ToolbarItem(placement: .cancellationAction) { Button("Close") { dismiss() } }
            }
        }
        .fullScreenCover(isPresented: $scanning) {
            QRScannerView { code in
                scanning = false
                if let code { pair(code) }
            }
        }
    }

    private func pair(_ text: String) {
        do {
            let found = try PairingLink.parse(text)
            boxes.pair(found)
            link = ""
            error = nil
            if !firstRun { dismiss() }
        } catch PairingError.noPairing {
            error = "That isn't a pairing link. Copy the whole link from Shipyard → Settings → Phone."
        } catch {
            self.error = "That pairing link is damaged. Copy it again."
        }
    }
}
