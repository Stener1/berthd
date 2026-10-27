import type { LinearIssue, LinearIssueDetail, LinearStatus, LinearTeam } from "@berth/plugin";

import { ApiError } from "@/lib/api";

// The demo's Linear: an invented workspace, Acme, with two teams. It starts
// connected, so the Issues screen has something to show; ?linear=off (or a
// new account, ?fresh=1) starts without a key, and any key connects it
// except one with "bad" in it, which Linear "refuses". Comments posted here
// only ever land in this file's memory.

const params = new URLSearchParams(location.search);
let connected = !params.has("fresh") && params.get("linear") !== "off";

const FILE = "/Users/robin/Library/Application Support/berth/client/linear.json";
const STATUS: LinearStatus = { connected: true, file: FILE, viewer: { id: "u-me", name: "Robin Vance", email: "robin@acme.test" }, workspace: { name: "Acme", url_key: "acme" } };

const WEB: LinearTeam = { id: "t-web", key: "WEB", name: "Web" };
const PLA: LinearTeam = { id: "t-pla", key: "PLA", name: "Platform" };
const TEAMS = [PLA, WEB];

const STATES = {
  triage: { name: "Triage", type: "triage", color: "#fc7840" },
  backlog: { name: "Backlog", type: "backlog", color: "#bec2c8" },
  todo: { name: "Todo", type: "unstarted", color: "#e2e2e2" },
  progress: { name: "In Progress", type: "started", color: "#f2c94c" },
  review: { name: "In Review", type: "started", color: "#0f783c" },
};

const LABELS = {
  bug: { name: "Bug", color: "#eb5757" },
  feature: { name: "Feature", color: "#bb87fc" },
  checkout: { name: "Checkout", color: "#26b5ce" },
  perf: { name: "Performance", color: "#f2994a" },
  a11y: { name: "Accessibility", color: "#4ea7fc" },
  infra: { name: "Infra", color: "#5e6ad2" },
};

const PRIORITY = ["No priority", "Urgent", "High", "Medium", "Low"];

const ago = (hours: number) => new Date(Date.now() - hours * 3600_000).toISOString();
const slug = (s: string) =>
  s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "")
    .slice(0, 40)
    .replace(/-$/, "");

interface Fixture {
  n: number;
  team: LinearTeam;
  title: string;
  state: keyof typeof STATES;
  priority: number;
  labels: (keyof typeof LABELS)[];
  assignee?: { id: string; name: string };
  creator: string;
  days: number;
  updated: number;
  description: string;
  comments?: { author: string; hours: number; body: string }[];
  links?: { title: string; url: string; source?: string }[];
}

const ME = { id: "u-me", name: "Robin Vance" };
const DANA = { id: "u-dana", name: "Dana Reyes" };
const SAM = { id: "u-sam", name: "Sam Ortiz" };

const FIXTURES: Fixture[] = [
  {
    n: 412,
    team: WEB,
    title: "Retrying a failed payment charges the card twice",
    state: "progress",
    priority: 1,
    labels: ["bug", "checkout"],
    assignee: ME,
    creator: "Dana Reyes",
    days: 3,
    updated: 2,
    description:
      "A customer whose first payment attempt times out and who presses **Pay** again is charged twice.\n\n## Steps\n\n1. Add anything to the cart and go to checkout.\n2. Throttle the network so the first `POST /api/checkout` times out.\n3. Press **Pay** again.\n\n## Expected\n\nOne charge. The retry should reuse the first attempt's idempotency key.\n\n## Seen\n\nTwo charges, 4 to 9 seconds apart, in 14 orders last week.",
    comments: [
      { author: "Sam Ortiz", hours: 40, body: "The key is made in the click handler, so every press makes a new one. It should be made once per cart." },
      { author: "Dana Reyes", hours: 6, body: "Support has refunded the 14. Can we get a test that presses Pay twice?" },
    ],
    links: [{ title: "acme/shop#18231 Reuse the idempotency key across retries", url: "https://github.com/acme/shop/pull/18231", source: "github" }],
  },
  {
    n: 409,
    team: WEB,
    title: "Search results jump when the filters load",
    state: "todo",
    priority: 2,
    labels: ["bug", "perf"],
    assignee: ME,
    creator: "Sam Ortiz",
    days: 6,
    updated: 20,
    description: "The filter rail arrives after the first results and pushes them down by about 180px. Reserve its height, or render it with the results.\n\nLayout shift on `/search` is 0.31 at the 75th percentile.",
    comments: [{ author: "Dana Reyes", hours: 30, body: "Same on the category pages, which share the rail." }],
  },
  {
    n: 405,
    team: WEB,
    title: "Let people save a cart for later",
    state: "backlog",
    priority: 3,
    labels: ["feature", "checkout"],
    creator: "Robin Vance",
    days: 12,
    updated: 70,
    description: "Signed-in customers can move a line from the cart to a **Saved for later** list under it, and back.\n\n- [ ] Saved lines keep their options (size, colour).\n- [ ] A saved line that goes out of stock says so.\n- [ ] Nothing saved counts toward the cart total.",
  },
  {
    n: 398,
    team: WEB,
    title: "The colour picker can't be reached with the keyboard",
    state: "todo",
    priority: 2,
    labels: ["bug", "a11y"],
    assignee: DANA,
    creator: "Dana Reyes",
    days: 15,
    updated: 96,
    description: "On a product page, Tab skips the colour swatches: they are `div`s with a click handler. They should be a radio group, with the arrows moving between colours and each swatch named by its colour.",
  },
  {
    n: 391,
    team: WEB,
    title: "Order confirmation email shows prices without tax",
    state: "triage",
    priority: 0,
    labels: ["bug"],
    creator: "Sam Ortiz",
    days: 1,
    updated: 5,
    description: "The email's line prices are net while the total is gross, so the lines don't add up to the total. The order page gets it right.",
  },
  {
    n: 207,
    team: PLA,
    title: "Preview deployments take nine minutes to build",
    state: "progress",
    priority: 2,
    labels: ["perf", "infra"],
    assignee: SAM,
    creator: "Sam Ortiz",
    days: 8,
    updated: 12,
    description: "Most of it is installing dependencies with a cold cache. Cache the package store between builds, keyed by the lockfile.",
    comments: [{ author: "Robin Vance", hours: 14, body: "The image step is another two minutes: it rebuilds every layer when only the app changed." }],
  },
  {
    n: 203,
    team: PLA,
    title: "Rotate the webhook signing secret without downtime",
    state: "todo",
    priority: 3,
    labels: ["infra"],
    assignee: ME,
    creator: "Dana Reyes",
    days: 20,
    updated: 130,
    description: "Accept two secrets at once for a day: the old one and the new. Verify against both, sign with the new.",
  },
  {
    n: 198,
    team: PLA,
    title: "Nightly database backup has no restore test",
    state: "review",
    priority: 2,
    labels: ["infra"],
    assignee: DANA,
    creator: "Robin Vance",
    days: 25,
    updated: 48,
    description: "We have never restored one. Restore last night's backup into a scratch database each morning and compare row counts for the ten largest tables.",
  },
];

const comments = new Map<string, { author?: string; body: string; created_at: string }[]>();

function issueOf(f: Fixture): LinearIssue {
  const identifier = `${f.team.key}-${f.n}`;
  return {
    id: `id-${identifier.toLowerCase()}`,
    identifier,
    title: f.title,
    url: `https://linear.app/acme/issue/${identifier}/${slug(f.title)}`,
    priority: f.priority,
    priority_label: PRIORITY[f.priority],
    state: STATES[f.state],
    team: f.team,
    assignee: f.assignee,
    creator: f.creator,
    labels: f.labels.map((l) => LABELS[l]),
    branch: `robin/${identifier.toLowerCase()}-${slug(f.title)}`,
    created_at: ago(f.days * 24),
    updated_at: ago(f.updated),
  };
}

function detailOf(f: Fixture): LinearIssueDetail {
  const issue = issueOf(f);
  return {
    ...issue,
    description: f.description,
    comments: [...(f.comments ?? []).map((c) => ({ author: c.author, body: c.body, created_at: ago(c.hours) })), ...(comments.get(issue.id) ?? [])],
    links: f.links ?? [],
  };
}

const find = (id: string) => FIXTURES.find((f) => `${f.team.key}-${f.n}`.toLowerCase() === id.toLowerCase() || `id-${f.team.key}-${f.n}`.toLowerCase() === id.toLowerCase());

export function linearCall(method: string, path: string, body: unknown, delay: <T>(v: T) => Promise<T>): Promise<unknown> | undefined {
  const [route, query = ""] = path.split("?");
  if (route !== "/v1/linear" && !route.startsWith("/v1/linear/")) return undefined;
  if (route === "/v1/linear") {
    if (method === "GET") return delay(connected ? STATUS : { connected: false, file: FILE });
    if (method === "DELETE") {
      connected = false;
      return delay({ connected: false, file: FILE });
    }
    if (method === "PUT") {
      const key = String((body as { api_key?: string }).api_key ?? "").trim();
      if (!key || /\s/.test(key)) return Promise.reject(new ApiError("paste a Linear API key: it starts with lin_api_", 400));
      return new Promise((resolve, reject) =>
        setTimeout(() => {
          if (key.includes("bad")) return reject(new ApiError("Linear didn't accept the API key. Make a new one in Linear and paste it in Settings.", 401, "linear_auth"));
          connected = true;
          resolve(STATUS);
        }, 500),
      );
    }
  }
  if (!connected) return Promise.reject(new ApiError("Linear isn't connected. Add an API key in Settings.", 409, "linear_off"));
  if (method === "GET" && route === "/v1/linear/teams") return delay({ teams: TEAMS });
  if (method === "GET" && route === "/v1/linear/issues") {
    const q = new URLSearchParams(query);
    const team = q.get("team");
    const mine = q.get("mine") === "1";
    const issues = FIXTURES.filter((f) => (!team || f.team.key === team) && (!mine || f.assignee?.id === ME.id))
      .map(issueOf)
      .sort((a, b) => b.updated_at.localeCompare(a.updated_at));
    return delay({ issues });
  }
  const one = /^\/v1\/linear\/issues\/([^/]+)(\/comments)?$/.exec(route);
  if (one) {
    const f = find(decodeURIComponent(one[1]));
    if (!f) return Promise.reject(new ApiError(`Linear has no issue ${decodeURIComponent(one[1])}`, 502));
    if (method === "GET" && !one[2]) return delay(detailOf(f));
    if (method === "POST" && one[2]) {
      const id = issueOf(f).id;
      comments.set(id, [...(comments.get(id) ?? []), { author: ME.name, body: String((body as { body?: string }).body ?? ""), created_at: new Date().toISOString() }]);
      return delay({ ok: true });
    }
  }
  return undefined;
}
