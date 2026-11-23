import Foundation
@testable import Shipyard

enum Fixture {
    static func data(_ name: String) throws -> Data {
        let url = Bundle(for: BundleToken.self).url(forResource: name, withExtension: "json")!
        return try Data(contentsOf: url)
    }
    static func decode<T: Decodable>(_ type: T.Type, _ name: String) throws -> T {
        try JSONDecoder.phone.decode(T.self, from: data(name))
    }
    private final class BundleToken {}
}

/// A fake box: canned answers, and a record of what the app sent.
final class FakePhoneClient: PhoneAPIClient, @unchecked Sendable {
    var sessionsResult: Result<[Session], PhoneAPIError> = .success([])
    var runsResult: Result<[RunSummary], PhoneAPIError> = .success([])
    var sentTexts: [String] = []
    var sentKeys: [String] = []
    var decisions: [GateDecision] = []
    var sendError: PhoneAPIError?
    var sent: [(text: String, enter: Bool)] = []
    var answered: [(tool: String, answers: [QuestionAnswer])] = []
    var transcriptResult: Result<TranscriptResult, PhoneAPIError> = .success(TranscriptResult(source: "none", items: [], next: 0))
    var transcriptAsks: [(since: Int, gen: String?)] = []

    func info() async throws -> BoxInfo { BoxInfo(box: "fake", version: 1, capabilities: ["transcript", "answer"]) }
    func sessions() async throws -> [Session] { try sessionsResult.get() }
    func screen(session: String, history: Int) async throws -> String { "screen" }
    func send(session: String, text: String, enter: Bool) async throws {
        if let sendError { throw sendError }
        sentTexts.append(text)
        sent.append((text, enter))
    }
    func transcript(session: String, since: Int, gen: String?) async throws -> TranscriptResult {
        transcriptAsks.append((since, gen))
        return try transcriptResult.get()
    }
    func answer(session: String, tool: String, answers: [QuestionAnswer]) async throws {
        answered.append((tool, answers))
    }
    func key(session: String, key: String) async throws { sentKeys.append(key) }
    func runs() async throws -> [RunSummary] { try runsResult.get() }
    func run(id: String) async throws -> Run { try Fixture.decode(Run.self, "run") }
    func decide(run: String, decision: GateDecision) async throws { decisions.append(decision) }
    func cancel(run: String) async throws {}
}
