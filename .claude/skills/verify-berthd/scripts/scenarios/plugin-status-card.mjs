// A plugin's own board draws an agent as the dashboard's live status card
// (AgentStatusCard in @berth/plugin/ui), with the plugin's own title, mark,
// line, button and Open. features/dashboard-cards.md (plugins).

// The list sidebar, where More is; the rail layout hides it.
export const params = { sidebar: "list" };

export const plugins = [
  {
    id: "board",
    name: "Board",
    source: `
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
`,
  },
];

export async function run({ page, shot, step, expect }) {
  step("open the plugin's Board from the sidebar's More");
  await page.getByTestId("nav-more").click();
  await page.getByRole("menu").getByRole("menuitem", { name: "Board", exact: true }).click();
  await expect(page.getByTestId("board-log")).toBeVisible();

  step("one card (the unknown session draws nothing): the plugin's mark, title and line, and the live card itself");
  const cards = page.locator("article[role=button]");
  await expect(cards).toHaveCount(1);
  const card = cards.first();
  await expect(card).toContainText("🔨");
  await expect(card).toContainText("Judge, as the plugin calls it");
  await expect(card).toContainText("What the plugin knows about the work");
  await expect(card.getByTestId("card-now")).toContainText("Running pnpm eval --suite judge");
  await expect(card).toContainText("Step 4 of 6");
  await shot("1-plugin-board");

  step("the plugin's Rename doesn't open the card; Open run does what the plugin says");
  await card.getByRole("button", { name: "Rename", exact: true }).click();
  await expect(page.getByTestId("board-log")).toHaveText("opened 0, renamed 1");
  await card.getByRole("button", { name: /Open run/ }).click();
  await expect(page.getByTestId("board-log")).toHaveText("opened 1, renamed 1");
  await shot("2-after-open");
}
