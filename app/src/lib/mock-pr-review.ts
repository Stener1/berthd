import type { BerthEvent, Location, Service, Status } from "@/lib/api";
import { ApiError } from "@/lib/api";
import type { ReviewBox, ReviewMark, ReviewSheet, ReviewStatus, ReviewWorktree, SetupChange } from "@/lib/pr-review";
import { parseReviewLink, reviewLink, validRepo } from "@/lib/review-link";

// Mock mode's PR reviews, behaving like the laptop agent (internal/agent
// prreview.go: gh pr view as the reviewer, the team's projects) and the box
// (internal/box prreview.go: a worktree pinned to the head commit, set up
// by the team's kit) closely enough to build the sheet on. Synthetic
// fixtures only: acme/shop's PRs
//   #42  Fix checkout rounding, by a member: reviewable
//   #57  Add refunds, by a collaborator: reviewable, and changes setup files
//   #61  from a fork (jo/shop): refused
//   #63  by someone outside acme: refused
// and acme/secret-tool#5, a repo that is not one of the team's projects.
// &reviewhold=setup keeps a review's setup running until
// window.__prReviewMock.advance(); window.__prReviewMock.push(n) adds n
// commits to a PR's head, so its review says it has new commits.

type Emit = (e: Omit<BerthEvent, "time">) => void;
type Delay = <T>(v: T) => Promise<T>;
export interface PrReviewMockCtx {
  status: Status;
  locations: Record<string, Location[]>;
  services: Record<string, Service[]>;
  emit: Emit;
  delay: Delay;
}

let ctx: PrReviewMockCtx | undefined;
const holds = new Set((new URLSearchParams(location.search).get("reviewhold") ?? "").split(",").filter(Boolean));
const waiting: (() => void)[] = [];

interface MockPR {
  title: string;
  author: string;
  association: string;
  branch: string;
  heads: string[];
  cross?: string;
  state?: "OPEN" | "CLOSED" | "MERGED";
  changes?: SetupChange[];
  files: number;
}

// A made-up commit id: the same seed, the same id.
const sha = (seed: string) => {
  let h = 0x811c9dc5;
  for (const ch of seed) h = Math.imul(h ^ ch.charCodeAt(0), 0x01000193) >>> 0;
  let out = "";
  for (let i = 0; out.length < 40; i++) {
    h = Math.imul(h ^ i, 0x01000193) >>> 0;
    out += h.toString(16).padStart(8, "0");
  }
  return out.slice(0, 40);
};

const SETUP_CHANGES: SetupChange[] = [
  {
    kind: "berth",
    title: "Changes Shipyard's own setup (.berth/)",
    detail: "Not used for this review: setup comes from your team's kit and the default branch's config, never from the PR",
    files: [".berth/config.json"],
  },
  {
    kind: "scripts",
    title: "Changes package.json scripts: postinstall",
    detail: "They run when setup installs dependencies or starts the dev server",
    files: ["package.json"],
  },
  {
    kind: "lockfile",
    title: "Updates the lockfile",
    files: ["yarn.lock"],
  },
  {
    kind: "compose",
    title: "Changes Docker Compose",
    detail: "Used if setup or a service starts containers",
    files: ["docker-compose.yml"],
  },
  {
    kind: "migrations",
    title: "Adds or changes a database migration",
    detail: "They run against this review's own database if setup migrates",
    files: ["prisma/migrations/20261007_add_refunds/migration.sql"],
  },
  {
    kind: "env",
    title: "Changes an environment file",
    detail: "Your box's keys still apply; the file can set defaults",
    files: [".env.example"],
  },
];

const prs: Record<string, MockPR> = {
  "acme/shop#42": {
    title: "Fix checkout rounding",
    author: "dana-acme",
    association: "MEMBER",
    branch: "dana/checkout-rounding",
    heads: [sha("42a")],
    files: 6,
  },
  "acme/shop#57": {
    title: "Add refunds to the admin",
    author: "lee-acme",
    association: "COLLABORATOR",
    branch: "lee/refunds",
    heads: [sha("57a")],
    changes: SETUP_CHANGES,
    files: 23,
  },
  "acme/shop#61": {
    title: "Speed up the product grid",
    author: "jo",
    association: "CONTRIBUTOR",
    branch: "grid-speed",
    heads: [sha("61a")],
    cross: "jo/shop",
    files: 4,
  },
  "acme/shop#63": {
    title: "Add a coupon field",
    author: "pat-outside",
    association: "NONE",
    branch: "coupon-field",
    heads: [sha("63a")],
    files: 3,
  },
  "acme/secret-tool#5": {
    title: "Rotate the signing key",
    author: "dana-acme",
    association: "MEMBER",
    branch: "rotate",
    heads: [sha("5a")],
    files: 2,
  },
};
// Where each PR's review was opened from: the head it is pinned to.
const TEAM_REPOS = new Set(["acme/shop", "acme/billing-api", "acme/docs"]);

const head = (pr: MockPR) => pr.heads[pr.heads.length - 1];

export function initPrReviewMock(c: PrReviewMockCtx) {
  ctx = c;
  (window as unknown as { __prReviewMock: unknown }).__prReviewMock = {
    // advance lets a held setup finish.
    advance: () => waiting.splice(0).forEach((f) => f()),
    // push adds commits to a PR's head, as its author would.
    push: (n = 3, key = "acme/shop#42") => {
      const pr = prs[key];
      for (let i = 0; i < n; i++) pr.heads.push(sha(`${key}-${pr.heads.length}`));
      return import("@/views/pr-review/use-review-status").then((m) => m.refreshAllReviewStatuses());
    },
  };
}

const boxesWith = (repo: string) =>
  (ctx?.status.boxes ?? [])
    .filter((b) => b.state === "online")
    .flatMap((b) => {
      const loc = ctx?.locations[b.name]?.find((l) => l.slug?.toLowerCase() === repo.toLowerCase());
      return loc ? [{ box: b.name, loc }] : [];
    });

const reviewName = (pr: number) => `review-${pr}`;

function reviewBox(box: string, loc: Location, pr: number): ReviewBox {
  const existing = loc.worktrees?.find((w) => w.name === reviewName(pr)) as ReviewWorktree | undefined;
  return {
    box,
    location: loc.name,
    setup: {
      from: "kit",
      kit: { id: "acme-shop", name: "Acme shop", version: "5476af4" },
      repo_config: "none",
      default_branch: loc.default_branch ?? "main",
      script: "yarn install && yarn db:create",
      archive: "yarn db:drop",
      services: [
        {
          name: "web",
          title: "Next.js dev server",
          run: "yarn dev --port $BERTH_PORT",
          autostart: true,
        },
        {
          name: "worker",
          title: "Jobs worker",
          run: "yarn worker",
          autostart: true,
        },
      ],
      hooks: 0,
      ports: 2,
    },
    secrets: {
      shared: ["MAPS_API_KEY", "STRIPE_PUBLISHABLE_KEY", "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET"],
      withheld: ["MAIL_API_KEY"],
    },
    idle_days: 7,
    ...(existing?.review ? { existing: { worktree: existing.name, sha: existing.review.sha } } : {}),
  };
}

function plan(repo: string, n: number, pickBox?: string, hintSha?: string): ReviewSheet {
  const key = `${repo}#${n}`;
  const pr = prs[key];
  const base: ReviewSheet = {
    repo,
    pr: n,
    link: reviewLink(repo, n),
    org: "acme",
    reviewer: "sean",
    verdict: { allowed: true },
    boxes: [],
    changes: [],
    files: 0,
    team: { id: "acme", name: "Acme", org: "acme" },
  };
  if (!pr)
    return {
      ...base,
      verdict: {
        allowed: false,
        code: "unreadable",
        reason: `You can't see ${key} with your GitHub account, or it doesn't exist`,
      },
    };
  const h = head(pr);
  const sheet: ReviewSheet = {
    ...base,
    url: `https://github.com/${repo}/pull/${n}`,
    title: pr.title,
    state: pr.state ?? "OPEN",
    author: { login: pr.author, association: pr.association },
    head: {
      branch: pr.branch,
      sha: h,
      repo: pr.cross ?? repo,
      cross: !!pr.cross,
    },
    base: { branch: "main" },
    changes: pr.changes ?? [],
    files: pr.files,
    ...(hintSha ? { hint: { sha: hintSha, matches: h.startsWith(hintSha) } } : {}),
  };
  const on = boxesWith(repo);
  sheet.boxes = on.map(({ box, loc }) => reviewBox(box, loc, n));
  sheet.box = sheet.boxes.find((b) => b.box === pickBox)?.box ?? sheet.boxes[0]?.box;
  // As the laptop agent: a refused PR's diff is never read.
  const refuse = (code: ReviewSheet["verdict"]["code"], reason: string): ReviewSheet => ({ ...sheet, files: 0, changes: [], verdict: { allowed: false, code, reason } });
  if (!TEAM_REPOS.has(repo) && on.length === 0) return refuse("not_a_project", `${repo} isn't one of your team's projects or on any of your boxes`);
  if (pr.cross) return refuse("fork", `Review this one by hand: it comes from a fork (${pr.cross})`);
  if (!["MEMBER", "OWNER", "COLLABORATOR"].includes(pr.association)) return refuse("outsider", "Review this one by hand: its author is outside acme");
  if (sheet.state !== "OPEN") return refuse("closed", sheet.state === "MERGED" ? "This PR is merged" : "This PR is closed");
  if (on.length === 0) return refuse("no_box", `No box of yours has ${repo} yet. Set it up from Team setup first`);
  return sheet;
}

const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));

function findReview(box: string, location: string, worktree: string) {
  const loc = ctx?.locations[box]?.find((l) => l.name === location);
  const wt = loc?.worktrees?.find((w) => w.name === worktree) as ReviewWorktree | undefined;
  return { loc, wt };
}

// setUp plays what the box does after the worktree is made: the kit's
// setup script, then the dev server.
function setUp(box: string, loc: Location, path: string, name: string) {
  const c = ctx!;
  setTimeout(
    () =>
      c.emit({
        type: "worktree.setup.started",
        box,
        data: {
          location: loc.name,
          name,
          path,
          script: "yarn install && yarn db:create",
        },
      }),
    250,
  );
  const finish = () => {
    c.emit({
      type: "worktree.setup.finished",
      box,
      data: {
        location: loc.name,
        name,
        path,
        script: "yarn install && yarn db:create",
      },
    });
    setTimeout(() => {
      const port = 3142;
      c.services[box] = [
        ...(c.services[box] ?? []).filter((s) => s.path !== path),
        {
          location: loc.name,
          worktree: name,
          path,
          port,
          process: `node ${path}/node_modules/.bin/next dev -p ${port}`,
        },
      ];
      c.emit({
        type: "service.started",
        box,
        data: { location: loc.name, name, path, service: "web", port },
      });
    }, 400);
  };
  setTimeout(() => (holds.has("setup") ? waiting.push(finish) : finish()), 1400);
}

export function prReviewLaptopCall(method: string, path: string, body: unknown, delay: Delay): Promise<unknown> | undefined {
  if (!path.startsWith("/v1/pr-review/")) return undefined;
  const [route, query = ""] = path.split("?");
  if (method === "POST" && route === "/v1/pr-review/plan") {
    const r = (body ?? {}) as {
      link?: string;
      repo?: string;
      pr?: number;
      box?: string;
    };
    let repo = r.repo ?? "";
    let n = r.pr ?? 0;
    let hint: string | undefined;
    if (r.link !== undefined) {
      const ref = parseReviewLink(r.link);
      if (!ref) return Promise.reject(new ApiError("This isn't a review link Shipyard can open", 400, "bad_link"));
      ({ repo, pr: n, sha: hint } = ref);
    }
    if (!validRepo(repo) || !(n > 0)) return Promise.reject(new ApiError("This isn't a review link Shipyard can open", 400, "bad_link"));
    return wait(350).then(() => delay(plan(repo, n, r.box, hint)));
  }
  if (method === "POST" && route === "/v1/pr-review/open") {
    const r = body as { repo: string; pr: number; sha: string; box: string };
    return wait(500).then(() => {
      const sheet = plan(r.repo, r.pr, r.box);
      if (!sheet.verdict.allowed) throw new ApiError(sheet.verdict.reason ?? "refused", 403, sheet.verdict.code);
      if (sheet.head?.sha !== r.sha) throw new ApiError(`The PR moved on since you opened it: its head is ${sheet.head?.sha.slice(0, 7)} now, not ${r.sha.slice(0, 7)}`, 409, "moved");
      const c = ctx!;
      const loc = c.locations[r.box]?.find((l) => l.slug?.toLowerCase() === r.repo.toLowerCase());
      if (!loc) throw new ApiError(`No box of yours has ${r.repo} yet`, 403, "no_box");
      const name = reviewName(r.pr);
      const path = `${loc.path}-${name}`;
      const review: ReviewMark = {
        repo: r.repo,
        pr: r.pr,
        sha: r.sha,
        title: sheet.title,
        author: sheet.author?.login,
        association: sheet.author?.association,
        head_branch: sheet.head?.branch,
        url: sheet.url,
        reviewer: "sean",
        opened: new Date().toISOString(),
        withheld: ["MAIL_API_KEY"],
      };
      const wt: ReviewWorktree = {
        name,
        path,
        branch: `review/pr-${r.pr}`,
        head: r.sha.slice(0, 10),
        title: `Review: #${r.pr} ${sheet.title}`,
        review,
      };
      const existing = loc.worktrees?.find((w) => w.name === name);
      if (existing)
        return delay({
          box: r.box,
          location: loc.name,
          worktree: existing,
          review: (existing as ReviewWorktree).review ?? review,
        });
      loc.worktrees = [...(loc.worktrees ?? []), wt];
      c.emit({
        type: "worktree.created",
        box: r.box,
        data: { location: loc.name, name, path, branch: wt.branch },
      });
      c.emit({
        type: "review.opened",
        box: r.box,
        data: {
          location: loc.name,
          name,
          path,
          repo: r.repo,
          pr: r.pr,
          sha: r.sha,
          author: review.author,
          association: review.association,
          box: r.box,
          reviewer: "sean",
          secrets: "shared",
          withheld: review.withheld,
        },
      });
      setUp(r.box, loc, path, name);
      return delay({ box: r.box, location: loc.name, worktree: wt, review });
    });
  }
  if (method === "POST" && route === "/v1/pr-review/update") {
    const r = body as {
      box: string;
      location: string;
      worktree: string;
      sha: string;
    };
    return wait(500).then(() => {
      const { loc, wt } = findReview(r.box, r.location, r.worktree);
      if (!loc || !wt?.review) throw new ApiError("That worktree isn't a review", 404);
      const sheet = plan(wt.review.repo, wt.review.pr, r.box);
      if (!sheet.verdict.allowed) throw new ApiError(sheet.verdict.reason ?? "refused", 403, sheet.verdict.code);
      if (sheet.head?.sha !== r.sha) throw new ApiError("The PR moved on again since you opened it", 409, "moved");
      const from = wt.review.sha;
      wt.review = {
        ...wt.review,
        sha: r.sha,
        updated: new Date().toISOString(),
        title: sheet.title,
      };
      wt.head = r.sha.slice(0, 10);
      ctx!.emit({
        type: "review.updated",
        box: r.box,
        data: {
          location: loc.name,
          name: wt.name,
          path: wt.path,
          repo: wt.review.repo,
          pr: wt.review.pr,
          from,
          sha: r.sha,
          author: sheet.author?.login,
          association: sheet.author?.association,
          reviewer: "sean",
        },
      });
      return delay({
        box: r.box,
        location: loc.name,
        worktree: wt,
        review: wt.review,
      });
    });
  }
  if (method === "GET" && route === "/v1/pr-review/status") {
    const q = new URLSearchParams(query);
    const { wt } = findReview(q.get("box") ?? "", q.get("location") ?? "", q.get("worktree") ?? "");
    if (!wt?.review) return Promise.reject(new ApiError("That worktree isn't a review", 404));
    const pr = prs[`${wt.review.repo}#${wt.review.pr}`];
    const h = pr ? head(pr) : wt.review.sha;
    const at = pr ? pr.heads.indexOf(wt.review.sha) : -1;
    const st: ReviewStatus = {
      head: h,
      new_commits: pr && at >= 0 ? pr.heads.length - 1 - at : 0,
      state: pr?.state ?? "OPEN",
      moved: h !== wt.review.sha,
    };
    return delay(st);
  }
  return undefined;
}
