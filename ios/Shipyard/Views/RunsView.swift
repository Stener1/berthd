import SwiftUI

struct RunsView: View {
    @Environment(BoxStore.self) private var boxes
    @Environment(BoardModel.self) private var board

    var body: some View {
        NavigationStack {
            List {
                ForEach(board.runs) { r in
                    NavigationLink(value: RunTarget(box: r.box, id: r.run.id)) {
                        RunRow(box: r.box, run: r.run)
                    }
                    .listRowBackground(Theme.card)
                }
                if board.runs.isEmpty && board.refreshedAt != nil {
                    ContentUnavailableView("No runs", systemImage: "point.3.connected.trianglepath.dotted", description: Text("Runs on your boxes show up here."))
                        .listRowBackground(Color.clear)
                }
            }
            .scrollContentBackground(.hidden)
            .background(Theme.background)
            .navigationTitle("Runs")
            .navigationDestination(for: RunTarget.self) { RunDetailView(box: $0.box, id: $0.id) }
            .refreshable { await board.refresh(boxes.boxes) }
        }
    }
}

struct RunTarget: Hashable {
    var box: Box
    var id: String
}

func runColor(_ status: String) -> Color {
    switch status {
    case "waiting_gate": Theme.waiting
    case "running", "queued": Theme.working
    case "succeeded": Theme.done
    case "failed", "cancelled", "interrupted": Theme.bad
    default: Theme.idle
    }
}

func runLabel(_ status: String) -> String {
    switch status {
    case "waiting_gate": "Needs you"
    case "running": "Running"
    case "queued": "Queued"
    case "succeeded": "Done"
    case "failed": "Failed"
    case "cancelled": "Cancelled"
    case "interrupted": "Interrupted"
    case "paused": "Paused"
    default: status.capitalized
    }
}

struct RunRow: View {
    var box: Box
    var run: RunSummary

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                Text(run.displayTitle).font(.headline).lineLimit(2)
                Spacer(minLength: 8)
                Text(relative(run.updated ?? run.created)).font(.caption.monospacedDigit()).foregroundStyle(Theme.muted)
            }
            HStack(spacing: 6) {
                StateBadge(text: runLabel(run.status), color: runColor(run.status))
                Text(run.gate.map { "Waiting: \($0.title)" } ?? box.name)
                    .font(.footnote).foregroundStyle(Theme.muted).lineLimit(1)
            }
        }
        .padding(.vertical, 4)
        .accessibilityElement(children: .combine)
    }
}

struct RunDetailView: View {
    @Environment(BoxStore.self) private var boxes
    var box: Box
    var id: String

    @State private var run: Run?
    @State private var error: String?
    @State private var confirmCancel = false
    @State private var working = false

    var body: some View {
        List {
            if let error {
                Section { Text(error).foregroundStyle(Theme.bad) }.listRowBackground(Theme.card)
            }
            if let run {
                Section {
                    HStack {
                        StateBadge(text: runLabel(run.status), color: runColor(run.status))
                        Spacer()
                        Text([run.template, box.name].joined(separator: " · ")).font(.footnote).foregroundStyle(Theme.muted)
                    }
                    if let e = run.error, !e.isEmpty { Text(e).font(.footnote).foregroundStyle(Theme.bad) }
                }
                .listRowBackground(Theme.card)
                if let gate = run.gate { GateSection(gate: gate, candidates: run.candidates ?? [], working: working) { d in await decide(d) } }
                Section("Steps") {
                    ForEach(Array(run.steps.flatMap { $0.flattened() }.enumerated()), id: \.offset) { _, row in
                        StepRow(step: row.step, depth: row.depth)
                    }
                }
                .listRowBackground(Theme.card)
                if !run.isFinished {
                    Section {
                        Button("Cancel run", role: .destructive) { confirmCancel = true }.disabled(working)
                    }
                    .listRowBackground(Theme.card)
                }
            } else if error == nil {
                ProgressView()
            }
        }
        .scrollContentBackground(.hidden)
        .background(Theme.background)
        .navigationTitle(run?.displayTitle ?? "Run")
        .navigationBarTitleDisplayMode(.inline)
        .confirmationDialog("Cancel this run?", isPresented: $confirmCancel, titleVisibility: .visible) {
            Button("Cancel run", role: .destructive) { Task { await cancel() } }
            Button("Keep running", role: .cancel) {}
        }
        .task {
            while !Task.isCancelled {
                await load()
                try? await Task.sleep(for: .seconds(3))
            }
        }
    }

    private func load() async {
        guard let client = boxes.client(for: box) else { error = PhoneAPIError.unauthorized.message; return }
        do {
            run = try await client.run(id: id)
            error = nil
        } catch let e as PhoneAPIError {
            error = e.message
        } catch {}
    }

    private func decide(_ d: GateDecision) async {
        guard let client = boxes.client(for: box) else { return }
        working = true
        defer { working = false }
        do {
            try await client.decide(run: id, decision: d)
            error = nil
        } catch let e as PhoneAPIError {
            error = e.message
        } catch {}
        await load()
    }

    private func cancel() async {
        guard let client = boxes.client(for: box) else { return }
        working = true
        defer { working = false }
        do { try await client.cancel(run: id); error = nil } catch let e as PhoneAPIError { error = e.message } catch {}
        await load()
    }
}

struct GateSection: View {
    var gate: RunGate
    var candidates: [Candidate]
    var working: Bool
    var decide: (GateDecision) async -> Void

    var body: some View {
        Section {
            Text(gate.title).font(.headline)
            if let t = gate.text, !t.isEmpty { Text(t).font(.system(.footnote, design: .monospaced)).foregroundStyle(Theme.muted) }
            if gate.pick == true {
                ForEach(candidates) { c in
                    Button { Task { await decide(GateDecision(approve: true, pick: c.index)) } } label: {
                        HStack {
                            Text("\(c.index + 1)").font(.headline.monospacedDigit())
                            VStack(alignment: .leading) {
                                Text(agentNames[c.agent ?? ""] ?? c.agent ?? "Attempt").font(.subheadline)
                                Text("+\(c.diff.added) −\(c.diff.removed) · check \(c.verify.passed ? "passed" : "failed")\(c.judge?.rank == 1 ? " · judge's pick" : "")")
                                    .font(.footnote).foregroundStyle(Theme.muted)
                            }
                            Spacer()
                            if c.index == gate.default { Image(systemName: "star.fill").foregroundStyle(Theme.waiting) }
                        }
                    }
                    .disabled(working)
                }
            }
            HStack {
                if gate.pick != true {
                    Button(gate.approveLabel ?? "Approve") { Task { await decide(GateDecision(approve: true)) } }
                        .buttonStyle(.borderedProminent).accessibilityIdentifier("gate-approve")
                }
                Button(gate.rejectLabel ?? "Reject", role: .destructive) { Task { await decide(GateDecision(approve: false)) } }
                    .buttonStyle(.bordered).accessibilityIdentifier("gate-reject")
            }
            .disabled(working)
        } header: { Text("Waiting for you") }
        .listRowBackground(Theme.card)
    }
}

struct StepRow: View {
    var step: StepRun
    var depth: Int

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Circle().fill(runColor(step.status == "waiting" ? "waiting_gate" : step.status)).frame(width: 8, height: 8).padding(.top, 6)
            VStack(alignment: .leading, spacing: 2) {
                Text(step.kind.isEmpty ? step.id : step.kind).font(.subheadline.weight(.medium))
                Text(step.status).font(.caption).foregroundStyle(Theme.muted)
                if let o = step.output ?? step.error, !o.isEmpty { Text(o).font(.footnote).foregroundStyle(Theme.muted).lineLimit(3) }
            }
        }
        .padding(.leading, CGFloat(depth) * 14)
        .accessibilityElement(children: .combine)
    }
}
