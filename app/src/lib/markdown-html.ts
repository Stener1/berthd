import { micromark } from "micromark";
import { gfm, gfmHtml } from "micromark-extension-gfm";
import { math } from "micromark-extension-math";
import type { Code, Construct, Extension, HtmlExtension, State, Token, TokenizeContext } from "micromark-util-types";

// A reply's Markdown as HTML, with its math: GFM, plus LaTeX as agents write
// it. Pure, so it can be tested (markdown-html.test.ts); the chat's
// components/conversation/markdown.tsx draws it.
//
// Math is $$…$$ (a block of its own lines, or within a line), \(…\) within
// a line and \[…\] for display. A single $ is left alone: replies talk
// about money ("$5 and $10") far more often than they write math with it.
// Code spans and code blocks keep their words as written, math or not:
// micromark reads them before any math.

declare module "micromark-util-types" {
  interface TokenTypeMap {
    texMath: "texMath";
    texMathSequence: "texMathSequence";
    texMathData: "texMathData";
  }
}

// Draws one formula as HTML; KaTeX in the app. Without one (KaTeX is still
// loading), a formula shows as its source.
export type TexRender = (tex: string, display: boolean) => string;

// What micromark wrote: the HTML, and whether there was any math in it.
export interface Drawn {
  html: string;
  math: boolean;
}

const isLineEnding = (code: Code) => code !== null && code < -2;
const BACKSLASH = 92;
const CLOSE: Record<number, number> = { 40: 41, 91: 93 }; // ( ) and [ ]

// \(…\) and \[…\], as micromark text: where a backslash escape would be,
// the same as $$…$$ is where text would be. A pair that doesn't close (a
// draft's, or one with a blank line in it) stays the escapes it was, so
// the text shows ( and [ until it does. Inside, a backslash takes the next
// character with it, as TeX reads it: \\) is a line break and a ).
const texText: Construct = {
  name: "texMath",
  tokenize(this: TokenizeContext, effects, ok, nok) {
    let close = 0;
    const start: State = (code) => {
      effects.enter("texMath");
      effects.enter("texMathSequence");
      effects.consume(code);
      return open;
    };
    const open: State = (code) => {
      if (code === null || !CLOSE[code]) return nok(code);
      close = CLOSE[code];
      effects.consume(code);
      effects.exit("texMathSequence");
      return between;
    };
    const between: State = (code) => {
      if (code === null) return nok(code);
      if (isLineEnding(code)) {
        effects.enter("lineEnding");
        effects.consume(code);
        effects.exit("lineEnding");
        return between;
      }
      effects.enter("texMathData");
      return data(code);
    };
    const data: State = (code) => {
      if (code === null || isLineEnding(code)) {
        effects.exit("texMathData");
        return between(code);
      }
      if (code === BACKSLASH) {
        effects.consume(code);
        return escaped;
      }
      effects.consume(code);
      return data;
    };
    const escaped: State = (code) => {
      if (code === close) {
        effects.exit("texMathData");
        effects.enter("texMathSequence");
        effects.consume(code);
        effects.exit("texMathSequence");
        effects.exit("texMath");
        return ok;
      }
      if (code === null || isLineEnding(code)) return data(code);
      effects.consume(code);
      return data;
    };
    return start;
  },
};

const syntax: Extension[] = [gfm(), math({ singleDollarTextMath: false }), { text: { [BACKSLASH]: texText } }];

// Each formula stands in the HTML as a mark until it's drawn, so what
// shapes the HTML after (the draft's caret, code frames) sees one word
// where it goes. Noncharacters, which no text carries.
const MARK = (n: number) => `﷐${n}﷑`;
const MARKS = /﷐(\d+)﷑/g;

const escapeHtml = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
const unescapeHtml = (s: string) => s.replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, '"').replace(/&amp;/g, "&");

// A code block of math: ```math, as GitHub writes a display, or ```latex
// and ```tex holding only equations. One that is a document (to compile
// with pdflatex, not to read here) stays code, as does one still open in
// a draft: a half-written formula would only show KaTeX's errors.
const MATH_LANGS = "math|latex|tex|katex";
const MATH_CODE = new RegExp(`<pre><code class="language-(${MATH_LANGS})">([\\s\\S]*?)<\\/code><\\/pre>`, "gi");
const MATH_FENCE_CLOSED = new RegExp(`^ {0,3}(\`{3,}|~{3,})[ \\t]*(?:${MATH_LANGS})\\b[^\\n]*\\n[\\s\\S]*?\\n {0,3}\\1[ \\t]*$`, "gim");
const TEX_DOCUMENT = /\\(documentclass|usepackage|begin\{document\}|section|subsection|title|maketitle|author|chapter|item)\b/;

interface Formula {
  tex: string;
  display: boolean;
  // How the reply wrote it, shown while KaTeX loads.
  source: string;
}

// mathHtml (micromark-extension-math's own) draws as it goes and throws on
// what KaTeX can't take; this one keeps each formula for later, and notes
// a $$ block that never closes.
function collect(formulas: Formula[], unclosed: number[]): HtmlExtension {
  let fences = 0;
  let fenceAt = -1;
  return {
    enter: {
      mathFlow() {
        this.lineEndingIfNeeded();
        fences = 0;
        fenceAt = -1;
      },
      mathFlowFenceSequence(token: Token) {
        if (fenceAt < 0) fenceAt = token.start.offset;
      },
      mathFlowFenceMeta() {
        this.buffer();
      },
      mathText() {
        this.buffer();
      },
      texMath() {
        this.buffer();
      },
    },
    exit: {
      mathFlowFence() {
        // After the opening fence, what follows (from its next line) is
        // the formula.
        if (++fences > 1) return;
        this.setData("slurpOneLineEnding", true);
        this.buffer();
      },
      mathFlowFenceMeta() {
        this.resume();
      },
      mathFlowValue(token: Token) {
        this.raw(this.sliceSerialize(token));
      },
      mathFlow(token: Token) {
        const tex = this.resume().replace(/(?:\r?\n|\r)$/, "");
        this.setData("slurpOneLineEnding");
        if (fences < 2) unclosed.push(fenceAt);
        formulas.push({ tex, display: true, source: this.sliceSerialize(token) });
        this.tag(`<div class="cv-math-block">${MARK(formulas.length - 1)}</div>`);
      },
      mathText(token: Token) {
        this.resume();
        const source = this.sliceSerialize(token);
        formulas.push({ tex: source.replace(/^\$+|\$+$/g, ""), display: false, source });
        this.raw(MARK(formulas.length - 1));
      },
      texMath(token: Token) {
        this.resume();
        const source = this.sliceSerialize(token);
        formulas.push({ tex: source.slice(2, -2), display: source[1] === "[", source });
        this.raw(MARK(formulas.length - 1));
      },
    },
  };
}

// markdownHtml writes the reply as HTML. shape reworks micromark's HTML
// before the math goes in (code frames, a draft's caret). tex draws the
// math; it's left out until KaTeX loads, and the math shows as its source.
export function markdownHtml(text: string, tex?: TexRender, shape: (html: string) => string = (h) => h): Drawn {
  let source = text;
  let formulas: Formula[] = [];
  let html = "";
  // A $$ that never closes would take the rest of the reply into its block
  // (a draft's, until the agent writes its end): it stays text instead.
  // Each pass escapes one, so this ends.
  for (;;) {
    const unclosed: number[] = [];
    formulas = [];
    html = micromark(source, { extensions: syntax, htmlExtensions: [gfmHtml(), collect(formulas, unclosed)] });
    if (!unclosed.length || unclosed[0] < 0) break;
    source = `${source.slice(0, unclosed[0])}\\${source.slice(unclosed[0])}`;
  }
  let closed = (source.match(MATH_FENCE_CLOSED) ?? []).length;
  html = html.replace(MATH_CODE, (all, lang: string, body: string) => {
    if (closed-- <= 0) return all;
    const src = unescapeHtml(body).replace(/\n$/, "");
    if (!src.trim() || (lang.toLowerCase() !== "math" && TEX_DOCUMENT.test(src))) return all;
    formulas.push({ tex: src, display: true, source: `\`\`\`${lang}\n${src}\n\`\`\`` });
    return `<div class="cv-math-block">${MARK(formulas.length - 1)}</div>`;
  });
  if (!formulas.length) return { html: shape(html), math: false };
  // $$…$$ on a line of its own is a display, as a $$ block is.
  html = html.replace(/<p>﷐(\d+)﷑<\/p>/g, (all, n) => {
    const f = formulas[Number(n)];
    if (f && f.source.startsWith("$$")) f.display = true;
    return all;
  });
  html = shape(html).replace(MARKS, (all, n) => {
    const f = formulas[Number(n)];
    if (!f) return all;
    const cls = f.display ? "cv-math cv-math-display" : "cv-math";
    return `<span class="${cls}">${drawTex(f, tex)}</span>`;
  });
  return { html, math: true };
}

// KaTeX shows what it can't read as its source, in its error colour
// (throwOnError: false, where the app sets it up); anything else it throws
// shows the source too.
function drawTex(f: Formula, tex: TexRender | undefined): string {
  if (tex) {
    try {
      return tex(f.tex, f.display);
    } catch {
      // As the source, below.
    }
  }
  return `<span class="cv-math-source">${escapeHtml(f.source)}</span>`;
}
