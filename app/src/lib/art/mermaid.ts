// A small reader for the Mermaid flowcharts agents write: graph or
// flowchart, LR/RL/TD/TB/BT; the usual node shapes ([ ], ( ), ([ ]), [[ ]],
// [( )], (( )), ((( ))), > ], { }, {{ }}, [/ /] and its slants, and
// A@{ shape: … }); edges -->, ---, -.->, ==>, ~~~, --o, --x, <-->, with
// labels (-- x -->, -->|x|, -. x .->, == x ==>), chains a --> b --> c and
// a & b --> c; and subgraphs, nested. Labels lose their quotes, a <br>
// breaks the line and entity codes (#quot;, &amp;) are decoded. Other
// diagram types (sequence, class, gantt…) are shown as their source.
// Mermaid itself isn't bundled: its renderer is large and has had XSS bugs;
// this draws text only.

export type Shape =
  | "box"
  | "round"
  | "stadium"
  | "subroutine"
  | "store"
  | "circle"
  | "double-circle"
  | "asym"
  | "diamond"
  | "hexagon"
  | "lean-r"
  | "lean-l"
  | "trapezoid"
  | "trapezoid-alt";

export type Dir = "LR" | "TD";

export interface MNode {
  id: string;
  // Lines are split by "\n" (a <br> in the source).
  label: string;
  shape: Shape;
  // The innermost subgraph it's in.
  group?: string;
}

export interface MEdge {
  from: string;
  to: string;
  label?: string;
  dotted?: boolean;
  thick?: boolean;
  arrow: boolean;
  // An end other than an arrow: --o, --x.
  tip?: "circle" | "cross";
  // Marked at both ends: <-->, o--o.
  both?: boolean;
  // ~~~: placed by, not drawn.
  hidden?: boolean;
}

export interface MGroup {
  id: string;
  label: string;
  parent?: string;
  dir?: Dir;
}

export interface Flowchart {
  dir: Dir;
  nodes: MNode[];
  edges: MEdge[];
  groups: MGroup[];
  // Nodes and subgraphs as they first appear, which layout keeps to.
  order: string[];
}

// ---- Labels ----

const ENTITIES: Record<string, string> = { quot: '"', amp: "&", lt: "<", gt: ">", apos: "'", nbsp: " ", ndash: "–", mdash: "—", hellip: "…", middot: "·", times: "×", deg: "°", larr: "←", rarr: "→", harr: "↔", uarr: "↑", darr: "↓", le: "≤", ge: "≥", ne: "≠", copy: "©", hearts: "♥", check: "✓" };

function entity(name: string): string | undefined {
  if (name[0] !== "#") return ENTITIES[name.toLowerCase()];
  const n = /^#x/i.test(name) ? Number.parseInt(name.slice(2), 16) : Number.parseInt(name.slice(1), 10);
  return n >= 32 && n <= 0x10ffff ? String.fromCodePoint(n) : undefined;
}

// labelText is a label as written made into the text drawn: its quotes
// off, a markdown string's backticks and ** gone, <br> a new line, other
// inline tags dropped, and Mermaid's #quot; codes and HTML's &quot;
// decoded. It stays text: nothing here is ever HTML.
export function labelText(raw: string): string {
  let s = raw.trim();
  if (s.length >= 2 && (s[0] === '"' || s[0] === "'") && s.endsWith(s[0])) s = s.slice(1, -1);
  if (s.length >= 2 && s[0] === "`" && s.endsWith("`"))
    s = s
      .slice(1, -1)
      .replace(/\*\*(.+?)\*\*|__(.+?)__/g, "$1$2")
      .replace(/(^|[^\w*])\*(?!\s)(.+?)\*(?!\w)/g, "$1$2")
      .replace(/(^|[^\w])_(?!\s)(.+?)_(?!\w)/g, "$1$2");
  s = s
    .replace(/<br\s*\/?>/gi, "\n")
    .replace(/<\/?(?:b|i|u|s|em|strong|small|big|code|sub|sup|span|font|mark|del|ins|p|div)\b[^>]*>/gi, "")
    .replace(/(?<!&)#(\w+);/g, (m, n: string) => entity(/^\d+$/.test(n) ? `#${n}` : n) ?? m)
    .replace(/&(#\d+|#x[0-9a-f]+|\w+);/gi, (m, n: string) => entity(n) ?? m);
  const lines = s.split("\n").map((l) => l.replace(/\s+/g, " ").trim());
  while (lines.length > 1 && !lines[0]) lines.shift();
  while (lines.length > 1 && !lines[lines.length - 1]) lines.pop();
  return lines.join("\n");
}

// ---- Parsing ----

// find is the first needle at or after from that isn't inside quotes;
// with the quotes unbalanced, the first anywhere.
function find(s: string, from: number, needle: string): number {
  let quote = false;
  for (let i = from; i < s.length; i++) {
    if (s[i] === '"') quote = !quote;
    else if (!quote && s.startsWith(needle, i)) return i;
  }
  return s.indexOf(needle, from);
}

// statements splits a line at ;s outside quotes and brackets, leaving the
// ; of an entity code (#quot;, &amp;) alone.
function statements(line: string): string[] {
  const out: string[] = [];
  let depth = 0;
  let quote = false;
  let start = 0;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (c === '"') quote = !quote;
    else if (quote) continue;
    else if (c === "[" || c === "(" || c === "{") depth++;
    else if (c === "]" || c === ")" || c === "}") depth = Math.max(0, depth - 1);
    else if (c === ";" && depth === 0 && !/[#&]#?\w+$/.test(line.slice(start, i))) {
      out.push(line.slice(start, i));
      start = i + 1;
    }
  }
  out.push(line.slice(start));
  return out.map((s) => s.trim()).filter(Boolean);
}

// The newer A@{ shape: … } names, those drawn; any other is a box.
const NAMED: Record<string, Shape> = {
  rounded: "round",
  event: "round",
  stadium: "stadium",
  pill: "stadium",
  terminal: "stadium",
  subproc: "subroutine",
  subroutine: "subroutine",
  "fr-rect": "subroutine",
  cyl: "store",
  cylinder: "store",
  database: "store",
  db: "store",
  circle: "circle",
  circ: "circle",
  "dbl-circ": "double-circle",
  "double-circle": "double-circle",
  odd: "asym",
  diam: "diamond",
  diamond: "diamond",
  decision: "diamond",
  question: "diamond",
  hex: "hexagon",
  hexagon: "hexagon",
  prepare: "hexagon",
  "lean-r": "lean-r",
  "lean-right": "lean-r",
  "in-out": "lean-r",
  "lean-l": "lean-l",
  "lean-left": "lean-l",
  "out-in": "lean-l",
  "trap-b": "trapezoid",
  trapezoid: "trapezoid",
  priority: "trapezoid",
  "trap-t": "trapezoid-alt",
  "inv-trapezoid": "trapezoid-alt",
  manual: "trapezoid-alt",
};

const ID = /[\p{L}\p{N}_]+(?:-[\p{L}\p{N}_]+)*/uy;
const CLASS = /:::[\w-]+/y;
const EDGE_ID = /[\w-]+@(?=[-=<ox~])/y;
// An edge with no label in its middle, maybe one in pipes after: -->, ---,
// -.->, -.-, ==>, ===, ~~~, --o, --x, <-->, o--o; longer ones too.
const PLAIN = /(<|[ox](?=[-=]))?(?:(-{2,}|={2,}|-\.+-)(>|[ox](?![\p{L}\p{N}_]))|(-{3,}|={3,}|-\.+-|~{3,}))\s*(?:\|([^|]*)\|)?/uy;
// An edge with its label in the middle: -- x -->, == x ==>, -. x .->, and
// the same with a quoted label, which may hold anything.
const ENDS = String.raw`(-{2,}>|-{3,}|={2,}>|={3,}|\.-+>|\.-+|-{2,}[ox]|={2,}[ox])(?![-=.>])`;
const QUOTED = new RegExp(String.raw`(<)?(--|==|-\.)\s*("[^"]*")\s*${ENDS}`, "y");
const LABELLED = new RegExp(String.raw`(<)?(--|==|-\.)\s*(.*?)\s*${ENDS}`, "y");

interface Op {
  label?: string;
  dotted: boolean;
  thick: boolean;
  arrow: boolean;
  tip?: "circle" | "cross";
  both: boolean;
  hidden: boolean;
}

const dirOf = (s: string | undefined): Dir => (/^(TD|TB|BT)$/i.test(s ?? "") ? "TD" : "LR");

export function parseFlowchart(src: string): Flowchart | undefined {
  let lines = src
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l && !l.startsWith("%%"));
  // Front matter (--- title: … ---) comes before the diagram.
  if (lines[0] === "---") {
    const close = lines.indexOf("---", 1);
    lines = close > 0 ? lines.slice(close + 1) : lines;
  }
  const stmts = lines.flatMap(statements);
  const head = stmts.shift() ?? "";
  const hm = /^(graph|flowchart)\b\S*\s*(LR|RL|TD|TB|BT)?/i.exec(head);
  if (!hm) return undefined;
  const dir = dirOf(hm[2]);
  const nodes = new Map<string, MNode>();
  const groups = new Map<string, MGroup>();
  const order: string[] = [];
  const edges: MEdge[] = [];
  const stack: string[] = [];

  const seen = (id: string) => {
    if (!nodes.has(id) && !groups.has(id)) order.push(id);
  };
  // within is whether group a is inside group b.
  const within = (a: string | undefined, b: string) => {
    for (let p = a, k = 0; p && k < 64; p = groups.get(p)?.parent, k++) if (p === b) return true;
    return false;
  };
  // A node is in the subgraph it's first mentioned in; mentioned first
  // outside any, the next subgraph to mention it takes it, as does one
  // inside the subgraph it's in.
  const claim = (n: MNode) => {
    const top = stack[stack.length - 1];
    if (top && top !== n.group && (n.group === undefined || within(top, n.group))) n.group = top;
  };
  const node = (id: string, label?: string, shape: Shape = "box") => {
    if (groups.has(id)) return id;
    seen(id);
    const had = nodes.get(id);
    // A later mention with a label relabels it; a bare one leaves it be.
    let n = had;
    if (!n || label !== undefined) {
      n = { id, label: label === undefined ? id : labelText(label), shape: label === undefined ? "box" : shape };
      if (had?.group) n.group = had.group;
    }
    nodes.set(id, n);
    claim(n);
    return id;
  };

  const chain = (s: string) => {
    let i = 0;
    const ws = () => {
      while (i < s.length && /\s/.test(s[i])) i++;
    };
    const sticky = (re: RegExp) => {
      re.lastIndex = i;
      const m = re.exec(s);
      if (m) i = re.lastIndex;
      return m;
    };
    // shapeAt reads a node's shape and label, if one follows its id.
    const shapeAt = (): [string, Shape] | undefined => {
      const c = s[i];
      const take = (open: number, close: string, shape: Shape): [string, Shape] | undefined => {
        const j = find(s, i + open, close);
        if (j < 0) return undefined;
        const label = s.slice(i + open, j);
        i = j + close.length;
        return [label, shape];
      };
      if (c === "[") {
        const j = find(s, i + 1, "]");
        if (j < 0) return undefined;
        const inner = s.slice(i + 1, j);
        if (inner[0] === "[" && s[j + 1] === "]") {
          i = j + 2;
          return [inner.slice(1), "subroutine"];
        }
        i = j + 1;
        if (inner.length >= 2 && inner[0] === "(" && inner.endsWith(")")) return [inner.slice(1, -1), "store"];
        if (inner.length >= 2 && (inner[0] === "/" || inner[0] === "\\") && /[/\\]$/.test(inner)) {
          const [a, b] = [inner[0], inner[inner.length - 1]];
          return [inner.slice(1, -1), a === "/" ? (b === "/" ? "lean-r" : "trapezoid") : b === "\\" ? "lean-l" : "trapezoid-alt"];
        }
        return [inner, "box"];
      }
      if (c === "(") {
        if (s.startsWith("(((", i)) return take(3, ")))", "double-circle");
        if (s.startsWith("((", i)) return take(2, "))", "circle");
        if (s.startsWith("([", i)) return take(2, "])", "stadium");
        return take(1, ")", "round");
      }
      if (c === "{") return s.startsWith("{{", i) ? take(2, "}}", "hexagon") : take(1, "}", "diamond");
      if (c === ">") return take(1, "]", "asym");
      return undefined;
    };
    const readNode = (): string | undefined => {
      const m = sticky(ID);
      if (!m) return undefined;
      const id = m[0];
      sticky(CLASS);
      if (s.startsWith("@{", i)) {
        const j = find(s, i + 2, "}");
        const body = s.slice(i + 2, j < 0 ? s.length : j);
        i = j < 0 ? s.length : j + 1;
        const lm = /label\s*:\s*(?:"([^"]*)"|'([^']*)'|([^,}]+))/.exec(body);
        const sm = /shape\s*:\s*([\w-]+)/.exec(body);
        const label = lm ? (lm[1] ?? lm[2] ?? lm[3]) : undefined;
        if (lm || sm) return node(id, label ?? nodes.get(id)?.label ?? id, NAMED[sm?.[1]?.toLowerCase() ?? ""] ?? "box");
        return node(id);
      }
      const shaped = shapeAt();
      sticky(CLASS);
      return shaped ? node(id, shaped[0], shaped[1]) : node(id);
    };
    const readNodes = (): string[] | undefined => {
      const out: string[] = [];
      for (;;) {
        ws();
        const id = readNode();
        if (!id) return out.length ? out : undefined;
        out.push(id);
        ws();
        if (s[i] !== "&") return out;
        i++;
      }
    };
    const readOp = (): Op | undefined => {
      sticky(EDGE_ID);
      const p = sticky(PLAIN);
      const m = p ? undefined : (sticky(QUOTED) ?? sticky(LABELLED));
      const parts = p ? [p[1], p[2] ?? p[4], p[3] ?? "", p[5]] : m ? [m[1], m[2] + m[4], m[4].slice(-1), m[3]] : undefined;
      if (!parts) return undefined;
      const [tail, body, head, label] = parts;
      const end = /[>ox]/.test(head) ? head : "";
      return {
        label: label === undefined ? undefined : labelText(label).replace(/\n/g, " ") || undefined,
        dotted: body.includes("."),
        thick: body.includes("="),
        arrow: end === ">",
        tip: end === "o" ? "circle" : end === "x" ? "cross" : undefined,
        both: !!tail,
        hidden: body.startsWith("~"),
      };
    };
    // read says whether the line was read: to its end, or as far as an
    // edge at least.
    const read = (): boolean => {
      const had = edges.length;
      let left = readNodes();
      if (!left) return false;
      for (;;) {
        ws();
        if (i >= s.length) return true;
        const op = readOp();
        const right = op && readNodes();
        if (!op || !right) return edges.length > had;
        for (const from of left)
          for (const to of right)
            edges.push({ from, to, label: op.label, dotted: op.dotted, thick: op.thick, arrow: op.arrow, ...(op.tip && { tip: op.tip }), ...(op.both && { both: true }), ...(op.hidden && { hidden: true }) });
        left = right;
      }
    };
    // A line that isn't one (prose, half an edge) adds nothing, not even
    // the word it started with.
    const kept = new Map([...nodes].map(([id, n]) => [id, { ...n }]));
    const seenTo = order.length;
    if (!read()) {
      nodes.clear();
      for (const [id, n] of kept) nodes.set(id, n);
      order.length = seenTo;
    }
  };

  const subgraph = (rest: string) => {
    let id: string;
    let label: string;
    const m = /^([^\s[\]"]+)\s*\[(.*)\]$/.exec(rest);
    if (m) [id, label] = [m[1], m[2]];
    else [id, label] = [labelText(rest), rest];
    if (!id) id = `subgraph-${groups.size + 1}`;
    const parent = stack[stack.length - 1];
    if (!groups.has(id) && !within(parent, id)) {
      seen(id);
      // Named in an edge before it was opened, it was taken for a node.
      nodes.delete(id);
      groups.set(id, { id, label: labelText(label), ...(parent && { parent }) });
    }
    stack.push(id);
  };

  for (const s of stmts) {
    try {
      const sg = /^subgraph\b\s*(.*)$/.exec(s);
      if (sg) subgraph(sg[1].trim());
      else if (s === "end") stack.pop();
      else if (/^direction\b/.test(s)) {
        const top = groups.get(stack[stack.length - 1] ?? "");
        const d = /^direction\s+(LR|RL|TD|TB|BT)\b/i.exec(s);
        if (top && d) top.dir = dirOf(d[1]);
      } else if (/^(classDef|class|style|linkStyle|click|accTitle|accDescr)\b/.test(s)) continue;
      else chain(s);
    } catch {
      // A line this can't read is left out, never the whole chart.
    }
  }
  return { dir, nodes: [...nodes.values()], edges, groups: [...groups.values()], order: order.filter((id) => nodes.has(id) || groups.has(id)) };
}

// ---- Layout ----

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Layout {
  // Every node's and subgraph's box.
  box: Record<string, Rect>;
  // Each edge's flow: across (LR) or down, the flow of the subgraph that
  // holds both its ends.
  edgeLr: boolean[];
  W: number;
  H: number;
  lr: boolean;
}

// A node is NH tall, LINE more for each line after its first; nodes are
// as wide as the chart's longest line (up to CAP characters) or, in a
// chart with subgraphs, as their own, so the groups stay compact.
export const NH = 38;
export const LINE = 15;
export const CAP = 30;
const GX = 64;
const GY = 22;
// A subgraph's box: its padding, and the band its title sits in.
export const GPAD = 14;
export const GHEAD = 28;

export const lines = (label: string) => label.split("\n");
const longest = (label: string) => Math.max(...lines(label).map((l) => [...l].length));
const nodeW = (chars: number) => Math.round(Math.max(8, Math.min(chars, CAP)) * 6.8 + 28);

interface Item {
  id: string;
  w: number;
  h: number;
}

// backEdges are the edges that close a cycle, found depth first in the
// order things were written; layering leaves them out, so a loop doesn't
// push its nodes down without end.
function backEdges(items: Item[], edges: { from: string; to: string }[]): Set<number> {
  const out = new Map<string, number[]>();
  edges.forEach((e, k) => out.set(e.from, [...(out.get(e.from) ?? []), k]));
  const state = new Map<string, 1 | 2>();
  const back = new Set<number>();
  const visit = (id: string) => {
    state.set(id, 1);
    for (const k of out.get(id) ?? []) {
      const t = edges[k].to;
      if (state.get(t) === 1) back.add(k);
      else if (!state.has(t)) visit(t);
    }
    state.set(id, 2);
  };
  for (const x of items) if (!state.has(x.id)) visit(x.id);
  return back;
}

// place puts items in layers along the flow (longest path from a root),
// ordered within a layer by where their parents are, each layer centred
// across the flow.
function place(items: Item[], edges: { from: string; to: string; dotted?: boolean }[], lr: boolean) {
  const layer: Record<string, number> = {};
  for (const x of items) layer[x.id] = 0;
  const back = backEdges(items, edges);
  for (let i = 0; i < items.length; i++)
    edges.forEach((e, k) => {
      if (!back.has(k) && layer[e.to] <= layer[e.from] && !(e.dotted && layer[e.to] > 0)) layer[e.to] = Math.min(layer[e.from] + 1, items.length);
    });
  const n = Math.max(0, ...Object.values(layer)) + 1;
  const cols = Array.from({ length: n }, () => [] as Item[]);
  for (const x of items) cols[layer[x.id]].push(x);
  const layers = cols.filter((c) => c.length);
  const along = (x: Item) => (lr ? x.w : x.h);
  const across = (x: Item) => (lr ? x.h : x.w);
  const gapAlong = lr ? GX : GY * 2.5;
  const gapAcross = lr ? GY : GX;
  // mid is an item's centre across the flow, from the middle line.
  const mid: Record<string, number> = {};
  const spans = layers.map((c, ci) => {
    if (ci > 0) {
      const bary = (x: Item) => {
        const ps = edges.filter((e) => e.to === x.id && mid[e.from] !== undefined).map((e) => mid[e.from]);
        return ps.length ? ps.reduce((a, b) => a + b, 0) / ps.length : 0;
      };
      c.sort((a, b) => bary(a) - bary(b));
    }
    const total = c.reduce((t, x) => t + across(x), 0) + (c.length - 1) * gapAcross;
    let off = -total / 2;
    for (const x of c) {
      mid[x.id] = off + across(x) / 2;
      off += across(x) + gapAcross;
    }
    return total;
  });
  const thick = layers.map((c) => Math.max(...c.map(along)));
  const A = thick.reduce((t, x) => t + x, 0) + (layers.length - 1) * gapAlong;
  const C = Math.max(0, ...spans);
  const at: Record<string, { x: number; y: number }> = {};
  let start = 0;
  layers.forEach((c, ci) => {
    for (const x of c) {
      // Across, a layer's items are centred on it; down, their tops line
      // up, so a node beside a tall subgraph sits by its top.
      const a = start + (lr ? (thick[ci] - along(x)) / 2 : 0);
      const b = C / 2 + mid[x.id] - across(x) / 2;
      at[x.id] = lr ? { x: a, y: b } : { x: b, y: a };
    }
    start += thick[ci] + gapAlong;
  });
  return { at, W: lr ? A : C, H: lr ? C : A };
}

// layoutFlowchart lays a chart out level by level: a subgraph is laid out
// inside, then placed in its parent as one block of that size, so its
// nodes stay together and nested ones nest. An edge between levels counts,
// for placing, as one between the blocks that hold its ends. A subgraph's
// own direction holds unless a node in it links straight out, as Mermaid
// does.
export function layoutFlowchart(g: Flowchart): Layout {
  const { groups } = g;
  const nodeOf = new Map(g.nodes.map((n) => [n.id, n]));
  const groupOf = new Map(groups.map((x) => [x.id, x]));
  const parent = (id: string) => (nodeOf.has(id) ? nodeOf.get(id)?.group : groupOf.get(id)?.parent);
  const rank = new Map(g.order.map((id, i) => [id, i]));
  const kids = new Map<string | undefined, string[]>();
  const all = [...nodeOf.keys(), ...groupOf.keys()];
  all.sort((a, b) => (rank.get(a) ?? 1e9) - (rank.get(b) ?? 1e9));
  for (const id of all) {
    const p = parent(id);
    kids.set(p, [...(kids.get(p) ?? []), id]);
  }
  const ancestors = (id: string) => {
    const out: string[] = [];
    for (let p = parent(id); p && out.length < 64; p = parent(p)) out.push(p);
    return out;
  };
  const inside = (id: string, gid: string) => ancestors(id).includes(gid);
  const flow = new Map<string, Dir>();
  const dirIn = (gid: string | undefined): Dir => {
    if (gid === undefined) return g.dir;
    const had = flow.get(gid);
    if (had) return had;
    const gr = groupOf.get(gid);
    const up = dirIn(gr?.parent);
    const leaks = g.edges.some((e) => nodeOf.has(e.from) && nodeOf.has(e.to) && inside(e.from, gid) !== inside(e.to, gid));
    const d = gr?.dir && !leaks ? gr.dir : up;
    flow.set(gid, d);
    return d;
  };

  const grouped = groups.length > 0;
  const NW = nodeW(Math.max(0, ...g.nodes.map((n) => longest(n.label))));
  const levels = new Map<string | undefined, ReturnType<typeof place>>();
  const size = (id: string): { w: number; h: number } => {
    const n = nodeOf.get(id);
    if (n) return { w: grouped ? nodeW(longest(n.label)) : NW, h: NH + (lines(n.label).length - 1) * LINE };
    const l = level(id);
    const title = [...(groupOf.get(id)?.label ?? "").replace(/\n/g, " ")].length * 6.6;
    return { w: Math.max(l.W, title, 60) + GPAD * 2, h: l.H + GHEAD + GPAD };
  };
  const level = (gid: string | undefined): ReturnType<typeof place> => {
    const had = levels.get(gid);
    if (had) return had;
    const ids = kids.get(gid) ?? [];
    const here = new Set(ids);
    // lift is the item at this level that holds id.
    const lift = (id: string) => {
      let x = id;
      for (let k = 0; k < 64; k++) {
        const p = parent(x);
        if (p === gid) return x;
        if (p === undefined) return undefined;
        x = p;
      }
      return undefined;
    };
    const edges = g.edges.flatMap((e) => {
      const [from, to] = [lift(e.from), lift(e.to)];
      return from && to && from !== to && here.has(from) && here.has(to) ? [{ from, to, dotted: e.dotted }] : [];
    });
    const l = place(
      ids.map((id) => ({ id, ...size(id) })),
      edges,
      dirIn(gid) === "LR",
    );
    levels.set(gid, l);
    return l;
  };

  const box: Record<string, Rect> = {};
  const put = (gid: string | undefined, ox: number, oy: number) => {
    const l = level(gid);
    for (const id of kids.get(gid) ?? []) {
      const p = l.at[id];
      const s = size(id);
      box[id] = { x: ox + p.x, y: oy + p.y, w: s.w, h: s.h };
      if (groupOf.has(id)) put(id, box[id].x + (s.w - level(id).W) / 2, box[id].y + GHEAD);
    }
  };
  put(undefined, 0, 0);
  const root = level(undefined);
  const edgeLr = g.edges.map((e) => {
    const up = new Set(ancestors(e.to));
    return dirIn(ancestors(e.from).find((p) => up.has(p))) === "LR";
  });
  return { box, edgeLr, W: root.W, H: root.H, lr: g.dir === "LR" };
}
