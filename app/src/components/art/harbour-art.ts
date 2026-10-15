import dawn from "@/components/art/harbour/dawn.webp";
import day from "@/components/art/harbour/day.webp";
import dusk from "@/components/art/harbour/dusk.webp";
import night from "@/components/art/harbour/night.webp";
import { useEffect, useState } from "react";

import { useActiveTheme } from "@/hooks/use-theme";

// The harbour painting, one per time of day, all from the same plate so the
// composition never moves: sky, a lighthouse on the headland, open water. A
// dark theme always gets the night; a light one follows the clock, with
// dusk standing in after dark. ?light=dawn|day|dusk|night picks one, for demos.

export type HarbourLight = "dawn" | "day" | "dusk" | "night";

export const HARBOUR: Record<HarbourLight, string> = { dawn, day, dusk, night };

// How far each light is muted toward the page lives with the header
// picture's choice (lib/header-picture.ts), which tests can read.
export { HARBOUR_MUTE } from "@/lib/header-picture";

function byClock(d = new Date()): HarbourLight {
  const h = d.getHours();
  if (h >= 5 && h < 8) return "dawn";
  if (h >= 8 && h < 17) return "day";
  return "dusk";
}

export function useHarbourLight(): HarbourLight {
  const dark = useActiveTheme().appearance === "dark";
  const [clock, setClock] = useState(byClock);
  useEffect(() => {
    const t = window.setInterval(() => {
      if (!document.hidden) setClock(byClock());
    }, 600_000);
    return () => window.clearInterval(t);
  }, []);
  const q = new URLSearchParams(location.search).get("light");
  if (q === "dawn" || q === "day" || q === "dusk" || q === "night") return q;
  return dark ? "night" : clock;
}
