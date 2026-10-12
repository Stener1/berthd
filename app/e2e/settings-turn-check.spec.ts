import { expect, mockOnly, test } from "./fixtures";

// Turn check picks no model for you: it can't be turned on until you choose
// one and save a key.
test("turn check turns on only once you choose a model and save a key", async ({ app }) => {
  mockOnly("turn check's settings are a mock box's");
  await app.open();
  const agents = (await app.openSettings("agents")).getByTestId("turncheck-devl");
  const toggle = agents.getByRole("switch", { name: "Check finished turns on devl" });
  await expect(toggle).toBeDisabled();
  await expect(agents.getByText("Choose a model first")).toBeVisible();

  const model = agents.getByRole("textbox", { name: "Model that checks turns on devl" });
  await model.fill("typesafe-ai/jev");
  await model.locator("xpath=following::button[1]").click();
  await expect(agents.getByText("Save a key first")).toBeVisible();
  await expect(toggle).toBeDisabled();

  const key = agents.getByLabel("AI Gateway key for devl");
  await key.fill("vck_example");
  await key.locator("xpath=following::button[1]").click();
  await expect(agents.getByText("Key saved on devl")).toBeVisible();
  await expect(toggle).toBeEnabled();
  await toggle.click();
  await expect(toggle).toBeChecked();
  await app.page.screenshot({ path: test.info().outputPath("settings-turn-check.png") });
});
