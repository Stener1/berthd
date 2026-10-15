import { useEffect, useState } from "react";

import { DitherBand } from "@/components/art/dither-band";
import { HARBOUR, type HarbourLight, useHarbourLight } from "@/components/art/harbour-art";
import { plateSrc } from "@/components/art/plates";
import { useActiveTheme } from "@/hooks/use-theme";
import { imageBlob } from "@/lib/chat-background";
import { type HeaderArt, headerArt, type HeaderPicture } from "@/lib/header-picture";
import { usePrefs } from "@/lib/prefs";
import { cn } from "@/lib/utils";

// The header picture (lib/header-picture.ts) as the launcher, a new chat
// and Home draw it: one hook and one band, so the three never drift.

export type HeaderShown = HeaderArt & {
  // The painting's file, or a picture of your own as a blob: URL; missing
  // while that is read from IndexedDB.
  src?: string;
  // What it is, for tests: "harbour", a plate's id, or "image".
  name: string;
};

// useHeaderPicture is the chosen picture in this theme and light, or
// nothing for None. A screen lays itself out by whether there is one, and
// draws it with HeaderBand.
export function useHeaderPicture(choice?: HeaderPicture): HeaderShown | undefined {
  const saved = usePrefs((p) => p.headerPicture);
  const light = useHarbourLight();
  const dark = useActiveTheme().appearance === "dark";
  const art = headerArt(choice ?? saved, light, dark);
  const url = useImageUrl(art?.kind === "image" ? art.id : undefined);
  if (!art) return undefined;
  return { ...art, src: srcOf(art, light, url), name: art.kind === "plate" ? art.id : art.kind };
}

function srcOf(art: HeaderArt, light: HarbourLight, url?: string) {
  if (art.kind === "harbour") return HARBOUR[art.light];
  if (art.kind === "plate") return plateSrc(art.id, light);
  return url;
}

// HeaderBand draws it as a DitherBand: dissolving into the page at its
// foot, muted and, for a daylight picture in a dark theme, dimmed.
// position is where the screen likes a painting to sit; a picture of your
// own sits where its owner put it.
export function HeaderBand({ picture, position, fade, cell, className, style }: { picture: HeaderShown; position: number; fade: number; cell?: number; className?: string; style?: React.CSSProperties }) {
  return (
    <div aria-hidden data-testid="header-picture" data-picture={picture.name} className={cn("pointer-events-none", className)} style={style}>
      {picture.src && <DitherBand src={picture.src} position={picture.position ?? position} fade={fade} cell={cell} mute={picture.mute} dim={picture.dim} className="size-full" />}
    </div>
  );
}

// Pictures of your own as blob: URLs, a few kept so the three screens (and
// Settings' tiles) share one each. One pushed out is let go: only the
// chosen picture is ever on a screen for long.
const urls = new Map<string, Promise<string>>();
const KEEP = 10;

function imageUrl(id: string): Promise<string> {
  let p = urls.get(id);
  if (!p) {
    p = imageBlob(id).then((b) => {
      if (!b) throw new Error("gone");
      return URL.createObjectURL(b);
    });
    urls.set(id, p);
    p.catch(() => urls.delete(id));
    while (urls.size > KEEP) {
      const [oldest, old] = urls.entries().next().value as [string, Promise<string>];
      urls.delete(oldest);
      void old.then((u) => URL.revokeObjectURL(u), () => undefined);
    }
  }
  return p;
}

export function useImageUrl(id: string | undefined): string | undefined {
  const [got, setGot] = useState<{ id: string; url: string }>();
  useEffect(() => {
    if (!id) return;
    let alive = true;
    imageUrl(id).then((url) => alive && setGot({ id, url }), () => undefined);
    return () => {
      alive = false;
    };
  }, [id]);
  return got && got.id === id ? got.url : undefined;
}
