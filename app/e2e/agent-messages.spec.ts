import type { Page } from "@playwright/test";

import { BOX, fakeAgent, type FakeAgent } from "./fake-agent";
import { type App, expect, mockOnly, test } from "./fixtures";

// Messages to an agent that aren't the person's (internal/transcript/
// peer.go, components/conversation/agent-message): other agents' messages
// are cards, Claude Code's pings are lines, a mid-turn message is the
// person's own, and reminders never show. The demo's gpu/ci-flake chat
// (lib/mock-agent-messages) is the story; ?scene=busy a teammate on a team.

const pane = "[data-testid=pane]:visible";

// The chat draws the rows in view: these bring one end into view.
const scrollChat = (page: Page, to: "top" | "bottom") =>
  page.evaluate((to) => {
    const el = [...document.querySelectorAll<HTMLElement>("[data-testid=pane]")].find((x) => x.offsetParent)?.querySelector<HTMLElement>("[data-testid=chat]");
    if (!el) return;
    const sc = [el, ...el.querySelectorAll<HTMLElement>("*")].find((x) => x.scrollHeight > x.clientHeight + 4 && getComputedStyle(x).overflowY !== "visible");
    if (sc) sc.scrollTop = to === "top" ? 0 : sc.scrollHeight;
  }, to);

async function openStory(app: App, scene?: string) {
  mockOnly("reads the demo's chat");
  await app.open({ params: { view: "conversation", ...(scene ? { scene } : {}) } });
  await app.openWorktree("gpu/ci-flake");
  await expect(app.chat.locator("[data-testid=chat-item]").first()).toBeVisible();
}

const card = (app: App, kind: string, name: string) => app.chat.locator(`[data-am-card=${kind}]`).filter({ has: app.page.locator("[data-card-name]", { hasText: name }) });

test("each message reads as a card from its sender, never as the person's", async ({ app }) => {
  await openStory(app);
  await scrollChat(app.page, "top");
  const report = card(app, "report", "Map ledger writes");
  await expect(report).toBeVisible();
  await expect(report).toContainText("Helper");
  await expect(report.locator("[data-chip]")).toHaveText("Report");
  await expect(report).toContainText("Ledger writes in acme/billing");
  await expect(report).toContainText("two of them have no idempotency key");
  await expect(report).toContainText("5m 00s");
  // To Claude, with a tip that you didn't type it.
  await report.locator("[data-to-agent]").hover();
  await expect(app.page.getByText("Map ledger writes (helper) sent this to Claude. You didn't type it.")).toBeVisible();

  // A failed ping is one line, with its count.
  const failed = app.chat.locator("[data-ping=failed]");
  await expect(failed).toContainText("Fix retry tests failed: Agent stalled");
  await expect(failed.locator("[data-ping-repeat]")).toHaveText("×2");

  await scrollChat(app.page, "bottom");
  const question = card(app, "question", "payments-api");
  await expect(question).toContainText("Session");
  await expect(question.locator("[data-chip]")).toHaveText("Question");
  await expect(question.locator("[data-needs-you-mark]")).toHaveText("needs you");
  // Claude Code's own notice is a line too.
  await expect(app.chat.locator("[data-ping=info]")).toContainText("Not watching Artifact");
  // None of it is the person's bubble.
  await expect(app.chat.locator("[data-kind=user]").filter({ hasText: /Has the ledger migration merged|Every write to|Agent stalled/ })).toHaveCount(0);
  await expect(app.chat).not.toContainText("Another Claude session");
  await expect(app.chat).not.toContainText("task-notification");
});

test("a report unfolds in place and folds again", async ({ app }) => {
  await openStory(app);
  await scrollChat(app.page, "top");
  const report = card(app, "report", "Map ledger writes");
  const read = report.locator("[data-am-read]");
  await expect(read).toHaveText(/Read report/);
  await expect(report).toContainText("17 lines");
  await read.click();
  await expect(read).toHaveAttribute("aria-expanded", "true");
  const full = report.locator("[data-am-full]");
  await expect(full.getByRole("heading", { name: "Where the writes are" })).toBeVisible();
  await expect(full.locator("table")).toContainText("services/billing/refund.go:132");
  await expect(full).toContainText("91c55ae");
  // Short enough to read whole here: no tab needed.
  await expect(report.getByRole("button", { name: /Open in tab/ })).toHaveCount(0);
  await read.click();
  await expect(full).toHaveCount(0);
  await expect(read).toHaveText(/Read report/);
});

test("Open takes a helper's report to its conversation in a tab", async ({ app }) => {
  await openStory(app);
  await scrollChat(app.page, "top");
  const strip = app.page.locator("[data-tab-strip] [data-tab]");
  const before = await strip.count();
  await card(app, "report", "Map ledger writes").locator("[data-am-open]").click();
  await expect(strip).toHaveCount(before + 1);
  await expect(strip.and(app.page.locator("[aria-selected=true]"))).toContainText("Map ledger writes");
  const helper = app.page.locator("[data-testid=pane][data-pane-kind=helper]:visible");
  await expect(helper.getByRole("heading", { name: "Map ledger writes" })).toBeVisible();
});

test("a session's question needs you, and Reply quotes it into the reply box", async ({ app }) => {
  await openStory(app);
  await scrollChat(app.page, "bottom");
  const question = card(app, "question", "payments-api");
  await expect(question).toHaveAttribute("data-needs-you", "");
  await question.locator("[data-am-reply]").click();
  const box = app.composer.locator("textarea");
  await expect(box).toBeFocused();
  await expect(box).toHaveValue(/^> Has the ledger migration merged yet\?/);
  await expect(box).toHaveValue(/\n\nReply to payments-api: $/);
});

test("successes that came together fold to one line, open to each", async ({ app }) => {
  await openStory(app);
  await scrollChat(app.page, "top");
  const group = app.chat.locator("[data-ping-group]");
  await expect(group).toHaveAttribute("data-ping-group", "3");
  await expect(group).toContainText("3 finished");
  await expect(group).toContainText("Run the billing tests, Lint acme/billing, Check index sizes");
  await expect(group.locator("[data-ping]")).toHaveCount(0);
  await group.locator("button[aria-expanded]").click();
  await expect(group.locator("[data-ping=done]")).toHaveCount(3);
  await expect(group.locator("[data-ping=done]").first()).toContainText("Run the billing tests completed (exit code 0)");
  // The helper among them still opens.
  await expect(group.locator("[data-ping=done]").last().locator("[data-am-open]")).toBeVisible();
});

test("a message sent mid-turn is the person's own, marked as such", async ({ app }) => {
  await openStory(app);
  await scrollChat(app.page, "top");
  const mid = app.chat.locator("[data-kind=user]").filter({ hasText: "Leave ledger_v1 in place" });
  await expect(mid).toBeVisible();
  await expect(mid.locator("[data-mid-turn]")).toHaveText("Sent while Claude was working");
  // The first prompt was sent between turns.
  await expect(app.chat.locator("[data-kind=user]").first().locator("[data-mid-turn]")).toHaveCount(0);
});

test("a busy team: teammates, the lead and Shipyard, with teammates in the crew", async ({ app }) => {
  await openStory(app, "busy");
  await scrollChat(app.page, "top");
  const lead = card(app, "instruction", "Lead");
  await expect(lead.locator("[data-chip]")).toHaveText("Instruction");
  await expect(lead).toContainText("Hold the backfill");
  const schema = card(app, "update", "schema-review");
  await expect(schema).toContainText("Teammate");
  await expect(schema.locator("[data-avatar]")).toHaveText("SR");
  await expect(schema.locator("[data-am-open]")).toHaveCount(0);

  await scrollChat(app.page, "bottom");
  // An answered question waits on nobody.
  const asked = card(app, "question", "export-job");
  await expect(asked).not.toHaveAttribute("data-needs-you", "");
  await expect(asked.locator("[data-answered]")).toBeVisible();
  await expect(app.chat.locator("[data-ping=stopped]")).toContainText("Dry-run the backfill was stopped");
  await expect(app.chat.locator("[data-ping=stopped] [data-ping-repeat]")).toHaveText("×2");

  // A report longer than a screen reads its first screen here, and whole
  // in its helper's tab.
  const long = card(app, "report", "Map ledger writes");
  await expect(long).toContainText("54 lines");
  await long.locator("[data-am-read]").click();
  await expect(long.getByRole("button", { name: /Open in tab/ })).toBeVisible();

  // The crew lists the teammates who wrote.
  const crew = app.chat.locator("[data-crew]");
  if ((await crew.getAttribute("data-open")) === null) await crew.locator("[data-crew-toggle]").click();
  await expect(crew.locator("[data-teammate]")).toHaveCount(3);
  await expect(crew.locator("[data-teammate=schema-review]")).toContainText("Schema is fine; one nit");
  await expect(crew.locator("[data-teammate=export-job]")).toContainText("Teammate");
  await expect(crew).toContainText("7");
});

test("Shipyard's own report takes the same head as the agents'", async ({ app }) => {
  await openStory(app, "busy");
  await scrollChat(app.page, "bottom");
  const berth = app.chat.locator("[data-report=finished]");
  await expect(berth.locator("[data-card-name]")).toHaveText("billing-docs");
  await expect(berth.locator("[data-chip]")).toHaveText("Finished");
  await expect(berth).toContainText("+31");
  await expect(berth.locator("[data-to-agent]")).toBeVisible();
  await berth.locator("[data-to-agent]").hover();
  await expect(app.page.getByText(/Shipyard reported back on billing-docs-claude.*You didn't type it\./)).toBeVisible();
});

test("a crew row's Report badge brings the report into view", async ({ app }) => {
  await openStory(app);
  const crew = app.chat.locator("[data-crew]");
  if ((await crew.getAttribute("data-open")) === null) await crew.locator("[data-crew-toggle]").click();
  await scrollChat(app.page, "bottom");
  const report = card(app, "report", "Map ledger writes");
  // Scrolled to the bottom, the report is above, out of view (its edge may
  // peek in under the tab bar); the badge brings it into view.
  await expect(report).not.toBeInViewport({ ratio: 0.5 });
  await crew.locator("[data-crew-report=toolu_ledger]").click();
  await expect(report).toBeInViewport({ ratio: 0.5 });
  // The time column keeps "12m 00s"-wide times on one line.
  const time = crew.locator("[data-crew-list] li").first().locator("span.font-mono");
  await expect.poll(async () => (await time.boundingBox())?.height ?? 99).toBeLessThan(22);
});

// What a box sends, through the app's own reading of it.
test.describe("from a box", () => {
  let agent: FakeAgent;
  test.afterEach(async () => agent?.close());

  test("reminders never show, and the new kinds draw from the box's items", async ({ app }) => {
    mockOnly("runs on a stand-in agent");
    agent = await fakeAgent();
    const from = { id: "payments-api", name: "payments-api", kind: "session" };
    agent.transcript = () => ({
      body: {
        source: "claude",
        // An older box leaves the reminder in the prompt; a newer one sends
        // messages as their own kinds.
        items: [
          { kind: "user", id: "u1", text: "Fix the acme refunds\n\n<system-reminder>\nThe task tools haven't been used recently.\n</system-reminder>", off: 10 },
          { kind: "agent-message", id: "m1", off: 100, msg: { from, intent: "update", summary: "Webhook v2 is merged.", body: "Webhook v2 is merged." } },
          { kind: "ping", id: "p1", off: 200, msg: { from: { id: "b1", name: "Background command", kind: "harness" }, status: "failed", summary: "Typecheck failed with exit code 2", repeat: 2 } },
          { kind: "user", id: "u2", text: "Keep ledger_v1 for now", midTurn: true, off: 300 },
          { kind: "user", id: "u3", text: "<system-reminder>\nOnly a reminder.\n</system-reminder>", off: 400 },
        ],
        next: 5,
        crew: [],
        gen: "1.0",
        start: 10,
        file: "abc",
      },
    });
    await app.open({ agent, params: { view: "conversation" } });
    await app.openWorktree(`${BOX}/fix`);
    // "You:" is for screen readers (sr-only), the rest is the prompt as sent.
    await expect(app.chat.locator("[data-kind=user]").first()).toHaveText("You: Fix the acme refunds");
    await expect(app.chat).not.toContainText("system-reminder");
    await expect(app.chat).not.toContainText("task tools");
    await expect(app.chat).not.toContainText("Only a reminder");
    await expect(card(app, "update", "payments-api")).toContainText("Webhook v2 is merged.");
    await expect(app.chat.locator("[data-ping=failed]")).toContainText("Typecheck failed");
    await expect(app.chat.locator("[data-kind=user]").filter({ hasText: "Keep ledger_v1" }).locator("[data-mid-turn]")).toBeVisible();
  });
});
