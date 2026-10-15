import { useEffect, useRef, useState } from "react";

import { cn } from "@/lib/utils";

// DitherBand draws a picture with a halftone grain: an ordered (Bayer)
// dither of its lightness, strongest along edges and in gradients and faint
// in flat areas, so it reads as the painting first and the dots second. The
// colour is left smooth (dithering each channel apart is what makes static).
// It can be muted toward the page's background, which calms a busy night,
// and dimmed, which brings a daylight picture into a dark theme.
// Toward its foot the picture dissolves dot by dot (a threshold ramp, not a
// fade) into whatever is behind it, so set the band on the page's background
// and it melts into it in light and dark alike.
//
// It renders once, at a cell's resolution (cell CSS px per dot) on a small
// canvas scaled up by a whole number with hard pixels, so the dots stay crisp
// at any window size. It redraws only when its size changes (and not while
// the window is hidden), keeps no copy of the picture once drawn, and runs
// nothing in between: no timers, no animation frames.

const BAYER8 = [
  0, 32, 8, 40, 2, 34, 10, 42, 48, 16, 56, 24, 50, 18, 58, 26, 12, 44, 4, 36, 14, 46, 6, 38, 60, 28, 52, 20, 62, 30, 54, 22, 3, 35, 11, 43, 1, 33, 9, 41, 51, 19, 59, 27, 49, 17, 57, 25, 15, 47, 7, 39, 13, 45, 5, 37, 63,
  31, 55, 23, 61, 29, 53, 21,
].map((v) => (v + 0.5) / 64);

export interface DitherBandProps {
  // The picture. It covers the band, like object-fit: cover.
  src: string;
  // Where the picture sits vertically when cropped: 0 its top, 1 its foot.
  position?: number;
  // CSS px per dot.
  cell?: number;
  // Steps of lightness the dots move between: fewer is coarser.
  levels?: number;
  // How much of the band, from its foot, dissolves.
  fade?: number;
  // How far to mute the picture toward the page's background, 0 to 1.
  mute?: number;
  // How far to darken it first, 0 to 1: its light falls but its contrast
  // and hues stay, where muting alone would only grey it.
  dim?: number;
  className?: string;
}

export function DitherBand({ src, position = 0.5, cell = 2, levels = 6, fade = 0.4, mute = 0, dim = 0, className }: DitherBandProps) {
  const wrap = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const [drawn, setDrawn] = useState(false);

  useEffect(() => {
    const el = wrap.current;
    const cv = canvas.current;
    if (!el || !cv) return;
    let alive = true;
    let pending = false;
    let timer = 0;
    let size = "";

    const draw = async () => {
      if (document.hidden) {
        pending = true;
        return;
      }
      pending = false;
      const w = Math.ceil(el.clientWidth / cell);
      const h = Math.ceil(el.clientHeight / cell);
      if (!w || !h || `${w}x${h}` === size) return;
      const img = new Image();
      img.src = src;
      try {
        await img.decode();
      } catch {
        return;
      }
      if (!alive) return;
      size = `${w}x${h}`;
      cv.width = w;
      cv.height = h;
      cv.style.width = `${w * cell}px`;
      cv.style.height = `${h * cell}px`;
      const ctx = cv.getContext("2d", { willReadFrequently: true });
      if (!ctx) return;
      const scale = Math.max(w / img.naturalWidth, h / img.naturalHeight);
      const dw = img.naturalWidth * scale;
      const dh = img.naturalHeight * scale;
      ctx.imageSmoothingQuality = "high";
      ctx.clearRect(0, 0, w, h);
      // Drawn small, then up: the brushwork softens to its forms, so the
      // grain is the dither's, not the paint's.
      const soft = document.createElement("canvas");
      soft.width = Math.max(1, Math.round(w * 0.6));
      soft.height = Math.max(1, Math.round(h * 0.6));
      const sctx = soft.getContext("2d");
      if (sctx) {
        sctx.imageSmoothingQuality = "high";
        sctx.drawImage(img, ((w - dw) / 2) * 0.6, (h - dh) * position * 0.6, dw * 0.6, dh * 0.6);
        ctx.drawImage(soft, 0, 0, w, h);
        soft.width = soft.height = 0;
      }
      const frame = ctx.getImageData(0, 0, w, h);
      dither(frame, levels, fade, mute, dim, backgroundOf(el));
      ctx.putImageData(frame, 0, 0);
      setDrawn(true);
    };

    const later = () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => void draw(), 120);
    };
    const onVisible = () => {
      if (pending && !document.hidden) void draw();
    };
    const ro = new ResizeObserver(later);
    ro.observe(el);
    document.addEventListener("visibilitychange", onVisible);
    void draw();
    return () => {
      alive = false;
      window.clearTimeout(timer);
      ro.disconnect();
      document.removeEventListener("visibilitychange", onVisible);
      // Give the pixels back.
      cv.width = 0;
      cv.height = 0;
    };
  }, [src, position, cell, levels, fade, mute, dim]);

  return (
    <div ref={wrap} aria-hidden data-drawn={drawn || undefined} className={cn("pointer-events-none overflow-hidden", className)}>
      <canvas ref={canvas} className={cn("block [image-rendering:pixelated] transition-opacity duration-500", drawn ? "opacity-100" : "opacity-0")} />
    </div>
  );
}

// backgroundOf reads the page's --background as RGB.
function backgroundOf(el: HTMLElement): [number, number, number] {
  const probe = document.createElement("span");
  probe.style.color = "var(--background)";
  el.appendChild(probe);
  const m = getComputedStyle(probe).color.match(/[\d.]+/g)?.map(Number) ?? [255, 255, 255];
  probe.remove();
  // color(srgb r g b) comes as 0..1; rgb() as 0..255.
  return m[0] <= 1 && m[1] <= 1 && m[2] <= 1 ? [m[0] * 255, m[1] * 255, m[2] * 255] : [m[0], m[1], m[2]];
}

// dither moves each dot's lightness to one of a few steps against a Bayer
// threshold, by as much as the picture changes there: fully on edges and in
// gradients, about a third in flat areas. Then it clears the dots below a
// ramp toward the foot, with the matrix shifted so the dissolve's pattern
// does not line up with the grain's.
function dither(frame: ImageData, levels: number, fade: number, mute: number, dim: number, bg: [number, number, number]) {
  const { data, width: w, height: h } = frame;
  const L = levels - 1;
  const from = h * (1 - fade);
  const lum = new Float32Array(w * h);
  for (let i = 0, p = 0; p < w * h; i += 4, p++) {
    if (dim) {
      data[i] *= 1 - dim;
      data[i + 1] *= 1 - dim;
      data[i + 2] *= 1 - dim;
    }
    if (mute) {
      data[i] += (bg[0] - data[i]) * mute;
      data[i + 1] += (bg[1] - data[i + 1]) * mute;
      data[i + 2] += (bg[2] - data[i + 2]) * mute;
    }
    lum[p] = 0.299 * data[i] + 0.587 * data[i + 1] + 0.114 * data[i + 2];
  }
  for (let y = 0; y < h; y++) {
    let keep = 1;
    if (y > from) {
      const t = 1 - (y - from) / (h - from);
      keep = t * t * (3 - 2 * t);
    }
    const row = (y & 7) * 8;
    const rowB = ((y + 3) & 7) * 8;
    const up = Math.max(0, y - 1) * w;
    const down = Math.min(h - 1, y + 1) * w;
    for (let x = 0; x < w; x++) {
      const p = y * w + x;
      const i = p * 4;
      if (BAYER8[rowB + ((x + 5) & 7)] >= keep) {
        data[i + 3] = 0;
        continue;
      }
      const Y = lum[p];
      const edge = Math.abs(lum[y * w + Math.min(w - 1, x + 1)] - lum[y * w + Math.max(0, x - 1)]) + Math.abs(lum[down + x] - lum[up + x]);
      const amount = Math.min(1, 0.35 + edge / 40);
      const q = (Math.floor((Y / 255) * L + BAYER8[row + (x & 7)]) / L) * 255;
      const d = (q - Y) * amount;
      data[i] = data[i] + d;
      data[i + 1] = data[i + 1] + d;
      data[i + 2] = data[i + 2] + d;
      data[i + 3] = 255;
    }
  }
}
