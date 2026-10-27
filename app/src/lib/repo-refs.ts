// An issue, pull request or commit an agent names in its reply, as GitHub
// links them in a comment: #123 and acme/shop#45 to the issue or pull
// request (GitHub sends /issues/N on to /pull/N when it is one), a commit's
// hash to the commit. Only in a chat whose worktree's origin is on GitHub,
// which says whose #123 it is (components/conversation/repo-refs.tsx).
//
// What's left alone, so prose and code don't turn into links:
//   - #fff, #L14 (a line in a file), C#, ## headings, #0: a number, no
//     leading zero, right after the #.
//   - a # or hash run into a word, a path or a URL: page#123, a/b#4,
//     v1.2#3, &#123, x#12-y; a 64-character sha256 or a UUID's parts.
//   - deadbeef, cafe (letters only) and 1234567 (digits only): a hash has
//     7 to 40 lowercase hex characters, at least one a digit and one a–f.
// Kept apart from the app so it tests without it.

import { validRepo } from "./review-link.ts";

export type RefKind = "issue" | "commit";

export interface RepoRef {
  start: number;
  end: number;
  kind: RefKind;
  url: string;
}

// owner/repo#N, #N or a hash, not run into what's before or after it.
// Before: no letter, digit or _ . / # & @ - (so no path, URL, entity or
// colour). After: no letter, digit, _ / -, nor a . that goes on (a.js).
const REF = /(?<![\p{L}\p{N}_./#&@-])(?:([A-Za-z0-9][A-Za-z0-9-]{0,38})\/([A-Za-z0-9._-]{1,100})#([1-9]\d{0,6})|#([1-9]\d{0,6})|([0-9a-f]{7,40}))(?![\p{L}\p{N}_/-]|\.[\p{L}\p{N}])/gu;

// QUICK says whether a text could hold a ref at all, so a reply without
// one is passed over without walking it.
export const QUICK = /#[1-9]|[0-9a-f]{7}/;

const isHash = (s: string) => /\d/.test(s) && /[a-f]/.test(s);

// findRefs is every ref in text, in order, linked against repo ("owner/name"
// on GitHub). None without a repo.
export function findRefs(text: string, repo: string | undefined): RepoRef[] {
  if (!repo || !validRepo(repo) || !QUICK.test(text)) return [];
  const out: RepoRef[] = [];
  for (const m of text.matchAll(REF)) {
    const [whole, owner, name, n, own, sha] = m;
    const at = { start: m.index, end: m.index + whole.length };
    if (owner !== undefined) {
      const other = `${owner}/${name}`;
      if (validRepo(other)) out.push({ ...at, kind: "issue", url: `https://github.com/${other}/issues/${n}` });
    } else if (own !== undefined) {
      out.push({ ...at, kind: "issue", url: `https://github.com/${repo}/issues/${own}` });
    } else if (sha !== undefined && isHash(sha)) {
      out.push({ ...at, kind: "commit", url: `https://github.com/${repo}/commit/${sha}` });
    }
  }
  return out;
}

// wholeRef is the ref inline code is, when it is nothing else (`#123`,
// `a1b2c3d`). Code that only holds one, like `git show a1b2c3d`, is a
// command and stays as it is.
export function wholeRef(code: string, repo: string | undefined): RepoRef | undefined {
  const refs = findRefs(code, repo);
  return refs.length === 1 && refs[0].start === 0 && refs[0].end === code.length ? refs[0] : undefined;
}

const SCP = /^[\w.-]+@([\w.-]+):(.+)$/;

// githubRepo is "owner/name" for an origin on github.com, in any form
// (https://, ssh://, git@github.com:, or an SSH alias named github.com-*),
// as the box's GitHubSlug reads it; undefined for any other host.
export function githubRepo(remote: string | undefined): string | undefined {
  if (!remote) return undefined;
  let host: string;
  let path: string;
  const scp = SCP.exec(remote);
  if (scp) [, host, path] = scp;
  else {
    try {
      const u = new URL(remote);
      [host, path] = [u.hostname, u.pathname];
    } catch {
      return undefined;
    }
  }
  host = host.toLowerCase();
  if (host !== "github.com" && host !== "www.github.com" && host !== "ssh.github.com" && !host.startsWith("github.com-")) return undefined;
  const slug = path.replace(/^\/+|\/+$/g, "").replace(/\.git$/, "");
  return validRepo(slug) ? slug : undefined;
}
