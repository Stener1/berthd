import { expect, mockOnly, test } from "./fixtures";

// Turn check picks no provider or model for you: it can't be turned on until
// you choose who checks turns and give it what it needs.
test("turn check turns on only once you pick a provider and save its key", async ({ app }) => {
  mockOnly("turn check's settings are a mock box's");
  await app.open();
  const devl = (await app.openSettings("agents")).getByTestId("turncheck-devl");
  const toggle = devl.getByRole("switch", { name: "Check finished turns on devl" });
  await expect(toggle).toBeDisabled();
  await expect(devl.getByText("Choose who checks turns")).toBeVisible();

  await devl.getByRole("button", { name: "OpenAI Decisions" }).click();
  // Picking a provider fills in the model it serves; it is yours to change.
  await expect(devl.getByRole("textbox", { name: "Model that checks turns on devl" })).toHaveValue("gpt-6-luna");
  await expect(devl.getByText("Save a key first")).toBeVisible();
  await expect(toggle).toBeDisabled();

  const key = devl.getByLabel("OpenAI key for devl");
  await key.fill("sk-example");
  await key.locator("xpath=following::button[1]").click();
  await expect(devl.getByText("OpenAI key saved on devl")).toBeVisible();
  await toggle.click();
  await expect(toggle).toBeChecked();

  // Another provider is a new place turns go: off again, and OpenAI's key isn't its key.
  await devl.getByRole("button", { name: "Jev on AI Gateway" }).click();
  await expect(toggle).not.toBeChecked();
  await expect(devl.getByRole("textbox", { name: "Model that checks turns on devl" })).toHaveValue("typesafe-ai/jev");
  await expect(devl.getByText("Save a key first")).toBeVisible();
  await app.page.screenshot({ path: test.info().outputPath("settings-turn-check.png") });
});

test("turn check with Laya needs only your server's address", async ({ app }) => {
  mockOnly("turn check's settings are a mock box's");
  await app.open();
  const devl = (await app.openSettings("agents")).getByTestId("turncheck-devl");
  const toggle = devl.getByRole("switch", { name: "Check finished turns on devl" });
  await devl.getByRole("button", { name: "Laya, your own server" }).click();
  await expect(devl.getByText("Set your Laya server's address")).toBeVisible();
  await expect(devl.getByLabel("Paste your key")).toHaveCount(0);
  await expect(toggle).toBeDisabled();

  const server = devl.getByRole("textbox", { name: "Laya server on devl" });
  await server.fill("http://127.0.0.1:8770/v1/systemone");
  await server.locator("xpath=following::button[1]").click();
  await expect(devl.getByText("http://127.0.0.1:8770/v1/systemone")).toBeVisible();
  await toggle.click();
  await expect(toggle).toBeChecked();
  await app.page.screenshot({ path: test.info().outputPath("settings-turn-check-laya.png") });
});
