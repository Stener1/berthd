// A file an agent names in its reply: the inline code `src/app.ts:14`, read
// as a path in the session's worktree and the lines it points at. This is
// the text alone; whether the file is really there is the box's to say
// (lib/file-links.ts asks it), so anything that only looks like a path
// (`event.data`, `react-dom`) stays plain once the box says it isn't one.

export interface FileRef {
  // Relative to the worktree's top, without "./".
  path: string;
  // The line it points at, and the last of a range.
  line?: number;
  end?: number;
}

// The ways agents write where in a file: `a.ts:14`, `a.ts:14:30` (line 14,
// column 30), `a.ts:14-30`, `a.ts#L14`, `a.ts#L14-L30`, `a.ts L14` and
// `a.ts (L14-30)` or `a.ts (lines 14-30)`.
const AT = [
  /^(.+?):(\d+)(?::\d+|[-–](\d+))?$/,
  /^(.+?)#L(\d+)(?:[-–]L?(\d+))?$/,
  /^(.+?)\s+\(?L(\d+)(?:[-–]L?(\d+))?\)?$/,
  /^(.+?)\s+\(lines?\s+(\d+)(?:[-–](\d+))?\)$/i,
];

// A path is made of these: no spaces, quotes, globs or shell.
const PATH = /^[\w@+~.\-[\]()/]+$/;
// Files whose names have no dot, known by name.
const BARE = /^(?:[A-Z][A-Za-z]*file|LICENSE|LICENCE|CODEOWNERS|README|CHANGELOG|AUTHORS|NOTICE)$/;

// parseRef reads inline code as a file and its lines, or undefined when it
// can't be one. root is the worktree's folder on its box: an absolute path
// inside it is read from there, one outside it is not this worktree's.
export function parseRef(code: string, root?: string): FileRef | undefined {
  let text = code.trim();
  if (!text || text.length > 300) return undefined;
  let line: number | undefined;
  let end: number | undefined;
  for (const re of AT) {
    const m = re.exec(text);
    if (!m) continue;
    text = m[1];
    line = Number(m[2]);
    end = m[3] ? Number(m[3]) : undefined;
    break;
  }
  if (line !== undefined && line < 1) return undefined;
  // A range that runs backwards is the line alone.
  if (end !== undefined && (end <= (line ?? 0) || end - (line ?? 0) > 10_000)) end = undefined;
  const path = relative(text, root);
  if (!path) return undefined;
  return { path, ...(line !== undefined ? { line } : {}), ...(end !== undefined ? { end } : {}) };
}

// relative is text as a path from the worktree's top, or undefined: a URL,
// a call (`foo()`), a command (`npm test`), a folder (`src/`), a path
// outside the worktree, or a bare word that names no file.
function relative(text: string, root?: string): string | undefined {
  if (/^[a-z][\w+.-]*:/i.test(text)) return undefined;
  if (!PATH.test(text) || text.endsWith("/") || text.includes("()") || text.endsWith(")")) return undefined;
  let p = text;
  if (p.startsWith("/")) {
    const top = root?.replace(/\/+$/, "");
    if (!top || !p.startsWith(`${top}/`)) return undefined;
    p = p.slice(top.length + 1);
  }
  while (p.startsWith("./")) p = p.slice(2);
  const parts = p.split("/");
  if (parts.some((s) => !s || s === "." || s === ".." || s.toLowerCase() === ".git" || s === "~")) return undefined;
  const name = parts[parts.length - 1];
  // One word alone is a file only by its name: `package.json`, `.env`,
  // `Makefile`; not `react` or `npm`.
  if (parts.length === 1 && !name.includes(".") && !BARE.test(name)) return undefined;
  // An extension is letters, not a version (`1.2.3`) or a number (`0.5`).
  if (parts.length === 1 && /^[\d.]+$/.test(name)) return undefined;
  return p;
}

// splitPath is the folder a path is in ("" for the top), and its name.
export function splitPath(path: string): { dir: string; name: string } {
  const i = path.lastIndexOf("/");
  return i < 0 ? { dir: "", name: path } : { dir: path.slice(0, i), name: path.slice(i + 1) };
}

// A folder's listing as the box sends it (GET …/files?dir=).
export interface FolderEntry {
  name: string;
  dir?: boolean;
}

// kindIn says what a folder's listing has by that name: a file, a folder,
// or nothing.
export function kindIn(entries: readonly FolderEntry[] | null | undefined, name: string): "file" | "dir" | undefined {
  const e = entries?.find((x) => x.name === name);
  return e ? (e.dir ? "dir" : "file") : undefined;
}

// lineSpan is the line and the last of its range a ref points at, inside
// a file of so many lines; none when it names the file alone, or a line
// past its end.
export function lineSpan(ref: FileRef, lines: number): { from: number; to: number } | undefined {
  if (ref.line === undefined || ref.line > lines) return undefined;
  return { from: ref.line, to: Math.min(ref.end ?? ref.line, lines) };
}

// excerpt is the lines to show around a span: a few before it, the span,
// and a few after, numbered as the file numbers them. A span past a screen
// shows from its start; with no span, the file's top.
export function excerpt(text: string, span: { from: number; to: number } | undefined, before = 3, rows = 16): { start: number; lines: string[] } {
  const all = text.replace(/\n$/, "").split("\n");
  const from = span ? Math.max(1, span.from - before) : 1;
  const to = Math.min(all.length, Math.max(from + rows - 1, span ? span.to + before : 0));
  return { start: from, lines: all.slice(from - 1, to) };
}

// lineLabel is a span in words for the card's head: "line 14", "lines 14–30".
export function lineLabel(ref: FileRef): string | undefined {
  if (ref.line === undefined) return undefined;
  return ref.end ? `lines ${ref.line}–${ref.end}` : `line ${ref.line}`;
}
