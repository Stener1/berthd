import SwiftUI

@main
struct ShipyardApp: App {
    @State private var boxes: BoxStore
    @State private var board: BoardModel
    @State private var route = Router()

    init() {
        let store = BoxStore()
        _boxes = State(initialValue: store)
        _board = State(initialValue: BoardModel { store.client(for: $0) })
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(boxes)
                .environment(board)
                .environment(route)
                .onOpenURL { url in
                    if let t = DeepLink.parse(url) { route.open(t, boxes: boxes.boxes) }
                    else if let paired = try? PairingLink.parse(url.absoluteString) { boxes.pair(paired) }
                }
        }
    }
}

/// Which tab is up and which session a deep link wants open.
@Observable
final class Router {
    enum Tab: Hashable { case board, runs, boxes }
    var tab: Tab = .board
    var session: ResolvedTarget?

    struct ResolvedTarget: Hashable { var box: Box; var name: String }

    func open(_ t: SessionTarget, boxes: [Box]) {
        let box = boxes.first { $0.name == t.box } ?? (boxes.count == 1 ? boxes[0] : nil)
        guard let box else { return }
        tab = .board
        session = ResolvedTarget(box: box, name: t.session)
    }
}
