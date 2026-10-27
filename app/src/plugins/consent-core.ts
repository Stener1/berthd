// What decides whether a plugin from ~/.berth/plugins may load. Kept free of
// imports so it can be checked on its own (node app/scripts/check-plugin-consent.mjs).
//
// A plugin runs in the app's own page with the app's full access, so the
// app imports one only after the user has allowed it, and only while what it
// fetched is exactly what they allowed: the hash below must equal the one the
// agent recorded when they did (hooks.PluginHash in internal/hooks/plugins.go
// computes the same).

// What any plugin can do once it loads. Shown before the user allows one.
export const PLUGIN_POWERS = [
  "Use the Shipyard agent's API with your access, including its API token",
  "Run commands and open shells on every box you've paired",
  "Read what boxes return: files, logs, environment and session output",
  "Send anything it can read to any website",
  "Run its hooks as you on this computer, and on a box that has it installed",
];

// cleanPath is Go's path.Clean("/" + p) without the leading slash: what the
// agent serves, and hashes, for a plugin's main.
export function cleanPath(p: string): string {
  const out: string[] = [];
  for (const part of p.split("/")) {
    if (part === "" || part === ".") continue;
    if (part === "..") out.pop();
    else out.push(part);
  }
  return out.join("/");
}

// mainOf reads the main module a manifest names, cleaned; "" for none.
export function mainOf(manifest: Uint8Array): string {
  const m = JSON.parse(new TextDecoder().decode(manifest)) as { main?: unknown };
  return typeof m.main === "string" && m.main !== "" ? cleanPath(m.main) : "";
}

// A program a plugin carries for boxes: berth-plugin.json's "box.helper".
// The laptop agent installs it on a box only when the plugin asks, for a
// box the person turned it on for.
export interface PluginHelper {
  name: string;
  version?: string;
}

// helperOf reads the helper a manifest names, if it names one. A manifest
// that can't be read names none: mainOf has already refused it.
export function helperOf(manifest: Uint8Array): PluginHelper | undefined {
  try {
    const m = JSON.parse(new TextDecoder().decode(manifest)) as { box?: { helper?: { name?: unknown; version?: unknown } } };
    const h = m.box?.helper;
    if (!h || typeof h.name !== "string" || h.name === "") return undefined;
    return { name: h.name, version: typeof h.version === "string" && h.version !== "" ? h.version : undefined };
  } catch {
    return undefined;
  }
}

// helperPower is the line that says so, before the user allows the plugin
// and beside it in Settings.
export function helperPower(h: PluginHelper): string {
  return `Install and run a program, ${h.name}, on the boxes you turn it on for`;
}

// hashPluginFiles is
// sha256("berth-plugin-v1\n" + len(manifest) + "\n" + manifest + "\n" + len(main) + "\n" + main)
// as "sha256:<hex>", lengths in bytes.
export async function hashPluginFiles(manifest: Uint8Array, main: Uint8Array): Promise<string> {
  const enc = new TextEncoder();
  const parts = [enc.encode(`berth-plugin-v1\n${manifest.length}\n`), manifest, enc.encode(`\n${main.length}\n`), main];
  const all = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let at = 0;
  for (const p of parts) {
    all.set(p, at);
    at += p.length;
  }
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", all));
  return `sha256:${Array.from(digest, (b) => b.toString(16).padStart(2, "0")).join("")}`;
}

// mayLoad says whether a plugin may be imported, given the hash of the bytes
// the app fetched for it. Built-ins ship inside the app and need no consent;
// any other plugin needs the user's permission for exactly these bytes.
export function mayLoad(p: { builtin?: boolean; enabled?: boolean; allowed?: string }, fetchedHash: string): boolean {
  if (p.builtin) return true;
  return p.enabled === true && !!p.allowed && p.allowed === fetchedHash;
}
