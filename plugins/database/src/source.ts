// How a worktree is named in the helper: the same rule Leadline's helper
// uses (internal/leadline's attach.go), because the box daemon files a
// worktree's services under one name for every helper. Copied here, not
// imported: one plugin doesn't reach into another.

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

// sourceOf is the name a worktree's databases are filed under, and what
// db/connections takes as `source`: "<project>/<worktree>", or the project
// alone for its main checkout. The project is the repository as the box
// names it (its location).
export function sourceOf(w: { location: string; worktree: string; main?: boolean }): string {
  const project = namePart(w.location);
  const name = w.main ? "" : namePart(w.worktree);
  if (!project) return name;
  return !name || name === project ? project : `${project}/${name}`;
}
