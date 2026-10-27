import { getVersion } from "@tauri-apps/api/app";
import { invoke } from "@tauri-apps/api/core";
import { check, type Update } from "@tauri-apps/plugin-updater";
import { create } from "zustand";

import { toastManager } from "@/components/ui/toast";
import { isTauri } from "@/lib/api";
import { useStore } from "@/lib/store";

// Updates to the app itself. Shipyard checks the latest GitHub release's
// latest.json when it opens and every few hours after, and downloads a newer
// version quietly. Nothing changes on disk until the person clicks "Restart
// to update" (the status bar, or Settings → About): then the download is
// installed over Shipyard.app and the app relaunches. It never restarts by
// itself. Restarting stops no agent (they run on their boxes), but it does
// close the window, which is the person's call.
//
// The laptop agent outlives the app, so after an update it still runs the
// old berth (and keeps this Mac's box on the old berthd). The updated app,
// as it starts, has it restart (afterLaunch): the agent finishes its work
// under way, stops, and the new one starts, through the login service when
// it is installed at login, and brings this Mac's box up to date. A short
// note says Shipyard was updated.
//
// The update must carry a signature from the key in tauri.conf.json
// (plugins.updater.pubkey); the plugin refuses anything else.

export type UpdaterState =
  | { status: "idle" } // not checked yet, or not in the app
  | { status: "checking" }
  | { status: "current"; checkedAt: number }
  | { status: "downloading"; version: string; received: number; total?: number }
  | { status: "ready"; version: string }
  | { status: "installing"; version: string }
  | { status: "error"; error: string; checkedAt: number };

export const useUpdater = create<UpdaterState>(() => ({ status: "idle" }));

const EVERY = 4 * 60 * 60 * 1000;
let pending: Update | null = null;
let running: Promise<void> | null = null;

// updatesSupported is whether this copy of Shipyard can update itself: the
// packaged app. A dev build (pnpm tauri dev) can check, but only by hand.
export const updatesSupported = () => isTauri();

// checkForUpdate checks once, and downloads what it finds. A check while
// one runs joins it. `manual` is a click on Check now: only then is a
// failure worth showing; a failed check in the background waits for the
// next one.
export function checkForUpdate({ manual = false } = {}): Promise<void> {
  if (!isTauri()) return Promise.resolve();
  running ??= run(manual).finally(() => {
    running = null;
  });
  return running;
}

async function run(manual: boolean) {
  const set = useUpdater.setState;
  const before = useUpdater.getState();
  if (before.status === "installing") return;
  // A downloaded update still waits for its restart, but the feed may have
  // moved on since (releases a few hours apart): a newer one is fetched
  // behind it and takes its place, so a restart always lands on the latest.
  const waiting = before.status === "ready" ? before.version : undefined;
  if (!waiting) set({ status: "checking" }, true);
  try {
    const update = await check({ timeout: 30_000 });
    if (!update || update.version === waiting) {
      await update?.close().catch(() => {});
      if (!waiting) set({ status: "current", checkedAt: Date.now() }, true);
      return;
    }
    if (!waiting) set({ status: "downloading", version: update.version, received: 0 }, true);
    let received = 0;
    let total: number | undefined;
    await update.download(
      (e) => {
        if (waiting) return;
        if (e.event === "Started") total = e.data.contentLength;
        if (e.event === "Progress") received += e.data.chunkLength;
        set({ status: "downloading", version: update.version, received, total }, true);
      },
      { timeout: 10 * 60_000 },
    );
    // Restarting while it downloaded installed the one waiting.
    if (useUpdater.getState().status === "installing") return void update.close().catch(() => {});
    await pending?.close().catch(() => {});
    pending = update;
    set({ status: "ready", version: update.version }, true);
  } catch (e) {
    const error = e instanceof Error ? e.message : String(e);
    console.warn("berth: update check failed:", error);
    // The update already downloaded still installs.
    if (waiting) return;
    if (manual) set({ status: "error", error, checkedAt: Date.now() }, true);
    else set(before.status === "checking" ? { status: "idle" } : before, true);
  }
}

// restartToUpdate installs the downloaded update and relaunches Shipyard. Only
// ever from a click.
export async function restartToUpdate() {
  const state = useUpdater.getState();
  if (!pending || state.status !== "ready") return;
  useUpdater.setState({ status: "installing", version: state.version }, true);
  // One released since this one downloaded is the one to land on: fetched
  // now, unless the feed is slow to say (the one waiting installs then).
  const newer = await check({ timeout: 5_000 }).catch(() => null);
  if (newer && newer.version !== state.version) {
    useUpdater.setState({ status: "installing", version: newer.version }, true);
    try {
      await newer.download(undefined, { timeout: 10 * 60_000 });
      await pending.close().catch(() => {});
      pending = newer;
    } catch {
      await newer.close().catch(() => {});
      useUpdater.setState({ status: "installing", version: state.version }, true);
    }
  } else await newer?.close().catch(() => {});
  const update = pending;
  try {
    await update.install();
    await invoke("restart_app");
  } catch (e) {
    useUpdater.setState({ status: "error", error: e instanceof Error ? e.message : String(e), checkedAt: Date.now() }, true);
    pending = null;
  }
}

// The agent's restart as the app starts, for the status bar.
export const useAgentRestart = create<{ restarting: boolean }>(() => ({ restarting: false }));

// What `berth agent restart --if-stale --json` reports.
interface AgentRestart {
  restarted: boolean;
  reason?: string;
  from?: string;
  to?: string;
}

const LAST_VERSION = "berth.app-version";

// afterLaunch restarts an agent older than this app (an update replaced the
// app under it, or this app was installed over an older one), and says when
// Shipyard was updated: this version is not the one that ran last, or the
// agent was older.
async function afterLaunch() {
  const version = await getVersion().catch(() => "");
  let before: string | null = null;
  try {
    before = localStorage.getItem(LAST_VERSION);
    if (version) localStorage.setItem(LAST_VERSION, version);
  } catch {
    // No storage: the agent still says whether it was older.
  }
  useAgentRestart.setState({ restarting: true });
  let r: AgentRestart | undefined;
  try {
    r = JSON.parse(await invoke<string>("restart_stale_agent")) as AgentRestart;
  } catch (e) {
    console.warn("berth: restarting an older agent:", e);
  } finally {
    useAgentRestart.setState({ restarting: false });
  }
  if (r?.restarted) void useStore.getState().refreshAll();
  const updated = (!!before && !!version && before !== version) || !!r?.restarted;
  if (!updated || !version) return;
  toastManager.add({
    type: "success",
    title: `Updated to v${version}`,
    description: r?.restarted ? "The Shipyard agent restarted with it. Your agents kept running." : undefined,
  });
}

let started = false;

// startUpdater checks on launch and every few hours, in the packaged app
// only: a dev build would otherwise offer to replace itself with a release.
// A dev build leaves the agent alone too (it is often the one you run).
export function startUpdater() {
  if (started || !isTauri() || import.meta.env.DEV) return;
  started = true;
  void afterLaunch();
  // Let the window and the agent connection settle first.
  window.setTimeout(() => void checkForUpdate(), 10_000);
  window.setInterval(() => void checkForUpdate(), EVERY);
}
