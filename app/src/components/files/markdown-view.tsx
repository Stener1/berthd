import { useCallback, useEffect, useRef } from "react";

import { Markdown } from "@/components/conversation/markdown";
import { type Doc, filesApi, openFile } from "@/lib/files";
import { resolveImage, resolveLink, slug } from "@/lib/md-links";
import { openUrl } from "@/lib/open-url";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

// MarkdownView is a Markdown File tab's Preview: the file as GitHub would
// show it, drawn by the chat's own renderer (no raw HTML), from the text
// in the editor, so unsaved edits show as you type. Its pictures come from
// the worktree on the box; its links open the file they name in a File
// tab, a heading on the page, or a web page in the browser.
export function MarkdownView({ doc, text, className }: { doc: Doc; text: string; className?: string }) {
  const c = useStore((s) => s.client);
  const frame = useRef<HTMLDivElement>(null);
  // Pictures fetched, by worktree path, as blob: URLs, for as long as the
  // tab is open: a keystroke redraws the page, not the pictures.
  const urls = useRef(new Map<string, Promise<string>>());

  useEffect(() => {
    const kept = urls.current;
    return () => {
      for (const u of kept.values()) void u.then(URL.revokeObjectURL, () => {});
      kept.clear();
    };
  }, [doc.ref]);

  useEffect(() => {
    const el = frame.current;
    if (!el || !c) return;
    let live = true;
    for (const img of el.querySelectorAll<HTMLImageElement>("img[data-src]")) {
      const path = resolveImage(doc.path, img.dataset.src ?? "");
      if (!path) {
        img.dataset.missing = "";
        continue;
      }
      let u = urls.current.get(path);
      if (!u) {
        u = filesApi.image(c, doc.ref, path).then((b) => URL.createObjectURL(b));
        urls.current.set(path, u);
        // A picture that isn't there may be by the next redraw.
        u.catch(() => urls.current.delete(path));
      }
      u.then(
        (src) => {
          if (live) img.src = src;
        },
        () => {
          if (live) img.dataset.missing = "";
        },
      );
    }
    return () => {
      live = false;
    };
  }, [c, doc.ref, doc.path, text]);

  const onLink = useCallback(
    (href: string) => {
      const to = resolveLink(doc.path, href);
      if (!to) return;
      if (to.kind === "web") void openUrl(to.url);
      else if (to.kind === "file") openFile(to.path, "tab");
      else {
        const h = [...(frame.current?.querySelectorAll<HTMLElement>("h1, h2, h3, h4, h5, h6") ?? [])].find((x) => slug(x.textContent ?? "") === to.anchor);
        h?.scrollIntoView({ block: "start", behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
      }
    },
    [doc.path],
  );

  return (
    <div ref={frame} className={cn("min-h-0 min-w-0 overflow-auto", className)} data-testid="file-markdown">
      <div className="md-doc mx-auto w-full max-w-[48rem] px-8 py-6 text-body leading-relaxed max-[759px]:px-5">
        <Markdown text={text} copy={false} onLink={onLink} localImages />
      </div>
    </div>
  );
}
