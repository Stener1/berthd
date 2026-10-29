import SwiftUI

struct RootView: View {
    @Environment(BoxStore.self) private var boxes
    @Environment(BoardModel.self) private var board
    @Environment(Router.self) private var router

    var body: some View {
        @Bindable var router = router
        Group {
            if boxes.boxes.isEmpty {
                NavigationStack { PairView(firstRun: true) }
            } else {
                TabView(selection: $router.tab) {
                    BoardView().tabItem { Label("Agents", systemImage: "tray.full") }
                        .badge(board.waitingCount > 0 ? board.items.filter { $0.state == .waiting }.count : 0)
                        .tag(Router.Tab.board)
                    RunsView().tabItem { Label("Runs", systemImage: "point.3.connected.trianglepath.dotted") }
                        .badge(board.runs.filter { $0.run.isWaitingAtGate }.count)
                        .tag(Router.Tab.runs)
                    BoxesView().tabItem { Label("Boxes", systemImage: "server.rack") }
                        .tag(Router.Tab.boxes)
                }
            }
        }
        .tint(.primary)
        .task { await pollLoop() }
    }

    /// Refreshes every few seconds while the app is in the foreground.
    private func pollLoop() async {
        while !Task.isCancelled {
            await board.refresh(boxes.boxes)
            try? await Task.sleep(for: .seconds(4))
        }
    }
}
