import type { Page } from "@playwright/test";

import { DIR, fakeAgent, type FakeAgent, ITEMS, SESSION } from "./fake-agent";
import { expect, mockOnly, test } from "./fixtures";

// The reply an agent is writing, shown as it grows (lib/draft, lib/draft-
// text): a draft read off the agent's screen, with a caret, until the
// transcript has the message, which then takes the draft's row in place.
// The demo's screen is driven through window.__berthDraft (lib/mock-
// conversation); the real reading runs on a stand-in agent.

const BOX = "gpu";
const AGENT = "judge-v2-claude";

type DraftHook = { show(b: string, s: string, t: string, c?: boolean): void; land(b: string, s: string, t: string): void; clear(b: string, s: string): void };
const draftHook = (page: Page, fn: "show" | "land" | "clear", text = "", clipped = false) =>
  page.evaluate(([fn, b, s, t, c]) => {
    const h = (window as unknown as { __berthDraft: DraftHook }).__berthDraft;
    if (fn === "show") h.show(b as string, s as string, t as string, c as boolean);
    else if (fn === "land") h.land(b as string, s as string, t as string);
    else h.clear(b as string, s as string);
  }, [fn, BOX, AGENT, text, clipped] as const);

const REPLY = [
  "## Judge v2 against the labelled set",
  "",
  "Judge v2 agrees with people on **91%** of the 200 labelled samples, up from 84% with v1. The gain is almost all in the long answers, where v1 rewarded length.",
  "",
  "- **Pairwise mode** settles ties that the single score left at 3.",
  "- The rubric's `refusal` rule no longer marks a correct refusal down.",
].join("\n");

test("with replies-as-written off in Settings, no draft shows and the reply lands whole", async ({ app }) => {
  mockOnly("drives the demo's screen");
  await app.open({ params: { view: "conversation" } });
  const appearance = await app.openSettings("appearance");
  const toggle = appearance.getByRole("switch", { name: "Show replies as they're written" });
  await expect(toggle).toBeChecked();
  await toggle.click();
  await expect.poll(() => app.stored("berth.prefs")).toMatchObject({ chatDrafts: false });

  await app.openWorktree("gpu/judge-v2");
  await expect(app.chat.locator("[data-kind=thinking]")).toHaveCount(1);
  await draftHook(app.page, "show", REPLY);
  // A beat for a draft to have drawn, had it been on.
  await app.page.waitForTimeout(400);
  await expect(app.chat.locator("[data-testid=chat-item][data-draft]")).toHaveCount(0);
  await draftHook(app.page, "land", REPLY);
  await expect(app.chat.getByText("Judge v2 agrees with people on")).toBeVisible();
});

test("a reply grows as a draft, then its message takes the same row", async ({ app }) => {
  mockOnly("drives the demo's screen");
  await app.open({ params: { view: "conversation" } });
  await app.openWorktree("gpu/judge-v2");
  const draft = app.chat.locator("[data-testid=chat-item][data-draft]");
  await expect(app.chat.locator("[data-kind=thinking]")).toHaveCount(1);

  await draftHook(app.page, "show", REPLY.slice(0, 120));
  await expect(draft).toHaveCount(1);
  await expect(draft).toContainText("Judge v2 agrees with people on 91%");
  // In progress: a caret, no Copy yet, and the work fold still at work.
  await expect(draft.locator(".cv-caret")).toHaveCount(1);
  await expect(draft.locator("[aria-busy=true]")).toHaveCount(1);
  // The agent's status stays under it.
  await expect(app.chat.locator("[data-kind=thinking]")).toHaveCount(1);
  await expect(app.chat.getByRole("button", { name: /^Working/ })).toBeVisible();

  await draftHook(app.page, "show", REPLY);
  await expect(draft.locator("li")).toHaveCount(2);
  await expect(draft.locator("li").last().locator(".cv-caret")).toHaveCount(1);

  // The row it is drawn in, marked to tell whether the message replaces it.
  const row = draft.locator("xpath=ancestor::*[@data-chat-row][1]");
  const key = await row.getAttribute("data-chat-row");
  await row.evaluate((el) => {
    el.dataset.e2eMark = "draft";
  });
  const before = await row.boundingBox();

  await draftHook(app.page, "land", REPLY);
  await expect(draft).toHaveCount(0);
  const landed = app.chat.locator("[data-e2e-mark=draft]");
  await expect(landed).toHaveAttribute("data-chat-row", key!);
  await expect(landed.locator("[data-kind=text]")).toContainText("The rubric's refusal rule no longer marks a correct refusal down.");
  await expect(landed.locator(".cv-caret")).toHaveCount(0);
  await expect(landed.getByRole("button", { name: "Copy reply" })).toBeAttached();
  // Same place, same height: nothing moved.
  const after = await landed.boundingBox();
  expect(Math.abs(after!.height - before!.height)).toBeLessThanOrEqual(1);
  expect(Math.abs(after!.y - before!.y)).toBeLessThanOrEqual(1);
  // One message: the screen still shows those words, but they don't draw twice.
  await expect(app.chat.getByText("Pairwise mode")).toHaveCount(1);
  await draftHook(app.page, "show", REPLY);
  await expect(draft).toHaveCount(0);
});

test("a draft taller than the screen keeps its start; one first seen cut off says so", async ({ app }) => {
  mockOnly("drives the demo's screen");
  await app.open({ params: { view: "conversation" } });
  await app.openWorktree("gpu/judge-v2");
  const draft = app.chat.locator("[data-testid=chat-item][data-draft]");
  const start = "Here is where the two judges disagree, sample by sample, across the labelled set.";
  const middle = "Most disagreements are long answers that v1 scored on length rather than substance.";
  const end = "The rest are refusals, which the old rubric always marked down.";
  await draftHook(app.page, "show", `${start}\n\n${middle}`);
  await expect(draft).toContainText(middle);
  // The screen scrolled: its start is gone from it, not from the chat.
  await draftHook(app.page, "show", `${middle}\n\n${end}`, true);
  await expect(draft).toContainText(end);
  await expect(draft).toContainText(start);
  await expect(draft.locator(".cv-clipped")).toHaveCount(0);

  // A reply first seen already cut off: "…" above what shows.
  await draftHook(app.page, "clear");
  await expect(draft).toHaveCount(0);
  await draftHook(app.page, "show", "and the last of the refusals was a correct one, which v2 now passes.", true);
  await expect(draft.locator(".cv-clipped")).toContainText("…");
  await expect(draft.locator(".cv-clipped")).toContainText("Its start is above the agent's screen");
});

const scroller = (page: Page) => page.locator("[data-testid=pane]:visible [data-testid=chat] .overflow-y-auto").first();

// A step in the demo's record, beside its replies (lib/mock-conversation).
const recordHook = (page: Page, item: Record<string, unknown>) =>
  page.evaluate(([b, s, it]) => (window as unknown as { __berthDraft: { record(b: string, s: string, it: unknown): void } }).__berthDraft.record(b, s, it), [BOX, AGENT, item] as const);

test("when a long reply folds away and the chat fits again, every row draws, in WebKit too", async ({ app }) => {
  mockOnly("drives the demo's screen");
  await app.open({ params: { view: "conversation" } });
  await app.openWorktree("gpu/judge-v2");
  const rows = app.chat.locator("[data-chat-row]");
  const prompt = rows.filter({ hasText: "Pick up judge-v2" });
  await expect(app.chat.locator("[data-kind=thinking]")).toHaveCount(1);
  // More rows than the list draws beyond the view (edits always show), then
  // a reply taller than the screen: the chat keeps to its foot.
  for (let i = 0; i < 5; i++) await recordHook(app.page, { kind: "edit", file: `src/judge/rubric${i}.ts`, added: 23, removed: 1 });
  await draftHook(app.page, "land", Array.from({ length: 40 }, (_, i) => `Note ${i}`).join("\n\n"));
  await expect(prompt).toHaveCount(0);
  // WebKit (the Mac app) moves a view the chat shrank under without a
  // scroll event: as there.
  await app.page.evaluate(() => window.addEventListener("scroll", (e) => e.isTrusted && e.stopImmediatePropagation(), true));
  // It runs a command and says something short: the long reply folds into
  // the turn's steps, and the chat fits on the screen.
  await recordHook(app.page, { kind: "tools", verb: "Run", done: true, items: [{ verb: "Run", target: "pnpm test judge" }] });
  await draftHook(app.page, "land", "Passing. Changelog entry, then the full checks.");
  await expect(app.chat.getByText("Passing. Changelog entry")).toBeVisible();
  await expect(scroller(app.page)).toHaveJSProperty("scrollTop", 0);
  await expect(prompt).toBeVisible();
  await expect(rows.filter({ hasText: "rubric0.ts" })).toBeVisible();
});

// The real reading, on a stand-in agent: the box offers drafts.
test.describe("on a box with drafts", () => {
  let agent: FakeAgent;
  test.beforeEach(async () => {
    mockOnly("runs on a stand-in agent");
    agent = await fakeAgent();
    agent.capabilities = ["transcript", "turns", "draft"];
  });
  test.afterEach(async () => agent?.close());

  test("the reply grows from the screen, and its message lands as soon as the box says", async ({ app }) => {
    const reply = "The retry loop never backed off, so a slow provider got hammered. It now waits 500ms, doubling up to 30s, and gives up after five tries.";
    let shown = reply.slice(0, 40);
    agent.draft = () => ({ body: { agent: "claude", text: shown, status: { word: "Whirring…", elapsed: "8s" } } });
    const reads: number[] = [];
    let items = ITEMS.slice(0, 1);
    agent.transcript = () => {
      reads.push(Date.now());
      return { body: { source: "claude", items, next: items.length, crew: [], gen: "1.0", start: 10, file: "abc" } };
    };
    await app.open({ agent, params: { view: "conversation" } });
    await app.openWorktree("devl/fix");
    const draft = app.chat.locator("[data-testid=chat-item][data-draft]");
    await expect(draft).toContainText("The retry loop never backed off");
    shown = reply;
    await expect(draft).toContainText("gives up after five tries");
    // Read every 600ms or so while it writes.
    const n = agent.calls.filter((c) => c.endsWith("/draft")).length;
    await app.page.waitForTimeout(2000);
    expect(agent.calls.filter((c) => c.endsWith("/draft")).length - n).toBeGreaterThanOrEqual(3);

    const row = draft.locator("xpath=ancestor::*[@data-chat-row][1]");
    await row.evaluate((el) => {
      el.dataset.e2eMark = "draft";
    });
    // The transcript has the message; the box says so just after a read,
    // so a read now is the event's, not the 2s poll's.
    items = [...ITEMS.slice(0, 1), { kind: "text", id: "t2", text: reply, off: 300 } as (typeof ITEMS)[number]];
    const seen = reads.length;
    await expect.poll(() => reads.length).toBeGreaterThan(seen);
    const at = Date.now();
    agent.event({ type: "transcript.changed", data: { session: SESSION, name: SESSION, path: DIR, size: 4096 } });
    await expect(draft).toHaveCount(0);
    expect(reads.find((t) => t >= at)! - at).toBeLessThan(800);
    await expect(app.chat.locator("[data-e2e-mark=draft] [data-kind=text]")).toContainText("gives up after five tries");
  });

  test("a box without drafts is read as before", async ({ app }) => {
    agent.capabilities = ["transcript", "turns"];
    await app.open({ agent, params: { view: "conversation" } });
    await app.openWorktree("devl/fix");
    await expect(app.chat.getByText("The retry loop never backs off; fixed it.")).toBeVisible();
    await expect.poll(() => agent.calls.filter((c) => c.endsWith("/screen")).length).toBeGreaterThan(0);
    expect(agent.calls.filter((c) => c.endsWith("/draft"))).toHaveLength(0);
  });
});
