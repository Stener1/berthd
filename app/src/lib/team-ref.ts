// What someone types or pastes to open a team setup (lib/team): an org
// name or a link. Kept apart from lib/team so it tests without the app.

// An org name as GitHub spells them: letters, digits and single dashes.
export const validOrg = (s: string) => /^[A-Za-z0-9](?:[A-Za-z0-9]|-(?=[A-Za-z0-9])){0,38}$/.test(s);

// teamRef takes what someone typed or pasted and gives what the routes
// take: an org name (calcom, @calcom, github.com/calcom, the org's .berth
// repo, berth://team?org=calcom), or a link to a setup elsewhere, as
// github.com/<owner>/<repo>[@ref] or github.com/<owner>/<repo>/tree/<ref>/<path>
// (with or without https://, or as berth://team?src=). An owner/repo typed
// after the field's github.com/ is a link too. undefined when it is neither.
export function teamRef(raw: string): string | undefined {
  let s = raw.trim();
  const q = /^berth:\/\/team\b[^?#]*\?([^#]*)/i.exec(s);
  if (q) {
    const p = new URLSearchParams(q[1]);
    s = p.get("src") ?? p.get("org") ?? "";
  }
  s = s.replace(/^@/, "").replace(/^(?:https?:\/\/)?(?:www\.)?github\.com\//i, "").replace(/[?#].*$/, "").replace(/\/+$/, "");
  const parts = s.split("/");
  if (parts.length === 1) return validOrg(parts[0]) ? parts[0] : undefined;
  const [owner, repoRef, ...rest] = parts;
  if (!validOrg(owner) || !repoRef) return undefined;
  const [repo, ref] = repoRef.split("@");
  if (!/^[A-Za-z0-9._-]+$/.test(repo) || ref === "") return undefined;
  // The org's own .berth is the org.
  if (repo.toLowerCase() === ".berth" && !ref && rest.length === 0) return owner;
  if (rest.length && (rest[0] !== "tree" || !rest[1] || ref)) return undefined;
  return `github.com/${[owner, repoRef, ...rest].join("/")}`;
}

export const isLink = (ref: string) => ref.startsWith("github.com/");
