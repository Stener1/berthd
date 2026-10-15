// node --experimental-strip-types --test src/lib/art/mermaid.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { type Flowchart, labelText, layoutFlowchart, parseFlowchart, type Rect } from "./mermaid.ts";

// The Mermaid flowcharts Shipyard draws itself (components/art/flowchart.tsx):
// what it reads from the syntax agents write, and where it puts things.

const chart = (src: string): Flowchart => {
  const g = parseFlowchart(src);
  assert.ok(g, "parses");
  return g;
};
const node = (g: Flowchart, id: string) => g.nodes.find((n) => n.id === id);
const edge = (g: Flowchart, from: string, to: string) => g.edges.find((e) => e.from === from && e.to === to);
const pairs = (g: Flowchart) => g.edges.map((e) => `${e.from}>${e.to}`);

// A reply an agent wrote, as written.
const PHYSICS = `flowchart TD
    Frame(["Render frame"]) --> Acc["Accumulator<br/>A += frame time"]
    Acc --> Check{"A ≥ Δt?"}
    Check -- "no" --> Interp["Interpolate for rendering<br/>α = A / Δt"]
    Interp --> Frame

    Check -- "yes" --> Step

    subgraph Step["Physics step (fixed Δt)"]
        direction TB
        IV["1 · Integrate velocities<br/>v += Δt·(F/m + g)<br/>ω += Δt·I⁻¹(τ - ω×Iω)"]

        subgraph Detect["2 · Collision detection"]
            direction LR
            BP["Broadphase<br/>AABB tree / sweep-and-prune<br/>O(n log n)"] --> Pairs[/"Candidate pairs"/]
            Pairs --> NP{"Shape type"}
            NP -- "spheres" --> SS["Sphere test"]
            NP -- "boxes" --> SAT["SAT<br/>15 axes"]
            NP -- "convex" --> GJK["GJK"] --> EPA["EPA<br/>depth + normal"]
        end

        Manifold[/"Contacts<br/>point, normal n, depth d"/]

        subgraph Solve["3 · Constraint solver (PGS)"]
            direction TB
            Warm["Warm start"] --> Iter["Iterate constraints"]
            Iter --> Clamp["Clamp accumulated impulse"]
            Clamp --> Iter
        end

        IV --> Detect
        Detect --> Manifold
        Manifold --> Solve
        Solve --> Pos["Integrate positions"]
    end

    Step --> Acc`;

test("every common node shape gives its label without its brackets or quotes", () => {
  const g = chart(`flowchart LR
    a[Box] --> b(Round)
    c([Stadium]) --> d[[Subroutine]]
    e[(Database)] --> f((Circle))
    g(((Double))) --> h>Flag]
    i{Rhombus} --> j{{Hexagon}}
    k[/Lean right/] --> l[\\Lean left\\]
    m[/Trapezoid\\] --> n[\\Upside down/]
    o["Quoted [with] brackets"] --> p("Quoted (round)")
    q@{ shape: cyl, label: "Newer syntax" } --> r@{ shape: notch-rect }
    s:::hot --> t[Styled]:::cold`);
  const want: [string, string, string][] = [
    ["a", "Box", "box"],
    ["b", "Round", "round"],
    ["c", "Stadium", "stadium"],
    ["d", "Subroutine", "subroutine"],
    ["e", "Database", "store"],
    ["f", "Circle", "circle"],
    ["g", "Double", "double-circle"],
    ["h", "Flag", "asym"],
    ["i", "Rhombus", "diamond"],
    ["j", "Hexagon", "hexagon"],
    ["k", "Lean right", "lean-r"],
    ["l", "Lean left", "lean-l"],
    ["m", "Trapezoid", "trapezoid"],
    ["n", "Upside down", "trapezoid-alt"],
    ["o", "Quoted [with] brackets", "box"],
    ["p", "Quoted (round)", "round"],
    ["q", "Newer syntax", "store"],
    ["r", "r", "box"],
    ["s", "s", "box"],
    ["t", "Styled", "box"],
  ];
  for (const [id, label, shape] of want) assert.deepEqual([node(g, id)?.label, node(g, id)?.shape], [label, shape], id);
  assert.equal(g.edges.length, 10);
});

test("labels lose their quotes; <br> breaks a line; entity codes and markdown strings read as text", () => {
  assert.equal(labelText('"Render frame"'), "Render frame");
  assert.equal(labelText("Accumulator<br/>A += frame time"), "Accumulator\nA += frame time");
  assert.equal(labelText("a<br>b<BR />c<br/>"), "a\nb\nc");
  assert.equal(labelText("Say #quot;hi#quot; #9829; #amp;"), 'Say "hi" ♥ &');
  assert.equal(labelText("x &lt; y &amp;&amp; y &gt; z, it&#39;s &#x2192; &rarr;"), "x < y && y > z, it's → →");
  assert.equal(labelText('"`**Bold** and _calm_ text`"'), "Bold and calm text");
  assert.equal(labelText("<b>Bold</b> <i>tag</i>"), "Bold tag");
  // Anything else stays as the text it is: it's drawn as SVG text, never HTML.
  assert.equal(labelText('<img src=x onerror="alert(1)">'), '<img src=x onerror="alert(1)">');
  assert.equal(labelText("&lt;br&gt; is how"), "<br> is how");
  const g = chart(`graph TD
    A["#quot;Quoted#quot;<br />twice"] -->|"&quot;so&quot;"| B["Line one<br>line two"]`);
  assert.equal(node(g, "A")?.label, '"Quoted"\ntwice');
  assert.equal(node(g, "B")?.label, "Line one\nline two");
  assert.equal(edge(g, "A", "B")?.label, '"so"');
});

test("every edge form, with its label in the middle or in pipes, quoted or not", () => {
  const g = chart(`flowchart LR
    a --> b
    a --- c
    a -.-> d
    a ==> e
    a --o f
    a --x g
    a <--> h
    a ~~~ i
    a -- text --> j
    a -- "quoted text" --> k
    a -->|piped| l
    a -->|"piped quoted"| m
    a -. dotted text .-> n
    a == thick text ==> o
    a -.- p
    a === q
    a ---> r
    a -- open text --- s
    a -- "has --> in it" --> t`);
  const e = (to: string) => {
    const x = edge(g, "a", to);
    assert.ok(x, to);
    return x;
  };
  assert.deepEqual(e("b"), { from: "a", to: "b", label: undefined, dotted: false, thick: false, arrow: true });
  assert.equal(e("c").arrow, false);
  assert.deepEqual([e("d").dotted, e("d").arrow], [true, true]);
  assert.deepEqual([e("e").thick, e("e").arrow], [true, true]);
  assert.deepEqual([e("f").arrow, e("f").tip], [false, "circle"]);
  assert.deepEqual([e("g").arrow, e("g").tip], [false, "cross"]);
  assert.deepEqual([e("h").arrow, e("h").both], [true, true]);
  assert.equal(e("i").hidden, true);
  assert.equal(e("j").label, "text");
  assert.equal(e("k").label, "quoted text");
  assert.equal(e("l").label, "piped");
  assert.equal(e("m").label, "piped quoted");
  assert.deepEqual([e("n").label, e("n").dotted, e("n").arrow], ["dotted text", true, true]);
  assert.deepEqual([e("o").label, e("o").thick, e("o").arrow], ["thick text", true, true]);
  assert.deepEqual([e("p").dotted, e("p").arrow], [true, false]);
  assert.deepEqual([e("q").thick, e("q").arrow], [true, false]);
  assert.equal(e("r").arrow, true);
  assert.deepEqual([e("s").label, e("s").arrow], ["open text", false]);
  assert.equal(e("t").label, "has --> in it");
  assert.equal(g.edges.length, 19);
  // Without spaces, and with hyphens in ids.
  const t = chart("graph TD\nA-->B\nB-.->C\nmy-node-->|x|other-node\nC==>D");
  assert.deepEqual(pairs(t), ["A>B", "B>C", "my-node>other-node", "C>D"]);
  assert.equal(edge(t, "my-node", "other-node")?.label, "x");
});

test("chains and & read edge by edge, every left to every right", () => {
  const g = chart(`flowchart LR
    a[One] --> b[Two] -- next --> c((Three)) --> d
    e & f --> g
    g --> h & i
    j & k -->|both| l & m`);
  assert.deepEqual(pairs(g), ["a>b", "b>c", "c>d", "e>g", "f>g", "g>h", "g>i", "j>l", "j>m", "k>l", "k>m"]);
  assert.equal(edge(g, "b", "c")?.label, "next");
  assert.equal(node(g, "c")?.label, "Three");
  assert.equal(edge(g, "k", "m")?.label, "both");
});

test("subgraphs, nested, in all their header forms; an edge to one names the group", () => {
  const g = chart(`flowchart LR
    start --> backend
    subgraph backend["The backend"]
      direction TB
      api[API] --> db[(DB)]
      subgraph jobs [Jobs]
        worker --> queue
      end
    end
    subgraph Plain title
      x
    end
    subgraph ops
      y
    end
    api --> jobs
    jobs --> ops`);
  assert.deepEqual(g.groups, [
    { id: "backend", label: "The backend", dir: "TD" },
    { id: "jobs", label: "Jobs", parent: "backend" },
    { id: "Plain title", label: "Plain title" },
    { id: "ops", label: "ops" },
  ]);
  assert.deepEqual(
    g.nodes.map((n) => [n.id, n.group]),
    [["start", undefined], ["api", "backend"], ["db", "backend"], ["worker", "jobs"], ["queue", "jobs"], ["x", "Plain title"], ["y", "ops"]],
  );
  // jobs and ops are groups, not nodes, at both ends of their edges.
  assert.ok(edge(g, "api", "jobs") && edge(g, "jobs", "ops"));
  assert.equal(node(g, "jobs"), undefined);

  const l = layoutFlowchart(g);
  const inside = (a: Rect, b: Rect) => a.x >= b.x && a.y >= b.y && a.x + a.w <= b.x + b.w && a.y + a.h <= b.y + b.h;
  for (const id of ["api", "db", "jobs"]) assert.ok(inside(l.box[id], l.box.backend), `${id} in backend`);
  for (const id of ["worker", "queue"]) assert.ok(inside(l.box[id], l.box.jobs), `${id} in jobs`);
  assert.ok(!inside(l.box.start, l.box.backend));
  // backend runs top down inside a chart that runs across, and jobs, inside it, with it.
  assert.ok(l.box.db.y > l.box.api.y);
  assert.ok(l.box.queue.y > l.box.worker.y);
  assert.equal(l.edgeLr[g.edges.findIndex((e) => e.from === "api" && e.to === "db")], false);
  assert.equal(l.edgeLr[g.edges.findIndex((e) => e.from === "start")], true);
  // As in Mermaid, a subgraph a node links straight out of keeps the chart's flow.
  const out = chart("flowchart LR\n  start --> api\n  subgraph backend\n    direction TB\n    api --> db\n  end");
  const lo = layoutFlowchart(out);
  assert.ok(lo.box.db.x > lo.box.api.x);
});

test("a node first named outside a subgraph moves into the one that names it", () => {
  const g = chart("graph TD\n  a --> b\n  subgraph S\n    b --> c\n  end\n  subgraph T\n    c --> d\n  end");
  assert.deepEqual(
    g.nodes.map((n) => [n.id, n.group]),
    [["a", undefined], ["b", "S"], ["c", "S"], ["d", "T"]],
  );
});

test("the agent's physics reply reads as its nodes, edges and subgraphs", () => {
  const g = chart(PHYSICS);
  assert.equal(g.dir, "TD");
  assert.deepEqual(
    g.nodes.map((n) => [n.id, n.label, n.shape, n.group]),
    [
      ["Frame", "Render frame", "stadium", undefined],
      ["Acc", "Accumulator\nA += frame time", "box", undefined],
      ["Check", "A ≥ Δt?", "diamond", undefined],
      ["Interp", "Interpolate for rendering\nα = A / Δt", "box", undefined],
      ["IV", "1 · Integrate velocities\nv += Δt·(F/m + g)\nω += Δt·I⁻¹(τ - ω×Iω)", "box", "Step"],
      ["BP", "Broadphase\nAABB tree / sweep-and-prune\nO(n log n)", "box", "Detect"],
      ["Pairs", "Candidate pairs", "lean-r", "Detect"],
      ["NP", "Shape type", "diamond", "Detect"],
      ["SS", "Sphere test", "box", "Detect"],
      ["SAT", "SAT\n15 axes", "box", "Detect"],
      ["GJK", "GJK", "box", "Detect"],
      ["EPA", "EPA\ndepth + normal", "box", "Detect"],
      ["Manifold", "Contacts\npoint, normal n, depth d", "lean-r", "Step"],
      ["Warm", "Warm start", "box", "Solve"],
      ["Iter", "Iterate constraints", "box", "Solve"],
      ["Clamp", "Clamp accumulated impulse", "box", "Solve"],
      ["Pos", "Integrate positions", "box", "Step"],
    ],
  );
  assert.deepEqual(g.groups, [
    { id: "Step", label: "Physics step (fixed Δt)", dir: "TD" },
    { id: "Detect", label: "2 · Collision detection", parent: "Step", dir: "LR" },
    { id: "Solve", label: "3 · Constraint solver (PGS)", parent: "Step", dir: "TD" },
  ]);
  assert.deepEqual(pairs(g), [
    "Frame>Acc",
    "Acc>Check",
    "Check>Interp",
    "Interp>Frame",
    "Check>Step",
    "BP>Pairs",
    "Pairs>NP",
    "NP>SS",
    "NP>SAT",
    "NP>GJK",
    "GJK>EPA",
    "Warm>Iter",
    "Iter>Clamp",
    "Clamp>Iter",
    "IV>Detect",
    "Detect>Manifold",
    "Manifold>Solve",
    "Solve>Pos",
    "Step>Acc",
  ]);
  assert.deepEqual(
    g.edges.filter((e) => e.label).map((e) => [e.to, e.label]),
    [["Interp", "no"], ["Step", "yes"], ["SS", "spheres"], ["SAT", "boxes"], ["GJK", "convex"]],
  );

  const l = layoutFlowchart(g);
  const { box } = l;
  const inside = (a: Rect, b: Rect) => a.x >= b.x && a.y >= b.y && a.x + a.w <= b.x + b.w && a.y + a.h <= b.y + b.h;
  const apart = (a: Rect, b: Rect) => a.x + a.w <= b.x || b.x + b.w <= a.x || a.y + a.h <= b.y || b.y + b.h <= a.y;
  for (const n of g.nodes) if (n.group) assert.ok(inside(box[n.id], box[n.group]), `${n.id} in ${n.group}`);
  assert.ok(inside(box.Detect, box.Step) && inside(box.Solve, box.Step));
  // Nothing overlaps what's beside it.
  const level = (p: string | undefined) => [...g.nodes.filter((n) => n.group === p).map((n) => n.id), ...g.groups.filter((x) => x.parent === p).map((x) => x.id)];
  for (const p of [undefined, "Step", "Detect", "Solve"]) {
    const ids = level(p);
    for (const a of ids) for (const b of ids) if (a < b) assert.ok(apart(box[a], box[b]), `${a} and ${b} apart`);
  }
  // The loop back to Frame doesn't push the flow down: Frame leads.
  assert.ok(box.Frame.y < box.Acc.y && box.Acc.y < box.Check.y && box.Check.y < box.Step.y);
  // Detect runs across, the rest down; a node with three lines is taller.
  assert.ok(box.Pairs.x > box.BP.x && box.NP.x > box.Pairs.x);
  assert.ok(box.Detect.y > box.IV.y && box.Manifold.y > box.Detect.y && box.Solve.y > box.Manifold.y && box.Pos.y > box.Solve.y);
  assert.ok(box.IV.h > box.Acc.h && box.Acc.h > box.Frame.h);
  assert.equal(l.edgeLr[g.edges.findIndex((e) => e.from === "BP")], true);
  assert.equal(l.edgeLr[g.edges.findIndex((e) => e.from === "IV")], false);
});

// The artifact demo's map (lib/art/mock-artifacts.ts), and where it was
// drawn before subgraphs: a chart without them is laid out as it was.
const MAP = `graph LR
  web[acme-shop web] --> edge(Edge / CDN)
  edge --> suggest[Suggest API]
  edge --> search[Search API]
  suggest --> cache[(Suggest cache)]
  search --> trigram[(Trigram index)]
  search -. fallback .-> pg[(Postgres)]
  search --> recs[Recs service]
  recs --> pg
  importer[Catalogue importer] --> pg
  importer --> trigram`;

test("a chart without subgraphs is laid out exactly as it was", () => {
  const g = chart(MAP);
  const l = layoutFlowchart(g);
  assert.deepEqual([l.W, l.H], [1006, 158]);
  const at = Object.fromEntries(Object.entries(l.box).map(([id, b]) => [id, [b.x, b.y, b.w, b.h]]));
  assert.deepEqual(at, {
    web: [0, 30, 150, 38],
    importer: [0, 90, 150, 38],
    edge: [214, 60, 150, 38],
    suggest: [428, 30, 150, 38],
    search: [428, 90, 150, 38],
    cache: [642, 0, 150, 38],
    trigram: [642, 60, 150, 38],
    recs: [642, 120, 150, 38],
    pg: [856, 60, 150, 38],
  });
  assert.deepEqual(edge(g, "search", "pg"), { from: "search", to: "pg", label: "fallback", dotted: true, thick: false, arrow: true });

  const td = layoutFlowchart(chart("graph TD\n  a[Start] --> b[Load config]\n  a --> c[Read env]\n  b --> d{Valid?}\n  c --> d\n  d -->|yes| e[Run]\n  d -->|no| f[Fail]\n  e --> g((Done))"));
  assert.deepEqual([td.W, td.H], [270, 410]);
  assert.deepEqual(
    Object.fromEntries(Object.entries(td.box).map(([id, b]) => [id, [b.x, b.y]])),
    { a: [83.5, 0], b: [0, 93], c: [167, 93], d: [83.5, 186], e: [0, 279], f: [167, 279], g: [83.5, 372] },
  );
});

test("a cycle is laid out along the flow, not pushed off the end", () => {
  const l = layoutFlowchart(chart("graph LR\n  a --> b --> c --> a\n  c --> c"));
  assert.ok(l.box.a.x < l.box.b.x && l.box.b.x < l.box.c.x);
  assert.equal(l.W, 3 * l.box.a.w + 2 * 64);
});

test("what it can't read it leaves out, and what isn't a flowchart it doesn't take", () => {
  const g = chart(`---
title: Front matter
---
%%{init: {"theme": "dark"}}%%
graph TD; A-->B; B-->C
  classDef hot fill:#f96
  class A hot
  style B fill:#bbf
  linkStyle 0 stroke:#f00
  click A "https://example.com"
  accTitle: Steps
  this is not -->
  ]]] ((( {{
  end
  C --> D["Unclosed
  E -->`);
  assert.deepEqual(pairs(g), ["A>B", "B>C", "C>D"]);
  assert.deepEqual(
    g.nodes.map((n) => n.id),
    ["A", "B", "C", "D", "E"].filter((id) => node(g, id)),
  );
  assert.equal(parseFlowchart("sequenceDiagram\n  A->>B: hi"), undefined);
  assert.equal(parseFlowchart("pie title Pets"), undefined);
  assert.equal(parseFlowchart(""), undefined);
  // An empty subgraph is a box of its own; one never closed still holds what's in it.
  const e = chart("flowchart LR\n  subgraph empty[Nothing yet]\n  end\n  subgraph open\n    a --> b");
  assert.deepEqual(e.groups.map((x) => x.id), ["empty", "open"]);
  assert.deepEqual(e.nodes.map((n) => n.group), ["open", "open"]);
  const l = layoutFlowchart(e);
  assert.ok(l.box.empty.w > 0 && l.box.empty.h > 0);
});
