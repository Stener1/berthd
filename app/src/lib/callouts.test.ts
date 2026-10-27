// node --experimental-strip-types --test src/lib/callouts.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { micromark } from "micromark";
import { gfm, gfmHtml } from "micromark-extension-gfm";

import { CALLOUT, CALLOUTS, callout } from "./callouts.ts";

// As markdown.tsx draws a reply.
const draw = (md: string) => micromark(md, { extensions: [gfm()], htmlExtensions: [gfmHtml()] }).replace(CALLOUT, callout);
const kindOf = (html: string) => /data-callout="(\w+)"/.exec(html)?.[1];
const titleOf = (html: string) => /<p class="cv-callout-title"><svg[^]*?<\/svg>([^<]*)<\/p>/.exec(html)?.[1];

test("GitHub's five kinds, in any case", () => {
  for (const [marker, kind, label] of [
    ["NOTE", "note", "Note"],
    ["TIP", "tip", "Tip"],
    ["IMPORTANT", "important", "Important"],
    ["WARNING", "warning", "Warning"],
    ["CAUTION", "caution", "Caution"],
    ["note", "note", "Note"],
    ["Warning", "warning", "Warning"],
  ]) {
    const html = draw(`> [!${marker}]\n> Read this first.`);
    assert.equal(kindOf(html), kind, marker);
    assert.equal(titleOf(html), label, marker);
    // The marker is gone; the words follow in a paragraph of their own.
    assert.ok(!html.includes(`[!${marker}]`), marker);
    assert.match(html, /<\/p>\n<p>Read this first\.<\/p>\n<\/blockquote>/);
  }
  assert.deepEqual(Object.keys(CALLOUTS), ["note", "tip", "important", "warning", "caution"]);
});

test("the body can be paragraphs, lists and code, or nothing yet", () => {
  const paras = draw("> [!TIP]\n>\n> One.\n>\n> Two.");
  assert.equal(kindOf(paras), "tip");
  assert.match(paras, /<p>One\.<\/p>\n<p>Two\.<\/p>/);

  const list = draw("> [!IMPORTANT]\n> - a\n> - b");
  assert.equal(kindOf(list), "important");
  assert.match(list, /<\/p>\n<ul>\n<li>a<\/li>/);

  assert.equal(kindOf(draw("> [!CAUTION]")), "caution");
  // Two spaces or a backslash after the marker: a hard break micromark draws.
  const hard = draw("> [!NOTE]  \n> After.");
  assert.equal(kindOf(hard), "note");
  assert.ok(!hard.includes("<br />"));
  assert.equal(kindOf(draw("> [!NOTE]\\\n> After.")), "note");
  // In a list, as GitHub has it too.
  assert.equal(kindOf(draw("- > [!WARNING]\n  > Careful.")), "warning");
});

test("any other quote stays a quote", () => {
  for (const md of [
    "> Just a quote.",
    "> [!NOTE] on the same line as words",
    "> [!DANGER]\n> Not one of GitHub's.",
    "> [NOTE]\n> No bang.",
    "> **[!NOTE]**\n> Bold.",
    "> Words first.\n> [!NOTE]",
    "[!NOTE]\nNot in a quote.",
    "```\n> [!NOTE]\n```",
    "`> [!NOTE]`",
  ]) {
    const html = draw(md);
    assert.equal(kindOf(html), undefined, md);
    assert.ok(!html.includes("cv-callout"), md);
  }
});

test("a draft halfway through its marker doesn't throw, and is a quote", () => {
  for (const md of [">", "> [", "> [!", "> [!NO", "> [!NOTE", "> [!NOTE]\n>", "> [!NOTE]\n> half a sen"]) {
    assert.doesNotThrow(() => draw(md), md);
  }
  assert.equal(kindOf(draw("> [!NOTE")), undefined);
  assert.equal(kindOf(draw("> [!NOTE]\n> half a sen")), "note");
});

test("the reply's HTML stays escaped inside a callout", () => {
  const html = draw('> [!WARNING]\n> <script>alert(1)</script> <img src=x onerror="y">');
  assert.equal(kindOf(html), "warning");
  assert.ok(!html.includes("<script>"));
  assert.ok(!html.includes("<img"));
  assert.match(html, /&lt;script&gt;/);
});

test("callouts nested in a callout's quote each keep their own kind", () => {
  const html = draw("> [!NOTE]\n> Outer.\n>\n> > [!TIP]\n> > Inner.");
  assert.deepEqual([...html.matchAll(/data-callout="(\w+)"/g)].map((m) => m[1]), ["note", "tip"]);
});
