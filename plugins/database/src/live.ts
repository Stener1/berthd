import { useCallback, useEffect, useRef, useState } from "react";

import { type Fault, faultOf } from "./api";

// Reading from the helper while the panel is on screen.

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

export interface Read<T> {
  data?: T;
  fault?: Fault;
  // An answer for this question has not come back yet.
  loading: boolean;
  // data is this question's answer, not the last one's kept meanwhile.
  fresh: boolean;
  reload(): void;
}

// useRead reads with load when key changes, on reload, and every `every` ms
// when given one, while active. A new key is a new question: its answer
// replaces what is shown when it arrives, so a grid does not blink to empty
// between pages; what was read stays beside a fault.
export function useRead<T>(load: () => Promise<T>, key: string, opts: { active: boolean; every?: number }): Read<T> {
  const [state, setState] = useState<{ key?: string; data?: T; fault?: Fault }>({});
  const [stamp, setStamp] = useState(0);
  const [busy, setBusy] = useState(false);
  const latest = useRef(load);
  latest.current = load;
  const { active, every } = opts;
  useEffect(() => {
    if (!active) return;
    let live = true;
    let timer = 0;
    const run = async () => {
      setBusy(true);
      try {
        const data = await latest.current();
        if (live) setState({ key, data });
      } catch (err) {
        if (live) setState((s) => (s.key === key ? { ...s, fault: faultOf(err) } : { key, fault: faultOf(err) }));
      }
      if (!live) return;
      setBusy(false);
      if (every) timer = window.setTimeout(() => void run(), every);
    };
    void run();
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [key, active, every, stamp]);
  const reload = useCallback(() => setStamp((n) => n + 1), []);
  return { data: state.data, fault: state.key === key ? state.fault : undefined, loading: busy || (state.key !== key && active), fresh: state.key === key, reload };
}

// useNow is the time, read again every `every` ms while on: for a countdown.
export function useNow(on: boolean, every = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!on) return;
    setNow(Date.now());
    const t = setInterval(() => setNow(Date.now()), every);
    return () => clearInterval(t);
  }, [on, every]);
  return now;
}
