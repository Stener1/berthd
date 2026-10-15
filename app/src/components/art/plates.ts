import alpineLake from "@/components/art/plates/alpine-lake.webp";
import alpineLakeNight from "@/components/art/plates/alpine-lake-night.webp";
import desertMesa from "@/components/art/plates/desert-mesa.webp";
import desertMesaNight from "@/components/art/plates/desert-mesa-night.webp";
import lavenderFields from "@/components/art/plates/lavender-fields.webp";
import lavenderFieldsNight from "@/components/art/plates/lavender-fields-night.webp";
import meadow from "@/components/art/plates/meadow.webp";
import meadowNight from "@/components/art/plates/meadow-night.webp";
import seaOfClouds from "@/components/art/plates/sea-of-clouds.webp";
import seaOfCloudsNight from "@/components/art/plates/sea-of-clouds-night.webp";
import snowyFjord from "@/components/art/plates/snowy-fjord.webp";
import snowyFjordNight from "@/components/art/plates/snowy-fjord-night.webp";
import type { HarbourLight } from "@/components/art/harbour-art";
import type { PlateId } from "@/lib/header-picture";

// The plates' paintings (lib/header-picture.ts names them), generated with
// Codex for Shipyard: the days in the harbour's style, and each night from
// its day, painted like the harbour's night, so the composition never
// moves and a dark theme gets the night as the harbour does. They are
// separate files, fetched only when shown.

export const PLATE_ART: Record<PlateId, { day: string; night: string }> = {
  "alpine-lake": { day: alpineLake, night: alpineLakeNight },
  meadow: { day: meadow, night: meadowNight },
  "desert-mesa": { day: desertMesa, night: desertMesaNight },
  "snowy-fjord": { day: snowyFjord, night: snowyFjordNight },
  "lavender-fields": { day: lavenderFields, night: lavenderFieldsNight },
  "sea-of-clouds": { day: seaOfClouds, night: seaOfCloudsNight },
};

// plateSrc is a plate in the harbour's light: its night for the night, its
// day otherwise.
export const plateSrc = (id: PlateId, light: HarbourLight) => PLATE_ART[id][light === "night" ? "night" : "day"];
