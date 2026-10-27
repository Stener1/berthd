import { listen } from "@tauri-apps/api/event";
import { useEffect, useRef } from "react";
import { create } from "zustand";

import { agentBrowserDevtools } from "@/lib/agent-browser";
import { type AgentDevtools, appendConsole, appendNetwork, type ConsoleEntry, errorCount, fromAgent, type NetEntry, parseReport, type Report } from "@/lib/devtools-model";
import { poll, type Poller } from "@/lib/poll";
import { useStore } from "@/lib/store";

// A Browser tab's Console and Network drawer, per pane: what the page said
// (lib/devtools-model.ts) since it loaded, whether the drawer is open, and
// the feeds that fill it (useDevtoolsFeed). The badge on the tab and on the
// toolbar counts the page's errors since it loaded.

export type DevtoolsTab = "console" | "network";

export interface PaneLog {
  // The page's document, from its script's reports: a new one starts afresh.
  doc?: string;
  // From when the page's requests count: its navigation's start.
  since: number;
  console: ConsoleEntry[];
  network: NetEntry[];
  // Console entries the page logged faster than they could be kept.
  dropped: number;
  // Whether the page's console can be read at all (a script reported).
  heard: boolean;
}

interface State {
  logs: Record<string, PaneLog>;
  open: Record<string, boolean>;
  tab: Record<string, DevtoolsTab>;
}

export const useDevtools = create<State>(() => ({ logs: {}, open: {}, tab: {} }));

const empty = (since: number): PaneLog => ({ since, console: [], network: [], dropped: 0, heard: false });

function update(key: string, f: (l: PaneLog) => PaneLog) {
  // A log's first news may come just after the page started: its requests
  // from a moment before count, until its script says when it started.
  useDevtools.setState((s) => ({ logs: { ...s.logs, [key]: f(s.logs[key] ?? empty(Date.now() - 2000)) } }));
}

// ingestReport takes a report from the page's script: a new document starts
// a new log, keeping only requests made since it started.
export function ingestReport(key: string, r: Report) {
  update(key, (l) => {
    const fresh = l.doc !== r.doc;
    const since = fresh ? Math.min(r.t0 || Date.now(), Date.now()) : l.since;
    const base = fresh ? { ...l, doc: r.doc, since, console: [], dropped: 0, network: l.network.filter((n) => n.start >= since - 50) } : l;
    return { ...base, heard: true, console: appendConsole(base.console, r.entries), dropped: base.dropped + r.dropped };
  });
}

export function ingestNetwork(key: string, list: NetEntry[]) {
  if (!list.length) return;
  update(key, (l) => {
    const network = appendNetwork(l.network, list, l.since - 50);
    return network === l.network ? l : { ...l, network };
  });
}

// restart starts a pane's log afresh: a new page, as far as the app knows
// (a frame reloaded, the address changed).
export function restart(key: string, since = Date.now()) {
  useDevtools.setState((s) => ({ logs: { ...s.logs, [key]: empty(since) } }));
}

// clearLog empties one of the drawer's lists, as a console's clear does:
// what comes next still shows.
export function clearLog(key: string, what: DevtoolsTab) {
  update(key, (l) => (what === "console" ? { ...l, console: [], dropped: 0 } : { ...l, network: [], since: Date.now() }));
}

export function dropLog(key: string) {
  useDevtools.setState((s) => {
    const { [key]: _gone, ...logs } = s.logs;
    return { logs };
  });
}

export function setAgentLog(key: string, d: AgentDevtools) {
  const { console, network } = fromAgent(d);
  useDevtools.setState((s) => ({ logs: { ...s.logs, [key]: { since: 0, console, network, dropped: 0, heard: d.running } } }));
}

export function toggleDrawer(key: string, open?: boolean) {
  useDevtools.setState((s) => ({ open: { ...s.open, [key]: open ?? !s.open[key] } }));
}

export function setDrawerTab(key: string, tab: DevtoolsTab) {
  useDevtools.setState((s) => ({ tab: { ...s.tab, [key]: tab }, open: { ...s.open, [key]: true } }));
}

export const useDrawerOpen = (key: string) => useDevtools((s) => !!s.open[key]);
export const useDrawerTab = (key: string) => useDevtools((s) => s.tab[key] ?? "console");
export const useLog = (key: string): PaneLog | undefined => useDevtools((s) => s.logs[key]);
export const useErrorCount = (key: string) => useDevtools((s) => (s.logs[key] ? errorCount(s.logs[key].console, s.logs[key].network) : 0));
// The badge on a tab: its Browser panes' errors.
export const useErrorCountOf = (keys: string[]) =>
  useDevtools((s) => keys.reduce((n, k) => n + (s.logs[k] ? errorCount(s.logs[k].console, s.logs[k].network) : 0), 0));

// paneKey is the key a Browser pane's log is kept under: its pane id as
// browser-pane.tsx cleans it for its native view's label.
export const paneKey = (paneId: string) => paneId.replace(/[^A-Za-z0-9_-]/g, "");

// proxiedHost is a page's host when the laptop's proxy relays it (a
// worktree's page, http://<name>.localhost:<proxy port>/), whose requests
// the Network drawer can list; else undefined.
export function proxiedHost(url: string, proxyPort?: number): string | undefined {
  try {
    const u = new URL(url);
    const port = u.port ? Number(u.port) : u.protocol === "https:" ? 443 : 80;
    if (!u.hostname.endsWith(".localhost") || port !== (proxyPort ?? 1377)) return undefined;
    return u.hostname;
  } catch {
    return undefined;
  }
}

export interface FeedOptions {
  key: string;
  mode: "native" | "iframe";
  url: string;
  visible: boolean;
  proxyPort?: number;
}

// useDevtoolsFeed fills a pane's log while it shows a page: the page's
// console reports (from the native view's watcher as events, or posted by
// the frame) and the proxy's requests for its host, every second while the
// pane is on screen, every few seconds while not (for the tab's badge).
export function useDevtoolsFeed({ key, mode, url, visible, proxyPort }: FeedOptions) {
  // The native view's reports.
  useEffect(() => {
    if (mode !== "native") return;
    let stop: (() => void) | undefined;
    let gone = false;
    void listen<{ id: string; data: string }>("berth://browser-console", (e) => {
      if (e.payload.id !== key) return;
      const r = parseReport(e.payload.data);
      if (r) ingestReport(key, r);
    }).then((un) => (gone ? un() : (stop = un)));
    return () => {
      gone = true;
      stop?.();
    };
  }, [key, mode]);

  // A frame's new address is a new page, whether or not it reports (a page
  // outside the proxy has no script).
  useEffect(() => {
    if (mode === "iframe") restart(key);
  }, [key, mode, url]);

  // A frame's reports, from the frame the pane draws (data-pane) only.
  useEffect(() => {
    if (mode !== "iframe") return;
    const onMessage = (e: MessageEvent) => {
      const d = e.data as { berth?: unknown; id?: unknown } | null;
      if (!d || d.berth !== "devtools" || d.id !== key) return;
      const frame = document.querySelector<HTMLIFrameElement>(`iframe[data-pane="${key}"]`);
      if (!frame || e.source !== frame.contentWindow) return;
      const r = parseReport(d);
      if (r) ingestReport(key, r);
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [key, mode]);

  // The proxy's log for the page's host: every second while the pane shows
  // and the page asks for things, backing off to 8s while it doesn't (30s
  // behind another tab, for the tab's badge), and never while the window is
  // hidden. Anything a shown page reports (a console line, a load) looks
  // again at once: its requests come with it.
  const host = url ? proxiedHost(url, proxyPort) : undefined;
  const heard = useDevtools((s) => {
    const c = s.logs[key]?.console;
    return c?.length ? `${c.length}:${c[c.length - 1].time}:${c[c.length - 1].count}` : "";
  });
  const poller = useRef<Poller>(null);
  useEffect(() => {
    if (!host) return;
    let after = 0;
    const p = poll(
      async () => {
        const client = useStore.getState().client;
        if (!client) return;
        try {
          const r = await client.laptop<{ requests: NetEntry[] | null; last: number }>("GET", `/v1/proxy/requests?host=${encodeURIComponent(host)}&after=${after}`);
          if (p !== poller.current) return;
          const fresh = (r.requests ?? []).some((n) => n.seq > after);
          ingestNetwork(key, r.requests ?? []);
          after = Math.max(after, r.last ?? 0);
          return fresh;
        } catch {
          // An agent that doesn't keep the log yet (older), or is away.
          return false;
        }
      },
      visible ? { every: 1000, max: 8000 } : { every: 4000, max: 30_000 },
    );
    poller.current = p;
    return () => {
      p.stop();
      if (poller.current === p) poller.current = null;
    };
  }, [key, host, visible]);
  useEffect(() => {
    if (heard && visible) poller.current?.kick();
  }, [heard, visible]);

  // Gone with the pane.
  useEffect(() => () => dropLog(key), [key]);
}

// useAgentDevtoolsFeed keeps the agent's own browser's console lines and
// failed requests (from its box, GET …/browser/devtools) under key, every
// two seconds while on: while its view shows and the drawer is open. tab is
// the one the view watches (lib/browser-tabs), main when empty.
export function useAgentDevtoolsFeed(key: string, ref: { box: string; location: string; worktree: string } | undefined, on: boolean, tab = "") {
  const box = ref?.box;
  const location = ref?.location;
  const worktree = ref?.worktree;
  const capable = useStore((s) => !!box && !!s.boxes[box]?.info?.capabilities?.includes("browser.devtools"));
  useEffect(() => {
    if (!on || !capable || !box || !location || !worktree) return;
    let live = true;
    let last = "";
    // Every 2s while it changes, up to 8s while it doesn't; never while
    // the window is hidden.
    const p = poll(
      async () => {
        try {
          const d = await agentBrowserDevtools<AgentDevtools>(box, location, worktree, tab);
          if (!live || !d) return false;
          const sig = JSON.stringify(d);
          if (sig === last) return false;
          last = sig;
          setAgentLog(key, d);
          return true;
        } catch {
          // The box is away; the drawer keeps what it had.
          return false;
        }
      },
      { every: 2000, max: 8000 },
    );
    return () => {
      live = false;
      p.stop();
    };
  }, [key, on, capable, box, location, worktree, tab]);
  return capable;
}

// A Browser tab's frame asks the proxy for its page with the console script
// (internal/proxy/devtools.go): the flag on its address, its pane in its
// name.
export const DEVTOOLS_FRAME = "berth-devtools:";
export function withDevtoolsFlag(url: string): string {
  try {
    const u = new URL(url);
    u.searchParams.set("__berth_devtools", "1");
    return u.toString();
  } catch {
    return url;
  }
}
