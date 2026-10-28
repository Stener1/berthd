import Testing
import Foundation
@testable import Shipyard

struct BoardTests {
    @Test func waitingFirstThenWorkingDoneReadyExited() throws {
        let sessions = try Fixture.decode([Session].self, "sessions")
        let board = Board.sorted(Board.items(box: "devbox", sessions: sessions.reversed()))
        #expect(board.map(\.state) == [.waiting, .working, .done, .ready, .exited])
        #expect(board.first?.session.name == "acme-checkout-fix-claude-1a")
    }

    @Test func plainTerminalsAreNotCards() throws {
        let sessions = try Fixture.decode([Session].self, "sessions")
        #expect(Board.items(box: "devbox", sessions: sessions).count == 5)
    }

    @Test func longestWaitingComesFirstAndTiesAreStable() {
        func s(_ name: String, since: TimeInterval) -> Session {
            Session(name: name, agent: "claude", agentState: "waiting", stateSince: Date(timeIntervalSince1970: since))
        }
        let items = [
            BoardItem(box: "b", session: s("late", since: 300)),
            BoardItem(box: "b", session: s("tie-b", since: 100)),
            BoardItem(box: "a", session: s("tie-a", since: 100)),
        ]
        #expect(Board.sorted(items).map(\.session.name) == ["tie-a", "tie-b", "late"])
        #expect(Board.sorted(items.reversed()).map(\.session.name) == ["tie-a", "tie-b", "late"])
    }

    @Test func cardTitlePrefersTheSessionTitleThenTheWorktreeTitle() throws {
        let sessions = try Fixture.decode([Session].self, "sessions")
        let items = Board.items(box: "devbox", sessions: sessions)
        #expect(items[0].title == "Fix the checkout total rounding")
        #expect(items[2].title == "hello")
        #expect(items[3].title == "Review PR")
    }

    @Test func missingAgentStateMeansWorking() {
        #expect(BoardState(Session(name: "x", agent: "claude")) == .working)
    }
}

struct BoardModelTests {
    private func box(_ name: String) -> Box { Box(id: UUID(), name: name, url: URL(string: "http://100.64.1.2:1379")!) }

    @Test func oneOfflineBoxDoesNotBreakTheRest() async throws {
        let sessions = try Fixture.decode([Session].self, "sessions")
        let up = FakePhoneClient()
        up.sessionsResult = .success(sessions)
        up.runsResult = .success(try Fixture.decode([RunSummary].self, "runs"))
        let down = FakePhoneClient()
        down.sessionsResult = .failure(.offline)
        let stale = FakePhoneClient()
        stale.sessionsResult = .failure(.unauthorized)
        let (a, b, c, d) = (box("devbox"), box("mini"), box("old"), box("unpaired"))
        let clients = [a.id: up, b.id: down, c.id: stale]
        let model = BoardModel { clients[$0.id] }
        await model.refresh([a, b, c, d])
        #expect(model.status[a.id] == .online)
        #expect(model.status[b.id] == .offline)
        #expect(model.status[c.id] == .needsRepair)
        #expect(model.status[d.id] == .needsRepair)
        #expect(model.items.count == 5)
        #expect(model.items.allSatisfy { $0.box == "devbox" })
        #expect(model.shellCount == 1)
        #expect(model.runs.first?.run.isWaitingAtGate == true)
        #expect(model.waitingCount == 2)
    }

    @Test func sessionsFromTwoBoxesShareOneSortedList() async throws {
        func waiting(_ n: String, since: TimeInterval) -> Session {
            Session(name: n, agent: "claude", agentState: "waiting", stateSince: Date(timeIntervalSince1970: since))
        }
        let one = FakePhoneClient(), two = FakePhoneClient()
        one.sessionsResult = .success([Session(name: "w", agent: "claude", agentState: "running"), waiting("old", since: 10)])
        two.sessionsResult = .success([waiting("older", since: 5)])
        let (a, b) = (box("devbox"), box("mini"))
        let clients = [a.id: one, b.id: two]
        let model = BoardModel { clients[$0.id] }
        await model.refresh([a, b])
        #expect(model.items.map(\.session.name) == ["older", "old", "w"])
    }
}
