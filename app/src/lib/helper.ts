import type { BerthHelper, HelperEnvLook, HelperStatus } from "@berth/plugin";

import { ApiError, type Client } from "@/lib/api";
import { helperEnvPath, helperRelayPath } from "@/lib/helper-path";

// A plugin's helper: a program it carries (berth-plugin.json's "box.helper")
// that the laptop agent installs on a box and the box daemon runs. The agent
// downloads the build for the box's platform, checks it against the
// manifest's checksum and hands it to the box; the app only asks for it, by
// plugin and box.

const route = (id: string, box: string, purge = false) => `/v1/plugins/${encodeURIComponent(id)}/helper?${new URLSearchParams({ box, ...(purge ? { purge: "1" } : {}) })}`;

export const helperApi = {
  status: (c: Client, id: string, box: string) => c.laptop<HelperStatus>("GET", route(id, box)),
  install: (c: Client, id: string, box: string) => c.laptop<HelperStatus>("POST", route(id, box)),
  remove: (c: Client, id: string, box: string, purge = false) => c.laptop<HelperStatus>("DELETE", route(id, box, purge)),
  // The helper's own API (/api/{path} on the helper) through the box's relay.
  request: <T,>(c: Client, id: string, box: string, method: string, path: string, body?: unknown) =>
    c.box<T>(box, method, helperRelayPath(id, path), body).catch((err: unknown) => {
      throw helperError(err);
    }),
  // The box looks in a worktree's .env files and tells the helper what it
  // found there (internal/box/helperenv.go). It is the box's own route, not
  // the helper's, so its errors are the box's.
  //
  // With env the box reads no file and tells the helper those variables,
  // which the person gave. They are a secret: in this request's body and
  // nowhere else.
  attachEnv: <A,>(c: Client, id: string, box: string, where: { location: string; worktree: string; env?: Record<string, string> }) =>
    c.box<HelperEnvLook<A>>(box, "POST", helperEnvPath(id), { location: where.location, worktree: where.worktree, ...(where.env ? { env: where.env } : {}) }),
};

// helperError gives a helper's own refusal the shape every other error has.
// The box passes a helper's answer through as it is, and a helper may say
// what went wrong as {"error": {"code", "message", "hint"}} where Shipyard's
// own APIs say {"error": "…", "code": "…"}: the message and the code are
// lifted out, and the hint stays in detail.
export function helperError(err: unknown): unknown {
  if (!(err instanceof ApiError)) return err;
  const inner = err.detail?.error;
  if (!inner || typeof inner !== "object") return err;
  const { code, message } = inner as { code?: unknown; message?: unknown };
  const out = new ApiError(typeof message === "string" && message ? message : `The helper answered ${err.status}`, err.status, typeof code === "string" ? code : err.code);
  out.box = err.box;
  out.detail = err.detail;
  return out;
}

// makeHelperApi is a plugin's helper as it sees it (berth.helper): bound to
// its own id, so one plugin can't reach another's.
export function makeHelperApi(client: Client, id: string): BerthHelper {
  return {
    status: (box) => helperApi.status(client, id, box),
    install: (box) => helperApi.install(client, id, box),
    remove: (box, opts) => helperApi.remove(client, id, box, opts?.purge),
    request: (box, method, path, body) => helperApi.request(client, id, box, method, path, body),
    attachEnv: (box, where) => helperApi.attachEnv(client, id, box, where),
  };
}
