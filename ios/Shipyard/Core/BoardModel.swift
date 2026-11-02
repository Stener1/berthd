import Foundation
import Observation

enum BoxStatus: Equatable {
    case loading
    case online
    case offline
    case needsRepair
    case failed(String)
}

/// Every box's sessions and runs, fetched side by side. One box failing marks
/// that box and leaves the rest alone.
@Observable
final class BoardModel {
    typealias ClientFactory = (Box) -> (any PhoneAPIClient)?

    private(set) var items: [BoardItem] = []
    private(set) var runs: [BoxRun] = []
    private(set) var status: [UUID: BoxStatus] = [:]
    private(set) var shellCount = 0
    private(set) var refreshedAt: Date?

    private let clientFor: ClientFactory

    init(clientFor: @escaping ClientFactory) { self.clientFor = clientFor }

    struct BoxRun: Equatable, Identifiable {
        var box: Box
        var run: RunSummary
        var id: String { box.id.uuidString + "/" + run.id }
    }

    func refresh(_ boxes: [Box]) async {
        var next: [UUID: BoxStatus] = [:]
        var items: [BoardItem] = []
        var runs: [BoxRun] = []
        var shells = 0
        await withTaskGroup(of: (Box, Result<([Session], [RunSummary]), PhoneAPIError>).self) { group in
            for box in boxes {
                guard let client = clientFor(box) else { next[box.id] = .needsRepair; continue }
                group.addTask {
                    do {
                        async let s = client.sessions()
                        async let r = client.runs()
                        let sessions = try await s
                        // Runs are a bonus: a box without them still lists sessions.
                        let rs = (try? await r) ?? []
                        return (box, .success((sessions, rs)))
                    } catch let e as PhoneAPIError {
                        return (box, .failure(e))
                    } catch {
                        return (box, .failure(.offline))
                    }
                }
            }
            for await (box, result) in group {
                switch result {
                case .success(let (sessions, rs)):
                    next[box.id] = .online
                    items += Board.items(box: box.name, sessions: sessions)
                    shells += sessions.filter { !$0.isAgent && !$0.isExited }.count
                    runs += rs.map { BoxRun(box: box, run: $0) }
                case .failure(let e):
                    switch e {
                    case .offline: next[box.id] = .offline
                    case .unauthorized: next[box.id] = .needsRepair
                    default: next[box.id] = .failed(e.message)
                    }
                }
            }
        }
        self.items = Board.sorted(items)
        self.runs = runs.sorted { a, b in
            if a.run.isWaitingAtGate != b.run.isWaitingAtGate { return a.run.isWaitingAtGate }
            if a.run.isFinished != b.run.isFinished { return !a.run.isFinished }
            return (a.run.updated ?? .distantPast) > (b.run.updated ?? .distantPast)
        }
        self.shellCount = shells
        self.status = next
        self.refreshedAt = Date()
    }

    var waitingCount: Int {
        items.filter { $0.state == .waiting }.count + runs.filter { $0.run.isWaitingAtGate }.count
    }
}
