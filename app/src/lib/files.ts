import { create } from "zustand";

import { ApiError, type Client } from "@/lib/api";
import { openEditor } from "@/components/editors/open";
import { leaves, paneWorktree } from "@/lib/layout";
import { load, save as store } from "@/lib/storage";
import { useStore } from "@/lib/store";
import { activateTab, focusPane, here, openFor, refFor, showWorktree, useWorkspaces, type WorktreeRef } from "@/lib/workspaces";

// The ⌘P picker and File tabs: a worktree's files, read and written on its
// box (internal/box/worktreefiles.go). A File tab knows what the agent
// changed in its file this turn, reloads in place when the agent writes it
// while you have no unsaved edits, and stops to ask when it writes it under
// yours. A save names the version it replaces (its etag), so it can never
// overwrite the agent's write unseen.

// WorktreeFile is the box's answer for one file (GET …/file).
export interface WorktreeFile {
  path: string;
  etag: string;
  mtime: number;
  size: number;
  content?: string;
  binary?: boolean;
  image?: string;
  too_large?: boolean;
  reason?: string;
  deleted?: boolean;
  turn?: FileTurn;
}

// TouchedFile is a file an agent changed in its latest turn (GET …/touched).
export interface TouchedFile {
  path: string;
  added: number;
  removed: number;
  created?: boolean;
  deleted?: boolean;
  at?: number;
  // The agent wrote it in the last 20s and its session is working: it is
  // likely writing it now.
  live?: boolean;
  session: string;
  agent: string;
  // What the counts are from: the file as the turn found it, or the last
  // commit when the agent's record doesn't keep it.
  base: "turn" | "head";
}

// WorktreeFolder is one folder's children (GET …/files?dir=): a folder's
// children says it has something to open (a submodule's doesn't).
export interface WorktreeFolder {
  dir: string;
  entries: { name: string; dir?: boolean; children?: boolean }[] | null;
  truncated?: boolean;
}

export interface FileTurn extends TouchedFile {
  // The file as the turn found it; null when the turn made it.
  before: string | null;
}

const enc = encodeURIComponent;
const base = (ref: WorktreeRef) => `locations/${enc(ref.location)}/worktrees/${enc(ref.worktree)}`;

export const filesApi = {
  list: (c: Client, ref: WorktreeRef, q: string, limit = 60, signal?: AbortSignal) =>
    c.box<{ files: string[] | null; truncated?: boolean }>(ref.box, "GET", `${base(ref)}/files?q=${enc(q)}&limit=${limit}`, undefined, signal),
  read: (c: Client, ref: WorktreeRef, path: string, opts: { turn?: boolean; stat?: boolean } = {}) =>
    c.box<WorktreeFile>(ref.box, "GET", `${base(ref)}/file?path=${enc(path)}${opts.turn ? "&turn=1" : ""}${opts.stat ? "&stat=1" : ""}`),
  // write replaces the version etag names, or with none makes a new file.
  write: (c: Client, ref: WorktreeRef, path: string, content: string, etag: string | undefined) =>
    c.box<WorktreeFile>(ref.box, "PUT", `${base(ref)}/file?path=${enc(path)}`, { content }, undefined, etag ? { "If-Match": `"${etag}"` } : { "If-None-Match": "*" }),
  // One folder's children, folders first ("" is the top), for the Files
  // panel (internal/box/worktreefolder.go).
  dir: (c: Client, ref: WorktreeRef, dir: string, signal?: AbortSignal) =>
    c.box<WorktreeFolder>(ref.box, "GET", `${base(ref)}/files?dir=${enc(dir)}`, undefined, signal),
  touched: (c: Client, ref: WorktreeRef) => c.box<{ files: TouchedFile[] | null }>(ref.box, "GET", `${base(ref)}/touched`),
  image: (c: Client, ref: WorktreeRef, path: string) => c.boxBlob(ref.box, `${base(ref)}/file?path=${enc(path)}&raw=1`),
};

const client = () => useStore.getState().client;

// ---- Documents ----

export interface Doc {
  key: string;
  ref: WorktreeRef;
  path: string;
  state: "loading" | "ready" | "error" | "gone";
  error?: string;
  etag?: string;
  mtime?: number;
  size?: number;
  // A picture, another binary file, or one too large: no text.
  binary?: boolean;
  image?: string;
  tooLarge?: boolean;
  reason?: string;
  // The agent's latest version, as read from the box: its marks are
  // against it, so what you saved yourself isn't counted as the agent's.
  agentText: string;
  // What your edits are against (what you opened, saved or reloaded).
  base: string;
  // The editor's text.
  text: string;
  // What the agent's latest turn did to the file.
  turn?: FileTurn;
  // The agent wrote (or deleted) the file under your unsaved edits.
  conflict?: { content?: string; etag?: string; deleted?: boolean };
  comparing?: boolean;
  saving?: boolean;
  saveError?: string;
  savedAt?: number;
  // The file was deleted and you kept yours: the next save makes it again.
  create?: boolean;
}

export const docKey = (ws: string, path: string) => `${ws}\0${path}`;
export const isDirty = (d: Doc) => d.state === "ready" && (d.text !== d.base || !!d.create);

interface State {
  docs: Record<string, Doc>;
  pickerOpen: boolean;
  // Files opened lately, by worktree key, newest first.
  recent: Record<string, string[]>;
  // What the agents touched this turn, by worktree key.
  touched: Record<string, { files: TouchedFile[]; at: number } | undefined>;
  // Long lines wrap in File tabs (else they scroll sideways). Unset, they
  // wrap only in a narrow pane, where a long line would hide past its edge.
  wrap?: boolean;
  // A line to show once its File tab is up (a path named in a chat): the
  // tab takes it, and it goes.
  goto?: Goto;
  // How a Markdown File tab shows its file: the rendered page, the source
  // in the editor, or both side by side.
  mdView: MdView;
}

export interface Goto {
  key: string;
  line: number;
  end?: number;
}

export type MdView = "preview" | "source" | "split";

const RECENT_KEY = "berth.files.recent";
const WRAP_KEY = "berth.files.wrap";
const MD_VIEW_KEY = "berth.files.mdView";

export const useFiles = create<State>()(() => ({ docs: {}, pickerOpen: false, recent: load<Record<string, string[]>>(RECENT_KEY, {}), touched: {}, wrap: load<boolean | undefined>(WRAP_KEY, undefined), mdView: load<MdView>(MD_VIEW_KEY, "preview") }));

export function setWrap(wrap: boolean) {
  store(WRAP_KEY, wrap);
  useFiles.setState({ wrap });
}

export function setMdView(mdView: MdView) {
  store(MD_VIEW_KEY, mdView);
  useFiles.setState({ mdView });
}

// Mock mode starts with a few files "opened before" in checkout-fix.
if (typeof window !== "undefined" && new URLSearchParams(location.search).has("mock") && !Object.keys(useFiles.getState().recent).length) {
  void import("@/lib/mock-files").then((m) => useFiles.setState((s) => (Object.keys(s.recent).length ? s : { recent: m.MOCK_RECENT })));
}

export const setPickerOpen = (open: boolean) => useFiles.setState({ pickerOpen: open });

const patch = (key: string, fn: (d: Doc) => Partial<Doc> | undefined) =>
  useFiles.setState((s) => {
    const d = s.docs[key];
    const p = d && fn(d);
    return d && p ? { docs: { ...s.docs, [key]: { ...d, ...p } } } : s;
  });

// fromFile is what a read says about the file, for its document.
function fromFile(f: WorktreeFile): Partial<Doc> {
  return { etag: f.etag, mtime: f.mtime, size: f.size, binary: !!f.binary, image: f.image, tooLarge: !!f.too_large, reason: f.reason, ...(f.turn !== undefined ? { turn: f.turn } : {}) };
}

const message = (err: unknown) => (err instanceof Error ? err.message : String(err));

// openDoc reads a file into its document, once: a File tab, or the
// picker's preview. Read again only when it failed.
export async function openDoc(ws: string, ref: WorktreeRef, path: string): Promise<void> {
  const key = docKey(ws, path);
  const have = useFiles.getState().docs[key];
  if (have && have.state !== "error") return;
  useFiles.setState((s) => ({ docs: { ...s.docs, [key]: { key, ref, path, state: "loading", agentText: "", base: "", text: "" } } }));
  const c = client();
  if (!c) return;
  try {
    const f = await filesApi.read(c, ref, path, { turn: true });
    const text = f.content ?? "";
    patch(key, () => ({ state: "ready", error: undefined, ...fromFile(f), turn: f.turn, agentText: text, base: text, text }));
  } catch (err) {
    patch(key, () => (err instanceof ApiError && err.status === 404 ? { state: "gone", error: message(err) } : { state: "error", error: message(err) }));
  }
}

export const edit = (key: string, text: string) => patch(key, (d) => (d.state === "ready" && d.text !== text ? { text, saveError: undefined } : undefined));

// save writes your text (⌘S) against the version you have. The agent's
// write in between is refused by the box (412) and becomes a conflict.
export async function save(key: string): Promise<void> {
  const d = useFiles.getState().docs[key];
  const c = client();
  if (!d || !c || d.state !== "ready" || d.binary || d.tooLarge || d.saving || d.conflict || !isDirty(d)) return;
  const text = d.text;
  patch(key, () => ({ saving: true, saveError: undefined }));
  try {
    const f = await filesApi.write(c, d.ref, d.path, text, d.create ? undefined : d.etag);
    patch(key, () => ({ saving: false, etag: f.etag, mtime: f.mtime, size: f.size, base: text, create: false, savedAt: Date.now() }));
  } catch (err) {
    const detail = err instanceof ApiError && err.status === 412 ? (err.detail as Partial<WorktreeFile> | undefined) : undefined;
    if (detail) {
      patch(key, () => ({ saving: false, conflict: { content: detail.content, etag: detail.etag, deleted: !!detail.deleted } }));
      return;
    }
    patch(key, () => ({ saving: false, saveError: message(err) }));
  }
}

// poll asks the box whether the file changed (its etag): read again in
// place when you have no unsaved edits, a conflict when you do.
export async function poll(key: string, withTurn = false): Promise<void> {
  const d = useFiles.getState().docs[key];
  const c = client();
  if (!d || !c || (d.state !== "ready" && d.state !== "gone") || d.saving) return;
  let stat: WorktreeFile;
  try {
    stat = await filesApi.read(c, d.ref, d.path, { stat: true, turn: withTurn });
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) gone(key);
    return;
  }
  const now = useFiles.getState().docs[key];
  if (!now || now.saving) return;
  if (stat.etag === now.etag && now.state === "ready") {
    if (withTurn) patch(key, () => ({ turn: stat.turn }));
    return;
  }
  if (now.conflict && !now.conflict.deleted && stat.etag === now.conflict.etag) return;
  let f: WorktreeFile;
  try {
    f = await filesApi.read(c, d.ref, d.path, { turn: true });
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) gone(key);
    return;
  }
  patch(key, (cur) => {
    if (cur.saving) return undefined;
    const text = f.content ?? "";
    if (cur.state === "gone" || !isDirty(cur) || cur.binary || cur.tooLarge || f.binary || f.too_large)
      return { state: "ready", error: undefined, ...fromFile(f), turn: f.turn, agentText: text, base: text, text, conflict: undefined, comparing: false, create: false };
    return { turn: f.turn, conflict: { content: text, etag: f.etag } };
  });
}

// gone: the file was deleted. With unsaved edits, that is a conflict too.
function gone(key: string) {
  patch(key, (d) => (isDirty(d) ? (d.conflict?.deleted ? undefined : { conflict: { deleted: true } }) : d.state === "gone" ? undefined : { state: "gone", conflict: undefined, comparing: false }));
}

// reload takes the agent's version: your unsaved edits go.
export function reload(key: string) {
  patch(key, (d) => {
    if (!d.conflict) return { text: d.base };
    if (d.conflict.deleted) return { state: "gone", text: d.base, conflict: undefined, comparing: false };
    const t = d.conflict.content ?? "";
    return { text: t, base: t, agentText: t, etag: d.conflict.etag, conflict: undefined, comparing: false };
  });
}

// keepMine keeps your text over the agent's version, to save over it
// (or, if it deleted the file, to make it again).
export function keepMine(key: string) {
  patch(key, (d) => {
    if (!d.conflict) return undefined;
    if (d.conflict.deleted) return { create: true, etag: undefined, conflict: undefined, comparing: false };
    const t = d.conflict.content ?? "";
    return { base: t, agentText: t, etag: d.conflict.etag, conflict: undefined, comparing: false };
  });
}

export const setComparing = (key: string, comparing: boolean) => patch(key, () => ({ comparing }));

// forgetDoc drops a document nothing shows any more (its tab closed) when
// it holds nothing unsaved.
export function forgetDoc(key: string) {
  useFiles.setState((s) => {
    const d = s.docs[key];
    if (!d || isDirty(d)) return s;
    const docs = { ...s.docs };
    delete docs[key];
    return { docs };
  });
}

// ---- What the agents touched ----

export async function loadTouched(ws: string, ref: WorktreeRef): Promise<void> {
  const c = client();
  if (!c) return;
  try {
    const r = await filesApi.touched(c, ref);
    const files = r.files ?? [];
    // The same answer as last time changes nothing, so nothing draws again.
    const was = useFiles.getState().touched[ws];
    if (was && JSON.stringify(was.files) === JSON.stringify(files)) return;
    useFiles.setState((s) => ({ touched: { ...s.touched, [ws]: { files, at: Date.now() } } }));
  } catch {
    // An older box, or none reachable: the picker lists recents only.
    useFiles.setState((s) => ({ touched: { ...s.touched, [ws]: { files: [], at: Date.now() } } }));
  }
}

// ---- Opening ----

export type How = "tab" | "split" | "external";

function remember(ws: string, path: string) {
  useFiles.setState((s) => {
    const recent = { ...s.recent, [ws]: [path, ...(s.recent[ws] ?? []).filter((p) => p !== path)].slice(0, 12) };
    store(RECENT_KEY, recent);
    return { recent };
  });
}

// openFile opens a file of the worktree you are acting in (here): as a File
// tab (↵), beside the focused pane (⌥↵), or in your editor (⌘↵). A file
// already open in a tab is brought to the front instead. Given a line (and
// the last of a range), the tab shows it.
export function openFile(path: string, how: How = "tab", line?: number, end?: number) {
  const ws = here();
  const ref = refFor(ws);
  if (!ws || !ref) return;
  remember(ws, path);
  if (how === "external") {
    void openEditor({ box: ref.box, path: ref.path, file: path, line });
    return;
  }
  if (line) useFiles.setState({ goto: { key: docKey(ws, path), line, end } });
  if (how === "tab") {
    const s = useWorkspaces.getState();
    for (const key of new Set([ws, s.current ?? ws])) {
      for (const t of s.spaces[key]?.tabs ?? []) {
        const l = leaves(t.root).find((x) => x.content.kind === "file" && x.content.path === path && paneWorktree(key, x) === ws);
        if (!l) continue;
        if (key !== s.current) showWorktree(key);
        activateTab(key, t.id);
        focusPane(key, t.id, l.id);
        return;
      }
    }
  }
  openFor({ kind: "file", path }, how === "split" ? { split: "row" } : {});
}
