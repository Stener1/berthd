import { type RefObject, useEffect, useLayoutEffect } from "react";

import { loadDiffs } from "@/components/diff/load";
import { useActiveTheme } from "@/hooks/use-theme";
import { readableTokens } from "@/lib/syntax-colors";
import { syntaxThemes } from "@/themes/apply";
import { mix, over } from "@/themes/color";

// Code blocks in an agent's reply, coloured as the diffs are: the theme's
// own Shiki theme, tokenized in the diff renderer's workers. Nothing loads
// until a reply with a fenced block shows. Only the colours change, never
// the text or its box: the block is the same <code> with spans in it, so
// it selects, copies and sizes as before, and nothing moves when they
// arrive.

// Longer blocks stay plain: tokenizing them costs more than it gives.
const MAX_LINES = 2000;
// While a reply streams in, its last block changes; wait for a pause.
const SETTLE = 250;

// Highlighted blocks by theme, language and text, so a chat scrolled back
// over, or a reply drawn again, is coloured at once.
const cache = new Map<string, string | null>();
const CACHE_MAX = 400;

function remember(key: string, html: string | null) {
  cache.delete(key);
  cache.set(key, html);
  if (cache.size > CACHE_MAX) cache.delete(cache.keys().next().value as string);
}

// Fence names Shiki doesn't know by that name.
const ALIASES: Record<string, string> = { sh: "bash", shell: "bash", zsh: "bash", console: "shellsession", golang: "go", patch: "diff", yml: "yaml", "c++": "cpp", tsx: "tsx", plaintext: "text", txt: "text" };

// guess names the language of a block whose fence doesn't: only the
// unmistakable ones, plain text otherwise.
export function guessLanguage(code: string): string {
  const t = code.trim();
  if (/^(diff --git |--- a\/|@@ -\d)/m.test(t) && /^[+-]/m.test(t)) return "diff";
  if (/^[[{]/.test(t)) {
    try {
      JSON.parse(t);
      return "json";
    } catch {}
  }
  if (/^\$ /m.test(t)) return "shellsession";
  if (/^#!.*\b(ba|z)?sh\b/.test(t) || /^(cd|git|npm|pnpm|yarn|brew|curl|make|go (run|build|test)|export \w+=|sudo) /m.test(t)) return "bash";
  if (/^package \w+$/m.test(t) && /\bfunc\b/.test(t)) return "go";
  if (/^(import|export) .+ from ["']/m.test(t) || /^(interface|type) \w+.*[={]/m.test(t)) return "typescript";
  if (/^(def|class) \w+.*:$/m.test(t) || /^from \w+ import /m.test(t)) return "python";
  if (/^\s*fn \w+|^use \w+(::\w+)+;/m.test(t)) return "rust";
  return "text";
}

// languageName is how a code block's head names its language: as people
// write it, or the fence's own word for one not listed.
const NAMES: Record<string, string> = {
  ts: "TypeScript", typescript: "TypeScript", tsx: "TSX", js: "JavaScript", javascript: "JavaScript", jsx: "JSX", mjs: "JavaScript",
  sh: "Shell", bash: "Shell", zsh: "Shell", shell: "Shell", console: "Shell", shellsession: "Shell", fish: "Fish",
  go: "Go", golang: "Go", rs: "Rust", rust: "Rust", py: "Python", python: "Python", rb: "Ruby", ruby: "Ruby", swift: "Swift", kotlin: "Kotlin", java: "Java",
  c: "C", cpp: "C++", "c++": "C++", cs: "C#", csharp: "C#", php: "PHP", lua: "Lua", sql: "SQL", graphql: "GraphQL",
  json: "JSON", jsonc: "JSON", yaml: "YAML", yml: "YAML", toml: "TOML", xml: "XML", html: "HTML", css: "CSS", scss: "SCSS",
  md: "Markdown", markdown: "Markdown", mermaid: "Mermaid", diff: "Diff", patch: "Diff", dockerfile: "Dockerfile", docker: "Dockerfile", makefile: "Makefile", nix: "Nix",
  text: "Text", txt: "Text", plaintext: "Text",
};
export function languageName(lang: string): string {
  return NAMES[lang] ?? lang.replace(/[<>&"]/g, "");
}

function languageOf(code: HTMLElement, text: string): string {
  const named = /\blanguage-([\w#+.-]+)/.exec(code.className)?.[1]?.toLowerCase();
  if (!named) return guessLanguage(text);
  return ALIASES[named] ?? named;
}

// useCodeHighlight colours the code blocks under ref whenever its HTML (or
// the theme) changes.
export function useCodeHighlight(ref: RefObject<HTMLElement | null>, html: string) {
  const theme = useActiveTheme();
  const { dark, light } = syntaxThemes(theme);
  // A block's background (conversation.css: the muted wash at 60% over the
  // page), which every token colour is made to read on, 4.5:1.
  const bg = mix(over(theme.colors.muted, theme.colors.background), theme.colors.background, 0.6);
  const themeKey = `${dark}|${light}|${bg}`;

  // Blocks already done in this theme colour before paint, no flash.
  useLayoutEffect(() => {
    for (const { el, key } of blocks(ref.current, themeKey)) {
      const done = cache.get(key);
      if (done) el.innerHTML = done;
    }
  }, [ref, html, themeKey]);

  useEffect(() => {
    const todo = blocks(ref.current, themeKey).filter(({ key }) => !cache.has(key));
    if (!todo.length) return;
    let live = true;
    const timer = window.setTimeout(async () => {
      const mod = await loadDiffs().catch(() => undefined);
      for (const { el, key, text, lang } of todo) {
        if (!live || !mod) return;
        const raw = await mod.highlightCode(text, lang, { dark, light }).catch(() => undefined);
        const out = raw && readableTokens(raw, bg);
        remember(key, out ?? null);
        // The block may have been drawn again, or changed, meanwhile.
        if (live && out && el.isConnected && el.textContent === text) el.innerHTML = out;
      }
    }, SETTLE);
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [ref, html, themeKey, dark, light, bg]);
}

function blocks(root: HTMLElement | null, themeKey: string) {
  if (!root) return [];
  return [...root.querySelectorAll<HTMLElement>("pre > code")].flatMap((el) => {
    const text = el.textContent ?? "";
    if (!text.trim() || text.split("\n").length > MAX_LINES) return [];
    const lang = languageOf(el, text);
    if (lang === "text") return [];
    return [{ el, text, lang, key: `${themeKey}|${lang}|${text}` }];
  });
}
