import type { BerthEvent } from "@/lib/api";
import type { ApplyLine, InstalledKit, InstalledKitOn, KitInfo, KitTarget } from "@/lib/kits";

// Mock mode's kits, behaving like internal/agent/kits.go closely enough to
// build the UI on: two kits on the laptop, one installed on devl's shop in an
// older version, and any link previewing as a kit made from its name.

type Emit = (e: Omit<BerthEvent, "time">) => void;

const daysAgo = (d: number) => new Date(Date.now() - d * 86_400_000).toISOString();

const setupScript = `#!/bin/sh
# Each worktree gets its own database, copied from the main one so it starts
# with real data, and its own .env pointing at it.
set -eu
createdb -T shop "$BERTH_WORKTREE_SLUG" 2>/dev/null || createdb "$BERTH_WORKTREE_SLUG"
cp "$BERTH_ROOT_PATH/.env" .env
pnpm install --frozen-lockfile
pnpm db:migrate
`;

const teardownScript = `#!/bin/sh
dropdb --if-exists "$BERTH_WORKTREE_SLUG"
`;

const shopKit: KitInfo = {
  id: "shop-dev",
  name: "Shop development",
  description: "Every worktree of the shop gets its own database, ports, dev server and URL, and is cleaned up when it goes.",
  version: "3",
  match: { slug: "acme/shop" },
  requires: [
    { tool: "psql", hint: "PostgreSQL client: sudo apt install postgresql-client" },
    { tool: "pnpm", hint: "corepack enable" },
  ],
  config: {
    setup: "$BERTH_KIT_DIR/scripts/setup.sh",
    archive: "$BERTH_KIT_DIR/scripts/teardown.sh",
    ports: 3,
    env: {
      DATABASE_URL: "postgresql://postgres@localhost:5432/$BERTH_WORKTREE_SLUG",
      NEXT_PUBLIC_WEBAPP_URL: "http://$BERTH_WORKTREE_NAME.shop.$BERTH_BOX.localhost:1377",
    },
    services: [
      // The dev server runs in a terminal tab of its own.
      { name: "web", run: "pnpm dev --port $BERTH_PORT", autostart: true, terminal: true, title: "Next.js" },
      { name: "api", run: "pnpm --filter @shop/api dev --port $BERTH_PORT_1" },
    ],
    flows: [
      {
        id: "lint-after-turn",
        name: "Lint what changed after every Claude turn",
        enabled: true,
        trigger: { event: "agent.finished", where: { agent: "claude" } },
        steps: [
          { kind: "run", command: "pnpm lint --filter=...[HEAD]", timeout: "10m" },
          { kind: "prompt", when: "failure", text: "Lint failed:\n\n{{prev.output}}\n\nFix it." },
        ],
      },
    ],
    hooks: [{ on: "before:worktree.create", run: "test \"$BERTH_BRANCH\" != main || { echo 'Make a branch, not main'; exit 1; }" }],

    // Seeded users a worktree's pages can be opened logged in as.
    login: {
      script: "scripts/login.sh",
      users: [
        { email: "pro@acme.test", label: "Pro user" },
        { email: "admin@acme.test", label: "Team admin" },
        { email: "free@acme.test" },
      ],
    },
  },
  origin: "user",
  source: { src: "https://github.com/acme/kits/tree/main/shop-dev", commit: "4f2a9c1e8b7d3a6f5e4c2b1a0d9e8f7c6b5a4d3e", fetched: daysAgo(2) },
  hash: "c41e9a2b77f0",
  path: "/Users/me/.berth/kits/shop-dev",
  file_list: [
    { path: "kit.json", size: 1420 },
    { path: "scripts/setup.sh", size: setupScript.length, text: setupScript },
    { path: "scripts/teardown.sh", size: teardownScript.length, text: teardownScript },
    { path: "seed/fixtures.sql.gz", size: 284_211 },
  ],
};

const nodeKit: KitInfo = {
  id: "node-app",
  name: "Node app",
  description: "pnpm install in new worktrees and a dev server on the worktree's own port.",
  version: "1",
  config: {
    setup: "pnpm install",
    services: [{ name: "web", run: "pnpm dev --port $BERTH_PORT", autostart: true }],
  },
  origin: "plugin:hello-ports",
  hash: "7d10b3c9e2aa",
  path: "/Users/me/.berth/plugins/hello-ports/kits/node-app",
  file_list: [{ path: "kit.json", size: 240 }],
};

const kept: KitInfo[] = [shopKit, nodeKit];

const installedFor = (k: KitInfo, hash = k.hash, at = daysAgo(9)): InstalledKit => ({
  id: k.id,
  name: k.name,
  version: k.version,
  source: k.source?.src,
  hash,
  dir: `/home/me/.config/berth/box/kits/${k.id}`,
  config: k.config,
  installed_at: at,
});

// Where kits are installed, by "box/location".
const installed: Record<string, InstalledKit> = {
  "devl/shop": installedFor(shopKit, "9b0e11d4c2f3", daysAgo(9)),
};

const slugs: Record<string, string> = { "devl/shop": "acme/shop", "devl/notes": "me/notes", "gpu/evals": "me/evals" };

// kitOn is a project's installed kit, for the config mock.
export function kitOn(box: string, location: string): InstalledKit | undefined {
  return installed[`${box}/${location}`];
}

const strip = (k: KitInfo): KitInfo => ({ ...k, file_list: k.file_list.map(({ path, size }) => ({ path, size })) });

function previewOf(src: string): KitInfo {
  const known = kept.find((k) => k.source?.src === src);
  if (known) return { ...known, source: { ...known.source!, fetched: new Date().toISOString() } };
  const name = src.split(/[/#@]/).filter(Boolean).pop()?.replace(/\.git$|kit\.json$/, "") || "shared-kit";
  const id = name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "shared-kit";
  return {
    id,
    name: id.replace(/-/g, " ").replace(/^\w/, (c) => c.toUpperCase()),
    description: "Worktrees get their dependencies installed, a Postgres database of their own, and a dev server.",
    version: "1",
    match: { slug: "acme/shop" },
    requires: [{ tool: "psql", hint: "PostgreSQL client" }],
    config: {
      setup: "$BERTH_KIT_DIR/setup.sh",
      archive: "dropdb --if-exists $BERTH_WORKTREE_SLUG",
      env: { DATABASE_URL: "postgresql://localhost/$BERTH_WORKTREE_SLUG" },
      services: [{ name: "web", run: "pnpm dev --port $BERTH_PORT", autostart: true }],
      hooks: [{ on: "worktree.removed", run: "echo removed $BERTH_NAME >> ~/worktrees.log" }],
    },
    origin: "user",
    source: { src, commit: src.includes("github") ? "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678" : undefined, fetched: new Date().toISOString() },
    hash: Math.random().toString(16).slice(2, 14),
    path: `/Users/me/.berth/kits/${id}`,
    file_list: [
      { path: "kit.json", size: 812 },
      { path: "setup.sh", size: 96, text: "#!/bin/sh\nset -eu\npnpm install\ncreatedb \"$BERTH_WORKTREE_SLUG\" || true\n" },
    ],
  };
}

const wait = (ms: number, signal?: AbortSignal) =>
  new Promise<void>((resolve, reject) => {
    const t = setTimeout(resolve, ms);
    signal?.addEventListener("abort", () => (clearTimeout(t), reject(new DOMException("aborted", "AbortError"))));
  });

// kitsCall answers the laptop's kit routes, or returns undefined.
export function kitsCall(method: string, path: string, body: unknown, emit: Emit, delay: <T>(v: T) => Promise<T>): Promise<unknown> | undefined {
  const key = `${method} ${path}`;
  if (key === "GET /v1/kits") return delay(kept.map(strip));
  if (key === "GET /v1/kits/installed") {
    const out: InstalledKitOn[] = Object.entries(installed).map(([k, kit]) => {
      const [box, location] = k.split("/");
      return { box, location, slug: slugs[k], kit, outdated: kept.find((x) => x.id === kit.id)?.hash !== kit.hash };
    });
    return delay(out);
  }
  if (key === "POST /v1/kits/preview") {
    const { src } = body as { src: string };
    if (/fail|nope|404/.test(src)) return new Promise((_, reject) => setTimeout(() => reject(new Error(`git clone ${src}: repository not found`)), 700));
    const kit = previewOf(src);
    return new Promise((r) => setTimeout(() => r({ kit, replaces: kept.some((k) => k.id === kit.id) }), 700));
  }
  if (key === "POST /v1/kits/add") {
    const kit = previewOf((body as { src: string }).src);
    const i = kept.findIndex((k) => k.id === kit.id);
    if (i >= 0) kept[i] = kit;
    else kept.push(kit);
    setTimeout(() => emit({ type: "kit.added", data: { kit: kit.id, source: kit.source?.src } }), 20);
    return delay(strip(kit));
  }
  if (key === "POST /v1/kits/remove") {
    const t = body as KitTarget;
    const had = installed[`${t.box}/${t.location}`];
    delete installed[`${t.box}/${t.location}`];
    setTimeout(() => emit({ type: "config.changed", box: t.box, data: { location: t.location } }), 20);
    return delay(had);
  }
  if (key === "POST /v1/kits/save") {
    const r = body as KitTarget & { id: string; name: string; description?: string };
    const kit: KitInfo = { ...previewOf(r.id), id: r.id, name: r.name, description: r.description, match: { slug: slugs[`${r.box}/${r.location}`] }, source: undefined, origin: "user", path: `/Users/me/.berth/kits/${r.id}` };
    kept.push(kit);
    return delay(strip(kit));
  }
  const m = /^\/v1\/kits\/([^/]+)(?:\/(update))?$/.exec(path);
  if (m) {
    const id = decodeURIComponent(m[1]);
    const k = kept.find((x) => x.id === id);
    if (!k) return Promise.reject(new Error(`no kit called ${id}`));
    if (method === "GET") return delay(k);
    if (method === "DELETE") {
      kept.splice(kept.indexOf(k), 1);
      return delay({ removed: id });
    }
    if (method === "POST" && m[2] === "update") {
      if (!k.source) return Promise.reject(new Error("only kits added from a link can be updated from it"));
      return new Promise((r) => setTimeout(() => r({ kit: strip(k), changed: false }), 900));
    }
  }
  return undefined;
}

// kitsStream applies a kit to projects, one line each, like the agent.
export async function kitsStream(path: string, body: unknown, onValue: (v: ApplyLine) => void, emit: Emit, signal?: AbortSignal): Promise<boolean> {
  const m = /^\/v1\/kits\/([^/]+)\/apply$/.exec(path);
  if (!m) return false;
  const k = kept.find((x) => x.id === decodeURIComponent(m[1]));
  const { targets } = body as { targets: KitTarget[] };
  let applied = 0;
  for (const t of targets) {
    await wait(650, signal);
    if (!k) {
      onValue({ box: t.box, location: t.location, error: "no such kit" });
      continue;
    }
    if (t.box === "gpu") {
      onValue({ box: t.box, location: t.location, error: `${t.location} on gpu is not a git repository` });
      continue;
    }
    const kit = installedFor(k, k.hash, new Date().toISOString());
    installed[`${t.box}/${t.location}`] = kit;
    applied++;
    const warnings = (k.requires ?? []).filter((r) => r.tool === "psql").map((r) => `psql is not installed on ${t.box}: ${r.hint ?? "install it"}`);
    onValue({ box: t.box, location: t.location, kit, warnings });
    emit({ type: "config.changed", box: t.box, data: { location: t.location } });
  }
  onValue({ done: true, applied, ...(applied < targets.length ? { error: `${targets.length - applied} of ${targets.length} projects failed` } : {}) });
  return true;
}
