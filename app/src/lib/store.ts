import { create } from "zustand";

import { boxApi, type BoxInfo, type Client, type Location, type Service, type Session, type Stats, type Status, type TaskTemplate, type Theme } from "@/lib/api";
import { closeComposer, fromOrchestrateDraft, fromWorktreeDraft, openComposer } from "@/lib/composer";
import { errorMessage } from "@/lib/format";
import { withLocalTitles } from "@/lib/local-titles";
import { coalesce } from "@/lib/net";
import { share } from "@/lib/share";
import { load, save } from "@/lib/storage";

// The app's state. Everything here can be rebuilt from the agent at any
// time, which is what makes reconnecting safe: on every reconnect the store
// refetches, and in between events and a slow poll keep it current.

export interface BoxData {
  locations?: Location[];
  sessions?: Session[];
  stats?: Stats;
  services?: Service[];
  info?: BoxInfo;
  error?: string;
}

// The main area shows the current worktree's workspace, or one of the
// full-page views.
export type View =
  | { kind: "workspace" }
  | { kind: "dashboard" }
  // open, when set, opens the flow editor: a flow by id, or a new one there.
  | { kind: "automations"; open?: { box: string; scope: string; id?: string } }
  | { kind: "project"; box: string; location: string }
  | { kind: "kits" }
  // run, when set, opens Review's Compare of an attempts run.
  | { kind: "review"; run?: { box: string; id: string } }
  | { kind: "worktrees" }
  | { kind: "settings"; section?: string }
  | { kind: "plugin"; screen: string }
  // Team setup (lib/team): an org's <org>/.berth, read from GitHub, to set a
  // box up with. No org asks for one; from says how the page was reached.
  // update opens it on a newer commit's changes, to review and apply.
  // from "suggestion": the laptop noticed the org publishes one (lib/team-suggest).
  | { kind: "team"; org?: string; from?: "onboarding" | "addbox" | "link" | "sidebar" | "palette" | "suggestion"; box?: string; update?: boolean };

export interface Connection {
  state: "connecting" | "online" | "offline";
  error?: string;
}

// What ⌘N's composer opens with (lib/composer).
export interface WorktreeDraft {
  box?: string;
  location?: string;
  template?: string;
  // agent preselects an agent preset, such as "claude".
  agent?: string;
  // name pre-fills what to make, such as a search the palette had.
  name?: string;
}

// Orchestrating a session, in the composer (lib/composer): send it a
// prompt, hand its work to another agent, have another agent review it, or
// loop it until a check passes.
export interface OrchestrateDraft {
  kind: "send" | "handoff" | "review" | "loop";
  box: string;
  session: string;
  // A hand-off's prompt to start from.
  prompt?: string;
  // Where the session ran, for one that has ended.
  location?: string;
}

interface State {
  client?: Client;
  connection: Connection;
  status?: Status;
  boxes: Record<string, BoxData>;
  serverThemes: Theme[];
  themeId: string;
  templates: TaskTemplate[];
  view: View;
  paletteOpen: boolean;
  newTabMenuOpen: boolean;
  locationDraft?: { box?: string };
}

interface Actions {
  setClient(client: Client): void;
  setConnection(c: Connection): void;
  refreshStatus(): Promise<void>;
  refreshBox(box: string, parts?: BoxPart[]): Promise<void>;
  refreshAll(): Promise<void>;
  setView(view: View): void;
  setTheme(id: string): void;
  setPaletteOpen(open: boolean): void;
  setNewTabMenuOpen(open: boolean): void;
  openNewWorktree(draft?: WorktreeDraft): void;
  closeNewWorktree(): void;
  setOrchestrate(d?: OrchestrateDraft): void;
  openAddLocation(box?: string): void;
  // openAddProject is openAddLocation by the name the dialog has now.
  openAddProject(box?: string): void;
  closeAddLocation(): void;
}

export type BoxPart = "locations" | "sessions" | "stats" | "services" | "info";
const ALL_PARTS: BoxPart[] = ["locations", "sessions", "stats", "services", "info"];

const fetchers: Record<BoxPart, (c: Client, box: string) => Promise<unknown>> = {
  // A box too old to name worktrees has this laptop's names laid over.
  locations: async (c, box) => withLocalTitles(box, await boxApi.locations(c, box)),
  sessions: boxApi.sessions,
  stats: boxApi.stats,
  services: boxApi.services,
  info: boxApi.info,
};

// A stable empty list for selectors: returning a new [] each time would make
// every store update look like a change and re-render forever.
export const NONE: never[] = [];

const persisted = load<{ themeId?: string }>("berth.ui", {});

export const useStore = create<State & Actions>()((set, get) => ({
  connection: { state: "connecting" },
  boxes: {},
  serverThemes: [],
  // The live demo follows the visitor's light or dark, as the website does.
  themeId: persisted.themeId ?? (__BERTH_DEMO__ ? "system" : "berth-dark"),
  templates: [],
  view: { kind: "workspace" },
  paletteOpen: false,
  newTabMenuOpen: false,

  setClient: (client) => set({ client }),
  setConnection: (connection) => set({ connection }),

  async refreshStatus() {
    const { client } = get();
    if (!client) return;
    try {
      const status = share(get().status, await client.status());
      // Online and as it was: nothing to set, so nothing drawn again (a
      // refresh lands while a chat streams).
      const was = get();
      if (status === was.status && was.connection.state === "online" && !was.connection.error) return;
      set({ status, connection: was.connection.state === "online" && !was.connection.error ? was.connection : { state: "online" } });
    } catch (err) {
      set({ connection: { state: "offline", error: errorMessage(err) } });
    }
  },

  async refreshBox(box, parts = ALL_PARTS) {
    const { client } = get();
    if (!client) return;
    const results = await Promise.allSettled(parts.map((p) => fetchers[p](client, box)));
    set((s) => {
      const prev = s.boxes[box];
      const next: BoxData = { ...prev, error: undefined };
      results.forEach((r, i) => {
        // info is optional on older daemons; a failure there is not the box's.
        // What is as it was keeps its objects (lib/share.ts), so rows drawn
        // from them aren't drawn again.
        if (r.status === "fulfilled") (next as Record<string, unknown>)[parts[i]] = share(prev?.[parts[i]], r.value);
        else if (parts[i] !== "info") next.error = errorMessage(r.reason);
      });
      // Nothing new: nothing to draw.
      if (prev && prev.error === next.error && parts.every((p) => prev[p] === next[p])) return s;
      return { boxes: { ...s.boxes, [box]: next } };
    });
  },

  // One at a time: a reconnect, a focus, an event and the poll landing
  // together (as they do after a sleep) cost one refresh and at most one
  // more, never a pile of them (lib/net.ts).
  refreshAll: coalesce(async () => {
    const { client } = get();
    if (!client) return;
    await get().refreshStatus();
    const online = get().status?.boxes.filter((b) => b.state === "online") ?? [];
    await Promise.all([
      ...online.map((b) => get().refreshBox(b.name)),
      // Kept as they were when unchanged (lib/share.ts): a refresh landing
      // mid-stream re-rendered everything drawn from the theme.
      client.themes().then((t) => set((s) => (share(s.serverThemes, t) === s.serverThemes ? s : { serverThemes: t })), () => {}),
      client.templates().then((t) => set((s) => (share(s.templates, t) === s.templates ? s : { templates: t })), () => {}),
    ]);
  }),

  setView: (view) => set({ view }),

  setTheme: (themeId) => set({ themeId }),
  setPaletteOpen: (paletteOpen) => set({ paletteOpen }),
  setNewTabMenuOpen: (newTabMenuOpen) => set({ newTabMenuOpen }),
  // Both open the composer, the one way to start work.
  openNewWorktree: (draft = {}) => {
    set({ paletteOpen: false });
    openComposer(fromWorktreeDraft(draft));
  },
  closeNewWorktree: () => closeComposer(),
  setOrchestrate: (d) => {
    set({ paletteOpen: false });
    if (d) openComposer(fromOrchestrateDraft(d));
    else closeComposer();
  },
  openAddLocation: (box) => set({ locationDraft: { box }, paletteOpen: false }),
  openAddProject: (box) => set({ locationDraft: { box }, paletteOpen: false }),
  closeAddLocation: () => set({ locationDraft: undefined }),
}));

useStore.subscribe((s, prev) => {
  if (s.themeId !== prev.themeId) save("berth.ui", { themeId: s.themeId });
});

// Debounced per-box refreshes, so a burst of events costs one fetch.
const pending = new Map<string, { parts: Set<BoxPart>; timer: number }>();

export function scheduleRefresh(box: string, parts: BoxPart[]) {
  const p = pending.get(box) ?? { parts: new Set<BoxPart>(), timer: 0 };
  parts.forEach((x) => p.parts.add(x));
  window.clearTimeout(p.timer);
  p.timer = window.setTimeout(() => {
    pending.delete(box);
    void useStore.getState().refreshBox(box, [...p.parts]);
  }, 150);
  pending.set(box, p);
}

// For poking at state from the devtools console while developing.
if (import.meta.env.DEV) (window as unknown as Record<string, unknown>).__berthStore = useStore;
