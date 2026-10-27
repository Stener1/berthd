// Where a plugin's helper is reached on a box.
//
// berthd relays everything after helpers/{plugin}/api to the helper as the
// path it was sent (internal/box/helpersapi.go), and a helper's API lives
// under /api/ on it: /v1/ is where it takes telemetry. berth.helper.request
// is for that API, so its paths are written from there:
// request("devl", "GET", "queries?limit=50") is GET
// helpers/{plugin}/api/api/queries?limit=50 on the box, which reaches the
// helper as /api/queries?limit=50. A path on the helper outside /api/ is
// berth.api.request's to ask for.
const RELAY = /^helpers\/([^/]+)\/api\/(.*)$/;

export function helperRelayPath(plugin: string, path: string): string {
  return `helpers/${encodeURIComponent(plugin)}/api/api/${path.replace(/^\/+/, "")}`;
}

// Where a box is asked to look in a worktree's .env files for a plugin's
// helper: berthd's own route beside the relay, not a path on the helper.
const ENV = /^helpers\/([^/]+)\/attach-env$/;

export function helperEnvPath(plugin: string): string {
  return `helpers/${encodeURIComponent(plugin)}/attach-env`;
}

// helperEnvAsked is the plugin of a box path that is that route, for mock
// mode; undefined for any other box path.
export function helperEnvAsked(boxPath: string): string | undefined {
  const m = ENV.exec(boxPath);
  return m ? decodeURIComponent(m[1]) : undefined;
}

// helperRelayed is the other way round, for mock mode: the plugin and the
// path on the helper ("/api/queries"), with its query, of a box path that is
// a helper's relay; undefined for any other box path.
export function helperRelayed(boxPath: string): { plugin: string; path: string; query: string } | undefined {
  const m = RELAY.exec(boxPath);
  if (!m) return undefined;
  const at = m[2].indexOf("?");
  const path = at < 0 ? m[2] : m[2].slice(0, at);
  return { plugin: decodeURIComponent(m[1]), path: `/${path}`, query: at < 0 ? "" : m[2].slice(at + 1) };
}
