import { useEffect, useState } from "react";

import type { BrowserHealth } from "@/lib/browser-sandbox";
import { type AgentBrowserTab, tabQuery } from "@/lib/browser-tabs";
import { useStore } from "@/lib/store";

// The agent's browser on a box: a headless Chromium per worktree that the
// agent drives (berthd browser …). The app only watches it: its status, a
// live screencast while a pane shows it, and the shots it left for Review.
// On a box with "browser.tabs" each agent may have a tab of its own in it
// (lib/browser-tabs); every route takes the tab, and none is the main one.

export type { AgentBrowserTab };

// The agent's browser's page size: CSS pixels at a device scale, which the
// agent chooses (berthd browser resize); 1920×1080 at 1x by default.
export interface Viewport {
  width: number;
  height: number;
  scale: number;
}

export interface AgentBrowserStatus {
  running: boolean;
  text: string;
  status?: { url?: string; rss_bytes?: number; watchers?: number; viewport?: Viewport; size?: string; tabs?: AgentBrowserTab[] };
  // The size it has, or opens at ("1920×1080", "390×844 @3x").
  viewport?: Viewport;
  size?: string;
  // Its tabs, main first then by when they opened; absent on a box without
  // tabs.
  tabs?: AgentBrowserTab[];
  // Why one can't start, while none runs and it couldn't.
  health?: BrowserHealth;
}

export interface BrowserArtifacts {
  url?: string;
  shots?: string[];
  errors?: string[];
}

export const boxHasBrowser = (box: string) => !!useStore.getState().boxes[box]?.info?.capabilities?.includes("browser");

// A box that says why its browser can't start, and takes the no-sandbox
// setting.
export const boxHasBrowserHealth = (box: string) => !!useStore.getState().boxes[box]?.info?.capabilities?.includes("browser.health");

function client() {
  const c = useStore.getState().client;
  if (!c) throw new Error("not connected");
  return c;
}

// browserHealth is whether the box's browser can start, without starting it.
export const browserHealth = async (box: string) => client().box<BrowserHealth>(box, "GET", "browser/health");

// setBrowserNoSandbox turns the box's no-sandbox setting on or off; the next
// browser to start follows it.
export const setBrowserNoSandbox = async (box: string, on: boolean) => client().box<BrowserHealth>(box, "PUT", "browser/settings", { no_sandbox: on });

// checkBrowser starts Chromium once on the box, on a blank page, and closes
// it: whether a browser starts now.
export const checkBrowser = async (box: string) => client().box<BrowserHealth>(box, "POST", "browser/check");

const base = (location: string, worktree: string) => `worktrees/${encodeURIComponent(location)}/${encodeURIComponent(worktree)}/browser`;

export function agentBrowserStatus(box: string, location: string, worktree: string): Promise<AgentBrowserStatus> {
  const c = useStore.getState().client;
  if (!c) return Promise.reject(new Error("not connected"));
  return c.box<AgentBrowserStatus>(box, "GET", `${base(location, worktree)}/status`);
}

// A frame of the agent's page: w×h CSS pixels, its image at the page's
// scale (up to w·scale wide), so it shows sharp at up to w×h.
export interface Frame {
  data: string;
  mime?: string;
  w: number;
  h: number;
  scale?: number;
  url?: string;
}

// sizeLabel is a size as the box says it: 1920×1080, or 390×844 @3x.
export const sizeLabel = (w: number, h: number, scale?: number) => `${w}×${h}${scale && scale !== 1 ? ` @${scale}x` : ""}`;

// fitFrame is how big a w×h page shows in a space: as large as fits, but
// never past its own size, which would blur it.
export function fitFrame(w: number, h: number, availW: number, availH: number): { w: number; h: number; zoom: number } {
  if (w <= 0 || h <= 0 || availW <= 0 || availH <= 0) return { w: 0, h: 0, zoom: 0 };
  const zoom = Math.min(1, availW / w, availH / h);
  return { w: Math.floor(w * zoom), h: Math.floor(h * zoom), zoom };
}

// watchAgentBrowser streams the page as JPEG frames while signal is live:
// the box casts only while someone watches, and only the tab asked for.
export function watchAgentBrowser(box: string, location: string, worktree: string, onFrame: (f: Frame) => void, signal: AbortSignal, tab?: string): Promise<void> {
  const c = useStore.getState().client;
  if (!c) return Promise.reject(new Error("not connected"));
  return c.stream("GET", `/v1/boxes/${encodeURIComponent(box)}/api/${base(location, worktree)}/screencast${tabQuery(tab)}`, undefined, (v) => onFrame(v as Frame), signal);
}

// agentBrowserDevtools is a tab's console lines and failed requests, as its
// box keeps them.
export function agentBrowserDevtools<T>(box: string, location: string, worktree: string, tab?: string): Promise<T> {
  return client().box<T>(box, "GET", `${base(location, worktree)}/devtools${tabQuery(tab)}`);
}

const shotCache = new Map<string, Promise<string>>();

// useShotUrl is an object URL for a shot the agent's browser saved.
export function useShotUrl(box: string, location: string, worktree: string, name: string): string | undefined {
  const [url, setUrl] = useState<string>();
  useEffect(() => {
    const c = useStore.getState().client;
    if (!c) return;
    const key = `${box}/${location}/${worktree}/${name}`;
    let p = shotCache.get(key);
    if (!p) {
      p = c.boxBlob(box, `${base(location, worktree)}/shots/${encodeURIComponent(name)}`).then((b) => URL.createObjectURL(b));
      shotCache.set(key, p);
      // A small cache: shots are kept 20 per worktree on the box.
      if (shotCache.size > 60) shotCache.delete(shotCache.keys().next().value!);
    }
    let live = true;
    p.then((u) => live && setUrl(u)).catch(() => shotCache.delete(key));
    return () => {
      live = false;
    };
  }, [box, location, worktree, name]);
  return url;
}
