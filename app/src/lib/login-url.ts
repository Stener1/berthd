// Logging a worktree's pages in as a seeded user. The laptop agent's proxy
// answers GET /__berth/login?as=EMAIL&next=/path on a worktree's private
// host: it has the box run the project's login script for that user,
// against that worktree's own dev server and database, sets the cookies it
// printed for that host alone, and redirects to next.
//
// This file has no imports, so anything can build the link: the Browser
// tab's "Log in as…" menu, the ⌘K palette, and a review link's sheet
// (berth://review?…&as=EMAIL&path=/x opens the worktree through it).

export const LOGIN_PATH = "/__berth/login";

export interface LoginUserLike {
  email: string;
  label?: string;
}

// safeNext keeps a path on the page's own host: it starts with one "/", is
// not "//host" or "/\host" (which browsers read as another host), and has no
// scheme, backslash or control character, decoded or not. Anything else is
// "/". The proxy checks again.
export function safeNext(next?: string): string {
  if (!next) return "/";
  const bad = (p: string) => !p.startsWith("/") || p.startsWith("//") || p.includes("\\") || /[\u0000-\u001f\u007f]/.test(p);
  if (bad(next)) return "/";
  let decoded = next;
  try {
    decoded = decodeURIComponent(next);
  } catch {
    return "/";
  }
  if (bad(decoded)) return "/";
  try {
    const base = "http://next.invalid";
    if (new URL(next, base).origin !== base) return "/";
  } catch {
    return "/";
  }
  return next;
}

// loginUrl is the link that opens origin (a worktree's private URL, such as
// http://checkout.shop.devl.localhost:1377) logged in as email, at next.
export function loginUrl(origin: string, email: string, next?: string): string {
  const base = origin.replace(/\/+$/, "");
  return `${base}${LOGIN_PATH}?as=${encodeURIComponent(email)}&next=${encodeURIComponent(safeNext(next))}`;
}

// loginUserLabel is how a user is shown: its label, else its email.
export function loginUserLabel(u: LoginUserLike): string {
  return u.label?.trim() || u.email;
}

// plausibleEmail is a quick check before asking; the box checks strictly.
export function plausibleEmail(s: string): boolean {
  return s.length <= 254 && /^[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}$/.test(s);
}

// nextFrom is the path (with its query and hash) of url when it is on
// origin's host, so logging in keeps the page you were on; "/" otherwise.
export function nextFrom(url: string | undefined, origin: string): string {
  if (!url) return "/";
  try {
    const u = new URL(url);
    const o = new URL(origin);
    if (u.host !== o.host) return "/";
    if (u.pathname === LOGIN_PATH) return safeNext(u.searchParams.get("next") ?? "/");
    return safeNext(`${u.pathname}${u.search}${u.hash}`);
  } catch {
    return "/";
  }
}
