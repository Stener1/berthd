import { CopyIcon } from "lucide-react";
import { memo, useEffect, useMemo, useRef, useSyncExternalStore } from "react";

import { CodeBlocks } from "@/components/conversation/code-blocks";
import { InlineImages } from "@/components/conversation/inline-images";
import { guessLanguage, languageName, useCodeHighlight } from "@/components/conversation/code-highlight";
import { useFileLinks } from "@/components/conversation/file-links";
import { TableExplorers } from "@/components/conversation/table-explorers";
import { TestBlocks } from "@/components/conversation/test-blocks";
import { useRepoRefs } from "@/components/conversation/repo-refs";
import { Tip } from "@/components/tip";
import { copyText } from "@/lib/clipboard";
import { CALLOUT, callout } from "@/lib/callouts";
import { caretAfter } from "@/lib/draft-text";
import { imageSlots } from "@/lib/chat-images";
import { markdownHtml, type TexRender } from "@/lib/markdown-html";
import { openUrl } from "@/lib/open-url";
import { cn } from "@/lib/utils";

// Markdown draws an agent's reply as the agent wrote it: headings, lists,
// tables, code, bold, links and math, as its terminal does. micromark
// escapes any HTML in the text, so a reply can't inject markup; links open
// outside.
//
// The text is selectable (the app otherwise isn't), code blocks take the
// theme's syntax colours once they've drawn (code-highlight.ts), every
// code block has its own Copy, and the reply's Copy takes the Markdown
// itself, so tables and lists survive a paste into an issue or an editor.
// In a chat, a file of its worktree named in inline code links to it, with
// its lines on hover (file-links.tsx). A > [!NOTE] quote draws as GitHub's
// callouts do (lib/callouts.ts), and in a chat, #123 and a commit's hash
// link to its repository (repo-refs.tsx).

// A code block's head: its language, and Copy (lucide's copy and check).
const ICON = (d: string) => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${d}</svg>`;
const COPY_ICON = ICON('<rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>');
const DONE_ICON = ICON('<path d="M20 6 9 17l-5-5"/>');
const COPY_CODE = `<button type="button" class="cv-copy-code" data-copy-code aria-label="Copy code"><span class="cv-copy-idle">${COPY_ICON}Copy</span><span class="cv-copy-done">${DONE_ICON}Copied</span></button>`;

const unescape = (s: string) => s.replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, '"').replace(/&amp;/g, "&");

// codeBlock wraps a fenced block in its frame, headed by its language as
// the fence names it or, unnamed, as code-highlight guesses it.
const codeBlock = (_: string, attrs: string, lang: string | undefined, body: string) => {
  const name = languageName(lang?.toLowerCase() ?? guessLanguage(unescape(body)));
  const head = `<div class="cv-pre-head"><span class="cv-pre-lang">${name}</span>${COPY_CODE}</div>`;
  return `<div class="cv-pre">${head}<pre><code${attrs}>${body}</code></pre></div>`;
};

// A draft (the reply as the agent's screen shows it while it writes) ends
// in a caret, says "…" above when its start is off the screen, and keeps
// the room its Copy will take, so the message that replaces it lands in
// place: the same element, its words swapped (lib/draft-text).
const CARET = '<span class="cv-caret" aria-hidden="true"></span>';

// KaTeX, which draws the math, and its stylesheet and fonts load the first
// time a reply has any: most chats have none, and the app starts without
// them. Until then a formula shows as its source.
let tex: TexRender | undefined;
let texLoading: Promise<void> | undefined;
const texWaiting = new Set<() => void>();
const texNow = () => tex;
function texSubscribe(f: () => void) {
  texWaiting.add(f);
  return () => void texWaiting.delete(f);
}
function loadTex() {
  texLoading ??= Promise.all([import("katex"), import("katex/dist/katex.min.css")])
    .then(([{ default: katex }]) => {
      tex = (s, displayMode) =>
        katex
          // What KaTeX can't read shows as its source in the theme's red,
          // without the native tooltip that names the error.
          .renderToString(s, { displayMode, throwOnError: false, errorColor: "var(--destructive-foreground)" })
          .replace(/(<span class="katex-error") title="[^"]*"/g, "$1");
      for (const f of texWaiting) f();
    })
    .catch(() => {
      // The math stays as its source; another reply with math tries again.
      texLoading = undefined;
    });
}

// Replies drawn before, by their words: a long chat's rows come and go as
// it scrolls, and one scrolled back to draws without parsing again. Drafts
// change with every read, so they aren't kept, nor is math drawn before
// KaTeX was there.
const drawn = new Map<string, string>();
const DRAWN_MAX = 300;

// The code frames, wide tables and callouts micromark's HTML gets.
const shaped = (html: string) =>
  html
    // Wide tables scroll on their own instead of widening the column.
    .replace(/<table>/g, '<div class="cv-table"><table>')
    .replace(/<\/table>/g, "</table></div>")
    .replace(/<pre><code((?: class="language-([^"]+)")?)>([\s\S]*?)<\/code><\/pre>/g, codeBlock)
    .replace(CALLOUT, callout)
    .replace(/<img\b[^>]*>/g, (tag) => imageSlots(tag));

// A draft's code blocks name no language: its screen doesn't say, and
// a guess would change when the message lands.
const shapedDraft = (html: string) => caretAfter(shaped(html).replace(/<span class="cv-pre-lang">[^<]*<\/span>/g, '<span class="cv-pre-lang"></span>'), CARET);

function toHtml(text: string, draft: boolean, render: TexRender | undefined): { html: string; texWanted: boolean } {
  const kept = draft ? undefined : drawn.get(text);
  if (kept !== undefined) {
    // Most recently used last, so the oldest go first.
    drawn.delete(text);
    drawn.set(text, kept);
    return { html: kept, texWanted: false };
  }
  const out = markdownHtml(text, render, draft ? shapedDraft : shaped);
  const texWanted = out.math && !render;
  if (draft || texWanted) return { html: out.html, texWanted };
  drawn.set(text, out.html);
  if (drawn.size > DRAWN_MAX) drawn.delete(drawn.keys().next().value as string);
  return { html: out.html, texWanted };
}

export const Markdown = memo(function Markdown({ text, copy = true, draft = false, clipped = false }: { text: string; copy?: boolean; draft?: boolean; clipped?: boolean }) {
  const render = useSyncExternalStore(texSubscribe, texNow);
  const { html, texWanted } = useMemo(() => toHtml(text, draft, render), [text, draft, render]);
  useEffect(() => {
    if (texWanted) loadTex();
  }, [texWanted]);
  const body = useRef<HTMLDivElement>(null);
  useCodeHighlight(body, html);
  const links = useFileLinks(body, html, draft);
  useRepoRefs(body, html, draft);
  return (
    <div className="group/md relative cv-in" data-draft={draft ? "" : undefined} aria-busy={draft || undefined} aria-description={draft ? "Still being written: as the agent's screen shows it" : undefined}>
      {draft && clipped && (
        <p className="cv-clipped">
          <span aria-hidden>…</span> Its start is above the agent's screen: the whole reply shows once it's written
        </p>
      )}
      <div
        ref={body}
        className="cv-md"
        data-selectable
        // biome-ignore lint/security/noDangerouslySetInnerHtml: micromark escapes HTML in the source.
        dangerouslySetInnerHTML={{ __html: html }}
        onPointerOver={links?.onPointerOver}
        onPointerOut={links?.onPointerOut}
        onFocus={links?.onFocus}
        onBlur={links?.onBlur}
        onKeyDown={links?.onKeyDown}
        onClick={(e) => {
          if (links?.click(e)) return;
          const el = e.target as HTMLElement;
          const code = el.closest<HTMLElement>("[data-copy-code]");
          if (code) {
            const pre = code.closest(".cv-pre")?.querySelector("pre");
            if (pre) void copyText(pre.textContent ?? "", "Copied the code");
            // "Copied" for a moment, where the pointer is.
            code.dataset.copied = "";
            window.clearTimeout(Number(code.dataset.timer));
            code.dataset.timer = String(window.setTimeout(() => delete code.dataset.copied, 1600));
            return;
          }
          const a = el.closest("a");
          const href = a?.getAttribute("href");
          if (!href) return;
          e.preventDefault();
          if (/^https?:\/\//.test(href)) void openUrl(href);
        }}
      />
      <InlineImages body={body} html={html} draft={draft} />
      <TableExplorers body={body} html={html} draft={draft} />
      <TestBlocks body={body} html={html} draft={draft} />
      {/* Mermaid flowcharts and diffs, drawn into their blocks. */}
      <CodeBlocks body={body} html={html} draft={draft} />
      {copy && (
        <div className={cn("mt-1 flex h-6 opacity-0 transition-opacity focus-within:opacity-100 group-hover/md:opacity-100", draft && "invisible")} aria-hidden={draft || undefined}>
          <Tip label="Copy as Markdown">
            <button type="button" aria-label="Copy reply" onClick={() => void copyText(text, "Copied the reply")} className="inline-flex size-6 items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-accent hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring">
              <CopyIcon className="size-3.5" />
            </button>
          </Tip>
        </div>
      )}
    </div>
  );
});
