import { type CSSProperties, useId } from "react";

import { CAP, type Flowchart, GPAD, type Layout, LINE, lines, type MNode, NH, type Rect } from "@/lib/art/mermaid";

const PAD = 14;

// edgePath is an edge's curve, and its ends. Along the flow it leaves a
// node's far side for the next one's near side; an edge back up the flow
// loops out beside the nodes it joins (below them, across), on the side
// it comes from, so it doesn't cut through the ones between.
function edgePath(a: Rect, b: Rect, lr: boolean) {
  let [x0, y0, x1, y1] = lr ? [a.x + a.w, a.y + a.h / 2, b.x - 2, b.y + b.h / 2] : [a.x + a.w / 2, a.y + a.h, b.x + b.w / 2, b.y - 2];
  const back = lr ? x1 < x0 : y1 < y0;
  let c = lr ? [(x0 + x1) / 2, y0, (x0 + x1) / 2, y1] : [x0, (y0 + y1) / 2, x1, (y0 + y1) / 2];
  if (back) {
    const self = a === b;
    if (lr) {
      const below = a.y + a.h / 2 >= b.y + b.h / 2;
      [x0, x1] = self ? [a.x + a.w * 0.35, a.x + a.w * 0.65] : [a.x + a.w / 2, b.x + b.w / 2];
      [y0, y1] = below ? [a.y + a.h, b.y + b.h + 2] : [a.y, b.y - 2];
      const out = below ? Math.max(y0, y1) + 40 : Math.min(y0, y1) - 40;
      c = [x0, out, x1, out];
    } else {
      const right = a.x + a.w / 2 >= b.x + b.w / 2;
      [x0, x1] = right ? [a.x + a.w, b.x + b.w + 2] : [a.x, b.x - 2];
      [y0, y1] = self ? [a.y + a.h * 0.35, a.y + a.h * 0.65] : [a.y + a.h / 2, b.y + b.h / 2];
      const out = right ? Math.max(x0, x1) + 48 : Math.min(x0, x1) - 48;
      c = [out, y0, out, y1];
    }
  }
  // Its middle, for a label: the curve at t = ½.
  const mx = (x0 + 3 * c[0] + 3 * c[2] + x1) / 8;
  const my = (y0 + 3 * c[1] + 3 * c[3] + y1) / 8;
  return { x0, y0, x1, y1, back, c, mx, my, d: `M${x0},${y0} C${c[0]},${c[1]} ${c[2]},${c[3]} ${x1},${y1}` };
}

const ends = (g: Flowchart, layout: Layout, i: number) => {
  const e = g.edges[i];
  const a = layout.box[e.from];
  const b = layout.box[e.to];
  return a && b ? edgePath(a, b, layout.edgeLr[i] ?? layout.lr) : undefined;
};

// flowchartBox is the drawing's viewBox: the nodes, with room around
// them, and with fit, the loops of edges back up the flow too, which an
// artifact's diagram leaves to its frame to cut.
export function flowchartBox(g: Flowchart, layout: Layout, fit = false): { x: number; y: number; w: number; h: number } {
  let [x0, y0, x1, y1] = [0, 0, layout.W, layout.H];
  if (fit)
    g.edges.forEach((_, i) => {
      const p = ends(g, layout, i);
      if (!p?.back) return;
      // The curve's extent, sampled: near enough for a margin.
      for (let t = 0; t <= 1; t += 0.05) {
        const u = 1 - t;
        const x = u * u * u * p.x0 + 3 * u * u * t * p.c[0] + 3 * u * t * t * p.c[2] + t * t * t * p.x1;
        const y = u * u * u * p.y0 + 3 * u * u * t * p.c[1] + 3 * u * t * t * p.c[3] + t * t * t * p.y1;
        [x0, y0, x1, y1] = [Math.min(x0, x), Math.min(y0, y), Math.max(x1, x), Math.max(y1, y)];
      }
    });
  return { x: x0 - PAD, y: y0 - PAD, w: x1 - x0 + PAD * 2, h: y1 - y0 + PAD * 2 };
}

const clip = (s: string, n: number) => ([...s].length > n ? `${[...s].slice(0, n - 1).join("")}…` : s);

// outline is a node's shape in its w × h box: a rect for most, a path for
// the slanted and pointed ones, ellipses for circles.
function Outline({ n, w, h, fill, stroke, width }: { n: MNode; w: number; h: number; fill: string; stroke: string; width: number }) {
  const paint = { fill, stroke, strokeWidth: width };
  const k = Math.min(12, w / 5);
  const poly = (pts: number[][]) => <path d={`M${pts.map((p) => p.join(",")).join(" L")} Z`} strokeLinejoin="round" {...paint} />;
  switch (n.shape) {
    case "circle":
      return <ellipse cx={w / 2} cy={h / 2} rx={w / 2} ry={h / 2} {...paint} />;
    case "double-circle":
      return (
        <>
          <ellipse cx={w / 2} cy={h / 2} rx={w / 2} ry={h / 2} {...paint} />
          <ellipse cx={w / 2} cy={h / 2} rx={w / 2 - 3.5} ry={h / 2 - 3.5} fill="none" stroke={stroke} strokeWidth={width * 0.8} />
        </>
      );
    case "hexagon":
      return poly([[k, 0], [w - k, 0], [w, h / 2], [w - k, h], [k, h], [0, h / 2]]);
    case "lean-r":
      return poly([[k, 0], [w, 0], [w - k, h], [0, h]]);
    case "lean-l":
      return poly([[0, 0], [w - k, 0], [w, h], [k, h]]);
    case "trapezoid":
      return poly([[k, 0], [w - k, 0], [w, h], [0, h]]);
    case "trapezoid-alt":
      return poly([[0, 0], [w, 0], [w - k, h], [k, h]]);
    case "asym":
      return poly([[0, 0], [w, 0], [w, h], [0, h], [k, h / 2]]);
    case "subroutine":
      return (
        <>
          <rect width={w} height={h} rx={4} {...paint} />
          <path d={`M7,0 V${h} M${w - 7},0 V${h}`} fill="none" stroke={stroke} strokeWidth={width * 0.9} />
        </>
      );
    default: {
      // round and stadium are pills, a diamond is a square-cornered box
      // (a rhombus would need twice the width for its words).
      const r = n.shape === "round" ? Math.min(h, NH) / 2 : n.shape === "stadium" ? h / 2 : n.shape === "diamond" ? 4 : 8;
      return <rect width={w} height={h} rx={r} {...paint} />;
    }
  }
}

// FlowchartSvg draws a Mermaid flowchart laid out by lib/art/mermaid.ts in
// the theme's chart colours (--chart-*, set by an .art-scope around it):
// an artifact's diagram, and a mermaid block in a reply. Subgraphs are
// tinted boxes behind, titled at their top left; nested ones darken as
// they nest. A thumb is a card's small picture: bolder lines, no edge
// labels. fit: flowchartBox.
export function FlowchartSvg({ g, layout, thumb = false, fit = false, className, style }: { g: Flowchart; layout: Layout; thumb?: boolean; fit?: boolean; className?: string; style?: CSSProperties }) {
  const uid = useId().replace(/:/g, "");
  const [arrow, circle, cross] = ["arrow", "circle", "cross"].map((k) => `art-${k}-${uid}`);
  const { box } = layout;
  const view = flowchartBox(g, layout, fit);
  const line = "var(--chart-foreground-muted)";
  const marker = (e: Flowchart["edges"][number]) => (e.arrow ? arrow : e.tip === "circle" ? circle : e.tip === "cross" ? cross : undefined);
  return (
    <svg viewBox={`${view.x} ${view.y} ${view.w} ${view.h}`} className={className} style={style} preserveAspectRatio="xMidYMid meet" role="img" aria-label={`Diagram: ${g.nodes.map((n) => n.label.replace(/\n/g, " ")).join(", ")}`}>
      <defs>
        <marker id={arrow} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
          <path d="M0,0 L10,5 L0,10 z" fill={line} />
        </marker>
        <marker id={circle} viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
          <circle cx="5" cy="5" r="3.6" fill="var(--card)" stroke={line} strokeWidth="1.6" />
        </marker>
        <marker id={cross} viewBox="0 0 10 10" refX="7" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
          <path d="M2,1 L8,9 M8,1 L2,9" stroke={line} strokeWidth="1.8" strokeLinecap="round" />
        </marker>
      </defs>
      {g.groups.map((gr) => {
        const b = box[gr.id];
        if (!b) return null;
        const tint = `color-mix(in oklab, var(--chart-2) ${thumb ? 9 : 6}%, transparent)`;
        return (
          <g key={gr.id} className="art-group-g" data-group={gr.id}>
            <rect x={b.x} y={b.y} width={b.w} height={b.h} rx={10} fill={tint} stroke="color-mix(in oklab, var(--chart-2) 32%, var(--border))" strokeWidth={thumb ? 1.6 : 1} />
            {!thumb && gr.label && (
              <text x={b.x + GPAD} y={b.y + 18} className="art-group">
                {clip(gr.label.replace(/\n/g, " "), Math.max(4, Math.floor((b.w - GPAD * 2) / 6.6)))}
              </text>
            )}
          </g>
        );
      })}
      {g.edges.map((e, i) => {
        const p = e.hidden ? undefined : ends(g, layout, i);
        if (!p) return null;
        const m = marker(e);
        return (
          <g key={i} className="art-edge" style={{ animationDelay: `${Math.min(i * 40, 600)}ms` }}>
            <path d={p.d} fill="none" stroke={line} strokeOpacity={thumb ? 0.9 : 0.75} strokeWidth={(thumb ? 2.4 : 1.4) * (e.thick ? 1.7 : 1)} strokeDasharray={e.dotted ? "4 4" : undefined} markerEnd={m ? `url(#${m})` : undefined} markerStart={m && e.both ? `url(#${m})` : undefined} />
            {e.label && !thumb && (
              <g transform={`translate(${p.mx}, ${p.my})`}>
                <rect x={-e.label.length * 3.3 - 6} y={-9} width={e.label.length * 6.6 + 12} height={18} rx={9} fill="var(--background)" stroke="var(--border)" />
                <text textAnchor="middle" dy="0.32em" className="art-tick">
                  {e.label}
                </text>
              </g>
            )}
          </g>
        );
      })}
      {g.nodes.map((n, i) => {
        const b = box[n.id];
        if (!b) return null;
        const store = n.shape === "store";
        const tint = store ? "var(--chart-3)" : n.shape === "diamond" ? "var(--chart-4)" : i === 0 ? "var(--chart-1)" : undefined;
        const stroke = tint ? `color-mix(in oklab, ${tint} 60%, var(--border))` : "var(--border)";
        const rows = lines(n.label).map((l) => clip(l, CAP + 2));
        const top = b.h / 2 + (store ? 3 : 0) - ((rows.length - 1) * LINE) / 2;
        return (
          <g key={n.id} transform={`translate(${b.x}, ${b.y})`} className="art-node-g" data-shape={n.shape} style={{ animationDelay: `${Math.min(i * 50, 600)}ms` }}>
            <Outline n={n} w={b.w} h={b.h} fill={tint ? `color-mix(in oklab, ${tint} ${thumb ? 30 : 16}%, var(--card))` : "var(--card)"} stroke={stroke} width={thumb ? 2 : 1.2} />
            {store && <path d={`M8,7 Q${b.w / 2},15 ${b.w - 8},7`} fill="none" stroke={stroke} strokeWidth={1.1} />}
            <text x={b.w / 2} y={top} textAnchor="middle" dy="0.34em" className="art-node" fontWeight={500}>
              {rows.length === 1
                ? rows[0]
                : rows.map((r, k) => (
                    <tspan key={k} x={b.w / 2} dy={k ? LINE : undefined}>
                      {r}
                    </tspan>
                  ))}
            </text>
          </g>
        );
      })}
    </svg>
  );
}
