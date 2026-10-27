import assert from "node:assert/strict";
import { test } from "node:test";

import { marketAction, marketNote, type MarketPlugin } from "./marketplace.ts";

const listed: MarketPlugin = { id: "acme-standup", name: "Standup notes", version: "1.2.0", files: [], size: 2048 };

test("a plugin that isn't here is offered to install", () => {
  assert.equal(marketAction(listed), "install");
  assert.equal(marketNote(listed), undefined);
});

test("one installed from the listing is up to date or has an update", () => {
  assert.equal(marketAction({ ...listed, installed: true, installed_version: "1.2.0" }), "installed");
  assert.equal(marketNote({ ...listed, installed: true, installed_version: "1.2.0" }), undefined);
  const older = { ...listed, installed: true, installed_version: "1.1.0", update: true };
  assert.equal(marketAction(older), "update");
  assert.equal(marketNote(older), "You have 1.1.0.");
  // Published again under the same version: there is no older number to name.
  assert.equal(marketNote({ ...listed, installed: true, installed_version: "1.2.0", update: true }), "The listing has newer files for this version.");
});

test("a folder of your own is replaced only when asked, and the row says what that deletes", () => {
  const mine = { ...listed, installed: true, installed_version: "0.0.1", by_hand: true };
  assert.equal(marketAction(mine), "replace");
  assert.equal(marketNote(mine), "~/.berth/plugins/acme-standup is your own (0.0.1). Replacing it deletes what's there.");
  // The agent never says update for one, but a row must not offer it anyway.
  assert.equal(marketAction({ ...mine, update: true }), "replace");
  assert.equal(marketNote({ ...mine, installed_version: undefined }), "~/.berth/plugins/acme-standup is your own. Replacing it deletes what's there.");
});

test("a plugin the listing describes wrongly can't be installed, whatever is here", () => {
  assert.equal(marketAction({ ...listed, error: "its file ../x is not a path inside the plugin's folder" }), "unavailable");
  assert.equal(marketAction({ ...listed, installed: true, update: true, error: "it has no berth-plugin.json" }), "unavailable");
});
