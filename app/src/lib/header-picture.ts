import type { HarbourLight } from "@/components/art/harbour-art";
import type { StoredImageInfo } from "@/lib/chat-background";

// The picture across the top of a new chat, the launcher and Home
// (Settings › Appearance › Header picture): the harbour by the time of day,
// as Shipyard always had, one of the painted plates, a picture of the
// person's own, or none. The choice is kept with the prefs (lib/prefs.ts,
// headerPicture); a picture of their own is kept with the chat
// background's (lib/chat-background.ts), in this webview's IndexedDB, and
// named here by id, so the two choices stay apart. components/art/
// header-band.tsx draws it. This file imports nothing at run time, as
// prefs.ts reads it.

// The plates, in the harbour's style: a landmark far left, a clean middle,
// a paler foot for the page to sit on. Each has a day and a night painting
// (components/art/plates.ts).
export const PLATES = [
  { id: "alpine-lake", name: "Alpine lake" },
  { id: "meadow", name: "Meadow" },
  { id: "desert-mesa", name: "Desert mesa" },
  { id: "snowy-fjord", name: "Snowy fjord" },
  { id: "lavender-fields", name: "Lavender fields" },
  { id: "sea-of-clouds", name: "Sea of clouds" },
] as const;

export type PlateId = (typeof PLATES)[number]["id"];

export interface HeaderPicture {
  source: "builtin" | "image" | "none";
  // The painting when source is "builtin": "harbour" or a plate's id.
  builtin: "harbour" | PlateId;
  // The person's own picture, when source is "image".
  image?: StoredImageInfo;
  // Which part of a picture of your own shows when the band crops it.
  position: "top" | "center" | "bottom";
}

export const DEFAULT_HEADER_PICTURE: HeaderPicture = { source: "builtin", builtin: "harbour", position: "center" };

const BUILTIN_IDS: readonly string[] = ["harbour", ...PLATES.map((p) => p.id)];

// normalizeHeaderPicture keeps what is valid of a saved choice and fills the
// rest with defaults. Prefs from before there was a choice have none, and
// get the harbour, as they always had; so does a plate Shipyard no longer
// ships.
export function normalizeHeaderPicture(saved: unknown): HeaderPicture {
  const s = (saved && typeof saved === "object" ? saved : {}) as Record<string, unknown>;
  const d = DEFAULT_HEADER_PICTURE;
  const image = s.image && typeof s.image === "object" && typeof (s.image as StoredImageInfo).id === "string" ? (s.image as StoredImageInfo) : undefined;
  let source = s.source === "builtin" || s.source === "image" || s.source === "none" ? s.source : d.source;
  // A picture of your own that is gone is the harbour again.
  if (source === "image" && !image) source = d.source;
  return {
    source,
    builtin: typeof s.builtin === "string" && BUILTIN_IDS.includes(s.builtin) ? (s.builtin as HeaderPicture["builtin"]) : d.builtin,
    image,
    position: s.position === "top" || s.position === "bottom" ? s.position : "center",
  };
}

// How far each of the harbour's lights is muted toward the page in a
// DitherBand: the night most, so the stars and the moon's path stay calm.
export const HARBOUR_MUTE: Record<HarbourLight, number> = { dawn: 0.06, day: 0.04, dusk: 0.08, night: 0.3 };

// The plates' day and night, muted like the harbour's.
export const PLATE_MUTE = { day: 0.05, night: 0.3 };

// A picture of your own has no night, and in a dark theme a daylight one
// would glare. There it is dimmed by half, which keeps its hues and
// contrast, then muted a little toward the page; muting alone, far enough
// to calm it, only greys it.
export const IMAGE_MUTE = { light: 0.06, dark: 0.2 };
export const IMAGE_DIM = 0.5;

const POSITION = { top: 0, center: 0.5, bottom: 1 };

// HeaderArt is what the band draws for a choice in a theme: which painting
// (the harbour in a light, a plate by day or night, or a picture of your
// own by id), how far it is muted toward the page and dimmed, and, for a
// picture of your own, where it sits when cropped.
export type HeaderArt = ({ kind: "harbour"; light: HarbourLight } | { kind: "plate"; id: PlateId; night: boolean } | { kind: "image"; id: string }) & { mute: number; dim: number; position?: number };

// headerArt picks it: the harbour follows its light (useHarbourLight: a
// dark theme's is the night), a plate shows its night painting by the
// harbour's night, and None or a lost picture draws nothing.
export function headerArt(p: HeaderPicture, light: HarbourLight, dark: boolean): HeaderArt | undefined {
  if (p.source === "none") return undefined;
  if (p.source === "image") return p.image ? { kind: "image", id: p.image.id, mute: dark ? IMAGE_MUTE.dark : IMAGE_MUTE.light, dim: dark ? IMAGE_DIM : 0, position: POSITION[p.position] } : undefined;
  if (p.builtin === "harbour") return { kind: "harbour", light, mute: HARBOUR_MUTE[light], dim: 0 };
  const night = light === "night";
  return { kind: "plate", id: p.builtin, night, mute: night ? PLATE_MUTE.night : PLATE_MUTE.day, dim: 0 };
}
