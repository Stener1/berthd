import assert from "node:assert/strict";
import { test } from "node:test";

import { heldNote, helperWhere, listOf, type PluginHelpers, stopNote, stoppedNote } from "./plugin-helpers.ts";

const fleet: PluginHelpers = {
  plugin: "leadline",
  name: "berth-leadline",
  boxes: [
    { box: "devl", state: "running", version: "0.1.0" },
    { box: "gpu", state: "off" },
    { box: "old-vps", state: "offline" },
  ],
};

test("names read as a sentence has them", () => {
  assert.equal(listOf([]), "");
  assert.equal(listOf(["devl"]), "devl");
  assert.equal(listOf(["devl", "gpu"]), "devl and gpu");
  assert.equal(listOf(["devl", "gpu", "old-vps"]), "devl, gpu and old-vps");
});

test("the boxes a helper is on are the ones that have it in any state, and offline ones are apart", () => {
  assert.deepEqual(helperWhere(fleet), { known: true, on: ["devl"], away: ["old-vps"] });
  assert.deepEqual(helperWhere({ ...fleet, boxes: [{ box: "devl", state: "failed" }, { box: "gpu", state: "starting" }, { box: "mac", state: "unsupported" }] }), { known: true, on: ["devl", "gpu"], away: [] });
  assert.deepEqual(helperWhere(undefined), { known: false, on: [], away: [] });
});

test("turning a plugin off says where its program stops, and that what it recorded is kept", () => {
  assert.equal(
    stopNote("berth-leadline", fleet),
    "Its program, berth-leadline, is stopped and removed on devl. What it recorded there is kept. old-vps is offline: if it runs there, it's stopped when old-vps next connects.",
  );
  assert.equal(
    stopNote("berth-leadline", { ...fleet, boxes: [{ box: "devl", state: "running" }, { box: "gpu", state: "running" }] }),
    "Its program, berth-leadline, is stopped and removed on devl and gpu. What it recorded there is kept.",
  );
});

test("removing a plugin says what it recorded goes too", () => {
  assert.equal(
    stopNote("berth-leadline", fleet, true),
    "Its program, berth-leadline, is stopped and removed on devl. What it recorded on your boxes is deleted too. old-vps is offline: if it runs there, it's stopped when old-vps next connects.",
  );
  assert.equal(stopNote("berth-leadline", { ...fleet, boxes: [{ box: "gpu", state: "off" }] }, true), "Its program, berth-leadline, isn't running on any box. What it recorded on your boxes is deleted.");
});

test("with nothing running, nothing is said to stop", () => {
  assert.equal(stopNote("berth-leadline", { ...fleet, boxes: [{ box: "gpu", state: "off" }] }), "Its program, berth-leadline, isn't running on any box, so nothing is stopped.");
  assert.equal(
    stopNote("berth-leadline", { ...fleet, boxes: [{ box: "gpu", state: "off" }, { box: "old-vps", state: "offline" }, { box: "lab", state: "offline" }] }),
    "Its program, berth-leadline, isn't running on a box Shipyard can reach. old-vps and lab are offline: if it runs there, it's stopped when each next connects.",
  );
});

test("before the agent has said where it runs, no box is named", () => {
  assert.equal(stopNote("berth-leadline", undefined), "Its program, berth-leadline, is stopped and removed on every box it runs on. What it recorded there is kept.");
  assert.equal(stopNote("berth-leadline", undefined, true), "Its program, berth-leadline, is stopped and removed on every box it runs on. What it recorded there is deleted too.");
});

test("afterwards it says where it stopped, where that waits, and what to restart", () => {
  assert.equal(
    stoppedNote("berth-leadline", [
      { box: "devl", state: "removed", attached: [{ location: "shop", worktree: "checkout-fix", service: "web" }] },
      { box: "gpu", state: "none" },
      { box: "old-vps", state: "pending" },
    ]),
    "Stopped berth-leadline on devl. old-vps can't be reached: it's stopped there when old-vps next connects. Restart web in shop/checkout-fix on devl: it still points at berth-leadline.",
  );
  assert.equal(
    stoppedNote("berth-leadline", [
      {
        box: "devl",
        state: "removed",
        attached: [
          { location: "shop", worktree: "checkout-fix", service: "web" },
          { location: "shop", worktree: "checkout-fix", service: "worker" },
        ],
      },
      { box: "gpu", state: "failed", error: "its folder could not be removed" },
    ]),
    "Stopped berth-leadline on devl. gpu couldn't remove it: its folder could not be removed. Restart web in shop/checkout-fix and worker in shop/checkout-fix on devl: they still point at berth-leadline.",
  );
});

test("a helper that was on no box leaves nothing to say", () => {
  assert.equal(stoppedNote("berth-leadline", [{ box: "gpu", state: "none" }]), undefined);
  assert.equal(stoppedNote("berth-leadline", []), undefined);
  assert.equal(stoppedNote("berth-leadline", undefined), undefined);
});

test("a helper held while its plugin waits for review says where it still runs", () => {
  assert.equal(heldNote("berth-leadline", { ...fleet, held: true }), "berth-leadline is still running on devl while this waits for your review.");
  assert.equal(heldNote("berth-leadline", fleet), undefined);
  assert.equal(heldNote("berth-leadline", { ...fleet, held: true, boxes: [{ box: "gpu", state: "off" }] }), undefined);
});
