import SwiftUI

/// A session's conversation: the same items the desktop's Conversation view
/// draws, as bubbles and compact cards, with the ask at the foot.
struct ChatView: View {
    var chat: ChatState
    var session: Session?
    var canAnswer: Bool
    var busy: Bool
    var act: (AskAction) async -> Void
    var openScreen: () -> Void

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 10) {
                    ForEach(chat.items) { item in
                        ChatRow(item: item).id(item.id)
                    }
                    if let card = AskCard.make(session: session, chat: chat, canAnswer: canAnswer) {
                        AskCardView(card: card, busy: busy, act: act, openScreen: openScreen)
                    }
                    Color.clear.frame(height: 1).id("end")
                }
                .padding(12)
            }
            .background(Theme.background)
            .defaultScrollAnchor(.bottom)
            .onChange(of: chat.items.last?.id) { proxy.scrollTo("end", anchor: .bottom) }
            .onChange(of: chat.items.count) { proxy.scrollTo("end", anchor: .bottom) }
        }
        .accessibilityIdentifier("chat")
    }
}

struct ChatRow: View {
    var item: TranscriptItem

    var body: some View {
        switch item.kind {
        case "user": UserBubble(text: item.text ?? "", midTurn: item.midTurn ?? false)
        case "text": AgentText(text: item.text ?? "")
        case "tools": ToolsCard(item: item)
        case "edit": EditCard(item: item)
        case "question": if !item.isOpenQuestion { QuestionRecord(item: item) }
        case "artifact": ArtifactRow(item: item)
        case "notice": NoticeRow(item: item)
        case "command": CommandRow(item: item)
        default:
            if let t = item.text, !t.isEmpty { Quiet(text: t) }
        }
    }
}

struct UserBubble: View {
    var text: String
    var midTurn: Bool

    var body: some View {
        HStack {
            Spacer(minLength: 40)
            VStack(alignment: .trailing, spacing: 2) {
                Text(MarkdownBlock.attributed(text))
                    .textSelection(.enabled)
                    .padding(.horizontal, 12).padding(.vertical, 8)
                    .background(Theme.sunk, in: RoundedRectangle(cornerRadius: 16))
                if midTurn { Text("sent while it worked").font(.caption2).foregroundStyle(Theme.muted) }
            }
        }
    }
}

struct AgentText: View {
    var text: String

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            ForEach(Array(MarkdownBlock.parse(text).enumerated()), id: \.offset) { _, block in
                switch block {
                case .text(let t):
                    Text(MarkdownBlock.attributed(t)).textSelection(.enabled)
                case .code(let c):
                    ScrollView(.horizontal, showsIndicators: false) {
                        Text(c).font(.system(.footnote, design: .monospaced)).foregroundStyle(Theme.terminalText)
                            .textSelection(.enabled)
                            .padding(10)
                    }
                    .background(Theme.terminal, in: RoundedRectangle(cornerRadius: 10))
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct ToolsCard: View {
    var item: TranscriptItem
    @State private var open = false

    private var calls: [ToolCall] { item.items ?? [] }
    private var summary: String {
        let verb = item.verb ?? "Used tools"
        return calls.count > 1 ? "\(verb) · \(calls.count)" : verb
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Button { withAnimation(.snappy(duration: 0.2)) { open.toggle() } } label: {
                HStack(spacing: 8) {
                    if item.done ?? false {
                        Image(systemName: "wrench.and.screwdriver").foregroundStyle(Theme.muted)
                    } else {
                        ProgressView().controlSize(.small)
                    }
                    Text(summary).font(.subheadline.weight(.medium)).lineLimit(1)
                    if !open, let last = calls.last {
                        Text(last.target).font(.footnote).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.middle)
                    }
                    Spacer(minLength: 0)
                    if calls.count > 0 {
                        Image(systemName: "chevron.right").font(.caption2).foregroundStyle(Theme.muted)
                            .rotationEffect(.degrees(open ? 90 : 0))
                    }
                }
            }
            .buttonStyle(.plain)
            if open {
                ForEach(Array(calls.enumerated()), id: \.offset) { _, c in
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text(c.verb).font(.footnote.weight(.medium))
                        Text(c.target).font(.system(.footnote, design: .monospaced)).foregroundStyle(Theme.muted)
                            .lineLimit(2).truncationMode(.middle)
                    }
                }
            }
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.card, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(Theme.line))
        .accessibilityIdentifier("tools-card")
    }
}

struct EditCard: View {
    var item: TranscriptItem

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: "pencil").foregroundStyle(Theme.muted)
            Text(item.file ?? "A file").font(.system(.footnote, design: .monospaced)).lineLimit(1).truncationMode(.head)
            Spacer(minLength: 0)
            Text("+\(item.added ?? 0)").font(.footnote.monospacedDigit()).foregroundStyle(Theme.done)
            Text("−\(item.removed ?? 0)").font(.footnote.monospacedDigit()).foregroundStyle(Theme.bad)
        }
        .padding(10)
        .background(Theme.card, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(Theme.line))
    }
}

/// A question the agent asked, once it is in the record: what it asked, and
/// the answer when there is one. An open one is the card at the foot.
struct QuestionRecord: View {
    var item: TranscriptItem

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            ForEach(Array((item.questions ?? []).enumerated()), id: \.offset) { i, q in
                VStack(alignment: .leading, spacing: 2) {
                    Text(q.question).font(.subheadline)
                    if let a = item.answers, i < a.count, !a[i].isEmpty {
                        Label(a[i], systemImage: "checkmark").font(.footnote.weight(.medium)).foregroundStyle(Theme.done)
                    } else if item.error ?? false {
                        Text("Not answered").font(.footnote).foregroundStyle(Theme.bad)
                    }
                }
            }
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.card, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(Theme.line))
    }
}

struct ArtifactRow: View {
    var item: TranscriptItem

    /// Only web links open, and in Safari.
    private var link: URL? {
        guard let s = item.url, let u = URL(string: s), ["https", "http"].contains(u.scheme?.lowercased() ?? "") else { return nil }
        return u
    }

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: "doc.richtext").foregroundStyle(Theme.muted)
            VStack(alignment: .leading, spacing: 1) {
                if let link {
                    Link(item.text ?? "Open page", destination: link).font(.subheadline.weight(.medium))
                } else {
                    Text(item.text ?? "Page").font(.subheadline.weight(.medium))
                }
                if let d = item.description, !d.isEmpty {
                    Text(d).font(.footnote).foregroundStyle(Theme.muted).lineLimit(2)
                } else if !(item.done ?? true) {
                    Text("Publishing…").font(.footnote).foregroundStyle(Theme.muted)
                }
            }
            Spacer(minLength: 0)
            if link != nil { Image(systemName: "arrow.up.right").font(.caption).foregroundStyle(Theme.muted) }
        }
        .padding(10)
        .background(Theme.card, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(Theme.line))
        .accessibilityIdentifier("artifact-row")
    }
}

struct NoticeRow: View {
    var item: TranscriptItem

    private var tint: Color {
        switch item.level { case "error": Theme.bad; case "warning": Theme.waiting; default: Theme.muted }
    }

    var body: some View {
        Text(item.text ?? "")
            .font(.footnote)
            .foregroundStyle(tint)
            .padding(10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(tint.opacity(0.12), in: RoundedRectangle(cornerRadius: 10))
    }
}

struct CommandRow: View {
    var item: TranscriptItem

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text([item.command, item.args].compactMap { $0 }.joined(separator: " "))
                .font(.system(.footnote, design: .monospaced)).foregroundStyle(Theme.muted)
            if let t = item.text, !t.isEmpty { Text(t).font(.footnote).textSelection(.enabled) }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct Quiet: View {
    var text: String

    var body: some View {
        Text(text).font(.footnote).foregroundStyle(Theme.muted).lineLimit(3)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct AskCardView: View {
    var card: AskCard
    var busy: Bool
    var act: (AskAction) async -> Void
    var openScreen: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            switch card {
            case .choices(let title, let detail, let why, let choices):
                ChoicesAsk(title: title, detail: detail, why: why, choices: choices, busy: busy, act: act)
            case .form(let tool, let questions):
                FormAsk(tool: tool, questions: questions, busy: busy, act: act)
            case .screenOnly(let text):
                Text(text).font(.subheadline)
                Button("Open the screen", action: openScreen).buttonStyle(.bordered)
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.card, in: RoundedRectangle(cornerRadius: 14))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(Theme.waiting.opacity(0.7), lineWidth: 1.5))
        .accessibilityIdentifier("ask-card")
    }
}

struct ChoicesAsk: View {
    var title: String
    var detail: String?
    var why: String?
    var choices: [AskChoice]
    var busy: Bool
    var act: (AskAction) async -> Void
    @State private var words = ""

    var body: some View {
        Text(title).font(.headline)
        if let detail, !detail.isEmpty {
            Text(detail).font(.system(.footnote, design: .monospaced)).textSelection(.enabled)
                .padding(8).frame(maxWidth: .infinity, alignment: .leading)
                .background(Theme.sunk, in: RoundedRectangle(cornerRadius: 8))
        }
        if let why, !why.isEmpty { Text(why).font(.footnote).foregroundStyle(Theme.muted) }
        ForEach(choices, id: \.key) { c in
            Button { Task { await act(AskCard.action(forChoice: c.key, in: choices)) } } label: {
                Text(c.label).frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent).tint(c.key == choices.first?.key ? Theme.working : Theme.muted)
            .disabled(busy)
            .accessibilityIdentifier("ask-choice-\(c.key)")
        }
        OtherField(prompt: "Tell it what to do instead", text: $words, busy: busy) {
            guard let a = AskCard.action(forWords: words) else { return }
            words = ""
            await act(a)
        }
    }
}

struct FormAsk: View {
    var tool: String
    var questions: [ChatQuestion]
    var busy: Bool
    var act: (AskAction) async -> Void
    @State private var draft: FormDraft

    init(tool: String, questions: [ChatQuestion], busy: Bool, act: @escaping (AskAction) async -> Void) {
        self.tool = tool
        self.questions = questions
        self.busy = busy
        self.act = act
        _draft = State(initialValue: FormDraft(questions: questions))
    }

    var body: some View {
        ForEach(Array(questions.enumerated()), id: \.offset) { q, question in
            VStack(alignment: .leading, spacing: 8) {
                if let h = question.header, !h.isEmpty { Text(h.uppercased()).font(.caption2.weight(.semibold)).foregroundStyle(Theme.muted) }
                Text(question.question).font(.headline)
                if question.takesSeveral { Text("Pick any").font(.footnote).foregroundStyle(Theme.muted) }
                ForEach(Array(question.choices.enumerated()), id: \.offset) { _, o in
                    let on = draft.isPicked(q, o.label)
                    Button {
                        if let answers = draft.tap(q, o.label, in: questions) {
                            Task { await act(.answer(tool: tool, answers: answers)) }
                        }
                    } label: {
                        HStack(alignment: .top, spacing: 8) {
                            if question.takesSeveral || questions.count > 1 {
                                Image(systemName: on ? "checkmark.circle.fill" : "circle").foregroundStyle(on ? Theme.working : Theme.muted)
                            }
                            VStack(alignment: .leading, spacing: 2) {
                                Text(o.label).font(.subheadline.weight(.medium))
                                if let d = o.description, !d.isEmpty { Text(d).font(.footnote).foregroundStyle(Theme.muted) }
                            }
                            Spacer(minLength: 0)
                        }
                        .padding(10)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(on ? Theme.working.opacity(0.14) : Theme.sunk, in: RoundedRectangle(cornerRadius: 10))
                    }
                    .buttonStyle(.plain)
                    .disabled(busy)
                    .accessibilityIdentifier("ask-option-\(q)-\(o.label)")
                }
                HStack {
                    TextField("Other", text: Binding(get: { draft.other[q] }, set: { draft.setOther(q, $0, in: questions) }), axis: .vertical)
                        .textFieldStyle(.roundedBorder)
                        .accessibilityIdentifier("ask-other-\(q)")
                }
            }
        }
        if questions.count > 1 || questions.first?.takesSeveral == true || !draft.other.allSatisfy(\.isEmpty) {
            Button { Task { await act(.answer(tool: tool, answers: draft.answers(for: questions))) } } label: {
                Text("Send answers").frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent).tint(Theme.working)
            .disabled(busy || !draft.isComplete(for: questions))
            .accessibilityIdentifier("ask-submit")
        }
    }
}

struct OtherField: View {
    var prompt: String
    @Binding var text: String
    var busy: Bool
    var submit: () async -> Void

    var body: some View {
        HStack(spacing: 8) {
            TextField(prompt, text: $text, axis: .vertical)
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("ask-other")
            Button { Task { await submit() } } label: {
                Image(systemName: "arrow.up.circle.fill").font(.system(size: 28))
            }
            .disabled(busy || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            .accessibilityLabel("Send")
        }
    }
}
