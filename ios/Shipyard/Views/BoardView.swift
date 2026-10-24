import SwiftUI

struct BoardView: View {
    @Environment(BoxStore.self) private var boxes
    @Environment(BoardModel.self) private var board
    @Environment(Router.self) private var router
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
        @Bindable var router = router
        NavigationStack {
            List {
                ForEach(board.boxProblems(boxes.boxes), id: \.box.id) { p in
                    BoxProblemRow(box: p.box, status: p.status)
                }
                ForEach(board.items) { item in
                    if let box = boxes.boxes.first(where: { $0.name == item.box }) {
                        NavigationLink(value: Router.ResolvedTarget(box: box, name: item.session.name)) {
                            SessionCard(item: item)
                        }
                        .listRowBackground(Theme.card)
                    }
                }
                if board.items.isEmpty && board.refreshedAt != nil {
                    ContentUnavailableView("No agents running", systemImage: "tray", description: Text("Agents on your online boxes show up here."))
                        .listRowBackground(Color.clear)
                }
                if board.shellCount > 0 {
                    Text("\(board.shellCount) plain terminal\(board.shellCount == 1 ? "" : "s") not shown")
                        .font(.footnote).foregroundStyle(Theme.muted).listRowBackground(Color.clear)
                }
            }
            .scrollContentBackground(.hidden)
            .background(Theme.background)
            .navigationTitle("Agents")
            .navigationDestination(item: $router.session) { t in
                SessionView(box: t.box, name: t.name)
            }
            .navigationDestination(for: Router.ResolvedTarget.self) { t in
                SessionView(box: t.box, name: t.name)
            }
            .refreshable { await board.refresh(boxes.boxes) }
            .overlay { if board.refreshedAt == nil { ProgressView() } }
        }
    }
}

extension BoardModel {
    struct Problem { var box: Box; var status: BoxStatus }
    func boxProblems(_ boxes: [Box]) -> [Problem] {
        boxes.compactMap { b in
            switch status[b.id] {
            case .offline, .needsRepair, .failed: Problem(box: b, status: status[b.id]!)
            default: nil
            }
        }
    }
}

struct BoxProblemRow: View {
    var box: Box
    var status: BoxStatus

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: status == .needsRepair ? "key.slash" : "wifi.slash").foregroundStyle(Theme.bad)
            VStack(alignment: .leading, spacing: 2) {
                Text(box.name).font(.subheadline.weight(.semibold))
                Text(text).font(.footnote).foregroundStyle(Theme.muted)
            }
        }
        .listRowBackground(Theme.card)
        .accessibilityElement(children: .combine)
    }

    private var text: String {
        switch status {
        case .offline: "Offline. Check that Tailscale is on."
        case .needsRepair: "Re-pair this box: its token no longer works."
        case .failed(let m): m
        default: ""
        }
    }
}

struct SessionCard: View {
    var item: BoardItem

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                Text(item.title).font(.headline).lineLimit(2)
                Spacer(minLength: 8)
                Text(relative(item.session.stateSince ?? item.session.created)).font(.caption.monospacedDigit()).foregroundStyle(Theme.muted)
            }
            HStack(spacing: 6) {
                StateBadge(text: item.state.label, color: Theme.color(item.state))
                Text([item.box, item.place].filter { !$0.isEmpty }.joined(separator: " · "))
                    .font(.footnote).foregroundStyle(Theme.muted).lineLimit(1)
            }
        }
        .padding(.vertical, 4)
        .overlay(alignment: .leading) {
            if item.state == .waiting {
                Rectangle().fill(Theme.waiting).frame(width: 3).padding(.leading, -20)
            }
        }
        .accessibilityElement(children: .combine)
    }
}
