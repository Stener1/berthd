// The chat's question card on a real box (features/chat-ask-card.md): a
// local command (/rc), a mode switch from the mode menu, and a last message
// that asks in words draw no "Needs your answer" card and no Yes / No; a
// permission prompt and a real question (AskUserQuestion) still get theirs.
//
// On a dev box of this checkout (../dev-box.sh, which exports
// VERIFY_WORKTREE and the token), with turn check on for it
// (../../features/chat-ask-card.md):
//
//   node drive.mjs chat-ask-card --port $PORT --agent http://127.0.0.1:9378
//
// VERIFY_SESSION names the session (default "ask", dev-box.sh's): Claude
// Code at its prompt, with bypass among its modes. It types prompts there.

export const live = true;

const WORKTREE = process.env.VERIFY_WORKTREE ?? "devq/acme-shop";
const BOX = WORKTREE.split("/")[0];
const SESSION = process.env.VERIFY_SESSION ?? "ask";
const TURN = 120_000;

export async function run({ page, shot, step, expect, call }) {
  const api = (path, init) => call(`/v1/boxes/${BOX}/api/sessions/${SESSION}${path}`, init);
  const session = async () => (await call(`/v1/boxes/${BOX}/api/sessions`)).find((s) => s.name === SESSION);
  // One of the session's fields as the box lists it, for expect.poll.
  const field = (read) => async () => read((await session()) ?? {});
  const screen = async () => (await api("/screen")).screen ?? "";
  const keys = (...k) => api("/keys", { method: "POST", body: JSON.stringify({ keys: k }) });
  const chat = page.locator("[data-testid=pane]:visible [data-testid=chat]");
  const reply = page.locator("[data-testid=pane]:visible [data-testid=composer]").getByRole("textbox", { name: "Reply" });
  const noCard = async () => {
    await expect(chat.getByText("Needs your answer")).toHaveCount(0);
    await expect(chat.getByRole("button", { name: "Yes", exact: true })).toHaveCount(0);
  };
  // Enter again only while the words are still in the box: a command's menu
  // takes the first, and the chat holds it while the agent's own screen
  // shows. A sent reply leaves the box empty, so nothing goes twice.
  const type = async (text) => {
    await reply.fill(text);
    await reply.press("Enter");
    await expect
      .poll(
        async () => {
          if ((await reply.inputValue()) !== "") await reply.press("Enter");
          return reply.inputValue();
        },
        { timeout: 20_000, intervals: [1_000] },
      )
      .toBe("");
  };
  // Claude Code at its prompt: the input between two rules, no menu open.
  const atPrompt = (text) => /─{8,}\s*\n❯[^\n]*\n─{8,}/.test(text) && !/Esc to (continue|cancel)/.test(text.split("\n").slice(-12).join("\n"));
  const settle = () => expect.poll(async () => atPrompt(await screen()), { timeout: 20_000, message: "back at its prompt" }).toBe(true);
  // /rc turns Remote Control on and says so; once it is on, it opens its
  // menu instead, a moment later (it asks claude.ai first), which Esc
  // closes without a word.
  const ON = /remote-control is active/gi;
  const MENU = /Esc to continue/;
  const rc = async () => {
    const said = (text) => text.match(ON)?.length ?? 0;
    const before = said(await screen());
    await type("/rc");
    let menu = false;
    await expect
      .poll(
        async () => {
          const now = await screen();
          menu = MENU.test(now);
          return menu || said(now) > before;
        },
        { timeout: 30_000, message: "Remote Control answered" },
      )
      .toBe(true);
    await noCard();
    // Its menu can also draw after its line: the prompt has to stay clear
    // for a few reads, a late menu closed as it shows.
    return async () => {
      if (menu) await keys("escape");
      let clear = 0;
      await expect
        .poll(
          async () => {
            const now = await screen();
            if (MENU.test(now)) await keys("escape");
            clear = atPrompt(now) ? clear + 1 : 0;
            return clear;
          },
          { timeout: 30_000, intervals: [1_000], message: "Remote Control's menu closed" },
        )
        .toBeGreaterThanOrEqual(3);
    };
  };
  const modeTo = async (label, id) => {
    const chip = page.getByRole("button", { name: /^Permission mode:/ });
    await expect(chip).not.toHaveAttribute("aria-disabled", /.*/);
    await chip.click();
    await page.getByRole("menuitemradio", { name: new RegExp(label) }).click();
    await expect.poll(async () => (await api("/controls")).mode, { timeout: 20_000 }).toBe(id);
    await settle();
  };

  step(`open ${WORKTREE}'s agent as its chat`);
  const row = page.locator(`[data-testid=worktree-row][data-worktree="${WORKTREE}"]`);
  await expect(row).toBeVisible({ timeout: 20_000 });
  await row.click({ position: { x: 32, y: 12 } });
  const conversation = page.getByRole("button", { name: "Conversation" });
  if ((await conversation.getAttribute("aria-pressed")) !== "true") await conversation.click();
  // A new agent (idle: it has said nothing yet) takes a first task before
  // it has a chat to reply in.
  if ((await session())?.agent_state === "idle") {
    step("give it a first task");
    const first = page.getByRole("textbox", { name: /^What should .* do\?$/ });
    await first.fill("Reply with just the word: ready");
    await first.press("Enter");
    await expect.poll(field((s) => s.agent_state), { timeout: TURN }).toBe("finished");
  }
  await expect(reply).toBeVisible({ timeout: 20_000 });

  step("send /rc from the reply box: Remote Control's menu, no card");
  const closeRc = await rc();
  expect((await session()).agent_state).not.toBe("waiting");
  await shot("rc-menu");
  await closeRc();

  step("switch to bypass from the mode menu: no card, no dialog");
  await modeTo("Bypass", "bypassPermissions");
  await noCard();
  expect((await session()).agent_state).not.toBe("waiting");
  await shot("bypass");

  step("a last message that asks in words: Needs you, with no card");
  await type("Two tests are flaky: checkout retries the payment twice, and the cart badge miscounts a removed item. I can only have one fixed today and haven't decided which. Without using any tools, list the two as a numbered list, then ask me which one you should fix first, and stop there until I answer.");
  await expect.poll(field((s) => s.soft_wait ?? false), { timeout: TURN, message: "turn check read the turn as waiting on you" }).toBe(true);
  await expect(chat.getByText(/\?\s*$/).last()).toBeVisible();
  await noCard();
  await shot("asked-in-reply");

  step("at that wait, /rc and a mode switch: still no card");
  await (await rc())();
  await modeTo("Ask before edits", "default");
  await noCard();
  expect((await session()).soft_wait).toBe(true);
  await shot("asked-in-reply-after-rc-and-mode");

  step("a permission prompt still gets its card");
  await type("Create an empty file named verify-permission.txt here with the Bash tool (touch), and nothing else.");
  await expect.poll(field((s) => s.ask?.tool), { timeout: TURN }).toBe("Bash");
  const card = chat.locator("[data-testid=chat-item][data-kind=ask]");
  await expect(card).toBeVisible({ timeout: 20_000 });
  await expect(card.getByRole("button", { name: /Deny/ })).toBeVisible({ timeout: 20_000 });
  await shot("permission");
  // Allowed, it runs and its turn ends as usual.
  await card.getByRole("button", { name: /Allow/ }).filter({ hasNotText: /Always/ }).first().click();
  await expect.poll(field((s) => s.agent_state), { timeout: TURN }).not.toBe("waiting");
  // The chat follows the box a moment later: until its card goes, Enter in
  // the reply box is held for the menu.
  await expect(card).toHaveCount(0, { timeout: 20_000 });
  await settle();

  step("a real question (AskUserQuestion) still gets its card");
  await type("Use the AskUserQuestion tool to ask me one question: tea or coffee? Offer exactly those two options.");
  await expect.poll(field((s) => s.ask?.tool), { timeout: TURN }).toBe("AskUserQuestion");
  const form = chat.getByTestId("question-form");
  await expect(form).toBeVisible({ timeout: 20_000 });
  await expect(form).toContainText(/tea or coffee/i);
  await expect(form.getByRole("radio", { name: /Tea/ })).toBeVisible();
  await expect(form.getByRole("radio", { name: /Coffee/ })).toBeVisible();
  await shot("ask-user-question");
  // Esc declines it. Claude Code says so but sends no hook, so the box
  // keeps the wait until the next prompt: this check goes last.
  await keys("escape");
  await expect.poll(screen, { timeout: 20_000 }).toMatch(/declined to answer/);
}
