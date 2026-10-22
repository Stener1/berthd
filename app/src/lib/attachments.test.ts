import assert from "node:assert/strict";
import { test } from "node:test";

import type { Client } from "@/lib/api";

import { attachApi, MAX_ATTACHMENT } from "./attachments.ts";

// A client that records what it was asked to upload and answers as the box.
function fakeClient() {
  const calls: { box: string; path: string; body: Blob; onProgress?: unknown; signal?: AbortSignal }[] = [];
  const client = {
    upload: async (box: string, path: string, body: Blob, onProgress?: unknown, signal?: AbortSignal) => {
      calls.push({ box, path, body, onProgress, signal });
      const name = new URLSearchParams(path.split("?")[1]).get("name") ?? "";
      return { path: `/home/me/acme/.berth/attachments/${name}`, name, type: body.type, size: body.size };
    },
  } as unknown as Client;
  return { client, calls };
}

test("a plugin's attach uploads into a running session's worktree", async () => {
  const { client, calls } = fakeClient();
  const signal = new AbortController().signal;
  const onProgress = () => {};
  const a = await attachApi(client)("devl", { session: "acme web" }, new File(["hi"], "notes.md", { type: "text/markdown" }), { onProgress, signal });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].box, "devl");
  assert.equal(calls[0].path, "sessions/acme%20web/attachments?name=notes.md");
  assert.equal(calls[0].onProgress, onProgress);
  assert.equal(calls[0].signal, signal);
  assert.deepEqual(a, { path: "/home/me/acme/.berth/attachments/notes.md", name: "notes.md", type: "text/markdown", size: 2 });
});

test("a plugin's attach uploads into a worktree an agent is about to start in", async () => {
  const { client, calls } = fakeClient();
  await attachApi(client)("devl", { location: "acme", worktree: "fix/login" }, new File(["x"], "trace.log", { type: "text/plain" }));
  assert.equal(calls[0].path, "locations/acme/worktrees/fix%2Flogin/attachments?name=trace.log");
});

test("a pasted image is named for when it was pasted, and a .json file goes as plain bytes", async () => {
  const { client, calls } = fakeClient();
  await attachApi(client)("devl", { session: "s" }, new File(["png"], "image.png", { type: "image/png" }));
  assert.match(calls[0].path, /^sessions\/s\/attachments\?name=pasted-\d{6}(-\d+)?\.png$/);
  await attachApi(client)("devl", { session: "s" }, new File(["{}"], "data.json", { type: "application/json" }));
  assert.equal(calls[1].body.type, "application/octet-stream");
});

test("a plugin's attach refuses a file over 20 MB without uploading it", async () => {
  const { client, calls } = fakeClient();
  const big = new File([new Uint8Array(MAX_ATTACHMENT + 1)], "dump.log", { type: "text/plain" });
  await assert.rejects(attachApi(client)("devl", { session: "s" }, big), { message: "dump.log is 20 MB; attachments can be up to 20 MB." });
  assert.equal(calls.length, 0);
});
