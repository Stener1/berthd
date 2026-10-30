import type { BerthEvent, ExecResult, Session } from "@/lib/api";
import type { ReviewItem } from "@/views/review/review-store";

// Review in mock mode: the finished and waiting agents in mock.ts's
// fixtures, each with work in its worktree. Approving, sending back and
// discarding change that work, so the inbox can be seen emptying.

type Emit = (e: Omit<BerthEvent, "time">) => void;

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();

const work: Record<string, Omit<ReviewItem, "session" | "agent" | "agent_state" | "state_since">> = {
  "devl|/home/me/work/shop-checkout-fix": {
    location: "shop",
    worktree: "checkout-fix",
    path: "/home/me/work/shop-checkout-fix",
    branch: "me/checkout-fix",
    head: "4f1c9e2",
    base: "origin/main",
    upstream: "origin/me/checkout-fix",
    ahead: 2,
    behind: 0,
    files: [
      { path: "apps/web/lib/payments/retry.ts", code: " M", added: 14, removed: 3 },
      { path: "apps/web/lib/payments/webhook.ts", code: "M ", added: 22, removed: 9 },
      { path: "apps/web/lib/payments/retry.test.ts", code: "??", added: 48, removed: 0 },
    ],
    added: 84,
    removed: 12,
    commits: [
      { sha: "4f1c9e2b7a1d0c3e5f6a7b8c9d0e1f2a3b4c5d6e", subject: "fix(payments): cap retries at 3 with backoff", author: "Claude", when: ago(31) },
      { sha: "9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b", subject: "refactor(payments): move charge errors into ChargeError", author: "Claude", when: ago(44) },
    ],
    base_ahead: 2,
    committed: [
      { path: "apps/web/lib/payments/charge.ts", from: "apps/web/lib/payments/charge-old.ts", code: "R ", added: 2, removed: 2 },
      { path: "packages/lib/backoff.ts", code: "A ", added: 41, removed: 0 },
    ],
    browser: { url: "http://checkout-fix.shop.devl.localhost:1377/cart", shots: ["shot-20261004-101500.000.png", "shot-20261004-101420.000.png"], errors: ["error: Warning: Each child in a list should have a unique \"key\" prop."] },
  },
  "devl|/home/me/work/shop-search-perf": {
    location: "shop",
    worktree: "search-perf",
    path: "/home/me/work/shop-search-perf",
    branch: "me/search-perf",
    head: "c0ffee1",
    base: "origin/main",
    ahead: 0,
    behind: 0,
    files: [
      { path: "apps/web/components/search/SearchResults.tsx", code: " M", added: 31, removed: 18 },
      { path: "apps/web/components/search/utils/memo.ts", code: "??", added: 26, removed: 0 },
    ],
    added: 57,
    removed: 18,
    commits: [{ sha: "c0ffee1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b", subject: "perf(search): memoise the product grid", author: "Claude", when: ago(25) }],
    base_ahead: 1,
    committed: [{ path: "apps/web/components/search/ProductGrid.tsx", code: "M ", added: 12, removed: 30 }],
  },
  // ?crowd=1's agents on build (mock-crowd.ts): work in two more projects,
  // so the inbox has more than one project to group.
  "build|/home/me/berth-perf-agent-slow": {
    location: "berth",
    worktree: "perf-agent-slow",
    path: "/home/me/berth-perf-agent-slow",
    branch: "me/perf-agent-slow",
    head: "a11ce01",
    base: "origin/main",
    ahead: 0,
    behind: 0,
    files: [
      { path: "internal/agent/startup.go", code: " M", added: 19, removed: 7 },
      { path: "internal/agent/startup_test.go", code: " M", added: 33, removed: 0 },
    ],
    added: 52,
    removed: 7,
    commits: [],
    base_ahead: 0,
    committed: [],
  },
  "build|/home/me/berth-preview-tabs": {
    location: "berth",
    worktree: "preview-tabs",
    path: "/home/me/berth-preview-tabs",
    branch: "me/preview-tabs",
    head: "b0a7d11",
    base: "origin/main",
    ahead: 0,
    behind: 0,
    files: [
      { path: "app/src/components/preview/frames.tsx", code: " M", added: 64, removed: 21 },
      { path: "app/src/components/preview/sizes.ts", code: "??", added: 18, removed: 0 },
    ],
    added: 82,
    removed: 21,
    commits: [],
    base_ahead: 0,
    committed: [],
  },
  "build|/home/me/infra-dns-cutover": {
    location: "infra",
    worktree: "dns-cutover",
    path: "/home/me/infra-dns-cutover",
    branch: "me/dns-cutover",
    head: "d15c0de",
    base: "origin/main",
    upstream: "origin/me/dns-cutover",
    ahead: 1,
    behind: 0,
    files: [{ path: "dns/records.tf", code: " M", added: 6, removed: 6 }],
    added: 6,
    removed: 6,
    commits: [{ sha: "d15c0de4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0", subject: "feat(dns): point acme.example at the new load balancer", author: "Claude", when: ago(40) }],
    base_ahead: 1,
    committed: [{ path: "lb/main.tf", code: "M ", added: 24, removed: 3 }],
  },
};

export const reviewScreens: Record<string, string> = {
  "search-perf-claude": `❯ Make search results render faster on big categories

● I profiled search on a 500-product category and the product grid
  re-rendered on every hover. I memoised the grid and moved the price
  formatting out of render.

  - ProductGrid.tsx: wrapped in memo, keys by product id instead of index
  - utils/memo.ts: a small cache for the currency formatting
  - SearchResults.tsx: passes stable callbacks

  Render time for a big category went from ~180ms to ~45ms. Type check and
  the search tests pass.

✻ Brewed for 4m 12s · done 11:42 AM

────────────────────────────────────────────────
❯
────────────────────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle)`,
  "checkout-fix-claude": `● Retries are capped at 3 with exponential backoff, and 429s now count as
  retryable. I added tests for the cap and the backoff timing.

  Should I also cap the total retry time? A slow provider outage would hold
  the order lock for the whole backoff.

❯ 1. Yes, cap it at 30s
  2. No, leave it
`,
  "perf-agent-slow-claude": `● The agent spent 2.1s of its 2.6s start reading every box's worktrees
  before it answered. It now answers from the last list it saved and reads
  the boxes in the background, so it is ready in about 400ms.

  - startup.go: serves the saved list first, then refreshes it
  - startup_test.go: covers a box that is offline at start

✻ Cooked for 6m 3s · done 10:58 AM
`,
  "dns-cutover-claude": `● The DNS records point at the new load balancer, with the TTL dropped to
  60s for the switch. \`terraform plan\` shows 6 records changing and nothing
  destroyed.
`,
};

function itemFor(box: string, s: Session): ReviewItem | undefined {
  const w = work[`${box}|${s.dir}`];
  if (!w || s.exited || !s.agent || (s.agent_state !== "finished" && s.agent_state !== "waiting")) return undefined;
  if (w.files.length === 0 && w.ahead === 0 && w.base_ahead === 0) return undefined;
  return { ...structuredClone(w), session: s.name, agent: s.agent, agent_state: s.agent_state, state_since: s.state_since };
}

export function reviewCall(box: string, method: string, path: string, sessions: Session[] | undefined): Promise<unknown> | undefined {
  const screen = /^sessions\/([^/]+)\/screen/.exec(path);
  if (method === "GET" && screen && reviewScreens[decodeURIComponent(screen[1])]) {
    return new Promise((r) => setTimeout(() => r({ screen: reviewScreens[decodeURIComponent(screen[1])] }), 120));
  }
  if (method !== "GET" || (path !== "review" && !path.startsWith("review?"))) return undefined;
  const items = (sessions ?? []).map((s) => itemFor(box, s)).filter((i): i is ReviewItem => !!i);
  items.sort((a, b) => (b.state_since ?? "").localeCompare(a.state_since ?? ""));
  return new Promise((r) => setTimeout(() => r(items), 180));
}

// reviewExec carries out the review actions' git commands on the fixtures.
export function reviewExec(box: string, location: string, command: string, emit: Emit): ExecResult | undefined {
  const w = Object.entries(work).find(([k, v]) => k.startsWith(`${box}|`) && (location === v.location || location === `${v.location}/${v.worktree}`))?.[1];
  if (!w) return undefined;
  const changed = () => setTimeout(() => emit({ type: "exec.finished", box, data: { location, path: w.path, command: command.slice(0, 40), exit_code: 0 } }), 30);
  if (command.startsWith("git restore --staged . && git checkout -- . && git clean -fd")) {
    w.files = [];
    w.added = w.removed = 0;
    changed();
    return { exit_code: 0, output: "" };
  }
  if (command.startsWith("git add -A && printf %s")) {
    const subject = (() => {
      try {
        const b64 = /printf %s '([^']*)' \| base64 -d \| git commit/.exec(command)?.[1] ?? "";
        return new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))).split("\n")[0];
      } catch {
        return "Commit";
      }
    })();
    w.committed = [...w.committed, ...w.files.map((f) => ({ ...f, code: f.code === "??" ? "A " : "M " }))];
    w.commits = [{ sha: Math.random().toString(16).slice(2).padEnd(40, "0"), subject, author: "you", when: new Date().toISOString() }, ...w.commits];
    w.base_ahead += 1;
    w.ahead += 1;
    w.head = w.commits[0].sha.slice(0, 7);
    w.files = [];
    w.added = w.removed = 0;
  }
  if (command.includes("git push")) {
    w.ahead = 0;
    w.upstream = `origin/${w.branch}`;
  }
  if (command.startsWith("git add -A") || command.includes("git push")) {
    changed();
    const out = [command.includes("git push") ? `To github.com:acme/shop.git\n   4f1c9e2..${w.head}  HEAD -> ${w.branch}` : "", command.includes("gh pr create") ? "https://github.com/acme/shop/pull/9077" : ""].filter(Boolean).join("\n");
    return { exit_code: 0, output: out };
  }
  return undefined;
}
