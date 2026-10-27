// node --experimental-strip-types --test src/lib/helper-path.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { helperEnvAsked, helperEnvPath, helperRelayed, helperRelayPath } from "./helper-path.ts";

// The box's side of this is internal/box/helpersapi.go, and its test with
// the real berth-leadline (helpers_leadline_test.go) asks for
// /v1/helpers/leadline/api/api/health: the first "api" is the relay, the
// second the helper's own.
test("a helper's API is under /api/ on the helper, which is one more api after the box's relay", () => {
  assert.equal(helperRelayPath("leadline", "queries?limit=50"), "helpers/leadline/api/api/queries?limit=50");
  assert.equal(helperRelayPath("leadline", "/traces/0af7651916cd43dd8448eb211c80319c"), "helpers/leadline/api/api/traces/0af7651916cd43dd8448eb211c80319c");
  assert.equal(helperRelayPath("acme traces", "health"), "helpers/acme%20traces/api/api/health");
});

test("a relayed path is read back as the plugin and the path the helper sees", () => {
  assert.deepEqual(helperRelayed(helperRelayPath("leadline", "queries?source=acme%2Ffix-orders&limit=50")), { plugin: "leadline", path: "/api/queries", query: "source=acme%2Ffix-orders&limit=50" });
  assert.deepEqual(helperRelayed("helpers/leadline/api/v1/traces"), { plugin: "leadline", path: "/v1/traces", query: "" });
  // What the relay is not: the helper's own routes on the box, and the rest of a box's API.
  assert.equal(helperRelayed("helpers/leadline"), undefined);
  assert.equal(helperRelayed("helpers/leadline/log?limit=100"), undefined);
  assert.equal(helperRelayed("locations/acme/worktrees"), undefined);
  assert.equal(helperRelayed(helperEnvPath("leadline")), undefined);
});

// internal/box/helperenv.go: POST /v1/helpers/{plugin}/attach-env is the
// box's own, beside the relay, and never a path on the helper.
test("a box is asked to look in a worktree's .env files beside the relay, not through it", () => {
  assert.equal(helperEnvPath("database"), "helpers/database/attach-env");
  assert.equal(helperEnvPath("acme traces"), "helpers/acme%20traces/attach-env");
  assert.equal(helperEnvAsked(helperEnvPath("database")), "database");
  assert.equal(helperEnvAsked(helperEnvPath("acme traces")), "acme traces");
  // A helper's own route of that name is the helper's.
  assert.equal(helperEnvAsked(helperRelayPath("database", "attach-env")), undefined);
  assert.equal(helperEnvAsked("helpers/database"), undefined);
  assert.equal(helperEnvAsked("helpers/database/attach-env/more"), undefined);
  assert.equal(helperEnvAsked("locations/acme/attach-env"), undefined);
});
