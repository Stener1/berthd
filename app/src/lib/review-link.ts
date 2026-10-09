// A review link, berth://review?repo=OWNER/NAME&pr=N, opens a pull request
// for review on the reviewer's own box. The link carries no authority: a
// repository and a PR number, and at most a head commit as a hint. It is
// read strictly, and anything else makes it no link at all. The laptop
// agent reads it again (internal/prreview), and decides; this copy is so the
// app can refuse junk before it asks. Kept apart from the app so it tests
// without it.

export interface ReviewRef {
  repo: string;
  pr: number;
  // The head commit the link was made at: only a hint, compared with the
  // PR's head to warn, never used to pick what is checked out.
  sha?: string;
}

const PART = /^[A-Za-z0-9._-]+$/;
const MAX_PR = 2147483647;

// validRepo is "owner/name" as GitHub spells them, and nothing that could
// be a path (".", "..") or carry anything else.
export function validRepo(s: string): boolean {
  const parts = s.split("/");
  if (parts.length !== 2) return false;
  const [owner, name] = parts;
  if (!PART.test(owner) || !PART.test(name)) return false;
  if (owner === "." || owner === ".." || name === "." || name === "..") return false;
  return owner.length <= 39 && name.length <= 100;
}

// prNumber reads a positive integer as digits alone: no sign, no leading
// zero, no exponent, no spaces.
export function prNumber(s: string): number | undefined {
  if (!/^[1-9][0-9]{0,9}$/.test(s)) return undefined;
  const n = Number(s);
  return n <= MAX_PR ? n : undefined;
}

// parseReviewLink reads berth://review?repo=OWNER/NAME&pr=N[&sha=HEX]; any
// other parameter, one given twice, a fragment or a value out of shape makes
// it undefined.
export function parseReviewLink(raw: string): ReviewRef | undefined {
  const s = raw.trim();
  const m = /^berth:\/\/review\/?\?([^#]*)$/i.exec(s);
  if (!m || s.includes("#")) return undefined;
  const seen = new Map<string, string>();
  for (const pair of m[1].split("&")) {
    if (!pair) return undefined;
    const eq = pair.indexOf("=");
    if (eq <= 0) return undefined;
    const key = pair.slice(0, eq);
    let value: string;
    try {
      value = decodeURIComponent(pair.slice(eq + 1).replace(/\+/g, " "));
    } catch {
      return undefined;
    }
    if (!["repo", "pr", "sha"].includes(key) || seen.has(key)) return undefined;
    seen.set(key, value);
  }
  const repo = seen.get("repo");
  const pr = prNumber(seen.get("pr") ?? "");
  if (!repo || !validRepo(repo) || !pr) return undefined;
  const sha = seen.get("sha");
  if (sha !== undefined && !/^[0-9a-f]{7,40}$/i.test(sha)) return undefined;
  return { repo, pr, ...(sha ? { sha: sha.toLowerCase() } : {}) };
}

// parseReviewRef takes what someone pasted or typed: a review link, or
// OWNER/NAME#N as the CLI takes it.
export function parseReviewRef(raw: string): ReviewRef | undefined {
  const s = raw.trim();
  if (/^berth:/i.test(s)) return parseReviewLink(s);
  const m = /^([^#\s]+)#([^#\s]+)$/.exec(s);
  if (!m || !validRepo(m[1])) return undefined;
  const pr = prNumber(m[2]);
  return pr ? { repo: m[1], pr } : undefined;
}

// reviewLink is the link to paste in a PR's description or a chat.
export function reviewLink(repo: string, pr: number): string {
  return `berth://review?repo=${repo}&pr=${pr}`;
}
