import { type Flowchart, parseFlowchart } from "./art/mermaid.ts";

// A code block in a reply that the chat draws instead of showing it as
// text (components/conversation/code-blocks.tsx): a Mermaid flowchart as
// its diagram, a diff as the chat's edits are drawn. What can't be drawn
// stays the code it was.

export type DiffShape =
  // A patch with its files' headers (--- and +++): files, named and
  // numbered by their own lines.
  | "files"
  // Hunks (@@ -a,b +c,d @@) with no file: numbered, unnamed.
  | "hunks"
  // Lines led by +, - and a space, nothing else: no numbers, as there's
  // nothing to count them from.
  | "lines";

export type DrawnBlock =
  | { kind: "flowchart"; chart: Flowchart }
  // Mermaid that isn't a flowchart Shipyard can draw (a sequence diagram,
  // a gantt): its source, said so.
  | { kind: "mermaid" }
  // The block as a patch the diff renderer reads.
  | { kind: "diff"; shape: DiffShape; patch: string };

// Longer blocks stay as they are: drawing them would cost more than it
// gives (code-highlight.ts leaves them plain, too).
const MAX_LINES = 2000;

// drawnBlock is how a block whose fence names lang (or, unnamed, whose
// language was guessed) draws, if it does.
export function drawnBlock(lang: string, text: string): DrawnBlock | undefined {
  if (!text.trim() || text.split("\n").length > MAX_LINES) return undefined;
  if (lang === "mermaid") {
    const chart = parseFlowchart(text);
    return chart?.nodes.length ? { kind: "flowchart", chart } : { kind: "mermaid" };
  }
  if (lang === "diff" || lang === "patch") {
    const d = diffPatch(text);
    return d && { kind: "diff", ...d };
  }
  return undefined;
}

const HUNK = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@(.*)$/;
const GIT = /^(diff --git |index [0-9a-f]+\.\.|new file mode |deleted file mode |similarity index |rename (from|to) )/;
// The name the renderer gets for a change that doesn't name its file.
const UNNAMED = "change";

const fileHead = (lines: string[], i: number) => lines[i].startsWith("--- ") && lines[i + 1]?.startsWith("+++ ");

// diffPatch reads a diff block as a patch for the renderer, and its shape;
// undefined when it isn't one. Agents write diffs by hand: a hunk's counts
// are worked out again from its lines, and an empty line inside a hunk is
// a context line whose space got trimmed.
export function diffPatch(text: string): { shape: DiffShape; patch: string } | undefined {
  const lines = text.replace(/\r\n?/g, "\n").replace(/\n+$/, "").split("\n");
  const files = lines.some((_, i) => fileHead(lines, i));
  if (lines.some((l) => HUNK.test(l))) {
    const body = recount(lines);
    if (!body) return undefined;
    return files ? { shape: "files", patch: body } : { shape: "hunks", patch: `--- a/${UNNAMED}\n+++ b/${UNNAMED}\n${body}` };
  }
  // Headers with no hunk (a rename, a binary file) have no lines to draw.
  if (files || lines.some((l) => GIT.test(l))) return undefined;
  if (!lines.some((l) => /^[+-]/.test(l))) return undefined;
  // Bare lines: one hunk. A line led by neither is context, as written.
  const body = lines.map((l) => (/^[+\- \\]/.test(l) ? l : ` ${l}`));
  const old = body.filter((l) => l[0] === " " || l[0] === "-").length;
  const next = body.filter((l) => l[0] === " " || l[0] === "+").length;
  return { shape: "lines", patch: `--- a/${UNNAMED}\n+++ b/${UNNAMED}\n@@ -1,${old} +1,${next} @@\n${body.join("\n")}\n` };
}

// recount writes each hunk's header again with the lines it has. Lines
// before the first hunk and between files (headers) pass as they are; in
// a git patch, a file someone added by hand without git's own first line
// gets one, as the renderer starts a file there.
function recount(lines: string[]): string | undefined {
  const out: string[] = [];
  const git = lines.some((l) => l.startsWith("diff --git "));
  let named = false;
  let hunk: { at: number; a: string; c: string; rest: string; old: number; next: number } | undefined;
  const close = () => {
    if (hunk) out[hunk.at] = `@@ -${hunk.a},${hunk.old} +${hunk.c},${hunk.next} @@${hunk.rest}`;
    hunk = undefined;
  };
  for (let i = 0; i < lines.length; i++) {
    const l = lines[i];
    const m = HUNK.exec(l);
    if (m) {
      close();
      hunk = { at: out.length, a: m[1], c: m[2], rest: m[3], old: 0, next: 0 };
      out.push(l);
      continue;
    }
    if (fileHead(lines, i)) {
      close();
      if (git && !named) out.push(gitLines(l.slice(4), lines[i + 1].slice(4)));
      named = false;
      out.push(l);
      continue;
    }
    if (!hunk || GIT.test(l)) {
      close();
      if (l.startsWith("diff --git ")) named = true;
      out.push(l);
      continue;
    }
    if (l.startsWith("\\")) {
      out.push(l);
      continue;
    }
    // Empty lines before the next header (or the end) part hunks; others
    // are context.
    if (l === "") {
      let j = i;
      while (j < lines.length && lines[j] === "") j++;
      if (j === lines.length || HUNK.test(lines[j]) || GIT.test(lines[j]) || fileHead(lines, j)) {
        i = j - 1;
        close();
        continue;
      }
    }
    const line = /^[+\- ]/.test(l) ? l : ` ${l}`;
    if (line[0] !== "+") hunk.old++;
    if (line[0] !== "-") hunk.next++;
    out.push(line);
  }
  close();
  return out.length ? `${out.join("\n")}\n` : undefined;
}

// gitLines are git's first lines for a file, from its --- and +++ names:
// which file, and whether it's new or gone.
function gitLines(from: string, to: string): string {
  const name = (n: string) => n.replace(/\t.*$/, "").replace(/^[ab]\//, "");
  const [a, b] = [name(from), name(to)];
  const head = `diff --git a/${a === "/dev/null" ? b : a} b/${b === "/dev/null" ? a : b}`;
  return a === "/dev/null" ? `${head}\nnew file mode 100644` : b === "/dev/null" ? `${head}\ndeleted file mode 100644` : head;
}

type ChangeType = "change" | "rename-pure" | "rename-changed" | "new" | "deleted";

// tidyFile names a file as git would: a patch without git's own header
// (diff -u, or written by hand) leaves the a/ and b/ on its names and
// reads every file as renamed, or one side as /dev/null.
export function tidyFile<T extends { name: string; prevName?: string; type: ChangeType }>(f: T): T {
  // diff -u puts the file's time after a tab.
  let name = f.name.replace(/\t.*$/, "");
  if (f.prevName === undefined) return { ...f, name };
  let prev = f.prevName.replace(/\t.*$/, "");
  if ((prev === "/dev/null" || prev.startsWith("a/")) && (name === "/dev/null" || name.startsWith("b/"))) {
    prev = prev.replace(/^a\//, "");
    name = name.replace(/^b\//, "");
  }
  if (prev === "/dev/null") return { ...f, name, prevName: undefined, type: "new" };
  if (name === "/dev/null") return { ...f, name: prev, prevName: undefined, type: "deleted" };
  if (prev === name) return { ...f, name, prevName: undefined, type: "change" };
  return { ...f, name, prevName: prev };
}
