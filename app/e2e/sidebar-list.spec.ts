import { expect, mockOnly, test } from "./fixtures";

// The list sidebar (the layout the specs pin, ?sidebar=list) is on the same
// two lanes as the rail's sheet (index.css, the grid): rows' highlight boxes
// on the edge, their glyphs and labels on the lane, every right end (a row's,
// the window strip's buttons, Settings) on the edge.

test("the list sidebar's boxes, their contents and their ends each line up", async ({ app }) => {
  mockOnly();
  await app.open();
  const page = app.page;
  await expect(app.worktree("devl/checkout-fix")).toBeVisible();
  const lanes = () =>
    page.evaluate(() => {
      const side = document.querySelector("[data-testid=sidebar]")!;
      const box = side.getBoundingClientRect();
      const q = (s: string) => side.querySelector(s)!.getBoundingClientRect();
      const row = side.querySelector("[data-testid=worktree-row][data-worktree='devl/checkout-fix']")!;
      const buttons = [...side.querySelectorAll("button")];
      // A project's header ("shop") and a section's fold ("Work").
      const header = buttons.find((b) => b.getAttribute("aria-expanded") !== null && b.querySelector("span")?.textContent === "shop")!.querySelector("span")!;
      const section = buttons.find((b) => b.textContent?.startsWith("Work"))!.querySelector("svg")!;
      const left = (r: DOMRect) => Math.round(r.left - box.left);
      const right = (r: DOMRect) => Math.round(box.right - r.right);
      return {
        boxes: [left(q("[data-testid=nav-home]")), left(row.getBoundingClientRect()), left(q("[data-testid=sidebar-boxes]"))],
        contents: [
          left(q("[data-testid=nav-home] svg")),
          left(header.getBoundingClientRect()),
          left(section.getBoundingClientRect()),
          left(row.querySelector("[role=img]")!.getBoundingClientRect()),
          left(q("[data-testid=sidebar-boxes] > span")),
        ],
        ends: [right(q("[data-testid=nav-home]")), right(row.getBoundingClientRect()), right(q("[data-testid=nav-settings]")), right(q("button[aria-label='Hide the sidebar']"))],
      };
    });
  const same = (xs: number[]) => xs.every((x) => x === xs[0]);
  await expect
    .poll(async () => {
      const l = await lanes();
      return same(l.boxes) && same(l.contents) && same(l.ends) && l.contents[0] > l.boxes[0];
    })
    .toBe(true);
});
