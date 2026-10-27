import type { Page } from "@playwright/test";

import { expect, mockOnly, test } from "./fixtures";

// Math in a reply (lib/markdown-html): KaTeX loads the first time a reply
// has some, and draws $$…$$, \(…\) and \[…\]. A single $ and code stay as
// written. The reply lands through the demo's window.__berthDraft
// (lib/mock-conversation).

const BOX = "gpu";
const AGENT = "judge-v2-claude";

const MATH_REPLY = [
  "## The score, as a formula",
  "",
  "Each sample's weight is \\(w_i = \\frac{1}{1 + e^{-k(s_i - 3)}}\\), so a 3 counts half.",
  "",
  "$$\\text{agreement} = \\frac{\\sum_i w_i \\, [a_i = h_i]}{\\sum_i w_i}$$",
  "",
  "The variance of the mean, over the labelled set:",
  "",
  "\\[\\sigma^2_{\\bar{x}} = \\frac{1}{n(n-1)} \\sum_{i=1}^{n} (x_i - \\bar{x})^2\\]",
  "",
  "A run costs $5 and $10 with pairwise mode on; `$$x$$` in code stays as written.",
  "",
  "A typo shows as its source: \\(\\frac{1}{\\)",
].join("\n");

const landReply = (page: Page, text: string) =>
  page.evaluate(([b, s, t]) => (window as unknown as { __berthDraft: { land(b: string, s: string, t: string): void } }).__berthDraft.land(b, s, t), [BOX, AGENT, text] as const);

test("a reply's math draws as typeset math, and a single $ stays text", async ({ app }) => {
  mockOnly("lands a reply on the demo's agent");
  await app.open({ params: { view: "conversation" } });
  await app.openWorktree("gpu/judge-v2");
  await landReply(app.page, MATH_REPLY);
  const md = app.chat.locator("[data-kind=text] .cv-md").filter({ hasText: "The score, as a formula" });
  await expect(md.locator(".katex").first()).toBeVisible();
  // \(…\) inline, $$…$$ on its own line and \[…\] as displays.
  await expect(md.locator(".cv-math:not(.cv-math-display) .katex")).toHaveCount(1);
  await expect(md.locator(".cv-math-display .katex-display")).toHaveCount(2);
  await expect(md.locator("p", { hasText: "A run costs" })).toContainText("A run costs $5 and $10 with pairwise mode on;");
  await expect(md.locator("code", { hasText: "$$x$$" })).toBeVisible();
  // KaTeX's stylesheet came with it: its fonts, not the text's.
  await expect.poll(() => md.locator(".katex-html .mord.mathnormal").first().evaluate((el) => getComputedStyle(el).fontFamily)).toContain("KaTeX_Math");
  // A formula KaTeX can't read is its source, and the rest still draws.
  await expect(md.locator(".katex-error")).toHaveText("\\frac{1}{");
});

const BLOCKS_REPLY = [
  "## Rigid bodies, step by step",
  "",
  "Semi-implicit Euler, velocity first:",
  "",
  "```math",
  "\\begin{aligned} \\mathbf{v}_{t+\\Delta t} &= \\mathbf{v}_t + \\Delta t \\, M^{-1} \\mathbf{F} \\\\ \\mathbf{x}_{t+\\Delta t} &= \\mathbf{x}_t + \\Delta t \\, \\mathbf{v}_{t+\\Delta t} \\end{aligned}",
  "```",
  "",
  "The contact impulse, clamped so bodies only push:",
  "",
  "```latex",
  "\\lambda = \\max\\left(0, -\\frac{J\\mathbf{v} + b}{J M^{-1} J^{\\top}}\\right)",
  "```",
  "",
  "And the file to compile, which stays code:",
  "",
  "```latex",
  "\\documentclass{article}",
  "\\begin{document}",
  "Rigid bodies.",
  "\\end{document}",
  "```",
].join("\n");

test("a ```math block and a ```latex one of equations draw; a LaTeX document stays code", async ({ app }) => {
  mockOnly("lands a reply on the demo's agent");
  await app.open({ params: { view: "conversation" } });
  await app.openWorktree("gpu/judge-v2");
  await landReply(app.page, BLOCKS_REPLY);
  const md = app.chat.locator("[data-kind=text] .cv-md").filter({ hasText: "Rigid bodies, step by step" });
  await expect(md.locator(".cv-math-block .katex-display")).toHaveCount(2);
  await expect(md.locator(".katex-error")).toHaveCount(0);
  await expect(md.locator("pre code", { hasText: "\\documentclass{article}" })).toBeVisible();
});
