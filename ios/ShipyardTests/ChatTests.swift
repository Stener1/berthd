import Testing
import Foundation
@testable import Shipyard

struct ChatDecodingTests {
    @Test func decodesEveryItemKindTheChatDraws() throws {
        let r = try Fixture.decode(TranscriptResult.self, "transcript")
        #expect(r.hasChat && r.next == 6 && r.gen == "g1" && r.file == "conv-acme-1")
        #expect(r.items.map(\.kind) == ["user", "text", "tools", "edit", "artifact", "question"])
        #expect(r.items[2].items?.first == ToolCall(verb: "Read", target: "src/price.ts", id: "c1"))
        #expect(r.items[3].added == 3 && r.items[3].removed == 1)
        #expect(r.items[4].url == "https://claude.ai/artifact/acme-1")
        #expect(r.items[5].questions?.first?.choices.map(\.label) == ["Half up", "Half even"])
        #expect(r.items[5].isOpenQuestion)
    }

    @Test func aBoxWithoutTheCapabilityDecodesAsNone() throws {
        let old = try Fixture.decode(BoxInfo.self, "info")
        #expect(!old.can("transcript"))
        let new = try Fixture.decode(BoxInfo.self, "info-chat")
        #expect(new.can("transcript") && new.can("answer"))
    }

    @Test func aSessionCarriesWhatItAsks() throws {
        let s = try Fixture.decode(Session.self, "session-asking")
        #expect(s.ask?.tool == "Bash")
        #expect(s.ask?.choices?.map(\.key) == ["1", "2", "3"])
    }

    @Test func aTerminalOnlyAgentHasNoChat() throws {
        let none = TranscriptResult(source: "none", items: [], next: 0, reason: "not found")
        var chat = ChatState()
        chat.apply(none)
        #expect(chat.hasChat == false && chat.items.isEmpty)
    }
}

struct ChatMergeTests {
    private func item(_ id: String, off: Int64, text: String = "", done: Bool? = nil) -> TranscriptItem {
        TranscriptItem(kind: "text", id: id, text: text, done: done, off: off)
    }
    private func result(_ items: [TranscriptItem], next: Int, gen: String = "g1", reset: Bool = false, start: Int64? = nil, file: String = "f1") -> TranscriptResult {
        TranscriptResult(source: "claude", items: items, next: next, gen: gen, reset: reset, start: start, file: file)
    }

    @Test func newItemsAreAppendedAndTheCursorMoves() {
        var chat = ChatState()
        chat.apply(result([item("a", off: 0), item("b", off: 10)], next: 2))
        chat.apply(result([item("c", off: 20)], next: 3))
        #expect(chat.items.map(\.id) == ["a", "b", "c"])
        #expect(chat.next == 3 && chat.gen == "g1")
    }

    @Test func anOpenToolGroupSentAgainIsReplacedByItsId() {
        var chat = ChatState()
        chat.apply(result([item("a", off: 0), item("g", off: 10, text: "running", done: false)], next: 2))
        chat.apply(result([item("g", off: 10, text: "finished", done: true), item("b", off: 30)], next: 3))
        #expect(chat.items.map(\.id) == ["a", "g", "b"])
        #expect(chat.items[1].text == "finished" && chat.items[1].done == true)
    }

    @Test func aResetKeepsWhatIsBeforeItsStartAndTakesTheRestFromIt() {
        var chat = ChatState()
        chat.apply(result([item("a", off: 0), item("b", off: 10), item("c", off: 20)], next: 3))
        chat.apply(result([item("b", off: 10, text: "again"), item("c", off: 20), item("d", off: 30)], next: 3, gen: "g2", reset: true, start: 10))
        #expect(chat.items.map(\.id) == ["a", "b", "c", "d"])
        #expect(chat.items[1].text == "again")
        #expect(chat.gen == "g2" && chat.next == 3)
    }

    @Test func aResetWithoutAStartReplacesEverything() {
        var chat = ChatState()
        chat.apply(result([item("a", off: 0)], next: 1))
        chat.apply(result([item("z", off: 0)], next: 1, gen: "g2", reset: true))
        #expect(chat.items.map(\.id) == ["z"])
    }

    @Test func anotherConversationIsReadAgainFromTheTop() {
        var chat = ChatState()
        chat.apply(result([item("a", off: 0)], next: 1))
        chat.apply(result([item("x", off: 50)], next: 9, file: "f2"))
        #expect(chat.items.isEmpty && chat.next == 0 && chat.file == "f2" && chat.hasChat == true)
    }

    @Test func itKeepsTheNewestSixHundred() {
        var chat = ChatState()
        chat.apply(result((0..<650).map { item("i\($0)", off: Int64($0)) }, next: 650))
        #expect(chat.items.count == ChatState.keep && chat.items.last?.id == "i649")
    }
}

struct AskCardTests {
    private func asking() throws -> Session { try Fixture.decode(Session.self, "session-asking") }

    @Test func aPermissionIsOneButtonPerChoice() throws {
        let card = AskCard.make(session: try asking(), chat: ChatState(), canAnswer: true)
        guard case .choices(let title, let detail, _, let choices)? = card else { Issue.record("no card"); return }
        #expect(title == "Wants to use Bash" && detail == "pnpm test")
        #expect(choices.map(\.label) == ["Allow", "Always allow", "Deny"])
    }

    @Test func aNumberedChoiceIsItsKeyWithoutEnter() throws {
        let choices = try #require(try asking().ask?.choices)
        #expect(AskCard.action(forChoice: "2", in: choices) == .send(text: "2", enter: false))
        #expect(AskCard.action(forChoice: "yes", in: [AskChoice(key: "yes", label: "Allow"), AskChoice(key: "no", label: "Deny")]) == .send(text: "yes", enter: false))
        #expect(AskCard.action(forChoice: "yes", in: []) == .send(text: "yes", enter: true))
    }

    @Test func theChoiceReachesTheBoxAsTheRequest() async throws {
        let fake = FakePhoneClient()
        try await AskCard.action(forChoice: "3", in: try #require(try asking().ask?.choices)).perform(with: fake, session: "acme-agent")
        #expect(fake.sent.map(\.text) == ["3"] && fake.sent.map(\.enter) == [false])
    }

    @Test func wordsAtAPermissionAreSentWithEnter() {
        #expect(AskCard.action(forWords: "  use pnpm instead ") == .send(text: "use pnpm instead", enter: true))
        #expect(AskCard.action(forWords: "   ") == nil)
    }

    @Test func noCardWhileTheAgentDoesNotWait() throws {
        var s = try asking()
        s.agentState = "running"
        #expect(AskCard.make(session: s, chat: ChatState(), canAnswer: true) == nil)
    }

    @Test func anOpenFormBecomesAFormAndNeedsTheCapability() throws {
        let r = try Fixture.decode(TranscriptResult.self, "transcript")
        var chat = ChatState()
        chat.apply(r)
        var s = try asking()
        s.ask = SessionAsk(tool: "AskUserQuestion")
        guard case .form(let tool, let qs)? = AskCard.make(session: s, chat: chat, canAnswer: true) else { Issue.record("no form"); return }
        #expect(tool == "toolu_acme1" && qs.count == 1)
        guard case .screenOnly? = AskCard.make(session: s, chat: chat, canAnswer: false) else { Issue.record("expected the screen"); return }
    }

    @Test func tappingTheOnlyOptionOfALoneQuestionAnswersIt() async throws {
        let qs = [ChatQuestion(question: "Which?", header: nil, multi: false, options: [QuestionOption(label: "Half up"), QuestionOption(label: "Half even")])]
        var draft = FormDraft(questions: qs)
        let tapped = draft.tap(0, "Half even", in: qs)
        let answers = try #require(tapped)
        #expect(answers == [QuestionAnswer(picks: ["Half even"], other: nil)])
        let fake = FakePhoneClient()
        try await AskAction.answer(tool: "toolu_acme1", answers: answers).perform(with: fake, session: "s")
        #expect(fake.answered.first?.tool == "toolu_acme1" && fake.answered.first?.answers == answers)
    }

    @Test func severalQuestionsWaitForSubmitAndKeepTheOptionsOrder() {
        let qs = [
            ChatQuestion(question: "Which?", header: nil, multi: false, options: [QuestionOption(label: "A"), QuestionOption(label: "B")]),
            ChatQuestion(question: "Which of these?", header: nil, multi: true, options: [QuestionOption(label: "X"), QuestionOption(label: "Y"), QuestionOption(label: "Z")]),
        ]
        var draft = FormDraft(questions: qs)
        #expect(draft.tap(0, "B", in: qs) == nil)
        #expect(!draft.isComplete(for: qs))
        _ = draft.tap(1, "Z", in: qs)
        _ = draft.tap(1, "X", in: qs)
        draft.setOther(1, "  and  more ", in: qs)
        #expect(draft.isComplete(for: qs))
        #expect(draft.answers(for: qs) == [QuestionAnswer(picks: ["B"], other: nil), QuestionAnswer(picks: ["X", "Z"], other: "and more")])
    }

    @Test func ownWordsReplaceThePickOfASingleChoice() {
        let qs = [ChatQuestion(question: "Which?", header: nil, multi: false, options: [QuestionOption(label: "A")])]
        var draft = FormDraft(questions: qs)
        _ = draft.tap(0, "A", in: qs)
        draft.setOther(0, "neither", in: qs)
        #expect(draft.answers(for: qs) == [QuestionAnswer(picks: nil, other: "neither")])
    }
}

struct MarkdownBlockTests {
    @Test func fencedCodeIsKeptAsItIs() {
        let blocks = MarkdownBlock.parse("Before\n\n```ts\nconst a = 1\n\nconst b = 2\n```\nAfter")
        #expect(blocks == [.text("Before"), .code("const a = 1\n\nconst b = 2"), .text("After")])
    }

    @Test func anUnclosedFenceStillShowsItsCode() {
        #expect(MarkdownBlock.parse("```\nlet x") == [.code("let x")])
    }

    @Test func aHeadingIsBold() {
        let s = String(MarkdownBlock.attributed("## Plan\n- one").characters)
        #expect(s == "Plan\n- one")
    }
}

@Suite(.serialized) struct TranscriptClientTests {
    private func client() -> URLSessionPhoneClient {
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [StubProtocol.self]
        StubProtocol.seen = []
        return URLSessionPhoneClient(base: URL(string: "http://100.64.1.2:1379")!, token: "tok", session: URLSession(configuration: c))
    }

    @Test func transcriptAsksFromTheCursorWithTheGeneration() async throws {
        let body = try Fixture.data("transcript")
        StubProtocol.handler = { _ in (200, body) }
        let r = try await client().transcript(session: "a b", since: 4, gen: "g9")
        #expect(r.items.count == 6)
        let url = try #require(StubProtocol.seen[0].url)
        #expect(url.path(percentEncoded: true) == "/phone/v1/sessions/a%20b/transcript")
        #expect(URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems == [URLQueryItem(name: "since", value: "4"), URLQueryItem(name: "gen", value: "g9")])
    }

    @Test func answerPostsTheToolAndEachQuestionsPicks() async throws {
        StubProtocol.handler = { _ in (200, Data(#"{"answered":["Half up"]}"#.utf8)) }
        try await client().answer(session: "s1", tool: "toolu_1", answers: [QuestionAnswer(picks: ["Half up"], other: nil)])
        let r = StubProtocol.seen[0]
        #expect(r.httpMethod == "POST" && r.url?.path == "/phone/v1/sessions/s1/answer")
        let obj = try JSONSerialization.jsonObject(with: r.httpBody ?? Data()) as? [String: Any]
        #expect(obj?["tool"] as? String == "toolu_1")
        let answers = obj?["answers"] as? [[String: Any]]
        #expect(answers?.first?["picks"] as? [String] == ["Half up"])
        #expect(answers?.first?["other"] == nil)
    }
}
