import { create } from "zustand";

import type { Client, PluginInfo } from "@/lib/api";
import { hashPluginFiles, helperOf, mainOf, type PluginHelper } from "@/plugins/consent-core";

export { helperPower, mayLoad, PLUGIN_POWERS, type PluginHelper } from "@/plugins/consent-core";

// PluginFiles is what the app would import for a plugin, read once: the
// bytes that are hashed are the bytes that are reviewed and then imported.
export interface PluginFiles {
  manifest: Uint8Array;
  // The main module's path inside the plugin folder, "" for none.
  mainPath: string;
  main: Uint8Array;
  hash: string;
  // The hooks the manifest lists, to show before allowing it.
  hooks: { on: string; run: string }[];
  // The program it carries for boxes ("box.helper"), if it has one.
  helper?: PluginHelper;
}

const MANIFEST = "berth-plugin.json";

export async function readPluginFiles(client: Client, p: PluginInfo): Promise<PluginFiles> {
  const manifest = await client.pluginFile(p, MANIFEST);
  const mainPath = mainOf(manifest);
  const main = mainPath ? await client.pluginFile(p, mainPath) : new Uint8Array();
  let hooks: PluginFiles["hooks"] = [];
  try {
    const m = JSON.parse(new TextDecoder().decode(manifest)) as { hooks?: unknown };
    if (Array.isArray(m.hooks)) hooks = m.hooks.map((h: { on?: unknown; run?: unknown }) => ({ on: String(h?.on ?? ""), run: String(h?.run ?? "") }));
  } catch {
    // mainOf already parsed it; a manifest it could read has no other shape to fail on.
  }
  return { manifest, mainPath, main, hash: await hashPluginFiles(manifest, main), hooks, helper: helperOf(manifest) };
}

// readPluginHelper is the program a plugin carries for boxes, read from its
// manifest: what its row in Settings says it can install. None, or a
// manifest that can't be read, is undefined.
export async function readPluginHelper(client: Client, p: PluginInfo): Promise<PluginHelper | undefined> {
  return client.pluginFile(p, MANIFEST).then(helperOf, () => undefined);
}

// The consent dialog is opened from anywhere (Settings, a plugin's missing
// screen, a plugin that changed) through this store, and answers whether the
// user allowed the plugin.
interface ConsentRequest {
  plugin: PluginInfo;
  resolve(allowed: boolean): void;
}

export const usePluginConsent = create<{ request?: ConsentRequest; dismissed: string[] }>()(() => ({ dismissed: [] }));

// askToAllow opens the consent dialog for p and resolves true once the user
// has allowed it (the agent then has its hash), false otherwise.
export function askToAllow(plugin: PluginInfo): Promise<boolean> {
  return new Promise((resolve) => {
    usePluginConsent.getState().request?.resolve(false);
    usePluginConsent.setState({ request: { plugin, resolve } });
  });
}

export function answerConsent(allowed: boolean) {
  const { request, dismissed } = usePluginConsent.getState();
  if (!request) return;
  usePluginConsent.setState({ request: undefined, dismissed: allowed ? dismissed : [...dismissed, request.plugin.id] });
  request.resolve(allowed);
}
