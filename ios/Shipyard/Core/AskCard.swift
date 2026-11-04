import Foundation

/// One question's answer, as `POST /phone/v1/sessions/{name}/answer` takes it:
/// the options picked by label, and the person's own words.
struct QuestionAnswer: Codable, Equatable {
    var picks: [String]?
    var other: String?
}

/// What tapping an ask card sends to the box.
enum AskAction: Equatable {
    case send(text: String, enter: Bool)
    case answer(tool: String, answers: [QuestionAnswer])

    func perform(with client: any PhoneAPIClient, session: String) async throws {
        switch self {
        case .send(let text, let enter): try await client.send(session: session, text: text, enter: enter)
        case .answer(let tool, let answers): try await client.answer(session: session, tool: tool, answers: answers)
        }
    }
}

/// What a waiting session asks of the person, drawn at the foot of the chat.
enum AskCard: Equatable {
    /// A permission or a plan: one button per choice the box offers.
    case choices(title: String, detail: String?, why: String?, choices: [AskChoice])
    /// AskUserQuestion: a form, answered through the box's `answer`.
    case form(tool: String, questions: [ChatQuestion])
    /// It waits on something only its screen can answer.
    case screenOnly(String)

    static func make(session: Session?, chat: ChatState, canAnswer: Bool) -> AskCard? {
        guard let session, session.agentState == AgentState.waiting.rawValue else { return nil }
        if let q = chat.openQuestion {
            guard canAnswer, let tool = q.tool, !tool.isEmpty, let questions = q.questions, !questions.isEmpty else {
                return .screenOnly("This box can't take answers to questions from the app. Answer on the Screen tab.")
            }
            return .form(tool: tool, questions: questions)
        }
        if let ask = session.ask {
            if let choices = ask.choices, !choices.isEmpty {
                let plan = ask.tool == "ExitPlanMode"
                let tool = ask.tool ?? ""
                return .choices(
                    title: plan ? "Plan ready" : tool.isEmpty ? "Needs your answer" : "Wants to use \(tool)",
                    detail: [ask.input, ask.message].compactMap { $0 }.first { !$0.isEmpty },
                    why: ask.why,
                    choices: choices)
            }
            if ask.tool == "AskUserQuestion" { return nil }
            if ask.tool != nil { return .screenOnly("It asks for something only its screen shows. Answer on the Screen tab.") }
        }
        return nil
    }

    /// A numbered choice is its key alone, as the desktop types it; anything
    /// else (yes, no) is a word and Enter.
    static func action(forChoice key: String, in choices: [AskChoice]) -> AskAction {
        .send(text: key, enter: !choices.contains { $0.key == key })
    }

    /// The person's own words at a permission or a plan: the box reads them as
    /// no, and this instead.
    static func action(forWords text: String) -> AskAction? {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        return t.isEmpty ? nil : .send(text: t, enter: true)
    }
}

/// The picks of a form being filled in.
struct FormDraft: Equatable {
    var picks: [[String]]
    var other: [String]

    init(questions: [ChatQuestion]) {
        picks = Array(repeating: [], count: questions.count)
        other = Array(repeating: "", count: questions.count)
    }

    func isPicked(_ q: Int, _ label: String) -> Bool { picks[q].contains(label) }

    /// A tap on an option. A lone single-choice question is answered by it,
    /// and returns the answers to send; otherwise the pick is kept.
    mutating func tap(_ q: Int, _ label: String, in questions: [ChatQuestion]) -> [QuestionAnswer]? {
        if questions[q].takesSeveral {
            if let i = picks[q].firstIndex(of: label) { picks[q].remove(at: i) } else { picks[q].append(label) }
        } else {
            picks[q] = picks[q] == [label] ? [] : [label]
            other[q] = ""
        }
        return questions.count == 1 && !questions[q].takesSeveral && !picks[q].isEmpty ? answers(for: questions) : nil
    }

    mutating func setOther(_ q: Int, _ text: String, in questions: [ChatQuestion]) {
        other[q] = text
        if !questions[q].takesSeveral, !text.isEmpty { picks[q] = [] }
    }

    private func words(_ q: Int) -> String { other[q].split(whereSeparator: \.isWhitespace).joined(separator: " ") }

    func isComplete(for questions: [ChatQuestion]) -> Bool {
        questions.indices.allSatisfy { !picks[$0].isEmpty || !words($0).isEmpty }
    }

    /// The answers in the questions' order, the picks in the options' order.
    func answers(for questions: [ChatQuestion]) -> [QuestionAnswer] {
        questions.indices.map { q in
            let ordered = questions[q].choices.map(\.label).filter { picks[q].contains($0) }
            return QuestionAnswer(picks: ordered.isEmpty ? nil : ordered, other: words(q).isEmpty ? nil : words(q))
        }
    }
}
