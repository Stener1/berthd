// The chat's foot and composer for a turn that ended while its work runs on,
// and for a live turn (features/chat-turn-state.md), on the mock:
//
//   node drive.mjs chat-turn-state --port $PORT
//
// order-export's "Add tests for the export job" finished a turn that turn
// check read as working (unfinished): an hourglass, "Waiting on its work"
// with the dashboard card's tooltip, no spinner word or timer, and Send.
// judge-v2's agent is mid-turn: the spinner word with its timer, Stop and
// Queue.

const ON_ITS_WORK = "Its turn ended while its work runs on: a delegated task, a build, a deploy or a watcher. It picks up by itself.";

export async function run({ page, shot, step, expect }) {
  await page.goto(page.url().replace(/\?.*/, "?mock=1&sidebar=list&view=conversation"));
  await expect(page.getByTestId("nav-home")).toBeVisible();
  const chat = page.locator("[data-testid=pane]:visible [data-testid=chat]");
  const pane = page.locator("[data-testid=pane]:visible");
  const composer = pane.locator("[data-testid=composer]");
  const open = async (worktree, tab) => {
    await page.locator(`[data-testid=worktree-row][data-worktree="${worktree}"]`).click({ position: { x: 32, y: 12 } });
    if (tab) await page.locator("[data-tab-strip] [data-tab]", { hasText: tab }).click();
    await expect(chat.locator("[data-testid=chat-item]").first()).toBeVisible();
  };

  step("open the turn that ended while its work runs on (devl/order-export, Add tests for the export job)");
  await open("devl/order-export", "Add tests for the export job");
  const onWork = chat.locator("[data-kind=on-its-work]");
  await expect(onWork).toHaveText("Waiting on its work");
  await expect(onWork.locator("svg.lucide-hourglass")).toBeVisible();
  await expect(chat.locator("[data-kind=thinking]")).toHaveCount(0);
  await expect(composer.getByRole("button", { name: "Send", exact: true })).toBeVisible();
  await expect(composer.getByRole("button", { name: "Queue", exact: true })).toHaveCount(0);
  await expect(pane.getByRole("button", { name: /^Stop / })).toHaveCount(0);
  await expect(composer.getByRole("textbox", { name: "Reply" })).toHaveAttribute("placeholder", "Reply, or ask for something else");
  step("hover the hourglass for its tooltip");
  await onWork.getByText("Waiting on its work").hover();
  await expect(page.locator("[data-slot=tooltip-popup]")).toHaveText(ON_ITS_WORK);
  await page.waitForTimeout(300); // its fade-in, for the picture
  await shot("on-its-work");
  await page.mouse.move(10, 890);

  step("open a live turn (gpu/judge-v2)");
  await open("gpu/judge-v2");
  const thinking = chat.locator("[data-kind=thinking]");
  await expect(thinking).toBeVisible();
  // Its word, then the timer: "Making way… · 12s".
  await expect(thinking).toHaveText(/…\s*· \d+(s|m \d\ds)/);
  await expect(chat.locator("[data-kind=on-its-work]")).toHaveCount(0);
  await expect(pane.getByRole("button", { name: "Stop Claude" })).toBeVisible();
  await expect(composer.getByRole("button", { name: "Queue", exact: true })).toBeVisible();
  await expect(composer.getByRole("textbox", { name: "Reply" })).toHaveAttribute("placeholder", "Claude is working: it reads this at its next step");
  step(`live footer reads "${(await thinking.innerText()).replace(/\s+/g, " ").trim()}"`);
  await shot("live-turn");
}
