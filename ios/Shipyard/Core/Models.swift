import Foundation

/// Decoded shapes of `/phone/v1/*`, mirroring internal/box/phone.go. Fields the
/// app does not use are left out; fields the box may omit are optional.

struct BoxInfo: Codable, Equatable {
    var box: String
    var version: Int
}

enum AgentState: String, Codable {
    case idle, running, waiting, finished
}

struct Session: Codable, Equatable, Identifiable {
    var name: String
    var location: String?
    var dir: String?
    var created: Date?
    var exited: Bool?
    var agent: String?
    var agentState: String?
    var stateSince: Date?
    var title: String?
    var worktreeTitle: String?
    var softWait: Bool?

    var id: String { name }
    var isAgent: Bool { !(agent ?? "").isEmpty }
    var isExited: Bool { exited ?? false }

    enum CodingKeys: String, CodingKey {
        case name, location, dir, created, exited, agent, title
        case agentState = "agent_state"
        case stateSince = "state_since"
        case worktreeTitle = "worktree_title"
        case softWait = "soft_wait"
    }
}

struct ScreenResponse: Codable, Equatable {
    var screen: String
}

struct SentResponse: Codable, Equatable {
    var sent: Bool
}

struct RunGate: Codable, Equatable {
    var path: String
    var title: String
    var text: String?
    var pick: Bool?
    var `default`: Int?
    var approveLabel: String?
    var rejectLabel: String?

    enum CodingKeys: String, CodingKey {
        case path, title, text, pick, `default`
        case approveLabel = "approve_label"
        case rejectLabel = "reject_label"
    }
}

struct RunSummary: Codable, Equatable, Identifiable {
    var id: String
    var template: String
    var title: String?
    var status: String
    var cursor: String?
    var created: Date?
    var updated: Date?
    var error: String?
    var gate: RunGate?
    var steps: Int?
    var candidates: Int?

    var isWaitingAtGate: Bool { status == "waiting_gate" }
    var isFinished: Bool { RunStatus.finished.contains(status) }
    var displayTitle: String { (title?.isEmpty == false ? title : nil) ?? template }
}

enum RunStatus {
    static let finished: Set<String> = ["succeeded", "failed", "cancelled", "interrupted"]
}

struct StepRun: Codable, Equatable, Identifiable {
    var id: String
    var kind: String
    var path: String
    var status: String
    var output: String?
    var error: String?
    var duration: String?
    var children: [StepRun]?

    /// Steps and their nested rounds as one flat list, skipped steps left out,
    /// each with its depth.
    func flattened(depth: Int = 0) -> [(step: StepRun, depth: Int)] {
        guard status != "skipped" else { return [] }
        return [(self, depth)] + (children ?? []).flatMap { $0.flattened(depth: depth + 1) }
    }
}

struct Candidate: Codable, Equatable, Identifiable {
    struct Verify: Codable, Equatable { var passed: Bool }
    struct Diff: Codable, Equatable {
        var files: Int?
        var added: Int
        var removed: Int
    }
    struct Judge: Codable, Equatable { var rank: Int? }
    var index: Int
    var agent: String?
    var verify: Verify
    var diff: Diff
    var judge: Judge?
    var summary: String?

    var id: Int { index }
}

struct Run: Codable, Equatable, Identifiable {
    var id: String
    var template: String
    var title: String?
    var status: String
    var cursor: String?
    var created: Date?
    var updated: Date?
    var error: String?
    var gate: RunGate?
    var steps: [StepRun]
    var candidates: [Candidate]?

    var isFinished: Bool { RunStatus.finished.contains(status) }
    var displayTitle: String { (title?.isEmpty == false ? title : nil) ?? template }
}

struct GateDecision: Codable, Equatable {
    var approve: Bool
    var pick: Int?
    var note: String?
}

/// The box's error body: `{"error": "...", "code": "..."}`.
struct APIErrorBody: Codable, Equatable {
    var error: String
    var code: String?
}

extension JSONDecoder {
    /// The box writes RFC 3339 times, with or without fractional seconds.
    static let phone: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let s = try decoder.singleValueContainer().decode(String.self)
            if let date = PhoneDates.parse(s) { return date }
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "bad time \(s)"))
        }
        return d
    }()
}

enum PhoneDates {
    static func parse(_ s: String) -> Date? {
        let withFraction = ISO8601DateFormatter()
        withFraction.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = withFraction.date(from: s) { return d }
        let plain = ISO8601DateFormatter()
        plain.formatOptions = [.withInternetDateTime]
        return plain.date(from: s)
    }
}
