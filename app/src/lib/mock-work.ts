import { ApiError, type BerthEvent, type Session } from "@/lib/api";
import type { WorkPin, WorkRecord, WorkSession, WorkSessionLine } from "@/lib/needs-me";
import type { TranscriptItem } from "@/lib/transcript";

// A box's work records (/v1/work, internal/box/work.go), for the mock: one record
// per agent, all just seen, so nothing looks forgotten in the ordinary fixtures.
// ?scene=needs-me is a fleet with one of everything Needs me sorts, so a walk
// through it meets each pass in order.

type Ctx = {
  sessions: Record<string, Session[]>;
  emit(e: Omit<BerthEvent, "time"> & { time?: string }): void;
  delay<T>(v: T): Promise<T>;
};

const scene = () => new URLSearchParams(location.search).get("scene") === "needs-me";
const iso = (msAgo: number) => new Date(Date.now() - msAgo).toISOString();
const HOUR = 3_600_000;
const DAY = 24 * HOUR;

const records: Record<string, WorkRecord[]> = {};
const pins: Record<string, WorkPin[]> = {};
const transcripts: Record<string, TranscriptItem[]> = {};
let nextId = 1;
let refuseNext = false;

function line(name: string, seenHoursAgo: number, startedBy?: string): WorkSessionLine {
  return { name, last_seen: iso(seenHoursAgo * HOUR), started_by: startedBy, created: iso(9 * DAY) };
}

function record(id: string, title: string, sessions: WorkSessionLine[], over: Partial<WorkRecord> = {}): WorkRecord {
  const seen = Math.max(0, ...sessions.map((s) => Date.parse(s.last_seen ?? "") || 0));
  return { id, title, sessions, status: "open", created: iso(9 * DAY), updated: iso(HOUR), last_seen: new Date(seen || Date.now()).toISOString(), ...over };
}

function agent(name: string, title: string, location: string, over: Partial<WorkSession>): WorkSession {
  return { name, title, location, dir: `/home/me/work/${location.replace("/", "-")}`, command: "claude", created: iso(9 * DAY), attached: 0, exited: false, agent: "claude", agent_state: "waiting", state_since: iso(HOUR), ...over };
}

const said = (id: string, text: string): TranscriptItem[] => [
  { kind: "user", id: `${id}-u`, text: "Carry on with the next step." },
  { kind: "text", id: `${id}-t`, text },
];

// The form the pinned question asks, as Claude's AskUserQuestion reads.
const ROLLOUT_TOOL = "toolu_rollout";
const rolloutForm: TranscriptItem[] = [
  { kind: "user", id: "rollout-u", text: "Plan the checkout rollout for ACME-12." },
  {
    kind: "question",
    id: "rollout-q",
    tool: ROLLOUT_TOOL,
    questions: [
      {
        question: "How should acme roll out the new checkout (ACME-12)?",
        header: "Rollout",
        options: [
          { label: "Flag first", description: "Ship behind a flag, for staff, then everyone." },
          { label: "All at once", description: "Turn it on for everyone on release day." },
        ],
      },
    ],
  },
];

// needsMeScene puts one of everything on devl, in a known order.
export function needsMeScene(sessions: Record<string, Session[]>) {
  const devl: WorkSession[] = [
    agent("rollout-claude", "Choose the checkout rollout", "shop/rollout", {}),
    agent("pricing-claude", "Pick the pricing copy", "shop/pricing", { soft_wait: true }),
    agent("migrate-claude", "Approve the database migration", "shop/migrate", { ask: { tool: "Bash", input: "pnpm prisma migrate deploy", why: "The new column needs it", choices: [{ key: "1", label: "Yes" }, { key: "2", label: "Yes, and don't ask again" }, { key: "3", label: "No" }] } }),
    agent("release-lead-claude", "Coordinate the spring release", "shop/release", { agent_state: "running" }),
    agent("release-docs-claude", "Write the release notes", "shop/release-docs", { soft_wait: true, started_by: "release-lead-claude" }),
    agent("release-tests-claude", "Run the release tests", "shop/release-tests", { agent_state: "running", started_by: "release-lead-claude" }),
    agent("key-claude", "Add the staging API key", "evals/rotate", { agent_sub_state: "act", soft_wait: true }),
    agent("dns-claude", "Add the DNS record for acme.example", "shop/dns", { agent_sub_state: "act", soft_wait: true }),
    agent("webhooks-claude", "Retry the failed webhooks", "shop/webhooks", { agent_state: "finished" }),
    agent("spike-claude", "Spike the search index", "shop/spike", { agent_state: "idle", state_since: iso(5 * DAY) }),
    agent("badge-claude", "Fix the cart badge", "shop/badge", { agent_state: "idle", state_since: iso(2 * HOUR) }),
    agent("badge-tests-claude", "Test the cart badge", "shop/badge-tests", { agent_state: "idle", state_since: iso(2 * HOUR) }),
    // These never enter: a prompt is queued for one, and turn check read the other as parked.
    agent("queued-claude", "Wait for the queued prompt", "shop/queued", { agent_state: "finished", queued: 1 }),
    agent("parked-claude", "Check again in the morning", "shop/parked", { agent_state: "finished", parked: true }),
  ];
  sessions.devl = devl;
  sessions.gpu = [];
  records.devl = [
    record("w_rollout", "Choose the checkout rollout", [line("rollout-claude", 30)], { pinned: true }),
    record("w_pricing", "Pick the pricing copy", [line("pricing-claude", 20)]),
    record("w_migrate", "Approve the database migration", [line("migrate-claude", 10)]),
    record("w_release", "Ship the spring release", [line("release-lead-claude", 5), line("release-docs-claude", 5, "release-lead-claude"), line("release-tests-claude", 5, "release-lead-claude")]),
    record("w_key", "Add the staging API key", [line("key-claude", 15)]),
    record("w_dns", "Add the DNS record for acme.example", [line("dns-claude", 12)]),
    record("w_webhooks", "Retry the failed webhooks", [line("webhooks-claude", 3)]),
    record("w_spike", "Spike the search index", [line("spike-claude", 5 * 24)]),
    record("w_badge", "Fix the cart badge", [line("badge-claude", 1)], { suggested_with: ["w_badge_tests"] }),
    record("w_badge_tests", "Test the cart badge", [line("badge-tests-claude", 1)]),
    record("w_queued", "Wait for the queued prompt", [line("queued-claude", 40)]),
    record("w_parked", "Check again in the morning", [line("parked-claude", 40)]),
    record("w_legal", "Hear back from legal", [], { status: "waiting", waiting_on: "Legal at acme", check_back: iso(-2 * DAY), last_seen: iso(DAY) }),
  ];
  pins.devl = [
    { kind: "work", target: "w_rollout" },
    { kind: "repo", target: "evals" },
  ];
  transcripts["devl/rollout-claude"] = rolloutForm;
  transcripts["devl/pricing-claude"] = said("pricing", 'Should the pricing page say "from $9" or "$9 a month"? I would go with the second.');
  transcripts["devl/migrate-claude"] = said("migrate", "The migration adds one nullable column.");
  transcripts["devl/release-docs-claude"] = said("docs", "Which version should the release notes name, 2.4 or 3.0?");
  transcripts["devl/key-claude"] = said("key", "Add a staging key named ACME_STAGING_KEY, then tell me it's there.");
  transcripts["devl/dns-claude"] = said("dns", "Add a CNAME from shop.acme.example to edge.acme.example, then tell me it's done.");
  transcripts["devl/webhooks-claude"] = said("webhooks", "All 14 failed webhooks were retried, and every one went through. <script>alert(1)</script>");
  transcripts["devl/spike-claude"] = said("spike", "The search index spike is on its branch.");
}

function ensure(box: string, ctx: Ctx): WorkRecord[] {
  if (!records[box]) {
    // One record per agent, just seen.
    records[box] = (ctx.sessions[box] ?? []).filter((s) => s.agent && !s.service).map((s) => record(`w_${nextId++}`, s.title || s.name, [{ name: s.name, last_seen: iso(0), created: s.created }]));
    pins[box] ??= [];
  }
  return records[box];
}

// Ends a line whose session is gone, as a box's sweep does; a record stays.
function sweep(box: string, ctx: Ctx) {
  const live = new Set((ctx.sessions[box] ?? []).map((s) => s.name));
  for (const r of ensure(box, ctx)) for (const l of r.sessions) if (!live.has(l.name) && !l.ended) l.ended = new Date().toISOString();
}

function view(box: string, r: WorkRecord): WorkRecord {
  return structuredClone({ ...r, pinned: !!pins[box]?.some((p) => p.kind === "work" && p.target === r.id) });
}

// mockWorkCall answers /v1/work, and in the scene its agents' records and answers.
export function mockWorkCall(box: string, method: string, path: string, body: unknown, ctx: Ctx): Promise<unknown> | undefined {
  const bare = path.split("?")[0];
  const query = new URLSearchParams(path.split("?")[1] ?? "");
  const notify = (type: string, data: Record<string, unknown>) => setTimeout(() => ctx.emit({ type, box, data }), 30);

  if (scene()) {
    const sm = /^sessions\/([^/]+)\/(transcript|answer|send)$/.exec(bare);
    const key = sm ? `${box}/${decodeURIComponent(sm[1])}` : "";
    const session = sm ? (ctx.sessions[box] as WorkSession[] | undefined)?.find((s) => s.name === decodeURIComponent(sm[1])) : undefined;
    if (sm && session) {
      if (sm[2] === "transcript" && method === "GET") {
        const items = transcripts[key] ?? [];
        return ctx.delay({ source: "claude", items, next: items.length, gen: "1.0", start: 0, crew: [] });
      }
      if (sm[2] === "answer" && method === "POST") {
        const req = body as { tool: string; answers: { picks: string[]; other?: string }[] };
        const q = transcripts[key]?.find((it) => it.kind === "question" && it.tool === req.tool);
        if (!q || q.kind !== "question") return Promise.reject(new ApiError("no question waits for that answer", 409));
        const answered = req.answers.map((a) => [...a.picks, ...(a.other ? [a.other] : [])].join(", "));
        Object.assign(q, { done: true, answers: answered });
        mockWorkSpy.sent[key] = answered.join(" | ");
        session.agent_state = "running";
        session.state_since = new Date().toISOString();
        notify("agent.started", { session: session.name, path: session.dir });
        return ctx.delay({ answered });
      }
      if (sm[2] === "send" && method === "POST") {
        const req = body as { text?: string };
        mockWorkSpy.sent[key] = req.text ?? "";
        session.agent_state = "running";
        session.soft_wait = undefined;
        session.ask = undefined;
        session.state_since = new Date().toISOString();
        notify("agent.started", { session: session.name, path: session.dir });
        return ctx.delay({ sent: true, turn: `${session.name}#2`, at: new Date().toISOString() });
      }
    }
  }

  if (bare !== "work" && !bare.startsWith("work/")) return undefined;
  const list = ensure(box, ctx);
  sweep(box, ctx);
  const here = (pins[box] ??= []);

  if (bare === "work/pins") {
    if (method === "GET") return ctx.delay(structuredClone(here));
    if (method === "POST") {
      const { kind, target } = body as WorkPin;
      if (!here.some((p) => p.kind === kind && p.target === target)) here.push({ kind, target, at: new Date().toISOString() });
      notify("work.pinned", { kind, target });
      return ctx.delay({ kind, target });
    }
    if (method === "DELETE") {
      pins[box] = here.filter((p) => !(p.kind === query.get("kind") && p.target === query.get("target")));
      notify("work.unpinned", { kind: query.get("kind"), target: query.get("target") });
      return ctx.delay({ removed: true });
    }
  }
  if (bare === "work" && method === "GET") {
    const status = query.get("status");
    return ctx.delay(list.filter((r) => (status ? r.status === status : query.get("all") === "1" || r.status !== "closed")).map((r) => view(box, r)));
  }

  const m = /^work\/([^/]+)(?:\/(group|ungroup|accept|reject|seen))?$/.exec(bare);
  if (!m) return undefined;
  const r = list.find((x) => x.id === decodeURIComponent(m[1]));
  if (!r) return Promise.reject(new ApiError("no work record with that id", 404, "not_found"));
  if (refuseNext && method !== "GET") {
    refuseNext = false;
    return Promise.reject(new ApiError("the box refused that", 409));
  }
  const stamp = () => {
    r.updated = new Date().toISOString();
    notify("work.updated", { id: r.id, status: r.status });
  };

  // Moves other records' sessions into this one.
  const absorb = (ids: string[]) => {
    for (const id of ids) {
      const other = list.find((x) => x.id === id);
      if (!other || other === r) continue;
      r.sessions.push(...other.sessions);
      records[box] = records[box].filter((x) => x !== other);
    }
  };

  if (!m[2] && method === "GET") return ctx.delay(view(box, r));
  if (!m[2] && method === "PATCH") {
    const p = body as { status?: WorkRecord["status"]; not_until?: string; waiting_on?: string; check_back?: string };
    if (p.status) {
      r.status = p.status;
      if (p.status === "open") {
        r.not_until = r.waiting_on = r.check_back = undefined;
      }
      if (p.status === "closed") pins[box] = here.filter((x) => !(x.kind === "work" && x.target === r.id));
    }
    if (p.not_until !== undefined) r.not_until = p.not_until || undefined;
    if (p.waiting_on !== undefined) r.waiting_on = p.waiting_on || undefined;
    if (p.check_back !== undefined) r.check_back = p.check_back || undefined;
    stamp();
    return ctx.delay(view(box, r));
  }
  if (m[2] === "seen" && method === "POST") {
    const at = new Date().toISOString();
    r.last_seen = at;
    for (const l of r.sessions) if (!l.ended) l.last_seen = at;
    notify("work.seen", { id: r.id, source: "visit" });
    return ctx.delay(view(box, r));
  }
  if (m[2] === "accept" && method === "POST") {
    absorb(r.suggested_with ?? []);
    r.suggested_with = undefined;
    stamp();
    return ctx.delay(view(box, r));
  }
  if (m[2] === "reject" && method === "POST") {
    r.suggested_with = undefined;
    stamp();
    return ctx.delay(view(box, r));
  }
  if (m[2] === "group" && method === "POST") {
    absorb((body as { with?: string[] }).with ?? []);
    stamp();
    return ctx.delay(view(box, r));
  }
  if (m[2] === "ungroup" && method === "POST") {
    const names = new Set((body as { sessions?: string[] }).sessions ?? []);
    const out = r.sessions.filter((l) => names.has(l.name));
    r.sessions = r.sessions.filter((l) => !names.has(l.name));
    const made = out.map((l) => record(`w_${nextId++}`, l.title || l.name, [l]));
    records[box].push(...made);
    stamp();
    return ctx.delay([view(box, r), ...made.map((x) => view(box, x))]);
  }
  return undefined;
}

// What the agents got, and the moves a box or the clock would make, for tests.
const mockWorkSpy = {
  sent: {} as Record<string, string>,
  record: (id: string, box = "devl") => {
    const r = records[box]?.find((x) => x.id === id);
    return r ? view(box, r) : undefined;
  },
};

export function installMockWorkControls(ctx: Ctx) {
  (window as unknown as { __berthMockWork: unknown }).__berthMockWork = {
    ...mockWorkSpy,
    // A new pinned question arrives.
    arrive(name: string, title: string) {
      const box = "devl";
      (ctx.sessions[box] ??= []).push(agent(name, title, "shop/arrived", { soft_wait: true }));
      records[box].push(record(`w_${name}`, title, [line(name, 1)]));
      (pins[box] ??= []).push({ kind: "work", target: `w_${name}` });
      transcripts[`${box}/${name}`] = said(name, "This one can't wait: should the hotfix go out tonight?");
      ctx.emit({ type: "agent.waiting", box, data: { session: name } });
      ctx.emit({ type: "work.created", box, data: { id: `w_${name}` } });
    },
    // The box refuses the next change to a record.
    refuse() {
      refuseNext = true;
    },
    // A session reaches another wait, and the app hasn't heard yet.
    moveOn(name: string, box = "devl") {
      const s = ctx.sessions[box]?.find((x) => x.name === name);
      if (s) s.state_since = new Date().toISOString();
    },
    // Everything that needs a person is dealt with; what's put off stays.
    clear() {
      const box = "devl";
      ctx.sessions[box] = [];
      records[box] = records[box].filter((r) => r.status !== "open");
      ctx.emit({ type: "work.updated", box, data: {} });
      ctx.emit({ type: "session.stopped", box, data: {} });
    },
    // Its Not now or Waiting on date passes.
    pass(id: string, box = "devl") {
      const r = records[box]?.find((x) => x.id === id);
      if (!r) return;
      if (r.not_until) r.not_until = iso(60_000);
      if (r.check_back) r.check_back = iso(60_000);
      ctx.emit({ type: "work.updated", box, data: { id } });
    },
  };
}
