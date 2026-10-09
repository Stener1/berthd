import { type App, expect, mockOnly, test } from "./fixtures";

// "Log in as…": the seeded users a project's login lists (mock mode: the
// shop-dev kit on devl/shop lists pro@acme.test, admin@acme.test and
// free@acme.test). Mock mode has no laptop proxy, so a stand-in answers the
// worktree's host: its pages, and the login route. The real route answers
// with a redirect to next; the stand-in answers with a page instead, since a
// redirect Playwright fulfils is followed past its routes, to whatever
// listens on the port for real.

const HOST = "http://checkout-fix.shop.devl.localhost:1377";
const PROXIED = /^https?:\/\/[^/]+\.localhost:1377(?:\/|$)/;

interface Seen {
  logins: { as: string | null; next: string | null }[];
}

async function standIn(app: App): Promise<Seen> {
  const seen: Seen = { logins: [] };
  await app.context.route(PROXIED, (route) => {
    const u = new URL(route.request().url());
    if (u.pathname === "/__berth/login") {
      seen.logins.push({ as: u.searchParams.get("as"), next: u.searchParams.get("next") });
      return route.fulfill({ status: 200, contentType: "text/html", body: `<!doctype html><title>Signed in · acme</title><h1>Signed in as ${u.searchParams.get("as")}</h1>` });
    }
    return route.fulfill({ status: 200, contentType: "text/html", body: `<!doctype html><title>${u.pathname} · acme</title><h1>${u.pathname === "/cart" ? "Cart" : "Home"}</h1>` });
  });
  return seen;
}

async function openCart(app: App) {
  await app.openWorktree("devl/checkout-fix");
  await app.page.getByRole("button", { name: "New tab" }).click();
  await app.page.getByRole("option", { name: /New browser tab/ }).click();
  const pane = app.page.locator("[data-testid=browser-pane]:visible");
  const address = pane.getByRole("textbox", { name: "Address" });
  await address.fill(`${HOST}/cart`);
  await address.press("Enter");
  await expect(pane.frameLocator("iframe").getByRole("heading", { name: "Cart" })).toBeVisible();
  return pane;
}

test("Log in as… in the Browser tab opens the page through the login route as the user picked", async ({ app }) => {
  mockOnly("the project's login users come from the mock kit");
  const seen = await standIn(app);
  // BERTH_SHOTS=dir BERTH_SHOTS_THEME=light|dark keeps a screenshot of the menu.
  const shots = process.env.BERTH_SHOTS;
  const scheme = process.env.BERTH_SHOTS_THEME === "light" ? "light" : "dark";
  await app.open(shots ? { theme: `berth-${scheme}` } : {});
  const pane = await openCart(app);

  await pane.getByRole("button", { name: "Log in as…" }).click();
  const menu = app.page.getByRole("menu");
  await expect(menu.getByRole("menuitem")).toHaveCount(3);
  await expect(menu.getByRole("menuitem").nth(0)).toContainText("Pro user");
  await expect(menu.getByRole("menuitem").nth(0)).toContainText("pro@acme.test");
  // A user with no label shows by email.
  await expect(menu.getByRole("menuitem").nth(2)).toHaveText("free@acme.test");
  if (shots) await app.page.screenshot({ path: `${shots}/login-as-${scheme}.png` });

  await menu.getByRole("menuitem", { name: /Pro user/ }).click();
  // The page you were on is where the login lands.
  await expect(pane.getByRole("textbox", { name: "Address" })).toHaveValue(`${HOST}/__berth/login?as=pro%40acme.test&next=%2Fcart`);
  await expect.poll(() => seen.logins).toEqual([{ as: "pro@acme.test", next: "/cart" }]);
  await expect(pane.frameLocator("iframe").getByRole("heading", { name: "Signed in as pro@acme.test" })).toBeVisible();
});

test("⌘K offers Log in as each user of the worktree you are in", async ({ app }) => {
  mockOnly("the project's login users come from the mock kit");
  const seen = await standIn(app);
  await app.open();
  await app.openWorktree("devl/checkout-fix");

  await app.page.getByRole("button", { name: /^Search/ }).click();
  await app.page.getByPlaceholder("Jump to a session, worktree, or command…").fill("log in as team");
  await app.page.getByRole("option", { name: /Log in as Team admin/ }).click();

  const pane = app.page.locator("[data-testid=browser-pane]:visible");
  await expect(pane.getByRole("textbox", { name: "Address" })).toHaveValue(`${HOST}/__berth/login?as=admin%40acme.test&next=%2F`);
  await expect.poll(() => seen.logins).toEqual([{ as: "admin@acme.test", next: "/" }]);
});

test("a worktree whose project has no login shows no Log in as…", async ({ app }) => {
  mockOnly("which projects have a login is the mock's");
  await standIn(app);
  await app.open();
  await app.openWorktree("gpu/judge-v2");
  await app.page.getByRole("button", { name: "New tab" }).click();
  await app.page.getByRole("option", { name: /New browser tab/ }).click();
  const pane = app.page.locator("[data-testid=browser-pane]:visible");
  await expect(pane.getByRole("textbox", { name: "Address" })).toBeVisible();
  await expect(pane.getByRole("button", { name: "Log in as…" })).toHaveCount(0);
});
