import { expect, mockOnly, test } from "./fixtures";

// A reply's > [!NOTE] quotes draw as GitHub's callouts (lib/callouts.ts),
// and its #123, owner/repo#45 and commit hashes link to GitHub (components/
// conversation/repo-refs.tsx): on the mock's shop repository, whose origin
// is git@github.com:acme/shop.git.

const CALLOUTS = [
  "Five callouts, as GitHub draws them.",
  "",
  "> [!NOTE]",
  "> The webhook retries for up to three days.",
  "",
  "> [!TIP]",
  "> Run `pnpm test --filter payments` to check only this.",
  "",
  "> [!IMPORTANT]",
  "> The idempotency key must be set before the first charge.",
  "",
  "> [!WARNING]",
  "> This changes the order table's index.",
  "",
  "> [!caution]",
  "> Dropping the column loses every refund note.",
  "",
  "> And a quote that is only a quote.",
].join("\n");

const SHA = "9f3c1a2";
const REFS = [
  `Fixed in #123, alongside acme/billing#45 and commit ${SHA}.`,
  "",
  "The colour stays `#fff`, the line stays at webhook.ts#L14, C# stays C#, and so does #fff.",
  "",
  "- The fix: `a1b2c3d4` and `git show a1b2c3d4`",
  "- Already a link: [see #77](https://example.com/acme/77)",
  "",
  "```",
  "git revert 9f3c1a2 # undoes #123",
  "```",
].join("\n");

const BOX = "devl";
const SESSION = "checkout-fix-claude";

const land = (app: { page: import("@playwright/test").Page }, text: string) =>
  app.page.evaluate(([box, session, t]) => (window as unknown as { __berthDraft: { land(b: string, s: string, t: string): void } }).__berthDraft.land(box, session, t), [BOX, SESSION, text]);

const reply = (app: { chat: import("@playwright/test").Locator }, start: string) => app.chat.locator("[data-kind=text] .cv-md").filter({ hasText: start });

test.beforeEach(async ({ app }) => {
  mockOnly("lands a reply in the mock's conversation");
  await app.page.addInitScript(() => {
    const w = window as unknown as { __opened: string[] };
    w.__opened = [];
    window.open = (url?: string | URL) => {
      w.__opened.push(String(url));
      return null;
    };
  });
  await app.open({ params: { view: "conversation" } });
  await app.openWorktree("devl/checkout-fix");
  await expect(app.chat).toBeVisible();
});

test("a [!NOTE] quote and its four kin draw as callouts; other quotes stay quotes", async ({ app }) => {
  await land(app, CALLOUTS);
  const md = reply(app, "Five callouts");
  const boxes = md.locator("blockquote.cv-callout");
  await expect(boxes).toHaveCount(5);
  const kinds = ["note", "tip", "important", "warning", "caution"];
  const labels = ["Note", "Tip", "Important", "Warning", "Caution"];
  for (const [i, kind] of kinds.entries()) {
    const box = boxes.nth(i);
    await expect(box).toHaveAttribute("data-callout", kind);
    await expect(box.locator(".cv-callout-title")).toHaveText(labels[i]);
    await expect(box.locator(".cv-callout-title svg")).toBeVisible();
  }
  // The marker is gone, the words are there, and a plain quote is plain.
  await expect(md).not.toContainText("[!");
  await expect(boxes.nth(0)).toContainText("The webhook retries for up to three days.");
  await expect(md.locator("blockquote:not(.cv-callout)")).toHaveText("And a quote that is only a quote.");
});

test("issues, pull requests and commits link to the worktree's repository on GitHub", async ({ app }) => {
  await land(app, REFS);
  const md = reply(app, "Fixed in #123");
  await expect(md.locator("a.cv-ref", { hasText: "#123" })).toHaveAttribute("href", "https://github.com/acme/shop/issues/123");
  await expect(md.locator("a.cv-ref", { hasText: "acme/billing#45" })).toHaveAttribute("href", "https://github.com/acme/billing/issues/45");
  await expect(md.locator("a.cv-ref", { hasText: SHA })).toHaveAttribute("href", `https://github.com/acme/shop/commit/${SHA}`);
  // Inline code that is a hash and nothing else links; a command doesn't.
  await expect(md.locator("a.cv-ref > code")).toHaveText("a1b2c3d4");
  await expect(md.locator("code", { hasText: "git show a1b2c3d4" })).toBeVisible();
  await expect(md.locator("a code", { hasText: "git show" })).toHaveCount(0);
  // Nothing else: no colour, line anchor, C#, existing link or code block.
  await expect(md.locator("a.cv-ref")).toHaveCount(4);
  await expect(md.locator("pre a")).toHaveCount(0);
  await expect(md.locator("a:not(.cv-ref)")).toHaveAttribute("href", "https://example.com/acme/77");
});

test("clicking a reference opens it on GitHub, outside the app", async ({ app }) => {
  await land(app, REFS);
  await reply(app, "Fixed in #123").locator("a.cv-ref", { hasText: "#123" }).click();
  await expect.poll(() => app.page.evaluate(() => (window as unknown as { __opened: string[] }).__opened)).toEqual(["https://github.com/acme/shop/issues/123"]);
});
