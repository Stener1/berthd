import { ApiError } from "@/lib/api";
import type { Marketplace, MarketPlugin } from "@/lib/marketplace";
import type { HelperRemoved } from "@/lib/plugin-helpers";
import { hashPluginFiles } from "@/plugins/consent-core";

// Mock mode's plugin marketplace, behaving like internal/agent/marketplace.go
// closely enough to build the UI on. Three plugins are listed, one in each
// state a row has: hello-ports, which mock mode already has as a plugin of
// your own (Replace); Standup notes, which isn't installed (Install); and
// Deploy board, installed from the listing in an older version (Update).
// Installing one gives it a small module of its own, so the consent dialog
// has real bytes to hash and allowing it really loads it.
//
// ?market=offline fails every read of the listing, ?market=flaky only the
// first, and ?market=mismatch fails every install on its checksum.
//
// Leadline and Database are listed too, and they are the real thing: the
// files the listing ships (site/plugins/leadline/ and site/plugins/database/,
// built by scripts/build-marketplace.mjs), installed and allowed from the
// start, so mock mode and the live demo show their panels on the mock
// helpers (mock-helper.ts). A new account (?fresh=1) starts without them.

// MockPlugin is a plugin in the mock's ~/.berth/plugins, as GET /v1/plugins
// lists it.
export interface MockPlugin {
  id: string;
  name: string;
  version: string;
  main: string;
  description: string;
  entry: string;
  enabled: boolean;
  defaultEnabled?: boolean;
  allowed: string | undefined;
  changed?: boolean;
  source?: "marketplace";
}

type Delay = <T>(v: T) => Promise<T>;

const REGISTRY = "https://berthd.app/plugins/index.json";
const MAIN = "dist/index.js";
const scenario = typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("market");

interface Listed {
  id: string;
  name: string;
  description: string;
  version: string;
  author: string;
  homepage?: string;
  // The one command the stand-in module adds.
  command: string;
  // Its real files, for a plugin this repository builds (see shipped).
  files?: Record<string, Uint8Array>;
}

const listing: Listed[] = [
  {
    id: "hello-ports",
    name: "Hello ports (example)",
    description: "An example plugin to copy from: every dev server on every box, one click from your browser.",
    version: "0.1.0",
    author: "Shipyard",
    homepage: "https://github.com/cosscom/shipyard/tree/main/plugins/hello-ports",
    command: "Say hello to the ports",
  },
  {
    id: "acme-standup",
    name: "Standup notes",
    description: "What each agent finished since yesterday, ready to paste into standup.",
    version: "1.0.0",
    author: "Acme",
    homepage: "https://example.com/acme-standup",
    command: "Copy standup notes",
  },
  {
    id: "acme-deploys",
    name: "Deploy board",
    description: "Which branch is on which Acme environment, and who put it there.",
    version: "1.2.0",
    author: "Acme",
    command: "Show the deploy board",
  },
];

const manifestOf = (l: Listed, version: string) => `${JSON.stringify({ id: l.id, name: l.name, version, description: l.description, main: MAIN }, null, 2)}\n`;

// A stand-in for the bundle a real listing carries: one command.
const sourceOf = (l: Listed, version: string) =>
  `// ${l.name} ${version}\nexport default function activate(berth) {\n  berth.addCommand({ id: "run", title: ${JSON.stringify(l.command)}, run: () => berth.notify(${JSON.stringify(l.name)}, "Installed from the listing.") });\n}\n`;

const enc = new TextEncoder();
const filesOf = (l: Listed, version: string) => l.files ?? ({ "berth-plugin.json": enc.encode(manifestOf(l, version)), [MAIN]: enc.encode(sourceOf(l, version)) } as Record<string, Uint8Array>);

const installedAs = (l: Listed, version: string): MockPlugin => ({
  id: l.id,
  name: l.name,
  version,
  main: MAIN,
  description: l.description,
  entry: `/v1/plugins/${l.id}/${MAIN}`,
  enabled: false,
  allowed: undefined,
  source: "marketplace",
});

// What the marketplace has installed, by plugin: the files the mock agent
// serves for it (GET /v1/plugins/{id}/{file}).
const installedFiles = new Map<string, Record<string, Uint8Array>>();

// seededPlugins are in ~/.berth/plugins from the start: Deploy board, from
// the listing before its newest version, and never turned on.
const deploys = listing[2];
installedFiles.set(deploys.id, filesOf(deploys, "1.1.0"));
export const seededPlugins: MockPlugin[] = [installedAs(deploys, "1.1.0")];

// The plugins this repository lists, as the listing ships them, read when
// the mock first lists plugins: each one's manifest and its bundle, as text,
// in chunks of their own.
const SHIPPED = ["leadline", "database"];
const shipped = import.meta.glob<string>("../../../site/plugins/{leadline,database}/*/{berth-plugin.json,dist/index.js}", { query: "?raw", import: "default" });

let ready: Promise<void> | undefined;

// marketplaceReady resolves once those plugins' files are read and, unless
// this is a new account, each is installed and allowed in installed (the
// mock's ~/.berth/plugins): allowed for exactly the bytes the mock serves,
// as the consent dialog would have recorded.
export function marketplaceReady(installed: MockPlugin[], fresh: boolean): Promise<void> {
  ready ??= (async () => {
    for (const id of SHIPPED) {
      const files: Record<string, Uint8Array> = {};
      let version = "";
      for (const [path, read] of Object.entries(shipped)) {
        const at = /\/plugins\/([^/]+)\/([^/]+)\/(.+)$/.exec(path);
        if (!at || at[1] !== id) continue;
        version = at[2];
        files[at[3]] = enc.encode(await read());
      }
      const manifest = files["berth-plugin.json"];
      if (!manifest || !files[MAIN]) continue;
      const m = JSON.parse(new TextDecoder().decode(manifest)) as { name: string; description: string; author: string; homepage?: string };
      const l: Listed = { id, name: m.name, description: m.description, version, author: m.author, homepage: m.homepage, command: "", files };
      listing.push(l);
      if (fresh || installed.some((p) => p.id === l.id)) continue;
      installedFiles.set(l.id, files);
      // ?helper=held: Leadline changed since it was allowed (an older bundle
      // was), so it is off until it is reviewed again (mock-helper.ts).
      if (id === "leadline" && new URLSearchParams(window.location.search).get("helper") === "held") {
        installed.push({ ...installedAs(l, version), allowed: await hashPluginFiles(manifest, enc.encode("// the bundle before this one\n")), changed: true });
        continue;
      }
      installed.push({ ...installedAs(l, version), enabled: true, allowed: await hashPluginFiles(manifest, files[MAIN]) });
    }
  })().catch((err) => console.warn("mock: could not read the listed plugins' files", err));
  return ready;
}

// marketplaceFile is a file of a plugin the marketplace installed, or
// undefined for a plugin that came some other way.
export function marketplaceFile(id: string, file: string): Uint8Array | undefined {
  return installedFiles.get(id)?.[file];
}

function view(l: Listed, installed: MockPlugin[]): MarketPlugin {
  const files = Object.entries(filesOf(l, l.version)).map(([path, data]) => ({ path, url: `${l.id}/${l.version}/${path}`, sha256: "0".repeat(64), size: data.length }));
  const have = installed.find((p) => p.id === l.id);
  const mine = have?.source === "marketplace";
  return {
    id: l.id,
    name: l.name,
    description: l.description,
    version: l.version,
    author: l.author,
    homepage: l.homepage,
    files,
    size: files.reduce((n, f) => n + f.size, 0),
    installed: have ? true : undefined,
    installed_version: have?.version,
    update: have && mine && have.version !== l.version ? true : undefined,
    by_hand: have && !mine ? true : undefined,
  };
}

let reads = 0;

// The agent takes a moment over the web, and its refusals come after it.
const refuse = (delay: Delay, message: string, status: number, code?: string) =>
  delay(null).then(() => {
    throw new ApiError(message, status, code);
  });

// removeHelpers is what uninstalling a plugin does to its helper on the
// boxes (mock-helper.ts), and what the agent answers of each.
export function marketplaceCall(method: string, path: string, body: unknown, installed: MockPlugin[], delay: Delay, removeHelpers?: (id: string) => HelperRemoved[] | undefined): Promise<unknown> | undefined {
  if (method === "GET" && /^\/v1\/marketplace(\?fresh=1)?$/.test(path)) {
    reads++;
    if (scenario === "offline" || (scenario === "flaky" && reads === 1)) return refuse(delay, `could not reach ${REGISTRY}: dial tcp: lookup berthd.app: no such host`, 502, "registry_unreachable");
    // Leadline and Database join the listing once their files are read (marketplaceReady).
    return (ready ?? Promise.resolve()).then(() => delay<Marketplace>({ registry: REGISTRY, host: "berthd.app", fetched: new Date().toISOString(), plugins: listing.map((l) => view(l, installed)) }));
  }
  const install = /^\/v1\/marketplace\/([^/]+)\/install$/.exec(path);
  if (method === "POST" && install) {
    const id = decodeURIComponent(install[1]);
    const l = listing.find((x) => x.id === id);
    if (!l) return refuse(delay, `the listing has no plugin called ${id}`, 404);
    const req = (body ?? {}) as { version?: string; replace?: boolean };
    if (req.version && req.version !== l.version) return refuse(delay, `the listing now has ${id} ${l.version}, not ${req.version}; look at it again`, 409, "listing_changed");
    const have = installed.find((p) => p.id === id);
    if (have && have.source !== "marketplace" && !req.replace)
      return refuse(delay, `~/.berth/plugins/${id} wasn't installed from the listing, or was changed since; replace it to install over it`, 409, "plugin_by_hand");
    if (scenario === "mismatch") return refuse(delay, `${MAIN} of ${id} does not match the listing's checksum; nothing was installed`, 502, "plugin_mismatch");
    installedFiles.set(id, filesOf(l, l.version));
    // Installing never turns a plugin on: one that was allowed is off as
    // changed until it is reviewed again, and keeps what was allowed.
    const same = have?.source === "marketplace" && have.version === l.version;
    const next: MockPlugin = same ? have : { ...installedAs(l, l.version), allowed: have?.allowed, changed: have?.allowed ? true : undefined };
    if (have) Object.assign(have, next);
    else installed.push(next);
    return delay(view(l, installed));
  }
  const remove = /^\/v1\/marketplace\/([^/]+)$/.exec(path);
  if (method === "DELETE" && remove) {
    const id = decodeURIComponent(remove[1]);
    const at = installed.findIndex((p) => p.id === id && p.source === "marketplace");
    if (at < 0) return refuse(delay, `${id} wasn't installed from the listing; remove ~/.berth/plugins/${id} yourself`, 409, "plugin_by_hand");
    installed.splice(at, 1);
    installedFiles.delete(id);
    const helpers = removeHelpers?.(id);
    return delay({ removed: id, ...(helpers ? { helpers } : {}) });
  }
  return undefined;
}
