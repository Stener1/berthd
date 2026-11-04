import Testing
import Foundation
@testable import Shipyard

/// Answers every request from a closure, so the URLSession client runs
/// against fixtures without a network.
final class StubProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var handler: ((URLRequest) -> (Int, Data))?
    nonisolated(unsafe) static var seen: [URLRequest] = []

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        var req = request
        if req.httpBody == nil, let stream = req.httpBodyStream {
            stream.open()
            var data = Data()
            var buf = [UInt8](repeating: 0, count: 1024)
            while stream.hasBytesAvailable { let n = stream.read(&buf, maxLength: 1024); if n <= 0 { break }; data.append(buf, count: n) }
            req.httpBody = data
        }
        Self.seen.append(req)
        guard let (status, body) = Self.handler?(req) else {
            client?.urlProtocol(self, didFailWithError: URLError(.cannotConnectToHost))
            return
        }
        let resp = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: nil)!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: body)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

@Suite(.serialized) struct ClientTests {
    private func client() -> URLSessionPhoneClient {
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [StubProtocol.self]
        StubProtocol.seen = []
        return URLSessionPhoneClient(base: URL(string: "http://100.64.1.2:1379")!, token: "tok", session: URLSession(configuration: c))
    }

    @Test func readsSessionsWithTheTokenHeader() async throws {
        let body = try Fixture.data("sessions")
        StubProtocol.handler = { _ in (200, body) }
        let s = try await client().sessions()
        #expect(s.count == 6)
        let r = StubProtocol.seen[0]
        #expect(r.url?.absoluteString == "http://100.64.1.2:1379/phone/v1/sessions")
        #expect(r.value(forHTTPHeaderField: "Authorization") == "Bearer tok")
    }

    @Test func screenAsksForHistoryAndEncodesTheName() async throws {
        let body = try Fixture.data("screen")
        StubProtocol.handler = { _ in (200, body) }
        _ = try await client().screen(session: "a b", history: 200)
        #expect(StubProtocol.seen[0].url?.absoluteString == "http://100.64.1.2:1379/phone/v1/sessions/a%20b/screen?history=200")
    }

    @Test func sendPostsTextAndEnter() async throws {
        StubProtocol.handler = { _ in (200, Data(#"{"sent":true}"#.utf8)) }
        try await client().send(session: "s1", text: "hello", enter: true)
        let r = StubProtocol.seen[0]
        #expect(r.httpMethod == "POST")
        #expect(r.url?.path == "/phone/v1/sessions/s1/send")
        let obj = try JSONSerialization.jsonObject(with: r.httpBody ?? Data()) as? [String: Any]
        #expect(obj?["text"] as? String == "hello")
        #expect(obj?["enter"] as? Bool == true)
    }

    @Test func keyPostsTheKeyName() async throws {
        StubProtocol.handler = { _ in (200, Data(#"{"sent":true}"#.utf8)) }
        try await client().key(session: "s1", key: QuickKey.interrupt.rawValue)
        let obj = try JSONSerialization.jsonObject(with: StubProtocol.seen[0].httpBody ?? Data()) as? [String: Any]
        #expect(obj?["key"] as? String == "interrupt")
        #expect(StubProtocol.seen[0].url?.path == "/phone/v1/sessions/s1/keys")
    }

    @Test func decidePostsToTheCurrentGate() async throws {
        StubProtocol.handler = { _ in (200, Data("{}".utf8)) }
        try await client().decide(run: "r_01", decision: GateDecision(approve: true, pick: 1))
        #expect(StubProtocol.seen[0].url?.path == "/phone/v1/runs/r_01/gates/current/decide")
    }

    @Test func a401MeansRepair() async throws {
        let body = try Fixture.data("error-unauthorized")
        StubProtocol.handler = { _ in (401, body) }
        await #expect(throws: PhoneAPIError.unauthorized) { try await client().sessions() }
    }

    @Test func agentWaitingIsKeptAsACode() async throws {
        let body = try Fixture.data("error-agent-waiting")
        StubProtocol.handler = { _ in (409, body) }
        do {
            try await client().send(session: "s", text: "x", enter: true)
            Issue.record("should have thrown")
        } catch let e as PhoneAPIError {
            #expect(e.isAgentWaiting)
            #expect(e.message.contains("keys"))
        }
    }

    @Test func noConnectionIsOffline() async {
        StubProtocol.handler = nil
        await #expect(throws: PhoneAPIError.offline) { try await client().info() }
    }

    @Test func garbageIsADecodingError() async {
        StubProtocol.handler = { _ in (200, Data("nope".utf8)) }
        await #expect(throws: PhoneAPIError.decoding) { try await client().sessions() }
    }
}

struct LinkTests {
    @Test func urlsInScreenTextBecomeLinks() throws {
        let screen = try Fixture.decode(ScreenResponse.self, "screen").screen
        let urls = ScreenText.urls(in: screen).map(\.absoluteString)
        #expect(urls.contains("https://preview.acme.example/cart?id=42&x=1"))
        #expect(urls.contains("http://100.64.1.2:3000/docs"))
        let linked = ScreenText.linked(screen)
        #expect(linked.runs.contains { $0.link?.host == "preview.acme.example" })
    }

    @Test func nonWebSchemesAreNotLinked() {
        #expect(ScreenText.urls(in: "tel:123 and file:///etc/passwd and mailto:a@b.co").isEmpty)
    }

    @Test func tailKeepsTheLastLines() {
        #expect(ScreenText.tail("a\n\nb\n  \nc\nd\n", lines: 2) == "c\nd")
    }

    @Test func deepLinksOpenASession() {
        #expect(DeepLink.parse(URL(string: "shipyard://box/devbox/session/acme-1a")!) == SessionTarget(box: "devbox", session: "acme-1a"))
        #expect(DeepLink.parse(URL(string: "shipyard://box/devbox")!) == nil)
        #expect(DeepLink.parse(URL(string: "shipyard://other/devbox/session/x")!) == nil)
    }

    @Test func ntfyClickLinksOpenASession() {
        #expect(DeepLink.parse(URL(string: "http://100.64.1.2:1379/#s=acme-1a")!) == SessionTarget(box: nil, session: "acme-1a"))
        #expect(DeepLink.parse(URL(string: "http://100.64.1.2:1379/#s=devbox%2Facme-1a")!) == SessionTarget(box: "devbox", session: "acme-1a"))
        #expect(DeepLink.parse(URL(string: "http://100.64.1.2:1379/#r=run1")!) == nil)
    }
}
