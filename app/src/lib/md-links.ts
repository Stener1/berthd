// Where a Markdown file's links and pictures point, as the File tab's
// Preview follows them: a web address opens in the browser, a path opens
// that file of the worktree in a File tab, and #a-heading scrolls the page.
// Paths are the file's own folder's, as GitHub reads them, and a leading
// "/" is the top of the repository.

export type Link = { kind: "web"; url: string } | { kind: "file"; path: string; anchor?: string } | { kind: "anchor"; anchor: string };

const WEB = /^https?:\/\//i;
// mailto:, data:, file:, javascript: and the like: none is a worktree file.
const SCHEME = /^[a-z][a-z0-9+.-]*:/i;

// join resolves rel against the folder dir, as a URL path would, and is
// undefined when it climbs out of the worktree.
function join(dir: string, rel: string): string | undefined {
  const out = rel.startsWith("/") ? [] : dir.split("/").filter(Boolean);
  for (const part of rel.split("/")) {
    if (!part || part === ".") continue;
    if (part === "..") {
      if (!out.length) return undefined;
      out.pop();
    } else out.push(part);
  }
  return out.length ? out.join("/") : undefined;
}

const decode = (s: string) => {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
};

const dirOf = (path: string) => path.slice(0, Math.max(0, path.lastIndexOf("/")));

export function resolveLink(docPath: string, href: string): Link | undefined {
  const h = href.trim();
  if (!h) return undefined;
  if (WEB.test(h)) return { kind: "web", url: h };
  if (h.startsWith("#")) return h.length > 1 ? { kind: "anchor", anchor: decode(h.slice(1)) } : undefined;
  if (SCHEME.test(h) || h.startsWith("//")) return undefined;
  const hash = h.indexOf("#");
  const anchor = hash >= 0 ? decode(h.slice(hash + 1)) : "";
  const bare = (hash >= 0 ? h.slice(0, hash) : h).split("?")[0];
  const path = join(dirOf(docPath), decode(bare));
  if (!path) return undefined;
  return anchor ? { kind: "file", path, anchor } : { kind: "file", path };
}

// resolveImage is the worktree path a picture's src names, or undefined
// for one from the web (or a data: URL), which loads as it is.
export function resolveImage(docPath: string, src: string): string | undefined {
  const s = src.trim();
  if (!s || SCHEME.test(s) || s.startsWith("//")) return undefined;
  return join(dirOf(docPath), decode(s.split(/[?#]/)[0]));
}

// slug is a heading's anchor as GitHub makes it: lower case, punctuation
// gone, spaces to hyphens. "## Set up & run" is #set-up--run.
export const slug = (text: string) =>
  text
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s_-]/gu, "")
    .replace(/\s/g, "-");

// isMarkdown says whether a File tab offers Preview. MDX isn't: its JSX
// would show as text.
export const isMarkdown = (path: string) => /\.(md|markdown)$/i.test(path);
