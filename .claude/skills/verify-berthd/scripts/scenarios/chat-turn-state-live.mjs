// The chat's foot and composer on a real box (features/chat-turn-state.md):
// a turn Claude Code ends while a background task it started still runs,
// which turn check reads as working (finished + unfinished: the hourglass
// and Send), then a live turn (the spinner word, its timer, Stop and
// Queue), each read back from the box's own session list.
//
// On a dev box of this checkout (../dev-box.sh), with turn check on for it
// (features/chat-ask-card.md copies the settings in):
//
//   node drive.mjs chat-turn-state-live --port $PORT --agent http://127.0.0.1:9378
//
// VERIFY_SESSION names the session (default "ask", dev-box.sh's): Claude
// Code at its prompt. It types two prompts there.

export const live = true;
export const params = { view: "conversation", sidebar: "list" };

const WORKTREE = process.env.VERIFY_WORKTREE ?? "devq/acme-shop";
const BOX = WORKTREE.split("/")[0];
const SESSION = process.env.VERIFY_SESSION ?? "ask";
const ON_ITS_WORK = "Its turn ended while its work runs on: a delegated task, a build, a deploy or a watcher. It picks up by itself.";

export async function run({ page, shot, step, expect, call }) {
  const session = async () => (await call(`/v1/boxes/${BOX}/api/sessions`)).find((s) => s.name === SESSION) ?? {};
  const pane = page.locator("[data-testid=pane]:visible");
  const chat = pane.locator("[data-testid=chat]");
  const composer = pane.locator("[data-testid=composer]");
  const reply = composer.getByRole("textbox", { name: "Reply" });
  const send = async (text) => {
    await reply.fill(text);
    await reply.press("Enter");
    await expect(reply).toHaveValue("", { timeout: 20_000 });
  };

  await page.locator(`[data-testid=worktree-row][data-worktree="${WORKTREE}"]`).click({ position: { x: 32, y: 12 } });
  await expect(reply).toBeVisible({ timeout: 30_000 });

  step("a turn that ends while a background task it started still runs");
  await send(
    "Start `sleep 600` as a background task (run_in_background), don't wait for it, and end your turn right away with exactly: The background sleep is still running; I'll report back when it finishes.",
  );
  await expect.poll(async () => { const s = await session(); return `${s.agent_state}/${!!s.unfinished}`; }, { timeout: 180_000, intervals: [2_000], message: "turn check read it as working" }).toBe("finished/true");
  const onWork = chat.locator("[data-kind=on-its-work]");
  await expect(onWork).toHaveText("Waiting on its work", { timeout: 15_000 });
  await expect(chat.locator("[data-kind=thinking]")).toHaveCount(0);
  await expect(composer.getByRole("button", { name: "Send", exact: true })).toBeVisible();
  await expect(composer.getByRole("button", { name: "Queue", exact: true })).toHaveCount(0);
  await expect(pane.getByRole("button", { name: /^Stop / })).toHaveCount(0);
  await onWork.getByText("Waiting on its work").hover();
  await expect(page.locator("[data-slot=tooltip-popup]")).toHaveText(ON_ITS_WORK);
  await page.waitForTimeout(300); // its fade-in, for the picture
  await shot("live-on-its-work");
  await page.mouse.move(10, 890);

  step("a live turn: a new prompt starts one");
  await send("Run `sleep 45` in the foreground (not in the background), then reply: done.");
  await expect.poll(async () => (await session()).agent_state, { timeout: 60_000, intervals: [1_000] }).toBe("running");
  const thinking = chat.locator("[data-kind=thinking]");
  await expect(thinking).toBeVisible({ timeout: 15_000 });
  await expect(chat.locator("[data-kind=on-its-work]")).toHaveCount(0);
  await expect(pane.getByRole("button", { name: "Stop Claude" })).toBeVisible();
  await expect(composer.getByRole("button", { name: "Queue", exact: true })).toBeVisible();
  await page.waitForTimeout(3_000); // the timer ticks
  await expect(thinking).toHaveText(/· \d+(s|m \d\ds)/);
  step(`live footer reads "${(await thinking.innerText()).replace(/\s+/g, " ").trim()}"`);
  await shot("live-turn-real");
  await expect.poll(async () => (await session()).agent_state, { timeout: 120_000, intervals: [2_000] }).toBe("finished");
}
