// needs-me-live: a real Claude Code in its terminal on a dev box asks with a
// form (AskUserQuestion). Needs me brings it up, the number key and Enter answer
// it, and the box fills in Claude's own form. Second view: the agent's own record
// has the answer, and Claude says it back. See ../../features/needs-me.md.
import { existsSync, rmSync } from "node:fs";

export const live = true;
export const params = { labs: "0", sidebar: "list" };

const TITLE = "Needs me live check";
const NAME = "needs-me-live";
const PICKED = "Blue harbour";
const FORM = {
  questions: [
    {
      header: "Colour",
      question: "Which colour should the acme header use?",
      multiSelect: false,
      options: [
        { label: "Red lighthouse", description: "The warm one." },
        { label: PICKED, description: "The calm one." },
      ],
    },
  ],
};

export async function run({ page, shot, step, expect, call }) {
  const box = (process.env.VERIFY_WORKTREE ?? "devq/acme-shop").split("/")[0];
  const api = `/v1/boxes/${box}/api`;

  step("a real Claude Code starts, and asks with a form");
  await call(`${api}/sessions`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      location: "acme-shop",
      name: NAME,
      title: TITLE,
      agent: "claude",
      prompt: `Call the AskUserQuestion tool once with exactly this input JSON: ${JSON.stringify(FORM)}. After I answer, repeat my answer back word for word on its own line, then stop.`,
    }),
  });

  step("Needs me brings it up by itself");
  await page.getByTestId("nav-needs-me").click();
  const row = page.getByTestId("needs-me-row").filter({ hasText: TITLE });
  await expect(row).toHaveCount(1, { timeout: 180_000 });
  await row.click();
  await expect(page.getByTestId("needs-me-title")).toHaveText(TITLE);
  const form = page.getByTestId("question-form");
  await expect(form).toContainText("Which colour should the acme header use?", { timeout: 120_000 });
  await shot("1-real-form");

  step("the record behind the card is the box's own");
  const before = await call(`${api}/work`);
  const record = before.find((r) => r.sessions.some((s) => s.name === NAME));
  if (!record) throw new Error(`no work record holds ${NAME}`);

  step("answer with the number key, then Enter");
  await page.getByRole("heading", { name: TITLE }).click();
  await page.keyboard.press("2");
  await expect(form.getByRole("radio", { name: new RegExp(PICKED) })).toBeChecked();
  await shot("2-picked");
  await page.keyboard.press("Enter");

  step("the card leaves as soon as it's answered");
  await expect(row).toHaveCount(0, { timeout: 30_000 });
  await shot("3-answered-next");

  step("second view: the agent's record has the answer, and Claude says it back");
  await expect
    .poll(
      async () => {
        const t = await call(`${api}/sessions/${NAME}/transcript?since=0`);
        const q = (t.items ?? []).filter((i) => i.kind === "question").pop();
        return q?.done ? q.answers?.[0] : undefined;
      },
      { timeout: 120_000, intervals: [2000] },
    )
    .toBe(PICKED);
  await expect
    .poll(
      async () => {
        const t = await call(`${api}/sessions/${NAME}/transcript?since=0`);
        const items = t.items ?? [];
        const at = items.findLastIndex((i) => i.kind === "question");
        return items.slice(at + 1).some((i) => i.kind === "text" && i.text.includes(PICKED));
      },
      { timeout: 120_000, intervals: [2000] },
    )
    .toBe(true);

  step("answering counts as a person looking at it: last_seen moved");
  const after = await call(`${api}/work/${record.id}`);
  if (!after.last_seen || (record.last_seen && Date.parse(after.last_seen) <= Date.parse(record.last_seen))) throw new Error(`last_seen didn't move: ${record.last_seen} → ${after.last_seen}`);

  step("its finished turn comes back as a result to read, then Read takes it off");
  await expect(row).toHaveCount(1, { timeout: 120_000 });
  await row.click();
  const card = page.getByTestId("needs-me-card");
  await expect(card).toHaveAttribute("data-kind", "result", { timeout: 30_000 });
  await expect(page.getByTestId("needs-me-text")).toContainText(PICKED);
  await shot("4-result");

  step("Reply from the card: it reaches Claude, and its answer comes back as a result");
  await page.getByRole("textbox", { name: "Reply" }).fill("Say the word harbourmaster, then stop.");
  await page.getByRole("button", { name: "Reply", exact: true }).click();
  await expect(row).toHaveCount(0, { timeout: 30_000 });
  await expect(row).toHaveCount(1, { timeout: 180_000 });
  await row.click();
  await expect(page.getByTestId("needs-me-text")).toContainText(/harbourmaster/i, { timeout: 30_000 });

  step("Read takes it off");
  await card.getByRole("button", { name: "Read", exact: true }).click();
  await expect(row).toHaveCount(0, { timeout: 30_000 });
  const read = await call(`${api}/work/${record.id}`);
  if (read.status !== "open") throw new Error(`Read changed its status: ${read.status}`);

  step("Not now on a real record: parked with a date on the box, under Later, and Bring back returns it");
  await call(`${api}/sessions/${NAME}/send`, { method: "POST", body: JSON.stringify({ text: "Say the word ok, then stop.", enter: true, when: "idle" }) });
  await expect(row).toHaveCount(1, { timeout: 180_000 });
  await row.click();
  await expect(page.getByTestId("needs-me-title")).toHaveText(TITLE);
  await card.getByRole("button", { name: "Not now", exact: true }).click();
  await page.getByTestId("needs-me-not-now").getByRole("button", { name: "Tomorrow" }).click();
  await expect(row).toHaveCount(0, { timeout: 15_000 });
  const parked = await call(`${api}/work/${record.id}`);
  if (parked.status !== "parked" || !parked.not_until || Date.parse(parked.not_until) <= Date.now()) throw new Error(`Not now: ${JSON.stringify(parked)}`);
  const later = page.getByTestId("needs-me-later-row").filter({ hasText: TITLE });
  await expect(later).toContainText("Not now, back ");
  await shot("5-later");
  await later.getByRole("button", { name: "Bring back" }).click();
  await expect.poll(async () => (await call(`${api}/work/${record.id}`)).status, { timeout: 15_000 }).toBe("open");
  await expect(row).toHaveCount(1, { timeout: 15_000 });

  step("Waiting on a real record: who and when, on the box");
  await row.click();
  await card.getByRole("button", { name: "Waiting on", exact: true }).click();
  await page.getByTestId("needs-me-waiting-on").getByRole("textbox", { name: "Who or what" }).fill("Sam at acme");
  await page.getByTestId("needs-me-waiting-on").getByRole("button", { name: "Wait", exact: true }).click();
  await expect(row).toHaveCount(0, { timeout: 15_000 });
  const waits = await call(`${api}/work/${record.id}`);
  if (waits.status !== "waiting" || waits.waiting_on !== "Sam at acme" || !waits.check_back) throw new Error(`Waiting on: ${JSON.stringify(waits)}`);

  step("a date that passes brings it back by itself, with whom it waits for");
  await call(`${api}/work/${record.id}`, { method: "PATCH", body: JSON.stringify({ check_back: new Date(Date.now() - 60_000).toISOString() }) });
  await expect(row).toHaveCount(1, { timeout: 45_000 });
  await row.click();
  await expect(page.getByTestId("needs-me-why")).toHaveText("Waiting on Sam at acme: time to check back");
  await shot("6-back-when-due");

  step("Pin, on the box");
  await card.getByRole("button", { name: /^Pin/ }).click();
  await page.getByRole("menuitem", { name: "Pin this work" }).click();
  await expect.poll(async () => (await call(`${api}/work/${record.id}`)).pinned, { timeout: 15_000 }).toBe(true);

  step("Close ends the session and the record stays, and a closed record loses its pin");
  await card.getByRole("button", { name: "Close", exact: true }).click();
  await expect(row).toHaveCount(0, { timeout: 30_000 });
  await expect.poll(async () => (await call(`${api}/sessions`)).some((x) => x.name === NAME), { timeout: 30_000 }).toBe(false);
  const closed = (await call(`${api}/work?all=1`)).find((r) => r.id === record.id);
  if (closed?.status !== "closed") throw new Error(`Close should keep a closed record, got ${JSON.stringify(closed)}`);
  if ((await call(`${api}/work/pins`)).some((p) => p.target === record.id)) throw new Error("a closed record kept its pin");
  await shot("7-closed");

  step("a real permission, if this account asks for one: Allow by its key, on the real prompt");
  const marker = `/tmp/needs-me-live-${Date.now()}`;
  await call(`${api}/sessions`, {
    method: "POST",
    body: JSON.stringify({ location: "acme-shop", name: `${NAME}-perm`, title: "Needs me permission check", agent: "claude", prompt: `Use the Bash tool to run exactly: touch ${marker}. Then stop.` }),
  });
  const permRow = page.getByTestId("needs-me-row").filter({ hasText: "Needs me permission check" });
  const choices = page.getByTestId("needs-me-choices");
  await expect(permRow).toHaveCount(1, { timeout: 180_000 });
  await permRow.click();
  const asked = await choices.waitFor({ timeout: 30_000 }).then(() => true, () => false);
  if (asked) {
    await expect(choices.getByRole("button", { name: /^.?\s*Allow/ }).first()).toBeVisible();
    await shot("8-real-permission");
    await page.getByRole("heading", { name: "Needs me permission check" }).click();
    await page.keyboard.press(await choices.getByRole("button", { name: /Allow/ }).first().locator("kbd").innerText());
    await expect.poll(() => existsSync(marker), { timeout: 120_000, intervals: [2000] }).toBe(true);
    rmSync(marker, { force: true });
  } else {
    step("no permission prompt on this account, so this one isn't exercised");
  }

}
