import { create } from "zustand";

import type { Client, Location, Worktree } from "@/lib/api";
import { type ReviewRef, reviewLink } from "@/lib/review-link";

// Review a PR in one click: a review link (lib/review-link) opens a sheet
// that says everything a review would do, on the reviewer's own box, before
// anything is fetched or run. The laptop agent reads the PR with this
// computer's gh, decides whether it may be reviewed this way, and asks the
// box to make a worktree pinned to the head commit the sheet showed
// (internal/agent prreview.go, internal/box prreview.go).

export type VerdictCode = "not_a_project" | "fork" | "outsider" | "closed" | "unreadable" | "no_box" | "bad_pr";

export interface ReviewSetup {
  from: "kit" | "repo" | "box" | "none";
  kit?: { id: string; name: string; version?: string };
  repo_config: "trusted" | "untrusted" | "changed" | "none";
  default_branch?: string;
  // Whether the main checkout's .berth/config.json is the default branch's.
  matches_default?: boolean;
  script?: string;
  archive?: string;
  services: { name: string; title?: string; run: string; autostart: boolean }[];
  hooks: number;
  ports: number;
}

export interface ReviewBox {
  box: string;
  location: string;
  setup: ReviewSetup;
  secrets: { shared: string[]; withheld: string[] };
  idle_days: number;
  existing?: { worktree: string; sha: string };
}

export type SetupChangeKind = "berth" | "scripts" | "dependencies" | "lockfile" | "docker" | "compose" | "migrations" | "env" | "githooks" | "kit";

export interface SetupChange {
  kind: SetupChangeKind;
  title: string;
  detail?: string;
  files: string[];
}

export interface ReviewSheet {
  repo: string;
  pr: number;
  link: string;
  url?: string;
  title?: string;
  state?: "OPEN" | "CLOSED" | "MERGED";
  draft?: boolean;
  author?: { login: string; association: string };
  head?: { branch: string; sha: string; repo?: string; cross: boolean };
  base?: { branch: string };
  reviewer?: string;
  org: string;
  team?: { id: string; name: string; org: string };
  hint?: { sha: string; matches: boolean };
  verdict: { allowed: boolean; code?: VerdictCode; reason?: string };
  boxes: ReviewBox[];
  box?: string;
  changes: SetupChange[];
  files: number;
}

export interface ReviewMark {
  repo: string;
  pr: number;
  sha: string;
  title?: string;
  author?: string;
  association?: string;
  head_branch?: string;
  url?: string;
  reviewer?: string;
  opened: string;
  updated?: string;
  withheld?: string[];
  cleanup?: "merged" | "closed" | "idle";
}

export interface ReviewOpened {
  box: string;
  location: string;
  worktree: Worktree;
  review: ReviewMark;
}

export interface ReviewStatus {
  head: string;
  new_commits: number;
  state: "OPEN" | "CLOSED" | "MERGED";
  moved: boolean;
  forced?: boolean;
}

// A review worktree carries its mark in the box's locations.
export type ReviewWorktree = Worktree & { review?: ReviewMark };
export const reviewOf = (wt: Worktree): ReviewMark | undefined => (wt as ReviewWorktree).review;

export interface ReviewTarget {
  box: string;
  location: string;
  worktree: string;
}

export const prReviewApi = {
  plan: (c: Client, req: { link?: string; repo?: string; pr?: number; box?: string }) => c.laptop<ReviewSheet>("POST", "/v1/pr-review/plan", req),
  open: (c: Client, req: { repo: string; pr: number; sha: string; box: string }) => c.laptop<ReviewOpened>("POST", "/v1/pr-review/open", req),
  update: (c: Client, req: ReviewTarget & { sha: string }) => c.laptop<ReviewOpened>("POST", "/v1/pr-review/update", req),
  status: (c: Client, t: ReviewTarget) => c.laptop<ReviewStatus>("GET", `/v1/pr-review/status?${new URLSearchParams({ box: t.box, location: t.location, worktree: t.worktree })}`),
};

// The words an author association reads as on the sheet.
export function associationWords(association: string, org: string): string {
  switch (association) {
    case "OWNER":
      return `Owner of ${org}`;
    case "MEMBER":
      return `Member of ${org}`;
    case "COLLABORATOR":
      return `Collaborator on the repo`;
    case "CONTRIBUTOR":
      return `Contributor, not in ${org}`;
    case "FIRST_TIME_CONTRIBUTOR":
    case "FIRST_TIMER":
      return `First-time contributor, not in ${org}`;
    default:
      return `Not in ${org}`;
  }
}

export const shortSha = (sha: string) => sha.slice(0, 7);

// ---- The sheet ----

// The sheet shows one PR: from a link (open), or a review worktree's PR
// again (update: re-checks the author and the setup changes, and asks).
export interface SheetRequest {
  ref: ReviewRef;
  mode: "open" | "update";
  // update: the review worktree it moves.
  target?: ReviewTarget;
  // A link that was no review link: the sheet says so and nothing else.
  invalid?: string;
  // Why the sheet is shown again ("The PR moved on…").
  note?: string;
  // Bumped to read the PR again.
  nonce: number;
}

interface PrReviewState {
  sheet?: SheetRequest;
  // What each review worktree's PR has done since it was opened, by
  // "box:path".
  statuses: Record<string, ReviewStatus>;
  // Reviews someone chose to keep although clean-up was due, by "box:path".
  kept: Record<string, true>;
}

export const usePrReview = create<PrReviewState>()(() => ({
  statuses: {},
  kept: {},
}));

export function openReviewSheet(ref: ReviewRef, opts: Partial<Omit<SheetRequest, "ref" | "nonce">> = {}) {
  usePrReview.setState({
    sheet: {
      ref,
      mode: opts.mode ?? "open",
      target: opts.target,
      note: opts.note,
      nonce: Date.now(),
    },
  });
}

export function openInvalidReviewLink(raw: string) {
  usePrReview.setState({
    sheet: {
      ref: { repo: "", pr: 0 },
      mode: "open",
      invalid: raw,
      nonce: Date.now(),
    },
  });
}

export function closeReviewSheet() {
  usePrReview.setState({ sheet: undefined });
}

export const statusKey = (box: string, path: string) => `${box}:${path}`;

// refreshReviewStatus asks how a review worktree's PR moved on.
export async function refreshReviewStatus(c: Client, box: string, loc: Location, wt: Worktree) {
  if (!reviewOf(wt)) return;
  try {
    const st = await prReviewApi.status(c, {
      box,
      location: loc.name,
      worktree: wt.name,
    });
    usePrReview.setState((s) => ({
      statuses: { ...s.statuses, [statusKey(box, wt.path)]: st },
    }));
  } catch {
    // The PR can't be read now (offline, gh signed out): nothing to say.
  }
}

// openUpdate shows the sheet again for a review worktree's PR, to move it
// to the PR's latest commit.
export function openUpdate(box: string, loc: Location, wt: Worktree) {
  const r = reviewOf(wt);
  if (!r) return;
  openReviewSheet({ repo: r.repo, pr: r.pr }, { mode: "update", target: { box, location: loc.name, worktree: wt.name } });
}

export const linkFor = (r: Pick<ReviewMark, "repo" | "pr">) => reviewLink(r.repo, r.pr);
