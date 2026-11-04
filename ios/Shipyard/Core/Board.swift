import Foundation

/// What a card says a session is doing.
enum BoardState: Int, Comparable {
    case waiting, working, done, ready, exited

    static func < (a: BoardState, b: BoardState) -> Bool { a.rawValue < b.rawValue }

    var label: String {
        switch self {
        case .waiting: "Needs you"
        case .working: "Working"
        case .done: "Done"
        case .ready: "Ready"
        case .exited: "Exited"
        }
    }

    init(_ s: Session) {
        if s.isExited { self = .exited; return }
        switch s.agentState ?? "running" {
        case "waiting": self = .waiting
        case "finished": self = .done
        case "idle": self = .ready
        default: self = .working
        }
    }
}

/// One session on one box, as a card.
struct BoardItem: Equatable, Identifiable {
    var box: String
    var session: Session

    var id: String { box + "/" + session.name }
    var state: BoardState { BoardState(session) }

    var title: String {
        for t in [session.title, session.worktreeTitle] {
            if let t, !t.isEmpty { return t }
        }
        return session.worktreeName ?? session.name
    }

    /// "location/worktree" as the desktop dashboard shows it.
    var place: String { session.location ?? "" }
}

extension Session {
    var worktreeName: String? {
        guard let location, !location.isEmpty else { return nil }
        return location.split(separator: "/", maxSplits: 1).last.map(String.init)
    }
}

enum Board {
    /// Agent sessions from every box, those waiting for you first, then the
    /// ones at work, done, ready and exited. Within a state the longest-waiting
    /// comes first, as on the desktop; ties break by box and name so the order
    /// never jumps between refreshes.
    static func sorted(_ items: [BoardItem]) -> [BoardItem] {
        items.sorted { a, b in
            if a.state != b.state { return a.state < b.state }
            let ta = a.session.stateSince ?? a.session.created ?? .distantPast
            let tb = b.session.stateSince ?? b.session.created ?? .distantPast
            if ta != tb { return ta < tb }
            if a.box != b.box { return a.box < b.box }
            return a.session.name < b.session.name
        }
    }

    static func items(box: String, sessions: [Session]) -> [BoardItem] {
        sessions.filter { $0.isAgent }.map { BoardItem(box: box, session: $0) }
    }
}
