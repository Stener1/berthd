import Foundation

/// The chat of a session: `GET /phone/v1/sessions/{name}/transcript`, the same
/// contract as the app API's (docs/reference/app-api.mdx). Only the fields the
/// chat draws are decoded; tool output and thinking are never in it.

struct ToolCall: Codable, Equatable {
    var verb: String
    var target: String
    var id: String?
}

struct QuestionOption: Codable, Equatable {
    var label: String
    var description: String?
}

struct ChatQuestion: Codable, Equatable {
    var question: String
    var header: String?
    var multi: Bool?
    var options: [QuestionOption]?

    var choices: [QuestionOption] { options ?? [] }
    var takesSeveral: Bool { multi ?? false }
}

struct TranscriptItem: Codable, Equatable, Identifiable {
    var kind: String
    var id: String
    var text: String?
    var verb: String?
    var items: [ToolCall]?
    var done: Bool?
    var file: String?
    var added: Int?
    var removed: Int?
    var names: [String]?
    var notice: String?
    var level: String?
    /// Where the line that made this item starts in the record.
    var off: Int64?
    var url: String?
    var description: String?
    var command: String?
    var args: String?
    var error: Bool?
    var questions: [ChatQuestion]?
    var answers: [String]?
    var tool: String?
    var midTurn: Bool?

    /// A question's form is open until the agent's record has its answers.
    var isOpenQuestion: Bool { kind == "question" && !(done ?? false) && !(error ?? false) }
}

struct TranscriptResult: Codable, Equatable {
    var source: String
    var items: [TranscriptItem]
    var next: Int
    var gen: String?
    var reset: Bool?
    var start: Int64?
    var file: String?
    var reason: String?

    /// `none`: the box has no conversation to read for this session (a
    /// terminal-only agent, or none started): the app shows the screen.
    var hasChat: Bool { source != "none" }
}

/// What the app holds of one session's chat, and how a reading is merged in.
struct ChatState: Equatable {
    static let keep = 600

    var items: [TranscriptItem] = []
    var next = 0
    var gen: String?
    var file: String?
    /// Nil until the box has answered once.
    var hasChat: Bool?
    var reason: String?

    /// Items are named by where their line starts in the record, so a reading
    /// replaces the ones it holds by `id` (an open tool group is sent again
    /// until done) and appends the rest. A `reset` is the whole window from
    /// `start`: what is held before it stays, the rest is taken from the
    /// reading. Another `file` is another conversation: drop all and read it
    /// from the top.
    mutating func apply(_ r: TranscriptResult) {
        hasChat = r.hasChat
        reason = r.reason
        guard r.hasChat else {
            self = ChatState(hasChat: false, reason: r.reason)
            return
        }
        if let held = file, let now = r.file, held != now {
            self = ChatState(hasChat: true)
            file = now
            return
        }
        if r.file != nil { file = r.file }
        gen = r.gen ?? gen
        next = r.next
        if r.reset ?? false {
            if let start = r.start {
                items.removeAll { ($0.off ?? 0) >= start }
            } else {
                items.removeAll()
            }
            items.append(contentsOf: r.items)
        } else {
            for item in r.items {
                if let i = items.lastIndex(where: { $0.id == item.id }) {
                    items[i] = item
                } else {
                    items.append(item)
                }
            }
        }
        if items.count > Self.keep { items.removeFirst(items.count - Self.keep) }
    }

    /// The form the agent waits on, if the record shows one.
    var openQuestion: TranscriptItem? { items.last(where: \.isOpenQuestion) }
}
