// The rail's project tiles (components/sidebar/rail-layout.tsx) show the
// project's GitHub owner's avatar: the picture of the org or person a
// repository belongs to. These are the pure parts. No imports: pnpm test
// runs this file as it is.

// A GitHub login: letters, digits and single hyphens, not at either end,
// at most 39 characters.
const LOGIN = /^[a-z\d](?:[a-z\d]|-(?=[a-z\d])){0,38}$/i;

// githubOwner is the owner of a repository on GitHub, from its owner/name
// slug, or from its origin URL when there is no slug. A repository on
// another forge, or with no remote, has none.
export function githubOwner(slug?: string, remote?: string): string | undefined {
  const url = remote?.trim();
  if (!url || !/(^|[@/.])github\.com[:/]/i.test(url)) return undefined;
  const fromSlug = slug?.split("/")[0];
  if (fromSlug && LOGIN.test(fromSlug)) return fromSlug;
  const m = /github\.com[:/]+([^/]+)\//i.exec(url);
  return m && LOGIN.test(m[1]) ? m[1] : undefined;
}

// avatarUrl is where GitHub serves an owner's avatar, at size pixels (the
// tile is 36px; 96 stays sharp at 2x and 3x). GitHub's avatar host answers
// a login for people and orgs alike; the browser caches it.
export function avatarUrl(owner: string, size = 96): string {
  return `https://avatars.githubusercontent.com/${encodeURIComponent(owner)}?s=${size}`;
}
