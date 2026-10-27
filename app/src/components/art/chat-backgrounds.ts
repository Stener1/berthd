import dawn from "@/components/art/backgrounds/dawn.webp";
import fog from "@/components/art/backgrounds/fog.webp";
import night from "@/components/art/backgrounds/night.webp";
import openSea from "@/components/art/backgrounds/open-sea.webp";
import { HARBOUR, type HarbourLight } from "@/components/art/harbour-art";
import { plateSrc } from "@/components/art/plates";
import { PLATES } from "@/lib/header-picture";

// The chat backgrounds Shipyard ships (lib/chat-background.ts). Patterns and
// gradients are drawn in code, in the theme's colours
// (lib/chat-background-fields.ts); scenes are paintings, dithered into the
// theme's colours at a low contrast unless shown as they are.

export type BuiltinKind = "pattern" | "gradient" | "scene";

export interface Builtin {
  id: string;
  name: string;
  kind: BuiltinKind;
  // A scene's picture, by the harbour's light (dark themes get the night).
  src?(light: HarbourLight): string;
}

export const BUILTINS: Builtin[] = [
  { id: "contours", name: "Chart contours", kind: "pattern" },
  { id: "dots", name: "Dot grid", kind: "pattern" },
  { id: "grid", name: "Nautical grid", kind: "pattern" },
  { id: "waves", name: "Soft waves", kind: "pattern" },
  { id: "g-dawn", name: "Dawn", kind: "gradient" },
  { id: "g-dusk", name: "Dusk", kind: "gradient" },
  { id: "g-deep", name: "Deep sea", kind: "gradient" },
  // The first-prompt painting, by the time of day; the rest were generated
  // with Codex for Shipyard.
  { id: "harbour", name: "Harbour", kind: "scene", src: (l) => HARBOUR[l] },
  { id: "dawn", name: "Lighthouse at dawn", kind: "scene", src: () => dawn },
  { id: "night", name: "Moorings at night", kind: "scene", src: () => night },
  { id: "open-sea", name: "Open sea", kind: "scene", src: () => openSea },
  { id: "fog", name: "Fog", kind: "scene", src: () => fog },
  // The header picture's plates (lib/header-picture.ts), by day or night.
  ...PLATES.map((p): Builtin => ({ id: p.id, name: p.name, kind: "scene", src: (l) => plateSrc(p.id, l) })),
];

export const builtin = (id: string) => BUILTINS.find((b) => b.id === id) ?? BUILTINS[0];
