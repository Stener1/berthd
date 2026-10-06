import type { BerthEvent, Location, Session, Status, TerminalConnection, TerminalHandlers } from "@/lib/api";
import { ApiError } from "@/lib/api";
import initCal from "@/lib/fixtures/team-calcom/init-cal.sh.txt?raw";
import privateApiKit from "@/lib/fixtures/team-calcom/kit.json.txt?raw";
import readme from "@/lib/fixtures/team-calcom/README.md.txt?raw";
import setupSh from "@/lib/fixtures/team-calcom/setup.sh.txt?raw";
import type { Accepted, GitHubState, OrgRepo, PlanStep, ProjectView, SetupRequest, TeamSetup, TeamSource, TeamStatus, TeamUpdate, TeamView } from "@/lib/team";

// Mock mode's Team setup, behaving like the laptop agent (internal/agent
// team.go, reading <org>/.berth with gh) and the box's runner (internal/box
// team.go) closely enough to build the page on. Synthetic fixtures only.
//
// ?team= picks a scenario:
//   calcom (default)  the Cal.com setup, gh signed in, every repo readable
//   nogh              this computer has no GitHub CLI
//   signedout         gh is there, not signed in: Connect GitHub signs in
//   partial           calcom/private-api can't be read
//   unreadable        calcom/.berth can't be read (as from a link)
//   fail              the run stops at Postgres (port taken); Retry passes
//   update            set up at 4e1c9a2 on sean-dev; 9d03f17 is newer
//   done              set up already
// A link (?team-page= or ?team-link-src=) to
// github.com/sean-brydon/berth-kit-calcom/tree/team-setup/team is the same
// Cal.com setup, read from a user's repo on a branch; any other link can't
// be read.
// &teamhold=github,repos keeps the run at those points until
// window.__teamMock.advance(name), for screenshots; the sudo prompt always
// waits for a password typed in its terminal (or advance("sudo")).

type Emit = (e: Omit<BerthEvent, "time">) => void;
type Delay = <T>(v: T) => Promise<T>;
export interface TeamMockCtx {
  status: Status;
  locations: Record<string, Location[]>;
  sessions: Record<string, Session[]>;
  emit: Emit;
  delay: Delay;
  addBox(name: string, address: string): void;
}

const params = new URLSearchParams(location.search);
export const teamScenario = params.get("team") ?? "calcom";
const holds = new Set((params.get("teamhold") ?? "").split(",").filter(Boolean));

const now = Date.now();
const ago = (min: number) => new Date(now - min * 60_000).toISOString();

// The org's avatar and the person's, drawn here: nothing is fetched.
const svg = (body: string) => `data:image/svg+xml;utf8,${encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">${body}</svg>`)}`;
const CAL_AVATAR = svg(`<rect width="64" height="64" rx="14" fill="#111"/><text x="32" y="40" font-family="Inter,Helvetica,Arial,sans-serif" font-size="21" font-weight="700" fill="#fff" text-anchor="middle" letter-spacing="-1">Cal</text>`);
const NW_AVATAR = svg(`<rect width="64" height="64" rx="14" fill="#047857"/><text x="32" y="42" font-family="Inter,Helvetica,Arial,sans-serif" font-size="28" font-weight="700" fill="#fff" text-anchor="middle">N</text>`);
const ME_AVATAR = svg(`<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#38bdf8"/><stop offset="1" stop-color="#6366f1"/></linearGradient></defs><circle cx="32" cy="32" r="32" fill="url(#g)"/><text x="32" y="42" font-family="Inter,Helvetica,Arial,sans-serif" font-size="28" font-weight="600" fill="#fff" text-anchor="middle">S</text>`);
const AUTHOR_AVATAR = svg(`<circle cx="32" cy="32" r="32" fill="#f59e0b"/><text x="32" y="42" font-family="Inter,Helvetica,Arial,sans-serif" font-size="28" font-weight="600" fill="#fff" text-anchor="middle">K</text>`);

export const TEAM_BOX = "sean-dev";

const COMMIT = "4e1c9a2b7d0f3e58a1c64d2b9f7e0a35c8d1b246";
const NEXT = "9d03f17c2a4b6e8d0f1a3c5e7b9d2f4a6c8e0b13";

const calSetup: TeamSetup = {
  schema: "berth.team/v1",
  id: "calcom",
  name: "Cal.com",
  org: "calcom",
  description: "Everything a Cal.com engineer needs on their box: Node, Yarn, Postgres and Redis in Docker, and the Cal.com repos, each set up.",
  contact: "#eng-onboarding on Slack",
  docs: "https://github.com/calcom/.berth#readme",
  box: {
    os: ["ubuntu>=22.04", "debian>=12", "macos>=14"],
    script: "box/setup.sh",
    steps: [
      { id: "packages", title: "System packages", detail: "git, curl, build tools, python3, postgresql-client, jq", sudo: true },
      { id: "docker", title: "Docker", detail: "Docker Engine, and you in the docker group", sudo: true },
      { id: "cli", title: "GitHub CLI and 1Password CLI", detail: "gh for the sign-in and clones, op for secrets", sudo: true },
      { id: "node", title: "Node 20 with fnm", detail: "in your home folder, loaded from ~/.profile" },
      { id: "yarn", title: "Yarn with corepack", detail: "each repo's packageManager picks the version" },
      { id: "postgres", title: "Postgres 16 in Docker", detail: "cal-postgres on localhost:5450" },
      { id: "redis", title: "Redis 7 in Docker", detail: "cal-redis on localhost:6379" },
    ],
  },
  projects: [
    { id: "cal.com", repo: "calcom/cal.com", path: "~/code/cal.com", required: true, kit: "https://github.com/sean-brydon/berth-kit-calcom@7f93797", init: "box/init-cal.sh", init_detail: ".env from .env.example pointed at localhost:5450, yarn install, prisma migrate deploy, seed-basic", first_task: "Find an open issue labelled “good first issue” in calcom/cal.com, explain how you'd fix it, and make the change in a new worktree" },
    { id: "private-api", repo: "calcom/private-api", path: "~/code/private-api", required: false, kit: "./projects/private-api" },
    { id: "website", repo: "calcom/website", path: "~/code/website", required: false },
  ],
  keys: {
    "cal.com": {
      from: ".env.example",
      shared: {
        STRIPE_PRIVATE_KEY: "op://Engineering/Cal.com Stripe test/secret key",
        NEXT_PUBLIC_STRIPE_PUBLIC_KEY: "op://Engineering/Cal.com Stripe test/publishable key",
        STRIPE_WEBHOOK_SECRET: "op://Engineering/Cal.com Stripe test/webhook secret",
        DAILY_API_KEY: "op://Engineering/Daily dev/credential",
      },
      ask: ["SENDGRID_API_KEY"],
    },
    "private-api": { from: ".env.example", shared: { CAL_API_KEY: "op://Engineering/Cal.com API dev/credential" } },
  },
  updates: { notify: true },
};

// What each step runs, as the plan opens it: the script's own lines.
const STEP_COMMANDS: Record<string, string[]> = {
  packages: ["box/setup.sh packages", "# checks each package with dpkg -s first", "sudo apt-get update -qq", "sudo apt-get install -y git curl ca-certificates gnupg jq unzip build-essential python3 postgresql-client"],
  docker: ["box/setup.sh docker", "# Docker's own apt repository, not the distro's docker.io", "sudo install -m 0755 -d /etc/apt/keyrings", "curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg", "sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin", "sudo usermod -aG docker $USER"],
  cli: ["box/setup.sh cli", "sudo apt-get install -y gh 1password-cli", "# from cli.github.com and downloads.1password.com, keys checked"],
  node: ["box/setup.sh node", "curl -fsSL https://fnm.vercel.app/install | bash -s -- --skip-shell", "fnm install 20 && fnm default 20", "# fnm is loaded from ~/.profile, so login shells find node"],
  yarn: ["box/setup.sh yarn", "corepack enable && corepack prepare yarn@stable --activate"],
  postgres: ["box/setup.sh postgres", "docker run -d --name cal-postgres --restart unless-stopped \\", "  -p 127.0.0.1:5450:5432 -e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_DB=calendso \\", "  -v cal-postgres-data:/var/lib/postgresql/data postgres:16"],
  redis: ["box/setup.sh redis", "docker run -d --name cal-redis --restart unless-stopped -p 127.0.0.1:6379:6379 -v cal-redis-data:/data redis:7"],
};

const GITHUB_STEP: PlanStep = {
  id: "github",
  title: "GitHub on the box",
  detail: "its own sign-in, which you can revoke on GitHub",
  sudo: false,
  berth: true,
  commands: ["gh auth status || gh auth login --hostname github.com --git-protocol https --web", "gh auth setup-git", "# a device code: you enter it at github.com/login/device; this computer's sign-in is never copied"],
};

function planSteps(setup: TeamSetup): PlanStep[] {
  return [...(setup.box?.steps ?? []).map((s) => ({ id: s.id, title: s.title, detail: s.detail, sudo: !!s.sudo, commands: STEP_COMMANDS[s.id] ?? [`box/setup.sh ${s.id}`] })), GITHUB_STEP];
}

function calProjects(): ProjectView[] {
  return [
    {
      id: "cal.com",
      repo: "calcom/cal.com",
      path: "~/code/cal.com",
      required: true,
      access: true,
      private: false,
      size_kb: 2_200_000,
      description: "Scheduling infrastructure for absolutely everyone.",
      default_branch: "main",
      source: "kit",
      kit: { ref: "https://github.com/sean-brydon/berth-kit-calcom@7f93797", commit: "7f93797", id: "cal-com", name: "Cal.com", hash: "a91c03e5b27d" },
      services: ["Web", "Prisma Studio"],
      init: "box/init-cal.sh",
      init_detail: ".env from .env.example pointed at localhost:5450, yarn install, prisma migrate deploy, seed-basic",
      first_task: calSetup.projects![0].first_task,
      commands: ["git clone https://github.com/calcom/cal.com ~/code/cal.com", "# then the cal-com kit (sean-brydon/berth-kit-calcom @ 7f93797)", "box/init-cal.sh   # .env, yarn install, migrate, seed"],
    },
    {
      id: "private-api",
      repo: "calcom/private-api",
      path: "~/code/private-api",
      required: false,
      access: teamScenario !== "partial",
      private: true,
      size_kb: 184_000,
      description: "The private API",
      default_branch: "main",
      source: "repo",
      config_hash: "3b8f0c61d2e94a7b5c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b",
      services: ["Private API"],
      commands: ["git clone https://github.com/calcom/private-api ~/code/private-api", "# its own .berth/config.json, trusted as reviewed (3b8f0c6)", "[ -f .env ] || cp .env.example .env; yarn install --frozen-lockfile"],
    },
    {
      id: "website",
      repo: "calcom/website",
      path: "~/code/website",
      required: false,
      access: true,
      private: false,
      size_kb: 412_000,
      description: "cal.com, the website",
      default_branch: "main",
      source: "repo",
      config_hash: "c0ffee12d2e94a7b5c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b",
      services: ["Next.js"],
      commands: ["git clone https://github.com/calcom/website ~/code/website", "# its own .berth/config.json, trusted as reviewed (c0ffee1)", "pnpm install"],
    },
  ];
}

// A setup read from a link: a user's repo, a branch, a folder in it.
export const TEAM_LINK = "github.com/sean-brydon/berth-kit-calcom/tree/team-setup/team";
const SEAN_AVATAR = svg(`<rect width="64" height="64" rx="32" fill="#7c3aed"/><text x="32" y="42" font-family="Inter,Helvetica,Arial,sans-serif" font-size="28" font-weight="600" fill="#fff" text-anchor="middle">S</text>`);
const LINK_COMMIT = "b81d0e4a6c2f9e1d3b5a7c9e0f2a4b6c8d0e1f23";
const orgSource: TeamSource = { kind: "org", repo: "calcom/.berth", html_url: "https://github.com/calcom/.berth", label: "calcom/.berth", key: "calcom" };
const linkSource: TeamSource = {
  kind: "link",
  repo: "sean-brydon/berth-kit-calcom",
  ref: "team-setup",
  ref_kind: "branch",
  path: "team",
  html_url: "https://github.com/sean-brydon/berth-kit-calcom/tree/team-setup/team",
  label: "sean-brydon/berth-kit-calcom · team-setup branch · team/",
  key: TEAM_LINK,
};
const isMockLink = (key: string) => key.toLowerCase().startsWith("github.com/");
// The run id for a key: a link's setup is Cal.com's.
const idOf = (key: string) => (isMockLink(key) ? "calcom" : key.toLowerCase());

const calOrg = { login: "calcom", name: "Cal.com", avatar_url: CAL_AVATAR, verified: true, type: "Organization" as const, html_url: "https://github.com/calcom" };

function calFiles() {
  const team = JSON.stringify(calSetup, null, 2);
  const f = (path: string, text: string) => ({ path, size: text.length, text });
  return [f("team.json", team), f("box/setup.sh", setupSh), f("box/init-cal.sh", initCal), f("projects/private-api/kit.json", privateApiKit), f("README.md", readme)];
}

const UPDATE: TeamUpdate = {
  from: COMMIT.slice(0, 7),
  to: NEXT.slice(0, 7),
  author: "keithwillcode",
  date: ago(180),
  commits: 3,
  changes: [
    { kind: "add", area: "project", id: "cal-video", text: "calcom/cal-video", detail: "a new repo, with its own .berth/config.json · Video dev server" },
    { kind: "add", area: "step", id: "playwright", text: "Playwright's system libraries", detail: "npx playwright install-deps chromium", sudo: true },
    { kind: "change", area: "step", id: "node", text: "Node 20 → 22", detail: "fnm install 22 && fnm default 22 · no password needed" },
    { kind: "change", area: "project", id: "cal.com", text: "cal-com kit 7f93797 → b81d0e4", detail: "Prisma Studio starts with the worktree" },
    { kind: "remove", area: "key", id: "cal.com/DAILY_API_KEY", text: "DAILY_API_KEY", detail: "no longer read from 1Password" },
  ],
  sudo: ["Playwright's system libraries"],
};

// ——— state ———

let ghLoginAt = 0;
const accepted: Accepted[] = [];
if (teamScenario === "update" || teamScenario === "done") accepted.push({ org: "calcom", id: "calcom", name: "Cal.com", commit: COMMIT.slice(0, 7), box: TEAM_BOX, at: ago(60 * 24 * 9), key: "calcom" });

const runs: Record<string, TeamStatus> = {};
const logs: Record<string, string[]> = {};
const logListeners: Record<string, Set<(line: string) => void>> = {};
const gates: Record<string, () => void> = {};
let ctx: TeamMockCtx | undefined;

function github(): GitHubState {
  if (teamScenario === "nogh") return { state: "missing", install: { os: "darwin", command: "brew install gh", url: "https://cli.github.com" } };
  if (teamScenario === "signedout" && (!ghLoginAt || Date.now() - ghLoginAt < 1500)) return { state: "signed-out" };
  return { state: "ready", login: "sean-brydon", name: "Sean Brydon", avatar_url: ME_AVATAR };
}

function calView(box?: string): TeamView {
  const setup = structuredClone(calSetup);
  const projects = calProjects();
  const readable = projects.filter((p) => p.access);
  const run = box ? runs[`${box}/calcom`] : undefined;
  const view: TeamView = {
    org: calOrg,
    state: "found",
    repo: { full_name: "calcom/.berth", private: false, default_branch: "main", html_url: "https://github.com/calcom/.berth" },
    commit: { sha: COMMIT, short: COMMIT.slice(0, 7), author: "keithwillcode", author_avatar: AUTHOR_AVATAR, date: ago(60 * 24 * 2), message: "Postgres on 5450, as cal.com's .env.example expects" },
    setup,
    files: calFiles(),
    steps: planSteps(setup),
    projects,
    access: { readable: readable.length, total: projects.length, missing: projects.filter((p) => !p.access).map((p) => p.repo) },
    keys: { shared: teamScenario === "partial" ? 4 : 5, ask: [{ project: "cal.com", key: "SENDGRID_API_KEY", set: !!run?.keys_set.includes("cal.com/SENDGRID_API_KEY") }] },
    warnings: [],
  };
  view.source = orgSource;
  const acc = accepted.find((a) => a.org === "calcom");
  if (acc) view.accepted = { commit: acc.commit, box: acc.box, at: acc.at };
  if (teamScenario === "update" && acc?.commit === COMMIT.slice(0, 7)) view.update = UPDATE;
  return view;
}

// linkView is the same setup read from the link: its owner is a user, and
// the commit is the branch's.
function linkView(box?: string): TeamView {
  const v = calView(box);
  v.org = { login: "sean-brydon", name: "Sean Brydon", avatar_url: SEAN_AVATAR, verified: false, type: "User", html_url: "https://github.com/sean-brydon" };
  v.source = linkSource;
  v.repo = { full_name: "sean-brydon/berth-kit-calcom", private: false, default_branch: "main", html_url: "https://github.com/sean-brydon/berth-kit-calcom" };
  v.commit = { sha: LINK_COMMIT, short: LINK_COMMIT.slice(0, 7), author: "sean-brydon", author_avatar: SEAN_AVATAR, date: ago(50), message: "team: Postgres on 5450" };
  v.files = v.files?.map((f) => ({ ...f, path: `team/${f.path}` }));
  v.update = undefined;
  return v;
}

const northwindRepos: OrgRepo[] = [
  { full_name: "northwind/storefront", private: true, description: "The shop, Next.js", pushed_at: ago(40), has_berth: true },
  { full_name: "northwind/api", private: true, description: "Orders and inventory, Go", pushed_at: ago(180), has_berth: true },
  { full_name: "northwind/design-system", private: false, description: "Components and tokens", pushed_at: ago(60 * 26), has_berth: false },
  { full_name: "northwind/infra", private: true, description: "Terraform", pushed_at: ago(60 * 24 * 6), has_berth: false },
  { full_name: "northwind/handbook", private: false, description: "How we work", pushed_at: ago(60 * 24 * 30), has_berth: false },
];

function view(org: string, q: URLSearchParams): TeamView {
  const o = org.toLowerCase();
  const box = q.get("box") ?? undefined;
  if (isMockLink(o)) {
    if (o === TEAM_LINK) return linkView(box);
    // Any other link: GitHub won't show it to this account.
    const [, owner = "someone", repo = "repo"] = org.split("/");
    return {
      org: { login: owner, name: owner, avatar_url: "", verified: false, type: "User", html_url: `https://github.com/${owner}` },
      state: "unreadable",
      source: { kind: "link", repo: `${owner}/${repo}`, html_url: `https://${org}`, label: org.replace(/^github\.com\//, ""), key: org },
      projects: [],
      access: { readable: 0, total: 0, missing: [] },
      keys: { shared: 0, ask: [] },
      warnings: [],
    };
  }
  if (o === "calcom") {
    if (teamScenario === "unreadable") {
      return { org: calOrg, state: "unreadable", projects: [], access: { readable: 0, total: 0, missing: [] }, keys: { shared: 0, ask: [] }, warnings: [] };
    }
    return calView(box);
  }
  if (o === "northwind") {
    return {
      org: { login: "northwind", name: "Northwind Labs", avatar_url: NW_AVATAR, verified: false, type: "Organization", html_url: "https://github.com/northwind" },
      state: "none",
      projects: [],
      access: { readable: northwindRepos.length, total: northwindRepos.length, missing: [] },
      keys: { shared: 0, ask: [] },
      repos: northwindRepos,
      warnings: [],
    };
  }
  return { org: { login: org, name: org, avatar_url: "", verified: false, type: "Organization", html_url: `https://github.com/${org}` }, state: "no-org", projects: [], access: { readable: 0, total: 0, missing: [] }, keys: { shared: 0, ask: [] }, warnings: [] };
}

// ——— the run ———

const iso = () => new Date().toISOString();
const wait = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

function gate(name: string, autoMs?: number): Promise<void> {
  return new Promise<void>((resolve) => {
    gates[name] = () => {
      delete gates[name];
      resolve();
    };
    if (autoMs !== undefined && !holds.has(name)) setTimeout(() => gates[name]?.(), autoMs);
  });
}

function say(key: string, line: string) {
  (logs[key] ??= []).push(line);
  logListeners[key]?.forEach((fn) => fn(line));
}

function touch(r: TeamStatus, type: string, data: Record<string, unknown>) {
  r.updated = iso();
  ctx?.emit({ type, box: r.box, data: { team: r.id, ...data } });
}

function newRun(box: string, org: string, name: string, commit: string, steps: PlanStep[], projects: { id: string; repo: string }[], keysSet: string[]): TeamStatus {
  return {
    id: org,
    name,
    org,
    commit,
    box,
    phase: "steps",
    session: `team-${org}`,
    steps: steps.map((s) => ({ id: s.id, title: s.title, sudo: s.sudo, state: "todo" })),
    projects: projects.map((p) => ({ id: p.id, repo: p.repo, state: "queued" })),
    keys_set: keysSet,
    started: iso(),
    updated: iso(),
  };
}

let retried = false;

async function play(r: TeamStatus, from = 0, again = false) {
  const key = `${r.box}/${r.id}`;
  const session = r.session!;
  r.phase = "steps";
  r.error = undefined;
  say(key, `\x1b[2m$ ~/.berth/team/${r.id}/run.sh${from ? ` --from ${r.steps[from]?.id}` : ""}\x1b[0m\r\n`);
  let askedSudo = false;
  for (let i = from; i < r.steps.length; i++) {
    const s = r.steps[i];
    // Re-running an update skips what is already done (each step checks first).
    if (again && !["node"].includes(s.id) && s.id !== "github") {
      s.state = "skipped";
      touch(r, "team.step", { step: s.id, state: s.state });
      say(key, `\x1b[2m    ${s.title} (already done)\x1b[0m\r\n`);
      await wait(120);
      continue;
    }
    s.state = "running";
    s.error = undefined;
    touch(r, "team.step", { step: s.id, state: s.state });
    const started = Date.now();
    if (s.id === "github") {
      say(key, "\x1b[1m==> GitHub on this box\x1b[0m\r\n");
      await wait(500);
      s.state = "waiting";
      s.code = "C4L1-7Q2M";
      s.url = "https://github.com/login/device";
      say(key, `! First copy your one-time code: \x1b[1m${s.code}\x1b[0m\r\nOpen this URL to continue in your web browser: ${s.url}\r\n\x1b[2m⣾ Waiting for authentication…\x1b[0m\r\n`);
      touch(r, "team.step", { step: s.id, state: s.state, code: s.code });
      await gate("github", 1500);
      s.code = undefined;
      s.url = undefined;
      say(key, "✓ Authentication complete.\r\n✓ Logged in as sean-brydon\r\n\x1b[2m$ gh auth setup-git\x1b[0m\r\n");
    } else {
      say(key, `\x1b[1m==> ${s.title}\x1b[0m\r\n`);
      await wait(250);
      if (s.sudo && !askedSudo) {
        askedSudo = true;
        s.state = "waiting";
        say(key, "\x1b[1m==> The next step needs root. sudo asks for your password; Berth never sees or keeps it.\x1b[0m\r\n[sudo] password for me: ");
        touch(r, "team.step", { step: s.id, state: s.state });
        await gate("sudo");
        say(key, "\r\n");
        s.state = "running";
        touch(r, "team.step", { step: s.id, state: s.state });
        await wait(500);
      }
      if (s.id === "postgres" && teamScenario === "fail" && !retried) {
        say(
          key,
          "docker: Error response from daemon: driver failed programming external connectivity on endpoint cal-postgres:\r\n  Bind for 127.0.0.1:5450 failed: port is already allocated.\r\n\x1b[31merror:\x1b[0m Postgres did not start; see: docker logs cal-postgres\r\n\r\nBerth: stopped at postgres (exit 1). Fix it, then Retry from Postgres in Berth.\r\n",
        );
        s.state = "failed";
        s.error = "Bind for 127.0.0.1:5450 failed: port is already allocated.\nerror: Postgres did not start; see: docker logs cal-postgres";
        s.secs = 3;
        r.phase = "failed";
        r.error = `stopped at ${s.title}`;
        touch(r, "team.step", { step: s.id, state: "failed" });
        touch(r, "team.failed", { step: s.id, error: s.error });
        return;
      }
      await wait(200);
    }
    s.state = "done";
    s.secs = [41, 63, 18, 22, 3, 6, 4, 12][i] ?? Math.max(1, Math.round((Date.now() - started) / 1000));
    say(key, `\x1b[32m    ✓ ${s.id}\x1b[0m\r\n`);
    touch(r, "team.step", { step: s.id, state: s.state, secs: s.secs });
  }
  say(key, "\r\nBerth: the box is set up. The repos are next; you can close this tab.\r\n");
  const sess = ctx?.sessions[r.box]?.find((x) => x.name === session);
  if (sess) sess.exited = true;
  r.phase = "projects";
  touch(r, "team.step", { step: "", state: "done" });
  await Promise.all(r.projects.map((p, i) => project(r, p, i)));
  if (r.projects.some((p) => p.state === "failed")) {
    r.phase = "failed";
    touch(r, "team.failed", {});
    return;
  }
  r.phase = "done";
  touch(r, "team.done", {});
}

async function project(r: TeamStatus, p: TeamStatus["projects"][number], i: number) {
  if (p.state === "skipped" || p.state === "ready") return;
  await wait(i * 350);
  p.state = "cloning";
  p.line = "Receiving objects: 38%";
  touch(r, "team.project", { project: p.id, state: p.state });
  await wait(600);
  p.state = "setting-up";
  p.line = p.id === "cal.com" ? "box/init-cal.sh · yarn install" : p.id === "private-api" ? "yarn install" : "pnpm install";
  addLocation(r.box, p);
  touch(r, "team.project", { project: p.id, state: p.state, location: p.location });
  if (i === 0) await wait(700);
  else await gate(`repos-${p.id}`, holds.has("repos") ? undefined : 2200 + i * 300);
  p.state = "ready";
  p.line = undefined;
  p.trust = p.id === "cal.com" ? "none" : "trusted";
  touch(r, "team.project", { project: p.id, state: p.state, location: p.location });
}

function addLocation(box: string, p: TeamStatus["projects"][number]) {
  if (!ctx) return;
  const name = p.id;
  p.location = name;
  const list = (ctx.locations[box] ??= []);
  if (list.some((l) => l.name === name)) return;
  const path = `/home/me/code/${name}`;
  list.push({ name, path, repo: true, remote: `https://github.com/${p.repo}.git`, slug: p.repo, default_branch: "main", scripts: {}, worktrees: [{ name, path, branch: "main", main: true }] });
  ctx.emit({ type: "location.added", box, data: { location: name, path, url: `https://github.com/${p.repo}.git` } });
}

function startSession(box: string, name: string) {
  if (!ctx) return;
  const list = (ctx.sessions[box] ??= []);
  const at = list.findIndex((s) => s.name === name);
  if (at >= 0) list.splice(at, 1);
  list.push({ name, location: "", dir: "/home/me", command: `~/.berth/team/${name.replace(/^team-/, "")}/run.sh`, created: iso(), attached: 0, exited: false, title: "Team setup" });
  ctx.emit({ type: "session.started", box, data: { name, location: "", path: "/home/me", command: "run.sh" } });
}

function setup(org: string, req: SetupRequest): Promise<TeamStatus> {
  if (!ctx) return Promise.reject(new Error("mock: no context"));
  const box = req.box;
  if (!ctx.status.boxes.some((b) => b.name === box && b.state === "online")) return Promise.reject(new ApiError(`${box} is offline`, 503));
  const o = org.toLowerCase();
  let r: TeamStatus;
  let again = false;
  const link = isMockLink(o);
  if (o === "northwind") {
    const repos = req.repos ?? [];
    r = newRun(box, "northwind", "Northwind Labs", "", [GITHUB_STEP], repos.map((repo) => ({ id: repo.split("/")[1], repo })), []);
  } else {
    const v = link ? linkView(box) : calView(box);
    const picked = req.projects ?? v.projects.filter((p) => p.access).map((p) => p.id);
    const commit = (req.commit ?? (link ? LINK_COMMIT : COMMIT)).slice(0, 7);
    again = !!accepted.find((a) => a.org === "calcom" && a.box === box) && commit === NEXT.slice(0, 7);
    const projects = v.projects.filter((p) => picked.includes(p.id)).map((p) => ({ id: p.id, repo: p.repo }));
    if (again) projects.push({ id: "cal-video", repo: "calcom/cal-video" });
    const keys = Object.entries(req.keys ?? {}).flatMap(([p, kv]) => Object.keys(kv).filter((k) => kv[k]).map((k) => `${p}/${k}`));
    const steps = again ? [...(v.steps ?? []).slice(0, -1), { id: "playwright", title: "Playwright's system libraries", sudo: true, commands: [] }, GITHUB_STEP] : (v.steps ?? []);
    r = newRun(box, "calcom", "Cal.com", commit, steps, projects, keys);
    if (again) {
      // Repos already there stay ready; only the new one is cloned.
      for (const p of r.projects) if (p.id !== "cal-video") Object.assign(p, { state: "ready", location: p.id });
    }
    const source = link ? linkSource : orgSource;
    const acc = accepted.find((a) => a.org === "calcom");
    if (acc) Object.assign(acc, { commit, box, at: iso(), key: source.key, source });
    else accepted.push({ org: "calcom", id: "calcom", name: "Cal.com", commit, box, at: iso(), key: source.key, source });
  }
  const key = `${box}/${r.id}`;
  runs[key] = r;
  logs[key] = [];
  retried = false;
  startSession(box, r.session!);
  void play(r, 0, again);
  return ctx.delay(r);
}

function retry(box: string, id: string, from: string): Promise<TeamStatus> {
  const r = runs[`${box}/${id}`];
  if (!r || !ctx) return Promise.reject(new ApiError(`no team setup ${id} on ${box}`, 404));
  retried = true;
  const at = Math.max(0, r.steps.findIndex((s) => s.id === from));
  for (let i = at; i < r.steps.length; i++) Object.assign(r.steps[i], { state: "todo", error: undefined, secs: undefined });
  startSession(box, r.session!);
  say(`${box}/${id}`, "\r\n");
  void play(r, at);
  return ctx.delay(r);
}

// Runs that were there before this window opened.
function seedDone() {
  if (teamScenario !== "done" && teamScenario !== "update") return;
  const v = calView(TEAM_BOX);
  const r = newRun(TEAM_BOX, "calcom", "Cal.com", COMMIT.slice(0, 7), v.steps ?? [], v.projects.map((p) => ({ id: p.id, repo: p.repo })), ["cal.com/SENDGRID_API_KEY"]);
  r.phase = "done";
  r.started = ago(60 * 24 * 9);
  r.updated = teamScenario === "done" ? iso() : ago(60 * 24 * 9);
  r.steps.forEach((s, i) => Object.assign(s, { state: "done", secs: [41, 63, 18, 22, 3, 6, 4, 12][i] }));
  r.projects.forEach((p) => {
    p.state = "ready";
    addLocation(TEAM_BOX, p);
  });
  runs[`${TEAM_BOX}/calcom`] = r;
}

// ——— wiring ———

export function initTeamMock(c: TeamMockCtx) {
  ctx = c;
  // A box of the new engineer's own, set up for nothing yet.
  if (params.has("team") || [...params.keys()].some((k) => k.startsWith("team-"))) c.addBox(TEAM_BOX, "100.64.0.22:7444");
  seedDone();
  (window as unknown as Record<string, unknown>).__teamMock = {
    advance(name: string) {
      gates[name]?.();
      if (name === "repos") for (const k of Object.keys(gates)) if (k.startsWith("repos-")) gates[k]?.();
    },
    runs,
  };
}

export function teamLaptopCall(method: string, path: string, body: unknown, delay: Delay): Promise<unknown> | undefined {
  const [route, query = ""] = path.split("?");
  const q = new URLSearchParams(query);
  if (method === "GET" && route === "/v1/github") return delay(github());
  if (method === "POST" && route === "/v1/github/login") {
    ghLoginAt = Date.now();
    return delay({ opened: true, command: "gh auth login --hostname github.com --git-protocol https --web" });
  }
  if (!route.startsWith("/v1/team")) return undefined;
  if (method === "GET" && route === "/v1/team") return delay(accepted);
  const m = /^\/v1\/team\/([^/]+)(?:\/(setup|retry|update))?$/.exec(route);
  if (!m) return undefined;
  const org = decodeURIComponent(m[1]);
  const gh = github().state;
  if (gh === "missing") return Promise.reject(new ApiError("Install GitHub's CLI (gh) on this computer first", 412, "gh_missing"));
  if (gh !== "ready") return Promise.reject(new ApiError("Connect GitHub first: gh is not signed in on this computer", 412, "gh_signed_out"));
  if (method === "GET" && !m[2]) return delay(view(org, q));
  if (method === "POST" && m[2] === "setup") return setup(org, body as SetupRequest);
  if (method === "POST" && m[2] === "retry") {
    const r = body as { box: string; from: string };
    return retry(r.box, idOf(org), r.from);
  }
  if (method === "GET" && m[2] === "update") {
    const acc = accepted.find((a) => (a.key ?? a.org) === org.toLowerCase());
    return delay(teamScenario === "update" && acc?.commit === COMMIT.slice(0, 7) ? UPDATE : { update: null });
  }
  return undefined;
}

export function teamBoxCall(box: string, method: string, path: string, body: unknown, delay: Delay): Promise<unknown> | undefined {
  if (method === "GET" && path === "team") return delay(Object.values(runs).filter((r) => r.box === box));
  const m = /^team\/([^/]+)(?:\/(retry))?$/.exec(path);
  if (!m) return undefined;
  const id = decodeURIComponent(m[1]);
  if (method === "GET" && !m[2]) {
    const r = runs[`${box}/${id}`];
    return r ? delay(r) : Promise.reject(new ApiError(`no team setup ${id} on ${box}`, 404));
  }
  if (method === "POST" && m[2] === "retry") return retry(box, id, (body as { from: string }).from);
  return undefined;
}

export const isTeamSession = (box: string, session: string) => !!runs[`${box}/${session.replace(/^team-/, "")}`] && session.startsWith("team-");

// teamAttach is the runner's terminal: what it printed so far, then each
// line as the run goes. Typing at the sudo prompt (Enter) answers it.
export function teamAttach(box: string, session: string, h: TerminalHandlers): TerminalConnection {
  const key = `${box}/${session.replace(/^team-/, "")}`;
  let open = true;
  const listener = (line: string) => open && h.onData(line);
  const t = window.setTimeout(() => {
    h.onOpen();
    h.onData("\x1b[2J\x1b[H");
    for (const l of logs[key] ?? []) h.onData(l);
    (logListeners[key] ??= new Set()).add(listener);
  }, 120);
  return {
    send(data) {
      const text = typeof data === "string" ? data : new TextDecoder().decode(data);
      // A password is never echoed, as sudo doesn't.
      if (/[\r\n]/.test(text) && gates.sudo) gates.sudo();
    },
    resize() {},
    close() {
      open = false;
      window.clearTimeout(t);
      logListeners[key]?.delete(listener);
    },
  };
}
