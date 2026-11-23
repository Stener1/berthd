import Foundation

enum PhoneAPIError: Error, Equatable {
    /// The box did not answer: Tailscale off, box asleep, wrong address.
    case offline
    /// 401: the token is wrong or was replaced by a new code.
    case unauthorized
    case api(status: Int, code: String?, message: String)
    case decoding

    /// What to tell the person, in plain words.
    var message: String {
        switch self {
        case .offline:
            return "Can't reach this box. Check that Tailscale is on and the box is awake."
        case .unauthorized:
            return "This box no longer accepts the phone's token. Re-pair this box."
        case .decoding:
            return "The box answered with something this app doesn't understand. Update the app or the box."
        case .api(let status, let code, let message):
            switch code {
            case "agent_waiting":
                return "This agent is waiting for an answer. Use the keys (1–4, Enter) to answer it; typing would pick an option by accident."
            case "session_exited":
                return "This session has ended, so it takes no input."
            case "not_found":
                return "That no longer exists on the box."
            case "refused":
                return "A rule on the box refused this: \(message)"
            case "too_many":
                return "Too many tries. Wait a minute and try again."
            default:
                if status == 429 { return "Too many wrong tokens from this phone. Wait a minute and try again." }
                return message.isEmpty ? "The box answered \(status)." : message
            }
        }
    }

    var needsRepair: Bool { self == .unauthorized }
    var isAgentWaiting: Bool {
        if case .api(_, "agent_waiting", _) = self { return true }
        return false
    }
}

/// What the app asks of one box. The URLSession implementation talks to
/// `/phone/v1`; tests use a fake.
protocol PhoneAPIClient: Sendable {
    func info() async throws -> BoxInfo
    func sessions() async throws -> [Session]
    func screen(session: String, history: Int) async throws -> String
    func send(session: String, text: String, enter: Bool) async throws
    func transcript(session: String, since: Int, gen: String?) async throws -> TranscriptResult
    func answer(session: String, tool: String, answers: [QuestionAnswer]) async throws
    func key(session: String, key: String) async throws
    func runs() async throws -> [RunSummary]
    func run(id: String) async throws -> Run
    func decide(run: String, decision: GateDecision) async throws
    func cancel(run: String) async throws
}

/// The keys the box lets a phone press (`phoneKeys` in internal/box/phone.go).
enum QuickKey: String, CaseIterable, Identifiable {
    case enter, escape, up, down, tab, interrupt
    case one = "1", two = "2", three = "3", four = "4"
    case y, n

    var id: String { rawValue }
    var label: String {
        switch self {
        case .enter: "Enter"
        case .escape: "Esc"
        case .up: "↑"
        case .down: "↓"
        case .tab: "Tab"
        case .interrupt: "Ctrl-C"
        default: rawValue
        }
    }
}

struct URLSessionPhoneClient: PhoneAPIClient {
    let base: URL
    let token: String
    let session: URLSession

    init(base: URL, token: String, session: URLSession = .shared) {
        self.base = base
        self.token = token
        self.session = session
    }

    private func request(_ method: String, _ path: String, query: [URLQueryItem] = [], body: (any Encodable)? = nil) throws -> URLRequest {
        var c = URLComponents(url: base, resolvingAgainstBaseURL: false)!
        c.percentEncodedPath = "/phone/v1/" + path
        c.queryItems = query.isEmpty ? nil : query
        var r = URLRequest(url: c.url!, timeoutInterval: 8)
        r.httpMethod = method
        r.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        if let body {
            r.setValue("application/json", forHTTPHeaderField: "Content-Type")
            r.httpBody = try JSONEncoder().encode(body)
        }
        return r
    }

    private func data(_ r: URLRequest) async throws -> Data {
        let (data, response): (Data, URLResponse)
        do {
            (data, response) = try await session.data(for: r)
        } catch {
            throw PhoneAPIError.offline
        }
        guard let http = response as? HTTPURLResponse else { throw PhoneAPIError.offline }
        if http.statusCode == 401 { throw PhoneAPIError.unauthorized }
        guard (200..<300).contains(http.statusCode) else {
            let body = try? JSONDecoder().decode(APIErrorBody.self, from: data)
            throw PhoneAPIError.api(status: http.statusCode, code: body?.code, message: body?.error ?? "")
        }
        return data
    }

    private func decode<T: Decodable>(_ type: T.Type, _ r: URLRequest) async throws -> T {
        let d = try await data(r)
        do { return try JSONDecoder.phone.decode(T.self, from: d) } catch { throw PhoneAPIError.decoding }
    }

    private static func segment(_ s: String) -> String {
        s.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? s
    }

    func info() async throws -> BoxInfo { try await decode(BoxInfo.self, request("GET", "info")) }

    func sessions() async throws -> [Session] { try await decode([Session].self, request("GET", "sessions")) }

    func screen(session name: String, history: Int) async throws -> String {
        let q = history > 0 ? [URLQueryItem(name: "history", value: String(history))] : []
        return try await decode(ScreenResponse.self, request("GET", "sessions/\(Self.segment(name))/screen", query: q)).screen
    }

    func send(session name: String, text: String, enter: Bool) async throws {
        struct Body: Encodable { var text: String; var enter: Bool }
        _ = try await data(request("POST", "sessions/\(Self.segment(name))/send", body: Body(text: text, enter: enter)))
    }

    func transcript(session name: String, since: Int, gen: String?) async throws -> TranscriptResult {
        var q = [URLQueryItem(name: "since", value: String(max(since, 0)))]
        if let gen, !gen.isEmpty { q.append(URLQueryItem(name: "gen", value: gen)) }
        return try await decode(TranscriptResult.self, request("GET", "sessions/\(Self.segment(name))/transcript", query: q))
    }

    func answer(session name: String, tool: String, answers: [QuestionAnswer]) async throws {
        struct Body: Encodable { var tool: String; var answers: [QuestionAnswer] }
        _ = try await data(request("POST", "sessions/\(Self.segment(name))/answer", body: Body(tool: tool, answers: answers)))
    }

    func key(session name: String, key: String) async throws {
        struct Body: Encodable { var key: String }
        _ = try await data(request("POST", "sessions/\(Self.segment(name))/keys", body: Body(key: key)))
    }

    func runs() async throws -> [RunSummary] { try await decode([RunSummary].self, request("GET", "runs")) }

    func run(id: String) async throws -> Run { try await decode(Run.self, request("GET", "runs/\(Self.segment(id))")) }

    func decide(run id: String, decision: GateDecision) async throws {
        _ = try await data(request("POST", "runs/\(Self.segment(id))/gates/current/decide", body: decision))
    }

    func cancel(run id: String) async throws {
        _ = try await data(request("POST", "runs/\(Self.segment(id))/cancel"))
    }
}
