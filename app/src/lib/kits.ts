import type { AgentPreset, Client } from "@/lib/api";
import type { RepoConfig } from "@/lib/flows";

// A kit sets a project up the same way on every box: its config (setup and
// teardown, env, ports, services, hooks, flows, agents) and the files those
// use. It is its own layer per project, between the repository's committed
// config and the box's own. Kits live on this laptop (~/.berth/kits), come
// from a link someone shares, and are applied to projects on boxes.

export interface KitRequirement {
  tool: string;
  hint?: string;
}

export interface Kit {
  id: string;
  name: string;
  description?: string;
  version?: string;
  // match.slug ("acme/shop") is the repository a kit is for.
  match?: { slug?: string };
  requires?: KitRequirement[];
  config: RepoConfig;
}

export interface KitFile {
  path: string;
  size: number;
  // Small text files, for review before applying.
  text?: string;
}

export interface KitInfo extends Kit {
  // "user" for kits kept on this laptop, "plugin:<id>" for a plugin's.
  origin: string;
  source?: { src: string; commit?: string; fetched: string };
  hash: string;
  path: string;
  file_list: KitFile[];
}

// InstalledKit is a kit as a project on a box has it.
export interface InstalledKit {
  id: string;
  name: string;
  version?: string;
  source?: string;
  hash?: string;
  dir: string;
  config: RepoConfig;
  installed_at: string;
}

export interface InstalledKitOn {
  box: string;
  location: string;
  slug?: string;
  kit: InstalledKit;
  outdated: boolean;
}

export interface KitTarget {
  box: string;
  location: string;
}

// ApplyLine is one project's result while a kit is applied, then the end.
export interface ApplyLine {
  box?: string;
  location?: string;
  kit?: InstalledKit;
  warnings?: string[];
  error?: string;
  done?: boolean;
  applied?: number;
}

const enc = encodeURIComponent;

export const kitsApi = {
  list: async (c: Client) => (await c.laptop<KitInfo[] | null>("GET", "/v1/kits")) ?? [],
  get: (c: Client, id: string) => c.laptop<KitInfo>("GET", `/v1/kits/${enc(id)}`),
  preview: (c: Client, src: string) => c.laptop<{ kit: KitInfo; replaces: boolean }>("POST", "/v1/kits/preview", { src }),
  // hash is the reviewed kit's: the agent refuses a source that changed since.
  add: (c: Client, src: string, hash: string) => c.laptop<KitInfo>("POST", "/v1/kits/add", { src, hash }),
  update: (c: Client, id: string) => c.laptop<{ kit: KitInfo; changed: boolean }>("POST", `/v1/kits/${enc(id)}/update`),
  remove: (c: Client, id: string) => c.laptop("DELETE", `/v1/kits/${enc(id)}`),
  installed: async (c: Client) => (await c.laptop<InstalledKitOn[] | null>("GET", "/v1/kits/installed")) ?? [],
  uninstall: (c: Client, t: KitTarget) => c.laptop<InstalledKit>("POST", "/v1/kits/remove", t),
  save: (c: Client, req: KitTarget & { id: string; name: string; description?: string }) => c.laptop<KitInfo>("POST", "/v1/kits/save", req),
  // apply installs a kit on projects, calling onLine for each project's
  // result as it lands, and resolves with the final line.
  // hash, when given, is the kit the user reviewed: a kept kit that changed
  // since is refused rather than applied.
  async apply(c: Client, id: string, targets: KitTarget[], onLine: (l: ApplyLine) => void, signal?: AbortSignal, hash?: string): Promise<ApplyLine> {
    let last: ApplyLine = {};
    await c.stream(
      "POST",
      `/v1/kits/${enc(id)}/apply`,
      { targets, ...(hash ? { hash } : {}) },
      (v) => {
        const l = v as ApplyLine;
        if (l.done) last = l;
        else onLine(l);
      },
      signal,
    );
    if (!last.done && !signal?.aborted) throw new Error("The agent stopped answering before the kit was applied everywhere.");
    return last;
  },
};

// kitLink is the link that opens a kit in Shipyard, for kits added from one.
export function kitLink(src: string): string {
  return `berth://kit?src=${enc(src)}`;
}

// kitSrcFromLink reads the source out of a berth://kit link, or returns the
// input when it is already a source (a repo URL, a gist, a folder).
export function kitSrcFromLink(input: string): string {
  const v = input.trim();
  const m = /^berth:\/\/kit\/?\?(.*)$/i.exec(v);
  if (m) return new URLSearchParams(m[1]).get("src") ?? "";
  return v;
}

// describeSource names where a kit came from, for people.
export function describeSource(src?: string): string {
  if (!src) return "Made on this laptop";
  const gh = /^https?:\/\/(?:www\.)?github\.com\/([^/]+\/[^/#@]+?)(?:\.git)?(?:\/tree\/([^/]+)(?:\/(.+))?)?\/?(?:[#@].*)?$/.exec(src);
  if (gh) return [gh[1], gh[3]].filter(Boolean).join(" · ");
  if (/gist\.github/.test(src)) return "A gist";
  if (src.startsWith("/") || src.startsWith("~")) return src.replace(/^\/(Users|home)\/[^/]+/, "~");
  try {
    const u = new URL(src);
    return u.host + u.pathname.replace(/\/$/, "");
  } catch {
    return src;
  }
}

function mergeBy<T>(base: T[] | undefined, over: T[] | undefined, key: (t: T) => string): T[] | undefined {
  if (!base?.length && !over?.length) return undefined;
  const out = [...(base ?? [])];
  for (const o of over ?? []) {
    const i = out.findIndex((x) => key(x) === key(o));
    if (i >= 0) out[i] = o;
    else out.push(o);
  }
  return out;
}

// mergeConfig lays over on base the way boxes do (internal/box/repoconfig.go):
// scalars and env entries replace, services, agents and flows replace by
// name, and hooks add up.
export function mergeConfig(base: RepoConfig | null | undefined, over: RepoConfig | null | undefined): RepoConfig {
  const b = base ?? {};
  const o = over ?? {};
  return {
    setup: o.setup || b.setup,
    archive: o.archive || b.archive,
    ports: o.ports || b.ports,
    env: o.env && Object.keys(o.env).length ? { ...b.env, ...o.env } : b.env,
    services: mergeBy(b.services, o.services, (s) => s.name),
    agents: mergeBy<AgentPreset>(b.agents, o.agents, (a) => a.id),
    flows: mergeBy(b.flows, o.flows, (f) => f.id),
    hooks: b.hooks?.length || o.hooks?.length ? [...(b.hooks ?? []), ...(o.hooks ?? [])] : undefined,
    // A layer's login replaces the one under it whole.
    login: o.login ?? b.login,
  };
}

// kitHas says whether the kit's own layer sets a field (or one entry of it),
// so a value in the layer under this box's can be credited to the kit.
export function kitHas(kit: RepoConfig | undefined, field: "setup" | "archive" | "ports" | "env" | "services" | "agents", key?: string): boolean {
  if (!kit) return false;
  switch (field) {
    case "setup":
    case "archive":
      return !!kit[field];
    case "ports":
      return !!kit.ports;
    case "env":
      return key !== undefined && kit.env?.[key] !== undefined;
    case "services":
      return !!kit.services?.some((s) => s.name === key);
    case "agents":
      return !!kit.agents?.some((a) => a.id === key);
  }
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
