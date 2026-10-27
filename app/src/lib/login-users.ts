import { useEffect } from "react";
import { create } from "zustand";

import { useEventLog } from "@/lib/events";
import { flowsApi, type LoginConfig } from "@/lib/flows";
import { hostSuffix, worktreeHost } from "@/lib/browser-url";
import { loginUrl, loginUserLabel } from "@/lib/login-url";
import { useStore } from "@/lib/store";

// The users a project's worktrees can be logged in as: its effective
// config's "login" (from the kit, the box's own config, or the committed
// config once trusted). Kept per box and location, read once and again
// when the box says its config changed.

const useLogins = create<{ by: Record<string, LoginConfig | null> }>()(() => ({ by: {} }));
const loading = new Set<string>();

const keyOf = (box: string, location: string) => `${box}/${location}`;

async function load(box: string, location: string) {
  const key = keyOf(box, location);
  const client = useStore.getState().client;
  if (!client || loading.has(key)) return;
  loading.add(key);
  try {
    const c = await flowsApi.config(client, box, location);
    const login = c.effective?.login;
    useLogins.setState((s) => ({ by: { ...s.by, [key]: login?.users?.length || login?.any ? login : null } }));
  } catch {
    useLogins.setState((s) => ({ by: { ...s.by, [key]: s.by[key] ?? null } }));
  } finally {
    loading.delete(key);
  }
}

// useLoginConfig is a project's login, or undefined while it loads or when
// it has none.
export function useLoginConfig(box?: string, location?: string): LoginConfig | undefined {
  const key = box && location ? keyOf(box, location) : "";
  const known = useLogins((s) => (key ? s.by[key] : undefined));
  const hasClient = useStore((s) => !!s.client);
  const changed = useEventLog((s) => (key ? s.events.find((e) => e.type === "config.changed" && e.box === box && (!e.data?.location || e.data.location === location)) : undefined));
  useEffect(() => {
    if (box && location && hasClient) void load(box, location);
  }, [box, location, hasClient, changed]);
  return known ?? undefined;
}

export interface WorktreeRefLike {
  box: string;
  location: string;
  worktree: string;
  main?: boolean;
}

// worktreeOrigin is a worktree's private URL's origin, the host the login
// route answers on, or undefined when its names can't be a host.
export function worktreeOrigin(ref: WorktreeRefLike, urlPort?: number): string | undefined {
  const host = worktreeHost({ ...ref, path: "" });
  return host ? `http://${host}${hostSuffix(urlPort)}` : undefined;
}

export { loginUrl, loginUserLabel };
