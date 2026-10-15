import type { Locator } from "@playwright/test";

import { type App, expect, mockOnly, test } from "./fixtures";

// The header picture (Settings › Appearance › Header picture): the painting
// across the top of a new chat, an empty worktree and Home. The harbour by
// default; a plate, a picture of your own, or none.

// An 8×4 PNG, sky over sand, as a picture of your own.
const PNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAgAAAAECAIAAAA8r+mnAAAACXBIWXMAAAPoAAAD6AG1e1JrAAAAGElEQVQImWOI6rmEFTHglDixqgIrwikBAAqpOiFhDrg6AAAAAElFTkSuQmCC", "base64");

// ?scene=first-prompt: the mock's agents at rest haven't been asked anything.
const OPEN = { params: { view: "conversation", scene: "first-prompt" } };

const band = (scope: Locator | App["page"]) => scope.locator("[data-testid=header-picture]:visible");

async function settings(app: App) {
  const appearance = await app.openSettings("appearance");
  const section = appearance.getByTestId("header-picture-settings");
  await section.scrollIntoViewIfNeeded();
  return section;
}

// A new chat: an agent that hasn't been asked anything yet.
async function newChat(app: App) {
  await app.openWorktree("devl/order-export");
  const pane = app.page.locator("[data-testid=pane]:visible").first();
  await expect(pane.getByPlaceholder(/What should/).or(pane.getByRole("textbox")).first()).toBeVisible();
  return pane;
}

test("a painting chosen in Settings tops a new chat, an empty worktree and Home, and stays after a reload", async ({ app }) => {
  mockOnly("the first prompt is a mock scene");
  await app.open({ ...OPEN, theme: "berth-light" });
  const section = await settings(app);
  const meadow = section.getByRole("group", { name: "Paintings" }).getByRole("button", { name: "Meadow" });
  // The harbour until chosen otherwise.
  await expect(section.getByRole("button", { name: "Harbour, by the time of day" })).toHaveAttribute("aria-pressed", "true");
  await meadow.click();
  await expect(meadow).toHaveAttribute("aria-pressed", "true");
  await expect.poll(() => app.stored("berth.prefs")).toMatchObject({ headerPicture: { source: "builtin", builtin: "meadow" } });

  // A new chat.
  const chat = await newChat(app);
  await expect(band(chat)).toHaveAttribute("data-picture", "meadow");
  await expect(band(chat).locator("[data-drawn]")).toBeAttached();

  // Home.
  await app.page.getByTestId("nav-home").click();
  await expect(band(app.page)).toHaveAttribute("data-picture", "meadow");

  // An empty worktree: its launcher, once its tabs are closed.
  await app.openWorktree("devl/qa-deck");
  const tabs = app.page.locator("[data-tab-strip] [data-tab]:visible");
  await expect(tabs.first()).toBeVisible();
  while ((await tabs.count()) > 0) {
    const n = await tabs.count();
    await tabs.first().click({ button: "middle" });
    await expect(tabs).toHaveCount(n - 1);
  }
  await expect(app.page.getByRole("button", { name: "New shell" })).toBeVisible();
  await expect(band(app.page)).toHaveAttribute("data-picture", "meadow");

  // A reload keeps it.
  await app.page.reload();
  await expect(app.page.getByTestId("nav-home")).toBeVisible();
  await app.page.getByTestId("nav-home").click();
  await expect(band(app.page)).toHaveAttribute("data-picture", "meadow");
});

test("a dark theme shows a painting's night", async ({ app }) => {
  mockOnly("the first prompt is a mock scene");
  const fetched: string[] = [];
  app.page.on("request", (r) => /\/snowy-fjord/.test(r.url()) && fetched.push(r.url()));
  await app.open({ ...OPEN, theme: "berth-dark", prefs: { headerPicture: { source: "builtin", builtin: "snowy-fjord" } } });
  const chat = await newChat(app);
  await expect(band(chat)).toHaveAttribute("data-picture", "snowy-fjord");
  await expect(band(chat).locator("[data-drawn]")).toBeAttached();
  // The night painting, and not the day's.
  expect(fetched.some((u) => /snowy-fjord-night-[^/]*\.webp/.test(u))).toBe(true);
  expect(fetched.some((u) => /snowy-fjord-(?!night)[^/]*\.webp/.test(u))).toBe(false);
});

test("a picture of your own tops a new chat", async ({ app }) => {
  mockOnly("the first prompt is a mock scene");
  await app.open(OPEN);
  const section = await settings(app);
  await section.getByLabel("Header picture file").setInputFiles({ name: "boat.png", mimeType: "image/png", buffer: PNG });
  await expect.poll(() => app.stored("berth.prefs")).toMatchObject({ headerPicture: { source: "image", image: { name: "boat.png" } } });
  // It's a tile now, chosen, with where it sits.
  await expect(section.getByRole("group", { name: "Custom" }).getByRole("button", { name: "boat.png", exact: true })).toHaveAttribute("aria-pressed", "true");
  await section.getByRole("button", { name: "Top", exact: true }).click();
  await expect.poll(() => app.stored("berth.prefs")).toMatchObject({ headerPicture: { position: "top" } });
  // The chat background is its own choice, still none.
  await expect.poll(() => app.stored("berth.prefs")).toMatchObject({ chatBackground: { source: "none" } });

  const chat = await newChat(app);
  await expect(band(chat)).toHaveAttribute("data-picture", "image");
  // Read back from IndexedDB, as a blob: URL.
  await expect(band(chat).locator("[data-drawn]")).toBeAttached({ timeout: 20_000 });
});

test("None leaves the top of a new chat and Home plain", async ({ app }) => {
  mockOnly("the first prompt is a mock scene");
  await app.open(OPEN);
  const section = await settings(app);
  await section.getByRole("button", { name: "None", exact: true }).click();
  await expect.poll(() => app.stored("berth.prefs")).toMatchObject({ headerPicture: { source: "none" } });

  const chat = await newChat(app);
  await expect(chat.getByRole("textbox").first()).toBeVisible();
  await expect(band(chat)).toHaveCount(0);
  await app.page.getByTestId("nav-home").click();
  await expect(app.page.getByRole("heading", { name: "What should your agents work on?" })).toBeVisible();
  await expect(band(app.page)).toHaveCount(0);

  await app.page.reload();
  await expect(app.page.getByRole("heading", { name: "What should your agents work on?" })).toBeVisible();
  await expect(band(app.page)).toHaveCount(0);
});

test("the plates are chat backgrounds too", async ({ app }) => {
  await app.open({ params: { view: "conversation" } });
  const appearance = await app.openSettings("appearance");
  const scene = appearance.getByRole("group", { name: "Scenes" }).getByRole("button", { name: "Lavender fields" });
  await scene.click();
  await expect(scene).toHaveAttribute("aria-pressed", "true");
  await expect.poll(() => app.stored("berth.prefs")).toMatchObject({ chatBackground: { source: "builtin", builtin: "lavender-fields" }, headerPicture: { source: "builtin", builtin: "harbour" } });
});
