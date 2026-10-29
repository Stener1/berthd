import SwiftUI

struct SessionView: View {
    @Environment(BoxStore.self) private var boxes
    var box: Box
    var name: String

    enum Mode: String { case chat = "Chat", screen = "Screen" }

    @State private var screen = ""
    @State private var session: Session?
    @State private var info: BoxInfo?
    @State private var chat = ChatState()
    @State private var chosen: Mode?
    @State private var acting = false
    @State private var message = ""
    @State private var notice: String?
    @State private var noticeIsWaiting = false
    @State private var repair = false
    @State private var offline = false
    @State private var sending = false
    @FocusState private var focused: Bool
    @Environment(\.colorScheme) private var scheme

    private var title: String {
        session.map { BoardItem(box: box.name, session: $0).title } ?? name
    }

    /// The chat needs a box that serves it and a conversation it can read;
    /// otherwise (a terminal-only agent, an older box) it is the screen alone.
    private var chatAvailable: Bool { (info?.can("transcript") ?? false) && chat.hasChat != false }
    private var mode: Mode { chatAvailable ? (chosen ?? .chat) : .screen }

    var body: some View {
        VStack(spacing: 0) {
            if repair {
                Banner(text: "This box no longer accepts the phone's token. Re-pair it from the Boxes tab.", tint: Theme.bad)
            } else if offline {
                Banner(text: "Can't reach \(box.name). Showing the last screen.", tint: Theme.bad)
            }
            if mode == .chat {
                ChatView(chat: chat, session: session, canAnswer: info?.can("answer") ?? false, busy: acting,
                         act: { await perform($0) }, openScreen: { chosen = .screen })
            } else {
            ScrollViewReader { proxy in
                ScrollView {
                    Text(ScreenText.linked(screen.isEmpty ? "Loading…" : screen))
                        .font(.system(.footnote, design: .monospaced))
                        .foregroundStyle(Theme.terminalText)
                        .tint(Color(UIColor(hex: 0x7FB6FF)))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(12)
                    Color.clear.frame(height: 1).id("end")
                }
                .background(Theme.terminal)
                .onChange(of: screen) { proxy.scrollTo("end", anchor: .bottom) }
            }
            }
            if let notice {
                Banner(text: notice, tint: noticeIsWaiting ? Theme.waiting : Theme.bad)
            }
            if mode == .screen {
                KeyBar(disabled: repair) { key in await press(key) }
            }
            HStack(alignment: .bottom, spacing: 8) {
                TextField("Send a prompt", text: $message, axis: .vertical)
                    .lineLimit(1...5)
                    .textFieldStyle(.roundedBorder)
                    .focused($focused)
                    .submitLabel(.send)
                    .onSubmit { Task { await send() } }
                Button { Task { await send() } } label: {
                    Image(systemName: "arrow.up.circle.fill").font(.system(size: 30))
                }
                .disabled(message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || sending || repair)
                .accessibilityLabel("Send")
            }
            .padding(.horizontal, 12).padding(.vertical, 8)
            .background(Theme.background)
        }
        .navigationTitle(title)
        .navigationBarTitleDisplayMode(.inline)
        .toolbarBackground(Theme.background, for: .navigationBar)
        .toolbarBackground(.visible, for: .navigationBar)
        .toolbarColorScheme(scheme, for: .navigationBar)
        .toolbar(.hidden, for: .tabBar)
        .toolbar {
            if chatAvailable {
                ToolbarItem(placement: .topBarTrailing) {
                    Picker("View", selection: Binding(get: { mode }, set: { chosen = $0 })) {
                        Text(Mode.chat.rawValue).tag(Mode.chat)
                        Text(Mode.screen.rawValue).tag(Mode.screen)
                    }
                    .pickerStyle(.segmented)
                    .frame(width: 140)
                    .accessibilityIdentifier("view-toggle")
                }
            }
            if let s = session {
                ToolbarItem(placement: .principal) {
                    VStack(spacing: 0) {
                        Text(title).font(.headline).lineLimit(1)
                        let state = BoardState(s)
                        (Text([box.name, agentNames[s.agent ?? ""] ?? "Terminal"].joined(separator: " · ") + " · ").foregroundStyle(Theme.muted)
                            + Text(state.label).foregroundStyle(Theme.color(state)))
                            .font(.caption)
                    }
                }
            }
        }
        .task { await pollLoop() }
    }

    private func pollLoop() async {
        while !Task.isCancelled {
            await load()
            try? await Task.sleep(for: .seconds(2))
        }
    }

    private func load() async {
        guard let client = boxes.client(for: box) else { repair = true; return }
        do {
            async let all = client.sessions()
            if info == nil { info = try? await client.info() }
            if mode == .screen { screen = try await client.screen(session: name, history: 200) }
            if info?.can("transcript") ?? false { try await readChat(client) }
            session = try await all.first { $0.name == name }
            offline = false
            repair = false
        } catch let e as PhoneAPIError {
            offline = e == .offline
            repair = e.needsRepair
            if case .api(_, "not_found", _) = e { notice = e.message; noticeIsWaiting = false }
        } catch {}
    }

    /// Reads what is new in the chat; a conversation that changed under us
    /// is read again from its top.
    private func readChat(_ client: any PhoneAPIClient) async throws {
        for _ in 0..<2 {
            let before = chat.file
            chat.apply(try await client.transcript(session: name, since: chat.next, gen: chat.gen))
            if before == nil || chat.file == before || chat.hasChat != true { return }
        }
    }

    private func perform(_ action: AskAction) async {
        guard let client = boxes.client(for: box) else { return }
        acting = true
        defer { acting = false }
        do {
            try await action.perform(with: client, session: name)
            notice = nil
            await load()
        } catch let e as PhoneAPIError {
            show(e)
        } catch {}
    }

    private func send() async {
        let text = message.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, let client = boxes.client(for: box) else { return }
        sending = true
        defer { sending = false }
        do {
            try await client.send(session: name, text: text, enter: true)
            message = ""
            notice = nil
            await load()
        } catch let e as PhoneAPIError {
            show(e)
        } catch {}
    }

    private func press(_ key: QuickKey) async {
        guard let client = boxes.client(for: box) else { return }
        do {
            try await client.key(session: name, key: key.rawValue)
            notice = nil
            await load()
        } catch let e as PhoneAPIError {
            show(e)
        } catch {}
    }

    private func show(_ e: PhoneAPIError) {
        notice = e.message
        noticeIsWaiting = e.isAgentWaiting
        repair = e.needsRepair
        offline = e == .offline
    }
}

struct Banner: View {
    var text: String
    var tint: Color

    var body: some View {
        Text(text)
            .font(.footnote)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 12).padding(.vertical, 8)
            .background(tint.opacity(0.14))
            .foregroundStyle(tint)
    }
}

/// Enter, Esc, arrows, Tab, 1–4 and Ctrl-C: what answering or stopping an agent takes.
struct KeyBar: View {
    var disabled: Bool
    var press: (QuickKey) async -> Void
    private static let keys: [QuickKey] = [.enter, .escape, .up, .down, .tab, .one, .two, .three, .four, .interrupt]

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                ForEach(Self.keys) { k in
                    Button { Task { await press(k) } } label: {
                        Text(k.label)
                            .font(.system(.subheadline, design: .monospaced).weight(.medium))
                            .frame(minWidth: 40, minHeight: 36)
                            .padding(.horizontal, 6)
                            .background(Theme.sunk, in: RoundedRectangle(cornerRadius: 9))
                            .foregroundStyle(k == .interrupt ? Theme.bad : Color.primary)
                    }
                    .buttonStyle(.plain)
                    .disabled(disabled)
                    .accessibilityLabel(k.label)
                    .accessibilityIdentifier("key-\(k.rawValue)")
                }
            }
            .padding(.horizontal, 12).padding(.vertical, 6)
        }
        .background(Theme.background)
    }
}
