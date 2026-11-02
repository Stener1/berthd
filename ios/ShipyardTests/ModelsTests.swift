import Testing
import Foundation
@testable import Shipyard

struct ModelsTests {
    @Test func decodesInfo() throws {
        let i = try Fixture.decode(BoxInfo.self, "info")
        #expect(i == BoxInfo(box: "acme-devbox", version: 1))
    }

    @Test func decodesSessionsWithAndWithoutFractionalSeconds() throws {
        let s = try Fixture.decode([Session].self, "sessions")
        #expect(s.count == 6)
        #expect(s[0].agentState == "waiting")
        #expect(s[0].worktreeTitle == "Checkout rounding fix")
        #expect(s[0].stateSince != nil)
        #expect(s[1].created != nil)
        #expect(s[5].isAgent == false)
        #expect(s[4].isExited)
    }

    @Test func decodesScreen() throws {
        let s = try Fixture.decode(ScreenResponse.self, "screen")
        #expect(s.screen.contains("rounding"))
    }

    @Test func decodesRunSummaries() throws {
        let r = try Fixture.decode([RunSummary].self, "runs")
        #expect(r.map(\.status) == ["waiting_gate", "running", "succeeded"])
        #expect(r[0].gate?.pick == true)
        #expect(r[0].gate?.default == 2)
        #expect(r[2].displayTitle == "review")
        #expect(r[2].isFinished)
    }

    @Test func decodesRunWithStepsAndCandidates() throws {
        let r = try Fixture.decode(Run.self, "run")
        #expect(r.candidates?.count == 3)
        #expect(r.candidates?[2].judge?.rank == 1)
        #expect(r.candidates?[0].verify.passed == false)
        let flat = r.steps.flatMap { $0.flattened() }
        // The skipped child is left out; the child is nested one level.
        #expect(flat.map(\.step.id) == ["attempts", "a0", "judge", "pick"])
        #expect(flat.map(\.depth) == [0, 1, 0, 0])
    }

    @Test func decodesErrorBodies() throws {
        let e = try Fixture.decode(APIErrorBody.self, "error-agent-waiting")
        #expect(e.code == "agent_waiting")
        #expect(PhoneAPIError.api(status: 409, code: e.code, message: e.error).isAgentWaiting)
    }

    @Test func encodesGateDecisionLikeTheBoxExpects() throws {
        let d = try JSONEncoder().encode(GateDecision(approve: true, pick: 2))
        let obj = try JSONSerialization.jsonObject(with: d) as? [String: Any]
        #expect(obj?["approve"] as? Bool == true)
        #expect(obj?["pick"] as? Int == 2)
        #expect(obj?["note"] == nil)
    }

    @Test func errorMessagesArePlain() {
        #expect(PhoneAPIError.offline.message.contains("Tailscale"))
        #expect(PhoneAPIError.unauthorized.message.contains("Re-pair"))
        #expect(PhoneAPIError.unauthorized.needsRepair)
        #expect(PhoneAPIError.api(status: 409, code: "agent_waiting", message: "x").message.contains("keys"))
        #expect(PhoneAPIError.api(status: 410, code: "session_exited", message: "x").message.contains("ended"))
        #expect(PhoneAPIError.api(status: 429, code: nil, message: "").message.contains("Wait"))
    }
}
