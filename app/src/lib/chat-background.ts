import type { Client } from "@/lib/api";

// The picture behind a conversation, and what is done to it. The choice is
// kept with the prefs (lib/prefs.ts, chatBackground); a picture of the
// person's own, picked, dropped or generated, is kept in this webview's
// IndexedDB as a small WebP or JPEG (never base64 in localStorage), and the
// prefs name it by id. components/conversation/chat-background.tsx draws
// it; lib/chat-background-render.ts is the drawing, and
// components/art/chat-backgrounds.ts holds the built-ins. This file imports
// nothing at run time, as prefs.ts reads it.

export interface StoredImageInfo {
  id: string;
  name: string;
  // The prompt, when it was generated.
  prompt?: string;
  w: number;
  h: number;
}

export interface ChatBackground {
  source: "none" | "builtin" | "image";
  // The built-in shown when source is "builtin": a pattern, a gradient or
  // a scene (components/art/chat-backgrounds.ts).
  builtin: string;
  // The person's own picture, when source is "image".
  image?: StoredImageInfo;
  // How far the background rises from the page, 0 to 1.
  strength: number;
  // An ordered (Bayer) dither in dots of 2 (fine) or 4 (coarse) CSS px, or
  // none (smooth).
  dither: "fine" | "coarse" | "off";
  // Colour keeps a hint of its hues; ink draws it in the theme's text
  // colour. Either way it lies between the page and its text.
  tone: "colour" | "ink";
  // A scene or picture as it is: no toning or dither, and a frosted backing
  // behind the conversation as it needs. Advanced, off by default.
  original: boolean;
  // Fill crops a picture to the pane; fit shows all of it.
  fit: "cover" | "contain";
  position: "top" | "center" | "bottom";
}

export const DEFAULT_CHAT_BACKGROUND: ChatBackground = {
  source: "none",
  builtin: "contours",
  strength: 0.35,
  dither: "fine",
  tone: "colour",
  original: false,
  fit: "cover",
  position: "center",
};

// The effects alone, for Reset effects.
export const EFFECTS = ["strength", "dither", "tone", "original", "fit", "position"] as const;

// What a picture of your own starts with: Shipyard's style, dithered in the
// theme's ink at a low contrast.
export const PICTURE_PRESET: Partial<ChatBackground> = { strength: 0.35, dither: "fine", tone: "ink", original: false };

// What a built-in starts with, coming back from a picture of your own.
export const BUILTIN_PRESET: Partial<ChatBackground> = { strength: DEFAULT_CHAT_BACKGROUND.strength, dither: "fine", tone: "colour", original: false };

const pick = <T extends string>(v: unknown, all: readonly T[], d: T): T => (all.includes(v as T) ? (v as T) : d);

// normalizeChatBackground keeps what is valid of a saved choice (one from an
// older Shipyard may hold other fields) and fills the rest with defaults.
export function normalizeChatBackground(saved: unknown): ChatBackground {
  const s = (saved && typeof saved === "object" ? saved : {}) as Record<string, unknown>;
  const d = DEFAULT_CHAT_BACKGROUND;
  const image = s.image && typeof s.image === "object" && typeof (s.image as StoredImageInfo).id === "string" ? (s.image as StoredImageInfo) : undefined;
  return {
    source: pick(s.source, ["none", "builtin", "image"], d.source),
    builtin: typeof s.builtin === "string" ? s.builtin : d.builtin,
    image,
    strength: typeof s.strength === "number" && s.strength >= 0 && s.strength <= 1 ? s.strength : d.strength,
    dither: pick(s.dither, ["fine", "coarse", "off"], d.dither),
    tone: pick(s.tone, ["colour", "ink"], d.tone),
    original: s.original === true,
    fit: pick(s.fit, ["cover", "contain"], d.fit),
    position: pick(s.position, ["top", "center", "bottom"], d.position),
  };
}

// --- The person's own pictures -------------------------------------------

// Kept small: the long side at most MAX_SIDE px, as WebP (JPEG where the
// webview can't write WebP). A few are kept, newest first, to switch back to.
const MAX_SIDE = 2560;
const KEEP = 8;
const DB = "berth-chat-background";
const STORE = "images";

// The bytes are kept as an ArrayBuffer, not a Blob: WebKit refuses Blobs
// in IndexedDB in some stores (ephemeral ones among them).
interface StoredImage extends StoredImageInfo {
  bytes: ArrayBuffer;
  type: string;
  at: number;
}

const blobOf = (s: StoredImage) => new Blob([s.bytes], { type: s.type });

function db(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB, 1);
    req.onupgradeneeded = () => req.result.createObjectStore(STORE, { keyPath: "id" });
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

async function tx<T>(mode: IDBTransactionMode, run: (s: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const d = await db();
  try {
    return await new Promise<T>((resolve, reject) => {
      const req = run(d.transaction(STORE, mode).objectStore(STORE));
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });
  } finally {
    d.close();
  }
}

export async function imageBlob(id: string): Promise<Blob | undefined> {
  const s = await tx<StoredImage | undefined>("readonly", (st) => st.get(id));
  return s ? blobOf(s) : undefined;
}

// listImages is the kept pictures, newest first, without their bytes.
export async function listImages(): Promise<(StoredImageInfo & { blob: Blob })[]> {
  const all = await tx<StoredImage[]>("readonly", (s) => s.getAll());
  return all.sort((a, b) => b.at - a.at).map((s) => ({ id: s.id, name: s.name, prompt: s.prompt, w: s.w, h: s.h, blob: blobOf(s) }));
}

export async function removeImage(id: string) {
  await tx("readwrite", (s) => s.delete(id));
  notifyImages();
}

const imageListeners = new Set<() => void>();
export const onImagesChanged = (fn: () => void) => {
  imageListeners.add(fn);
  return () => void imageListeners.delete(fn);
};
const notifyImages = () => imageListeners.forEach((fn) => fn());

// keepImage shrinks a picture, keeps it, and forgets the oldest past KEEP,
// but never one in use: the chat background and the header picture
// (lib/header-picture.ts) choose from the same pictures, each its own.
export async function keepImage(file: Blob, meta: { name: string; prompt?: string }, inUse: (string | undefined)[] = []): Promise<StoredImageInfo> {
  if (file.size > 60 << 20) throw new Error("That file is over 60 MB; pick a smaller picture.");
  let bmp: ImageBitmap;
  try {
    bmp = await createImageBitmap(file);
  } catch {
    throw new Error("That file isn't a picture Shipyard can read. Try a PNG, JPEG or WebP.");
  }
  const scale = Math.min(1, MAX_SIDE / Math.max(bmp.width, bmp.height));
  const w = Math.max(1, Math.round(bmp.width * scale));
  const h = Math.max(1, Math.round(bmp.height * scale));
  const c = downscale(bmp, w, h);
  bmp.close();
  let blob = await toBlob(c, "image/webp", 0.86);
  if (!blob || blob.type !== "image/webp") blob = await toBlob(c, "image/jpeg", 0.88);
  c.width = c.height = 0;
  if (!blob) throw new Error("Couldn't save the picture.");
  const info: StoredImageInfo = { id: `img-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`, name: meta.name, prompt: meta.prompt, w, h };
  const bytes = await blob.arrayBuffer();
  await tx("readwrite", (s) => s.put({ ...info, bytes, type: blob.type, at: Date.now() } satisfies StoredImage));
  const all = await tx<StoredImage[]>("readonly", (s) => s.getAll());
  for (const old of all.sort((a, b) => b.at - a.at).filter((s) => !inUse.includes(s.id)).slice(KEEP)) await tx("readwrite", (s) => s.delete(old.id));
  notifyImages();
  return info;
}

const toBlob = (c: HTMLCanvasElement, type: string, q: number) => new Promise<Blob | null>((r) => c.toBlob(r, type, q));

// downscale draws src at w×h, halving in steps so a large picture averages
// instead of skipping pixels (one big step aliases in WebKit).
export function downscale(src: CanvasImageSource & { width: number; height: number }, w: number, h: number): HTMLCanvasElement {
  let cur: CanvasImageSource = src;
  let cw = src.width;
  let ch = src.height;
  const temps: HTMLCanvasElement[] = [];
  while (cw / 2 >= w && ch / 2 >= h) {
    const t = document.createElement("canvas");
    t.width = Math.max(1, Math.round(cw / 2));
    t.height = Math.max(1, Math.round(ch / 2));
    const x = t.getContext("2d")!;
    x.imageSmoothingQuality = "high";
    x.drawImage(cur, 0, 0, t.width, t.height);
    temps.push(t);
    cur = t;
    cw = t.width;
    ch = t.height;
  }
  const out = document.createElement("canvas");
  out.width = w;
  out.height = h;
  const x = out.getContext("2d")!;
  x.imageSmoothingQuality = "high";
  x.drawImage(cur, 0, 0, w, h);
  for (const t of temps) t.width = t.height = 0;
  return out;
}

// --- Generating one ------------------------------------------------------

export interface ImageGenerator {
  id: string;
  name: string;
  installed: boolean;
  signed_in: boolean;
  path?: string;
  note?: string;
  // What Generate runs; {prompt} stands for the instruction.
  command: string[];
}

export interface GeneratedImage {
  mime: string;
  data: string;
  prompt: string;
}

// The laptop agent runs the generator (internal/agent/imagegen.go), only
// when the person presses Generate.
export const imageGenApi = {
  list: async (c: Client) => (await c.laptop<{ generators: ImageGenerator[] | null }>("GET", "/v1/imagegen")).generators ?? [],
  generate: (c: Client, prompt: string, generator = "codex") => c.laptop<GeneratedImage>("POST", "/v1/imagegen", { generator, prompt }),
};

export function generatedBlob(img: GeneratedImage): Blob {
  const bin = atob(img.data);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return new Blob([bytes], { type: img.mime });
}

// The instruction the agent wraps a prompt in, shown with the command so
// what is sent is never a surprise. Keep in step with instruction() in
// internal/agent/imagegen.go.
export const instructionFor = (prompt: string) =>
  "Use your image generation tool to make exactly one image, then reply with the word done. Do not run commands, and do not read or write files. It is a wide background for a chat window: landscape 16:9, calm, low in detail, with no text, letters, logos or watermarks. The picture: " +
  prompt;
