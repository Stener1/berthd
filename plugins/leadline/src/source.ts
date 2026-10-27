// How a worktree is named in the helper, and how a statement is reduced to
// its shape. Both follow the Go side to the letter (internal/leadline's
// attach.go and ingest/fingerprint.go): the box daemon files a worktree's
// telemetry under the name made here, and the helper groups statements by
// the shape made here.

const enc = new TextEncoder();

// namePart makes a project's or a worktree's name safe to be one part of a
// source: no slash and no colon, which are the joins, and no white space or
// control characters; at most 60 bytes.
export function namePart(s: string): string {
  let out = "";
  for (const ch of s.trim()) {
    const c = ch.codePointAt(0)!;
    out += ch === "/" || ch === ":" || ch === "\\" || c <= 0x20 || c === 0x7f ? "-" : ch;
  }
  out = out.replace(/^-+|-+$/g, "");
  if (enc.encode(out).length <= 60) return out;
  // Cut at 60 bytes, never inside a character.
  let cut = "";
  let size = 0;
  for (const ch of out) {
    size += enc.encode(ch).length;
    if (size > 60) break;
    cut += ch;
  }
  return cut;
}

// sourceOf is the name a worktree's telemetry is filed under, and what every
// list route's `source` takes: "<project>/<worktree>", or the project alone
// for its main checkout. The project is the repository as the box names it
// (its location).
export function sourceOf(w: { location: string; worktree: string; main?: boolean }): string {
  const project = namePart(w.location);
  const name = w.main ? "" : namePart(w.worktree);
  if (!project) return name;
  return !name || name === project ? project : `${project}/${name}`;
}

// commandOf is the second command a service is for ("worker" of
// "shop/fix-orders:worker"), or "" for the worktree's own.
export function commandOf(service: string): string {
  const at = service.indexOf(":");
  return at < 0 ? "" : service.slice(at + 1);
}

// shapeOf reduces a statement to its shape, as the helper's fingerprint
// does: literals and placeholders become "?", an IN list of any length
// becomes "IN (?)", white space collapses and a closing semicolon is
// dropped. A trace carries each statement as it was sent, values and all;
// this is what is shown and sent in its place.
export function shapeOf(statement: string): string {
  let s = statement.replace(/'(?:[^']|'')*'/g, "?");
  // "::" is a Postgres cast, not a named parameter.
  s = s.replaceAll("::", "\u0000");
  s = s.replace(/\$\d+/g, "?");
  s = s.replace(/(^|[\s(,=<>])[:@]\w+/g, "$1?");
  s = s.replaceAll("\u0000", "::");
  s = s.replace(/\b\d+(?:\.\d+)?\b/g, "?");
  s = s.replace(/\bIN\s*\(\s*\?(?:\s*,\s*\?)*\s*\)/gi, "IN (?)");
  s = s.replace(/\s+/g, " ").trim().replace(/[; ]+$/, "");
  return s.length > 2000 ? s.slice(0, 2000) : s;
}

// tableOf is the table a statement is about, when it names one: the word to
// search for it by, and to call it by in a sentence.
export function tableOf(shape: string): string {
  const m = /\b(?:FROM|INTO|UPDATE|JOIN)\s+(?:ONLY\s+)?"?([A-Za-z_][\w.]*)"?/i.exec(shape);
  return m ? m[1] : "";
}
