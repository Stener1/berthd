import { expect, mockOnly, test } from "./fixtures";

// Agents run in chat wherever the box can (lib/structured-core.ts): there
// is no switch for it, in Labs or anywhere. Permissions (Bypass | Ask)
// lives in Settings › Agents and models.

type Saved = { structuredClaude?: boolean; approvals?: string; labs?: boolean };

test("Labs has no switch for agents in chat, and Permissions is in Agents and models", async ({ app }) => {
  await app.open();
  const labs = await app.openSettings("labs");
  await expect(labs.getByRole("switch", { name: "Harbour home and conversations" })).toBeVisible();
  await expect(labs.getByText("Structured agents")).toHaveCount(0);
  await expect(labs.getByText("Permissions", { exact: true })).toHaveCount(0);

  const agents = await app.openSettings("agents");
  await expect(agents.getByText("Permissions", { exact: true })).toBeVisible();
  await expect(agents.getByText(/in chat|structured/i)).toHaveCount(0);
  await expect(agents.getByRole("switch", { name: /chat|structured/i })).toHaveCount(0);
  const bypass = agents.getByRole("button", { name: "Bypass" });
  await expect(bypass).toHaveAttribute("aria-pressed", "true");
  await agents.getByRole("button", { name: "Ask" }).click();
  await expect.poll(async () => ((await app.stored("berth.prefs")) as Saved).approvals).toBe("ask");
});

test("searching Settings for permissions finds Agents and models", async ({ app, page }) => {
  await app.open();
  await app.openSettings("labs");
  const find = page.getByRole("textbox", { name: "Find a setting" });
  await find.fill("permissions");
  const found = page.getByRole("listbox", { name: "Settings found" });
  await expect(found.getByRole("option")).toHaveText(["Agents"]);
  // Nothing is called "structured" any more.
  await find.fill("structured");
  await expect(found.getByRole("option")).toHaveCount(0);
});

test("the Labs switch an older Shipyard saved is dropped and changes nothing", async ({ app }) => {
  await app.open({ prefs: { structuredClaude: false, approvals: "ask", version: 3 } });
  await expect.poll(async () => (await app.stored("berth.prefs")) as Saved).toMatchObject({ approvals: "ask" });
  expect(((await app.stored("berth.prefs")) as Saved).structuredClaude).toBeUndefined();
  const agents = await app.openSettings("agents");
  await expect(agents.getByRole("button", { name: "Ask" })).toHaveAttribute("aria-pressed", "true");
});

test("an agent in chat opens as its conversation with Labs off", async ({ app }) => {
  mockOnly("the demo's structured session is a mock fixture (?structured)");
  // ?structured: search-perf's Claude runs through its stream.
  await app.open({ prefs: { labs: false, labsChosen: true, version: 3 }, params: { structured: "1" } });
  await app.openWorktree("devl/search-perf");
  await expect(app.chat).toBeVisible();
  await expect(app.composer.getByRole("textbox", { name: "Reply" })).toBeVisible();
});

test("a new Claude Code starts in chat with Labs off", async ({ app, page }) => {
  mockOnly("the demo's box has driver.claude only with ?structured");
  await app.open({ prefs: { labs: false, labsChosen: true, version: 3 }, params: { structured: "1" } });
  await app.openWorktree("devl/search-perf");
  await expect(app.chat).toBeVisible();
  await page.getByRole("button", { name: "Pane actions" }).first().click();
  await page.getByRole("menuitem", { name: "Claude Code" }).first().click();
  // Two chats side by side: the new agent is a conversation too, not a terminal.
  await expect(app.chat).toHaveCount(2);
});
