import type { Page } from "@playwright/test";

import { expect, mockOnly, test } from "./fixtures";

// A plugin's own board draws an agent as the dashboard's live status card
// (AgentStatusCard in @berth/plugin/ui, plugins/ui.tsx), with its extras: its
// own title and mark, a line of its own, a button after the card's, and its
// own Open. The plugin is a built-in served by the test, beside the app's own.

const PLUGIN = `
import { createElement as h, useState } from "react";
import { AgentStatusCard, Button } from "@berth/plugin/ui";
export default function (berth) {
  function Board() {
    const [opened, setOpened] = useState(0);
    const [renamed, setRenamed] = useState(0);
    return h("div", { className: "w-96 space-y-3 p-4" },
      h("p", { "data-testid": "board-log" }, "opened " + opened + ", renamed " + renamed),
      h(AgentStatusCard, {
        box: "gpu",
        session: "judge-v2-claude",
        title: "Judge, as the plugin calls it",
        mark: "🔨",
        highlighted: true,
        onOpen: () => setOpened((n) => n + 1),
        actions: h(Button, { size: "xs", variant: "ghost", onClick: () => setRenamed((n) => n + 1) }, "Rename"),
      }, "What the plugin knows about the work"),
      h(AgentStatusCard, { box: "gpu", session: "no-such-session" }));
  }
  berth.addScreen({ id: "board", title: "Board", Component: Board });
  berth.addSidebarItem({ id: "board", title: "Board", screen: "board" });
}
`;

async function withPlugin(page: Page) {
  await page.route("**/builtin-plugins/index.json", async (route) => {
    const res = await route.fetch();
    const list = (await res.json()) as unknown[];
    await route.fulfill({ response: res, json: [...list, { id: "board", name: "Board", version: "0.1.0", main: "index.js" }] });
  });
  await page.route("**/builtin-plugins/board/index.js", (route) => route.fulfill({ contentType: "text/javascript", body: PLUGIN }));
}

test("a plugin's board shows an agent as the live status card, with the plugin's own title, line, button and Open", async ({ app }) => {
  mockOnly("serves a test plugin");
  await withPlugin(app.page);
  await app.open();
  const page = app.page;
  // Its screen, from the sidebar's More (exact: "Board" is in "Agent Dashboard" too).
  await page.getByTestId("nav-more").click();
  await page.getByRole("menu").getByRole("menuitem", { name: "Board", exact: true }).click();
  await expect(page.getByTestId("board-log")).toBeVisible();

  const cards = page.locator("article[role=button]");
  // The session its box doesn't list draws nothing.
  await expect(cards).toHaveCount(1);
  const card = cards.first();
  await expect(card).toContainText("🔨");
  await expect(card).toContainText("Judge, as the plugin calls it");
  await expect(card).toContainText("What the plugin knows about the work");
  // The live card itself: what it does now, its task progress, its steps.
  await expect(card.getByTestId("card-now")).toContainText("Running pnpm eval --suite judge");
  await expect(card).toContainText("Step 4 of 6");
  await expect(card.getByRole("list", { name: "Steps" }).getByRole("listitem")).toHaveCount(3);
  await expect(card.getByRole("button", { name: "Pause" })).toBeVisible();

  // The plugin's button, after the card's own; it doesn't open the card.
  await card.getByRole("button", { name: "Rename", exact: true }).click();
  await expect(page.getByTestId("board-log")).toHaveText("opened 0, renamed 1");
  // Open run does what the plugin says, not the agent's tab.
  await card.getByRole("button", { name: /Open run/ }).click();
  await expect(page.getByTestId("board-log")).toHaveText("opened 1, renamed 1");
  await page.screenshot({ path: test.info().outputPath("plugin-board.png") });
});
