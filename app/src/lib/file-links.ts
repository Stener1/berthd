import { ApiError } from "@/lib/api";
import { filesApi, type WorktreeFile, type WorktreeFolder } from "@/lib/files";
import { type FolderEntry, kindIn, splitPath } from "@/lib/file-refs";
import { useStore } from "@/lib/store";
import type { WorktreeRef } from "@/lib/workspaces";

// Whether a path an agent named is a file of its worktree, and what the
// file says, for the links in its replies (components/conversation/
// file-links.tsx). The box is asked about a path's folder (GET …/files?dir=,
// the Files panel's), so the paths in a reply cost a read per folder, and
// the answer serves every reply that names a file there. A file that wasn't
// there may be one the agent has made since: its folder is asked again,
// at most every few seconds.

const AGAIN = 4_000;
const FOLDERS_MAX = 400;

interface Folder {
  at: number;
  // Undefined: the folder isn't in the worktree.
  entries?: readonly FolderEntry[];
  // A box from before folders were listed: each path is asked alone.
  old?: boolean;
  reading?: Promise<Folder>;
}

const folders = new Map<string, Folder>();

const worktreeKey = (ref: WorktreeRef) => `${ref.box}\0${ref.location}\0${ref.worktree}`;
const client = () => useStore.getState().client;

function readFolder(ref: WorktreeRef, dir: string): Promise<Folder> {
  const key = `${worktreeKey(ref)}\0${dir}`;
  const have = folders.get(key);
  if (have?.reading) return have.reading;
  const c = client();
  if (!c) return Promise.resolve({ at: Date.now() });
  const reading = filesApi
    .dir(c, ref, dir)
    .then(
      (r: WorktreeFolder | { files?: unknown }): Folder => {
        // An older box answers ?dir= with its file list: ask about the
        // path itself instead (statFile).
        if (!("entries" in r)) return { at: Date.now(), old: true };
        return { at: Date.now(), entries: r.entries ?? [] };
      },
      (err): Folder => {
        // Not a folder of the worktree: nothing in it is a file. Anything
        // else (the box away) is asked again next time.
        if (err instanceof ApiError && (err.status === 404 || err.status === 400 || err.status === 403)) return { at: Date.now() };
        return { at: 0 };
      },
    )
    .then((f) => {
      folders.set(key, f);
      if (folders.size > FOLDERS_MAX) folders.delete(folders.keys().next().value as string);
      return f;
    });
  folders.set(key, { at: have?.at ?? 0, entries: have?.entries, reading });
  return reading;
}

// knownFile is what is known now, without asking: true, false, or
// undefined when the box has to be asked (or asked again).
export function knownFile(ref: WorktreeRef, path: string): boolean | undefined {
  const { dir, name } = splitPath(path);
  const f = folders.get(`${worktreeKey(ref)}\0${dir}`);
  if (!f || f.at === 0 || f.old) return undefined;
  const kind = kindIn(f.entries, name);
  if (kind) return kind === "file";
  return Date.now() - f.at < AGAIN ? false : undefined;
}

// isFile asks the box, through the cache, whether path is a file of the
// worktree (a folder is not).
export async function isFile(ref: WorktreeRef, path: string): Promise<boolean> {
  const known = knownFile(ref, path);
  if (known !== undefined) return known;
  const { dir, name } = splitPath(path);
  const f = await readFolder(ref, dir);
  if (f.old) return statFile(ref, path);
  return kindIn(f.entries, name) === "file";
}

async function statFile(ref: WorktreeRef, path: string): Promise<boolean> {
  const c = client();
  if (!c) return false;
  try {
    await filesApi.read(c, ref, path, { stat: true });
    return true;
  } catch {
    return false;
  }
}

// ---- What a file says ----

// A file shown on hover is read once and kept a few seconds: moving
// between two paths in a reply reads neither again.
const KEEP = 5_000;
const read = new Map<string, { at: number; file: Promise<WorktreeFile> }>();

export function readFile(ref: WorktreeRef, path: string): Promise<WorktreeFile> {
  const key = `${worktreeKey(ref)}\0${path}`;
  const have = read.get(key);
  if (have && Date.now() - have.at < KEEP) return have.file;
  const c = client();
  if (!c) return Promise.reject(new Error("Not connected"));
  const file = filesApi.read(c, ref, path);
  read.set(key, { at: Date.now(), file });
  if (read.size > 32) read.delete(read.keys().next().value as string);
  // A failed read is tried again next time.
  file.catch(() => read.get(key)?.file === file && read.delete(key));
  return file;
}
