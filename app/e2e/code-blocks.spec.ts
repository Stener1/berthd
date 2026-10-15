import type { Page } from "@playwright/test";

import { expect, mockOnly, test } from "./fixtures";

// A reply's mermaid and diff blocks, drawn (components/conversation/code-
// blocks.tsx): a flowchart as its diagram, with its source a click away;
// other Mermaid as written; a diff as the chat's edits are drawn. A draft
// stays text until it lands. Replies land through the demo's
// window.__berthDraft (lib/mock-conversation).

const BOX = "gpu";
const AGENT = "judge-v2-claude";

const FENCE = "```";

const DIAGRAM_REPLY = [
  "## How a judged sample flows",
  "",
  `${FENCE}mermaid`,
  "flowchart LR",
  "  A[Sample] --> B{Cached?}",
  "  B -->|yes| C[Reuse score]",
  "  B -->|no| D[Judge v2]",
  "  D --> E[(acme-scores)]",
  FENCE,
  "",
  "And the order the calls go in:",
  "",
  `${FENCE}mermaid`,
  "sequenceDiagram",
  "  Runner->>Judge: score(sample)",
  "  Judge-->>Runner: 4",
  FENCE,
].join("\n");

// A reply an agent wrote, as written: shapes, quoted labels, <br/>s,
// labelled edges and nested subgraphs with their own directions.
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
const PHYSICS_REPLY = ["## One physics step", "", `${FENCE}mermaid`, PHYSICS, FENCE].join("\n");

const DIFF_REPLY = [
  "## The fix",
  "",
  `${FENCE}diff`,
  "--- a/src/acme/judge.ts",
  "+++ b/src/acme/judge.ts",
  "@@ -12,4 +12,5 @@ export function score(sample: Sample) {",
  "   const rubric = loadRubric();",
  "-  return judge(sample, rubric);",
  "+  const pairs = pairwise(sample);",
  "+  return judge(pairs, rubric);",
  " }",
  FENCE,
  "",
  "Or, in short:",
  "",
  `${FENCE}diff`,
  "- timeout: 10",
  "+ timeout: 30",
  "  retries: 3",
  FENCE,
].join("\n");

type DraftHook = { show(b: string, s: string, t: string): void; land(b: string, s: string, t: string): void; clear(b: string, s: string): void };
const draftHook = (page: Page, fn: "show" | "land" | "clear", text = "") =>
  page.evaluate(([fn, b, s, t]) => {
    const h = (window as unknown as { __berthDraft: DraftHook }).__berthDraft;
    if (fn === "show") h.show(b, s, t);
    else if (fn === "land") h.land(b, s, t);
    else h.clear(b, s);
  }, [fn, BOX, AGENT, text] as const);

test("a mermaid flowchart in a reply draws as its diagram, with its source a click away", async ({ app }) => {
  mockOnly("lands a reply on the demo's agent");
  await app.open({ params: { view: "conversation" } });
  await app.openWorktree("gpu/judge-v2");
  await draftHook(app.page, "land", DIAGRAM_REPLY);
  const md = app.chat.locator("[data-kind=text] .cv-md").filter({ hasText: "How a judged sample flows" });
  const [flow, sequence] = [md.locator(".cv-pre").nth(0), md.locator(".cv-pre").nth(1)];

  await expect(flow.locator("svg[role=img]")).toBeVisible();
  await expect(flow.locator("svg[role=img]")).toHaveAttribute("aria-label", "Diagram: Sample, Cached?, Reuse score, Judge v2, acme-scores");
  await expect(flow.locator(":scope > pre")).toBeHidden();
  await expect(flow.locator(".cv-pre-lang")).toHaveText("Mermaid");

  await flow.getByRole("button", { name: "Source" }).click();
  await expect(flow.locator(":scope > pre")).toBeVisible();
  await expect(flow.locator(":scope > pre")).toContainText("A[Sample] --> B{Cached?}");
  await expect(flow.locator("svg[role=img]")).toBeHidden();
  await flow.getByRole("button", { name: "Diagram" }).click();
  await expect(flow.locator("svg[role=img]")).toBeVisible();

  // Mermaid other than a flowchart is its source, said so.
  await expect(sequence.locator(":scope > pre")).toBeVisible();
  await expect(sequence.locator("svg[role=img]")).toHaveCount(0);
  await expect(sequence.locator(".cv-pre-note")).toHaveText("Shown as written: Shipyard draws flowcharts");
});

test("a flowchart with subgraphs, shapes and line breaks draws as it was written", async ({ app }) => {
  mockOnly("lands a reply on the demo's agent");
  await app.open({ params: { view: "conversation" } });
  await app.openWorktree("gpu/judge-v2");
  await draftHook(app.page, "land", PHYSICS_REPLY);
  const md = app.chat.locator("[data-kind=text] .cv-md").filter({ hasText: "One physics step" });
  const flow = md.locator(".cv-pre").first();
  const svg = flow.locator("svg[role=img]");

  await expect(svg).toBeVisible();
  await expect(flow.locator(":scope > pre")).toBeHidden();
  await expect(svg.locator("g.art-node-g")).toHaveCount(17);
  // Each subgraph is a titled box, Detect and Solve inside Step.
  const groups = svg.locator("g.art-group-g");
  await expect(groups).toHaveCount(3);
  await expect(groups.locator("text")).toHaveText(["Physics step (fixed Δt)", "2 · Collision detection", "3 · Constraint solver (PGS)"]);
  const box = async (id: string) => (await svg.locator(`g[data-group="${id}"] rect`).boundingBox())!;
  const [step, detect, solve] = [await box("Step"), await box("Detect"), await box("Solve")];
  for (const inner of [detect, solve]) {
    expect(inner.x).toBeGreaterThan(step.x);
    expect(inner.y + inner.height).toBeLessThan(step.y + step.height);
  }
  // Labels without their brackets and quotes, a <br/> a line of its own.
  await expect(svg).toHaveAttribute("aria-label", /^Diagram: Render frame, Accumulator A \+= frame time, A ≥ Δt\?, /);
  const iv = svg.locator("g.art-node-g", { hasText: "Integrate velocities" });
  await expect(iv.locator("tspan")).toHaveText(["1 · Integrate velocities", "v += Δt·(F/m + g)", "ω += Δt·I⁻¹(τ - ω×Iω)"]);
  await expect(svg.locator("g.art-node-g[data-shape=lean-r]")).toHaveCount(2);
  await expect(svg.locator("g.art-node-g[data-shape=stadium]")).toHaveText("Render frame");
  await expect(svg.locator(".art-edge text")).toHaveText(["no", "yes", "spheres", "boxes", "convex"]);
  await expect(svg).not.toContainText("<br");
  await expect(svg).not.toContainText('"');
  await expect(svg).not.toContainText("[");
});

test("a diff in a reply draws as the chat's edits do: a patch with its file, bare lines without numbers", async ({ app }) => {
  mockOnly("lands a reply on the demo's agent");
  await app.open({ params: { view: "conversation" } });
  await app.openWorktree("gpu/judge-v2");
  await draftHook(app.page, "land", DIFF_REPLY);
  const md = app.chat.locator("[data-kind=text] .cv-md").filter({ hasText: "The fix" });
  const [patch, bare] = [md.locator(".cv-pre").nth(0), md.locator(".cv-pre").nth(1)];

  // The renderer loads on first use; then both blocks are drawn.
  await expect(patch).toHaveAttribute("data-drawn", "diff");
  await expect(bare).toHaveAttribute("data-drawn", "diff");
  await expect(patch.locator(".cv-pre-lang")).toHaveText("Diff");
  await expect(patch.locator(".cv-pre-file")).toContainText("src/acme/judge.ts");
  await expect(patch.locator(".cv-pre-file")).toContainText("+2");
  await expect(patch.locator(".cv-pre-file")).toContainText("−1");
  await expect(patch.locator(":scope > pre")).toBeHidden();
  // Numbered by the file's own lines; the bare lines number nothing.
  await expect(patch.locator("[data-column-number]").first()).toBeVisible();
  await expect(patch.locator("[data-column-number]", { hasText: /^12$/ }).first()).toBeVisible();
  await expect(bare.locator("pre[data-diff]")).toHaveAttribute("data-disable-line-numbers", "");
  await expect(patch.locator("pre[data-diff]")).not.toHaveAttribute("data-disable-line-numbers");
  await expect(bare.locator(".cv-pre-file")).not.toContainText("change");
});

test("a reply still being written keeps its diagram as text until it lands", async ({ app }) => {
  mockOnly("drives the demo's screen");
  await app.open({ params: { view: "conversation" } });
  await app.openWorktree("gpu/judge-v2");
  await draftHook(app.page, "show", DIAGRAM_REPLY);
  const draft = app.chat.locator("[data-draft] .cv-md").filter({ hasText: "How a judged sample flows" });
  await expect(draft.locator(".cv-pre").first().locator("pre")).toBeVisible();
  await expect(draft.locator("svg[role=img]")).toHaveCount(0);
  await draftHook(app.page, "land", DIAGRAM_REPLY);
  await draftHook(app.page, "clear");
  const md = app.chat.locator("[data-kind=text] .cv-md").filter({ hasText: "How a judged sample flows" });
  await expect(md.locator(".cv-pre").first().locator("svg[role=img]")).toBeVisible();
});
