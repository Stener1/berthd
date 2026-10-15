import { lazy, type RefObject, Suspense, useLayoutEffect, useState } from "react";

import "@/components/conversation/code-blocks.css";
import { guessLanguage } from "@/components/conversation/code-highlight";

// CodeBlocks draws a reply's mermaid and diff blocks (lib/code-blocks):
// a flowchart as its diagram, with Source in the frame's head to read what
// it was drawn from; a diff as the chat's edits are drawn (components/
// diff). The reply is HTML (markdown.tsx), so each drawing is a portal
// into its block's frame, beside the <pre>, which stays: Copy still takes
// the text, the highlighter still colours it, and Source (or a renderer
// that can't load) shows it. A draft stays text: a diagram drawn from half
// its lines would jump about as the rest came.
//
// What draws them (drawn-block.tsx) loads with the first such block, not
// with the app; until then the block is its text.
const Drawn = lazy(() => import("@/components/conversation/drawn-block"));

export interface Block {
  key: string;
  frame: HTMLElement;
  // Where the drawing goes, after the <pre>; and a place in the head,
  // before Copy.
  host: HTMLElement;
  slot: HTMLElement;
  lang: string;
  text: string;
}

const DRAWN = new Set(["mermaid", "diff", "patch"]);

let generation = 0;

export function CodeBlocks({ body, html, draft }: { body: RefObject<HTMLElement | null>; html: string; draft: boolean }) {
  const [blocks, setBlocks] = useState<Block[]>([]);
  // Before paint, so a block already drawable never shows as text first.
  useLayoutEffect(() => {
    const root = body.current;
    const found: Block[] = [];
    const gen = ++generation;
    if (root && !draft)
      root.querySelectorAll<HTMLElement>(".cv-pre").forEach((frame, i) => {
        const code = frame.querySelector<HTMLElement>(":scope > pre > code");
        const copy = frame.querySelector<HTMLElement>(":scope > .cv-pre-head > [data-copy-code]");
        if (!code || !copy) return;
        const text = code.textContent ?? "";
        const named = /\blanguage-([\w#+.-]+)/.exec(code.className)?.[1]?.toLowerCase();
        const lang = named ?? guessLanguage(text);
        if (!DRAWN.has(lang)) return;
        const host = document.createElement("div");
        host.className = "cv-pre-drawn";
        frame.append(host);
        const slot = document.createElement("span");
        slot.className = "cv-pre-slot";
        copy.before(slot);
        found.push({ key: `${gen}-${i}`, frame, host, slot, lang, text });
      });
    setBlocks((was) => (was.length || found.length ? found : was));
    return () => {
      for (const b of found) {
        b.host.remove();
        b.slot.remove();
        delete b.frame.dataset.drawn;
      }
    };
  }, [body, html, draft]);
  if (!blocks.length) return null;
  return (
    <Suspense fallback={null}>
      {blocks.map((b) => (
        <Drawn key={b.key} block={b} />
      ))}
    </Suspense>
  );
}
