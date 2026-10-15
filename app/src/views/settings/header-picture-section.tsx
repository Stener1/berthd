import { BanIcon, ImagePlusIcon, RotateCcwIcon } from "lucide-react";
import { useRef, useState } from "react";

import { HeaderBand, useHeaderPicture } from "@/components/art/header-band";
import { toastError } from "@/components/error-note";
import { Button } from "@/components/ui/button";
import { keepImage } from "@/lib/chat-background";
import { DEFAULT_HEADER_PICTURE, type HeaderPicture, PLATES } from "@/lib/header-picture";
import { setHeaderPicture, usePrefs } from "@/lib/prefs";
import { cn } from "@/lib/utils";
import { picturesInUse, RemovePicture, Tile, TileRow, TileWords, useOwnImages } from "@/views/settings/chat-background-section";
import { Segmented } from "@/views/settings/controls";
import { SettingsGroup, SettingsRow } from "@/views/settings/rows";

// Settings › Appearance › Header picture: the painting across the top of a
// new chat, an empty worktree and Home (lib/header-picture.ts). The
// harbour, a plate, a picture of your own (kept with the chat background's,
// chosen apart from it), or none. Every tile is drawn as the band draws it,
// so in a dark theme the paintings show their night.

const HARBOUR_CHOICE: HeaderPicture = { ...DEFAULT_HEADER_PICTURE };

export function HeaderPictureSettings() {
  const hp = usePrefs((p) => p.headerPicture);
  const [dragging, setDragging] = useState(false);
  const file = useRef<HTMLInputElement>(null);
  const images = useOwnImages();
  const custom = hp.source !== "builtin" || hp.builtin !== DEFAULT_HEADER_PICTURE.builtin;

  const add = async (f: File) => {
    try {
      const image = await keepImage(f, { name: f.name }, picturesInUse());
      setHeaderPicture({ source: "image", image, position: "center" });
    } catch (err) {
      toastError(err, { title: "Couldn't use that picture" });
    }
  };
  const builtin = (id: HeaderPicture["builtin"]) => hp.source === "builtin" && hp.builtin === id;

  return (
    <section
      id="header-picture"
      data-testid="header-picture-settings"
      className="scroll-mt-4"
      onDragOver={(e) => {
        if (!e.dataTransfer.types.includes("Files")) return;
        e.preventDefault();
        setDragging(true);
      }}
      onDragLeave={(e) => !e.currentTarget.contains(e.relatedTarget as Node) && setDragging(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDragging(false);
        const f = [...e.dataTransfer.files].find((x) => x.type.startsWith("image/"));
        if (f) void add(f);
      }}
    >
      <div className="mb-2 flex items-end gap-3">
        <div className="min-w-0 flex-1">
          <h2 className="font-medium text-[13px] text-muted-foreground">Header picture</h2>
          <p className="mt-0.5 text-muted-foreground text-xs">Across the top of a new chat, an empty worktree and Home. In a dark theme the paintings show their night.</p>
        </div>
        {custom && (
          <Button size="xs" variant="ghost" onClick={() => setHeaderPicture({ ...DEFAULT_HEADER_PICTURE, image: undefined })}>
            <RotateCcwIcon />
            Reset
          </Button>
        )}
      </div>
      <div className={cn("overflow-hidden rounded-xl border bg-card/40 transition-shadow", dragging && "ring-2 ring-ring")}>
        <Preview />
        <div className="space-y-3 border-t p-3">
          <TileRow title="Paintings">
            <Tile label="None" selected={hp.source === "none"} onClick={() => setHeaderPicture({ source: "none" })}>
              <TileWords icon={<BanIcon />}>None</TileWords>
            </Tile>
            <Tile label="Harbour, by the time of day" selected={builtin("harbour")} onClick={() => setHeaderPicture({ source: "builtin", builtin: "harbour" })}>
              <Thumb choice={HARBOUR_CHOICE} />
            </Tile>
            {PLATES.map((p) => (
              <Tile key={p.id} label={p.name} selected={builtin(p.id)} onClick={() => setHeaderPicture({ source: "builtin", builtin: p.id })}>
                <Thumb choice={{ ...DEFAULT_HEADER_PICTURE, builtin: p.id }} />
              </Tile>
            ))}
          </TileRow>
          <TileRow title="Custom">
            {images.map((img) => (
              <Tile
                key={img.id}
                label={img.prompt ? `Generated: ${img.prompt}` : img.name}
                selected={hp.source === "image" && hp.image?.id === img.id}
                onClick={() => setHeaderPicture({ source: "image", image: img })}
                extra={<RemovePicture img={img} />}
              >
                <Thumb choice={{ ...hp, source: "image", image: img }} />
              </Tile>
            ))}
            <Tile label="Add a picture of your own, or drop one here" onClick={() => file.current?.click()}>
              <TileWords icon={<ImagePlusIcon />}>Add…</TileWords>
            </Tile>
            <input
              ref={file}
              type="file"
              aria-label="Header picture file"
              accept="image/png,image/jpeg,image/webp,image/gif,image/avif"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0];
                e.target.value = "";
                if (f) void add(f);
              }}
            />
          </TileRow>
        </div>
      </div>
      {hp.source === "image" && hp.image && (
        <div className="mt-4">
          <SettingsGroup title="Your picture">
            <SettingsRow label="Position" description="Which part of the picture shows across the band.">
              <Segmented
                value={hp.position}
                options={[
                  { value: "top", label: "Top" },
                  { value: "center", label: "Center" },
                  { value: "bottom", label: "Bottom" },
                ]}
                onChange={(position) => setHeaderPicture({ position })}
              />
            </SettingsRow>
          </SettingsGroup>
        </div>
      )}
    </section>
  );
}

// Thumb draws a choice as the band would, at a tile's size: finer dots and
// no dissolve, so the painting reads in 76 px.
function Thumb({ choice }: { choice: HeaderPicture }) {
  const picture = useHeaderPicture(choice);
  if (!picture) return null;
  return <HeaderBand picture={picture} position={0.45} fade={0} cell={1} className="size-full" />;
}

// Preview is the top of a new chat in miniature, under the chosen picture.
function Preview() {
  const picture = useHeaderPicture();
  return (
    <div className="relative h-[180px] overflow-hidden bg-background" aria-label="Preview" role="img">
      {picture && <HeaderBand picture={picture} position={0.45} fade={0.5} className="absolute inset-x-0 top-0 h-[140px]" />}
      <div className={cn("relative mx-auto w-[62%] px-2", picture ? "pt-[92px]" : "pt-12")}>
        <div className={cn("mb-2", picture && "[text-shadow:0_0_6px_var(--background),0_0_14px_var(--background)]")}>
          <div className="font-semibold text-sm tracking-tight">checkout-fix</div>
          <div className="mt-0.5 flex gap-1 text-[10px] text-muted-foreground">
            <span className="rounded bg-accent px-1 font-mono">devl/checkout-fix</span>
          </div>
        </div>
        <div className="rounded-lg border border-input bg-background px-2.5 py-1.5 text-[11px] text-muted-foreground dark:bg-input/32">What should this agent work on?</div>
      </div>
    </div>
  );
}
