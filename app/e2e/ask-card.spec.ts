import { readFileSync } from "node:fs";
import { join } from "node:path";

import { BOX, fakeAgent, type FakeAgent, SESSION } from "./fake-agent";
import { expect, mockOnly, test } from "./fixtures";

// The chat's question card ("Needs your answer") is drawn from what the
// agent waits on: options its screen shows, or its hooks' ask. A wait with
// neither, such as turn check's reading of a last message that asks in
// words, draws none, and never a Yes / No it made up, whose Yes would type
// the word. Claude Code's footer ends in "/rc" when Remote Control is on,
// which is no question either.

let agent: FakeAgent;

test.beforeEach(async () => {
  mockOnly("runs on a stand-in agent");
  agent = await fakeAgent();
});

test.afterEach(async () => {
  await agent?.close();
});

// The box's own screens, which its tests read too.
const screen = (name: string) => readFileSync(join(import.meta.dirname, "../../internal/box/testdata/screens", name), "utf8");

const ASKED = "Which ones do you want moved? I'd say 1 and 3.";
const transcript = () => ({
  body: {
    source: "claude",
    items: [
      { kind: "user", id: "u1", text: "Which of the flaky tests should move to the nightly suite?", off: 10 },
      { kind: "text", id: "t1", text: `Three of them fail only under load:\n\n1. checkout retries the payment twice\n2. the cart badge counts a removed item\n3. search times out on the first query\n\n${ASKED}`, off: 200 },
    ],
    next: 2,
    crew: [],
    gen: "1.0",
    start: 10,
    file: "abc",
  },
});

// The session waiting (with session's fields), its screen, and its chat open.
async function openWaiting(app: import("./fixtures").App, session: Record<string, unknown>, shows: string) {
  agent.session = { agent_state: "waiting", turn: `${SESSION}#4`, ...session };
  agent.transcript = transcript;
  agent.screen = () => shows;
  await app.open({ agent, params: { view: "conversation" } });
  await app.openWorktree(`${BOX}/fix`);
}

async function noCard(app: import("./fixtures").App) {
  await expect(app.chat.getByText("Needs your answer")).toHaveCount(0);
  await expect(app.chat.getByRole("button", { name: "Yes", exact: true })).toHaveCount(0);
}

test("a last message that asks in words draws no question card, before or after /rc", async ({ app }) => {
  await openWaiting(app, { soft_wait: true }, screen("asked-in-reply.ansi"));
  await expect(app.chat.getByText(ASKED)).toBeVisible();
  await noCard(app);
  if (process.env.BERTH_SHOTS) await app.page.screenshot({ path: `${process.env.BERTH_SHOTS}/chat-asked-in-reply.png` });

  // /rc from the reply box goes to the agent, which shows Remote Control's
  // menu and stays waiting on its question: still no card.
  const box = app.composer.getByRole("textbox", { name: "Reply" });
  await box.fill("/rc");
  await box.press("Enter");
  await expect.poll(() => agent.sends.map((s) => s.text)).toContainEqual(expect.stringMatching(/^\/(rc|remote-control)\b/));
  await noCard(app);
});

for (const [what, file] of [
  ["its prompt in bypass mode", "bypass-switched.ansi"],
  ["Remote Control's menu", "remote-control.ansi"],
]) {
  test(`a wait with no options on screen draws no made-up Yes / No: ${what}`, async ({ app }) => {
    // An older box says nothing of turn check's wait: the screen is read,
    // and has no options to answer it with.
    await openWaiting(app, {}, screen(file));
    await expect(app.chat.getByText(ASKED)).toBeVisible();
    await expect.poll(() => agent.calls.filter((c) => c.includes(`/sessions/${SESSION}/screen`)).length).toBeGreaterThan(0);
    await noCard(app);
    await expect(app.chat.getByText("/rc", { exact: true })).toHaveCount(0);
  });
}

test("a question its screen shows options for still gets its card", async ({ app }) => {
  const RULE = "─".repeat(80);
  await openWaiting(app, {}, [RULE, " Overwrite the lockfile?", "", " ❯ 1. Overwrite it", "   2. Keep mine", "", " Esc to cancel"].join("\n"));
  const card = app.chat.locator("[data-testid=chat-item][data-kind=ask]");
  await expect(card).toContainText("Needs your answer");
  await expect(card).toContainText("Overwrite the lockfile?");
  await expect(card.getByRole("button", { name: /Overwrite it/ })).toBeVisible();
  await expect(card.getByRole("button", { name: /Keep mine/ })).toBeVisible();
  await expect(card.getByRole("button", { name: "Yes", exact: true })).toHaveCount(0);
  await card.getByRole("button", { name: /Keep mine/ }).click();
  await expect.poll(() => agent.sends.map((s) => s.text)).toContain("2");
});

// The board's card offers the options of a menu on the agent's screen as
// chips: a plan in its last message, above its prompt, is no menu.
const PLAN = [
  "● What I'd build:",
  "",
  "  1. checkout retries the payment twice",
  "  2. the cart badge counts a removed item",
  "  3. search times out on the first query",
  "  4. Later: the nightly suite, once you want it.",
  "",
  "  Should I start on 1-3?",
  "",
  "─".repeat(80),
  "❯ ",
  "─".repeat(80),
  "  ⏵⏵ bypass permissions on (shift+tab to cycle)",
  `${" ".repeat(75)}/rc`,
].join("\n");

async function openBoard(app: import("./fixtures").App, session: Record<string, unknown>, shows: string) {
  agent.session = { agent_state: "waiting", turn: `${SESSION}#4`, ...session };
  agent.screen = () => shows;
  await app.open({ agent, prefs: { labs: false, labsChosen: true, version: 3 } });
  await app.page.getByTestId("nav-home").click();
  const card = app.page.locator("article", { hasText: "Fix the flaky checkout test" });
  await expect(card).toBeVisible();
  await expect.poll(() => agent.calls.filter((c) => c.includes(`/sessions/${SESSION}/screen`)).length).toBeGreaterThan(0);
  return card;
}

for (const [what, session] of [
  ["turn check's wait", { soft_wait: true }],
  ["an older box's wait", {}],
] as const) {
  test(`the board offers no plan in a last message as answers: ${what}`, async ({ app }) => {
    const card = await openBoard(app, session, PLAN);
    await expect(card.getByText("Should I start on 1-3?")).toBeVisible();
    await expect(card.getByRole("button", { name: /checkout retries|search times out/ })).toHaveCount(0);
    if (process.env.BERTH_SHOTS) await app.page.screenshot({ path: `${process.env.BERTH_SHOTS}/board-plan-${Object.keys(session).length ? "soft" : "older"}.png` });
  });
}

test("the board still offers a menu's options as answers", async ({ app }) => {
  const card = await openBoard(app, {}, ["─".repeat(80), " Overwrite the lockfile?", "", " ❯ 1. Overwrite it", "   2. Keep mine", "", " Esc to cancel"].join("\n"));
  await expect(card.getByRole("button", { name: /Overwrite it/ })).toBeVisible();
  await expect(card.getByRole("button", { name: /Keep mine/ })).toBeVisible();
});
