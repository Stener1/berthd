import type { TurnCheckChange, TurnCheckProvider, TurnCheckStatus } from "@/lib/turncheck";

// Turn check in demo mode: off on every box until turned on, with a key
// kept per provider, as on a box.

const state: Record<string, { cfg: TurnCheckStatus; keys: Partial<Record<TurnCheckProvider, boolean>> }> = {};

export function turnCheckCall(box: string, method: string, path: string, body: unknown, delay: <T>(v: T) => Promise<T>): Promise<unknown> | undefined {
  if (path !== "turncheck") return undefined;
  const st = (state[box] ??= { cfg: { enabled: false, key_set: false }, keys: {} });
  if (method === "PUT") {
    const { key_value, remove_key, ...cfg } = body as TurnCheckChange;
    const p = cfg.provider;
    // The last one set wins, as on a box, and a reference stays with its provider.
    const key = key_value || (p !== st.cfg.provider && cfg.key === st.cfg.key) ? undefined : cfg.key;
    if (p && (key_value || remove_key || key)) st.keys[p] = !!key_value;
    st.cfg = { ...cfg, key, key_set: false };
  }
  if (method === "GET" || method === "PUT") return delay({ ...st.cfg, key_set: !!(st.cfg.provider && st.keys[st.cfg.provider]) });
  return undefined;
}
