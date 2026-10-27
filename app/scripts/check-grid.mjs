// Fails when the app lays something out off the grid.
//
// Every edge and centre line comes from the grid's tokens (index.css, "The
// layout grid"): h-bar for the window's top band, h-bar-pane for a pane's
// or panel's toolbar, px-edge and pl-lane for where boxes and their
// content start in a column, px-page for a full-page view's gutter, w-rail
// for the rail. Two things break it, and this finds them:
//
// - A bar of its own height: a strip with a bottom border (or one that
//   drags the window) at h-7, h-9, h-12… instead of h-bar or h-bar-pane.
//   Each such height is a line that won't meet its neighbour's.
// - A gutter in raw pixels (pl-[11px], mr-[15px]): a step nobody chose,
//   and why two columns that should line up don't. Pixels inside calc()
//   with a grid token (a border's pixel taken off) are fine, and so are
//   small vertical nudges (mt-[3px]) that sit text on a control's line.
//
// coss ui's own parts (components/ui) keep theirs, and so do the
// illustrations (components/art). A file in ALLOW, or an element with a
// `grid-ok: why` comment on one of the two lines above it, is a deliberate
// exception and says why.
//
//   node scripts/check-grid.mjs            check app/src
//   node scripts/check-grid.mjs --count    how many each file has, most first
import { readdir, readFile } from "node:fs/promises";
import { join, relative, resolve } from "node:path";

const app = resolve(import.meta.dirname, "..");
const src = join(app, "src");

// file → why its off-grid values are on purpose.
const ALLOW = new Map([
  ["components/whats-new/art.tsx", "What's new's pictures: miniature mock screens drawn on a fixed canvas"],
  ["views/settings/theme-preview.tsx", "a theme card's drawing of a sidebar and a terminal, in miniature"],
  ["views/settings/chat-miniature.tsx", "the chat drawn at about 60% in the chat-background preview"],
  ["components/workspace/zen.tsx", "zen's strip keeps clear of the traffic lights by their width, pl-[84px]"],
]);

// A className string: a bar is one with a bottom border, or one that drags
// the window, and a fixed height that isn't the grid's.
const CLASS = /className=(?:"([^"]*)"|\{cn\(\s*"([^"]*)")/g;
const OFF_BAR = /(?<![\w-])h-(?:7|7\.5|8|9|10|11|12|14|\[[0-9.]+px\])(?![\w.[-])/;
const BAR = /(?<![\w-])(?:border-b|data-tauri-drag-region)(?![\w-])/;
// A horizontal padding or margin in raw pixels, outside calc().
const RAW_GUTTER = /(?<![\w-])-?(?:p|px|pl|pr|m|mx|ml|mr)-\[[0-9.]+px\](?![\w-])/g;

async function* files(dir) {
  for (const e of await readdir(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) {
      if ([join("components", "ui"), join("components", "art")].includes(relative(src, p))) continue;
      yield* files(p);
    } else if (/\.tsx$/.test(e.name)) yield p;
  }
}

const found = [];
for await (const f of files(src)) {
  const rel = relative(src, f);
  if (ALLOW.has(rel)) continue;
  const lines = (await readFile(f, "utf8")).split("\n");
  lines.forEach((line, i) => {
    if (/grid-ok:/.test(`${lines[i - 2] ?? ""} ${lines[i - 1] ?? ""} ${line}`)) return;
    // A strip's attributes can sit on the line before its className.
    const tag = `${lines[i - 1] ?? ""} ${line}`;
    for (const m of line.matchAll(CLASS)) {
      const cls = m[1] ?? m[2];
      const h = cls.match(OFF_BAR);
      if (h && (BAR.test(cls) || /data-tauri-drag-region/.test(tag))) found.push({ rel, line: i + 1, what: `${h[0]} on a bar`, fix: "use h-bar (the top band) or h-bar-pane (a pane's toolbar)" });
    }
    for (const m of line.matchAll(RAW_GUTTER)) found.push({ rel, line: i + 1, what: m[0], fix: "use the grid (px-edge, pl-lane, px-page) or the spacing scale" });
  });
}

if (process.argv.includes("--count")) {
  const by = new Map();
  for (const f of found) by.set(f.rel, (by.get(f.rel) ?? 0) + 1);
  for (const [rel, n] of [...by].sort((a, b) => b[1] - a[1])) console.log(`${String(n).padStart(4)} ${rel}`);
  console.log(`${found.length} off the grid in ${by.size} files`);
  process.exit(0);
}

if (found.length) {
  for (const f of found) console.error(`src/${f.rel}:${f.line}: ${f.what} — ${f.fix}`);
  console.error(`\n${found.length} off the grid. See the comment in scripts/check-grid.mjs and "The layout grid" in src/index.css.`);
  process.exit(1);
}
console.log("check-grid: everything is on the grid");
