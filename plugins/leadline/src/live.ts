import type { BerthPluginContext } from "@berth/plugin";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";

import { merge } from "./pages.ts";

// What every panel shares: the time window, and reading again while a panel
// is on screen. (The helper's state on a box is the app's to keep:
// @berth/plugin/ui's useHelperState.)
//
// The helper's event stream (/api/events) is server-sent events, which a
// request through the box can't carry, so a panel asks again on a timer
// instead: every few seconds for the default window, less often for a wide
// one, which is slow on the helper, and never while it is out of sight.

export interface Window {
  value: string;
  label: string;
  ms: number;
  // How often a panel on screen reads again.
  every: number;
}

export const WINDOWS: Window[] = [
  { value: "5m", label: "5m", ms: 5 * 60_000, every: 3000 },
  { value: "15m", label: "15m", ms: 15 * 60_000, every: 3000 },
  { value: "1h", label: "1h", ms: 60 * 60_000, every: 5000 },
  { value: "6h", label: "6h", ms: 6 * 3_600_000, every: 15_000 },
  { value: "24h", label: "24h", ms: 24 * 3_600_000, every: 30_000 },
];
// Fifteen minutes: every route answers that in a few milliseconds.
const DEFAULT_WINDOW = "15m";

// A value every open panel reads and any of them changes.
function shared<T>(initial: T) {
  let value = initial;
  const listeners = new Set<() => void>();
  return {
    get: () => value,
    set(next: T) {
      value = next;
      listeners.forEach((l) => l());
    },
    subscribe(l: () => void) {
      listeners.add(l);
      return () => void listeners.delete(l);
    },
  };
}

const chosen = shared<string | undefined>(undefined);

// useWindow is the time window, the same in every panel and kept between
// launches.
export function useWindow(berth: BerthPluginContext): [Window, (value: string) => void] {
  const value = useSyncExternalStore(chosen.subscribe, chosen.get) ?? berth.storage.get("window", DEFAULT_WINDOW);
  const set = useCallback(
    (v: string) => {
      berth.storage.set("window", v);
      chosen.set(v);
    },
    [berth],
  );
  return [WINDOWS.find((w) => w.value === value) ?? WINDOWS[1], set];
}

// What went wrong with a request, in the app's words. code is what to
// branch on ("helper_off"), hint what the helper suggests trying.
export interface Fault {
  message: string;
  code?: string;
  status?: number;
  hint?: string;
}

export function faultOf(err: unknown): Fault {
  const e = (err ?? {}) as { message?: unknown; code?: unknown; status?: unknown; detail?: { error?: { hint?: unknown } } };
  const hint = e.detail?.error && typeof e.detail.error === "object" ? e.detail.error.hint : undefined;
  return {
    message: typeof e.message === "string" && e.message ? e.message : String(err),
    code: typeof e.code === "string" ? e.code : undefined,
    status: typeof e.status === "number" ? e.status : undefined,
    hint: typeof hint === "string" ? hint : undefined,
  };
}

// useVisible says whether an element is on screen in a window that is
// shown: a panel in a tab behind another, or behind another worktree, is not.
export function useVisible<T extends Element>(): [(el: T | null) => void, boolean] {
  const [el, setEl] = useState<T | null>(null);
  const [inView, setInView] = useState(false);
  const [shown, setShown] = useState(() => !document.hidden);
  useEffect(() => {
    if (!el) return;
    const io = new IntersectionObserver((entries) => setInView(entries.some((e) => e.isIntersecting)));
    io.observe(el);
    return () => io.disconnect();
  }, [el]);
  useEffect(() => {
    const on = () => setShown(!document.hidden);
    document.addEventListener("visibilitychange", on);
    return () => document.removeEventListener("visibilitychange", on);
  }, []);
  return [setEl, inView && shown];
}

export interface Live<T> {
  data?: T;
  fault?: Fault;
  // The first read for this question has not come back yet.
  loading: boolean;
  refresh(): void;
}

// useLive reads with load and reads again every `every` ms while active,
// counting from when the last answer came. A new key is a new question: its
// answer replaces what is shown when it arrives, so a list does not blink
// to empty on every keystroke of a search.
export function useLive<T>(load: () => Promise<T>, key: string, opts: { every: number; active: boolean; onFault?(fault: Fault): void }): Live<T> {
  const [state, setState] = useState<{ key?: string; data?: T; fault?: Fault }>({});
  const [stamp, setStamp] = useState(0);
  const latest = useRef({ load, onFault: opts.onFault });
  latest.current = { load, onFault: opts.onFault };
  const { every, active } = opts;
  useEffect(() => {
    if (!active) return;
    let live = true;
    let timer = 0;
    const run = async () => {
      try {
        const data = await latest.current.load();
        if (live) setState({ key, data });
      } catch (err) {
        if (!live) return;
        const fault = faultOf(err);
        // What was read stays on screen; the fault is said beside it.
        setState((s) => (s.key === key ? { ...s, fault } : { key, fault }));
        latest.current.onFault?.(fault);
      }
      if (live) timer = window.setTimeout(() => void run(), every);
    };
    void run();
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [key, every, active, stamp]);
  const refresh = useCallback(() => setStamp((n) => n + 1), []);
  return { data: state.data, fault: state.fault, loading: state.key !== key && !state.fault, refresh };
}

// useDebounced is a search box's text once typing pauses.
export function useDebounced(value: string, wait = 200): string {
  const [out, setOut] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setOut(value), wait);
    return () => clearTimeout(t);
  }, [value, wait]);
  return out;
}

export interface Paged<R> {
  rows?: R[];
  fault?: Fault;
  // There may be older rows than the ones loaded.
  more: boolean;
  loadingMore: boolean;
  loadMore(): void;
}

// usePaged is a list ordered by time (traces, logs): its newest page read
// again while active, and older pages on request, by the last row's cursor.
export function usePaged<R>(
  page: (before?: string) => Promise<R[]>,
  key: string,
  opts: { size: number; every: number; active: boolean; id(r: R): string; cursor(r: R): string; time(r: R): number; sinceMs(): number; onFault?(fault: Fault): void },
): Paged<R> {
  const [state, setState] = useState<{ key?: string; rows?: R[]; more: boolean }>({ more: false });
  const [loadingMore, setLoadingMore] = useState(false);
  const latest = useRef({ page, opts, key });
  latest.current = { page, opts, key };
  const first = useLive(
    async () => {
      const fresh = await page();
      setState((s) => {
        const o = latest.current.opts;
        if (s.key !== key || !s.rows) return { key, rows: fresh, more: fresh.length >= o.size };
        const rows = merge(s.rows, fresh, o.id, o.time, o.sinceMs());
        // Starting again from one page: whether there is more is that page's to say.
        return { key, rows, more: rows === fresh ? fresh.length >= o.size : s.more };
      });
      return true;
    },
    key,
    opts,
  );
  const loadMore = useCallback(() => {
    const { rows } = state;
    if (!rows?.length || loadingMore) return;
    setLoadingMore(true);
    const asked = latest.current.key;
    latest.current
      .page(latest.current.opts.cursor(rows[rows.length - 1]))
      .then((older) => {
        if (latest.current.key !== asked) return;
        const o = latest.current.opts;
        setState((s) => {
          const known = new Set((s.rows ?? []).map(o.id));
          return { ...s, rows: [...(s.rows ?? []), ...older.filter((r) => !known.has(o.id(r)))], more: older.length >= o.size };
        });
      })
      .catch((err) => latest.current.opts.onFault?.(faultOf(err)))
      .finally(() => setLoadingMore(false));
  }, [state, loadingMore]);
  // Until a new question's first page arrives, the last one's rows stay.
  return { rows: state.rows, fault: first.fault, more: state.more, loadingMore, loadMore };
}

// One panel asks another to open something: a log line its trace, a query
// the endpoint that runs it. The panel is brought to the front (or opened)
// and picks the row up from here.
export interface Jump {
  panel: "queries" | "traces" | "endpoints";
  box: string;
  source: string;
  // What to open, as the panel names its rows.
  key: string;
}

const jump = shared<Jump | undefined>(undefined);

export function jumpTo(berth: BerthPluginContext, to: Jump) {
  jump.set(to);
  berth.openPanel(to.panel);
}

// useJump hands a panel the row another panel asked it to open, once.
export function useJump(panel: Jump["panel"], box: string, source: string, open: (key: string) => void) {
  const pending = useSyncExternalStore(jump.subscribe, jump.get);
  const latest = useRef(open);
  latest.current = open;
  useEffect(() => {
    if (!pending || pending.panel !== panel || pending.box !== box || pending.source !== source) return;
    jump.set(undefined);
    latest.current(pending.key);
  }, [pending, panel, box, source]);
}
