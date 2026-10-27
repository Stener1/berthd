import { createContext, type ReactNode, type RefObject, useContext, useLayoutEffect } from "react";

import { findRefs, QUICK, type RepoRef, wholeRef } from "@/lib/repo-refs";

// The issues, pull requests and commits a reply names link to GitHub, in a
// chat whose worktree's origin is there (lib/repo-refs.ts says what counts).
//
// A reply is HTML drawn once and kept by its words (markdown.tsx), and which
// repository #123 means is the chat's, so the links are made in the page
// after it draws: in its text and in inline code that is a ref and nothing
// else, never in a code block, a link or a heading's marker. They are plain
// links, so the reply's own click opens them outside.

const Repo = createContext<string | undefined>(undefined);

// RepoRefs gives its chat's replies the GitHub repository ("owner/name")
// their refs belong to; with none, they stay text.
export function RepoRefs({ repo, children }: { repo?: string; children: ReactNode }) {
  return <Repo value={repo}>{children}</Repo>;
}

// Where a ref isn't made: links (made or written), code blocks, other
// inline code (whole refs are linked apart), buttons, a callout's heading,
// a file the reply names, and a draft's caret.
const SKIP = "a, pre, code, button, .cv-callout-title, [data-file-ref], .cv-caret";

// useRepoRefs links the refs in the reply drawn in root, once it has drawn,
// and again when the chat's repository changes. A draft is left as it is:
// a half-written #12 would link to the wrong issue for a moment.
export function useRepoRefs(root: RefObject<HTMLElement | null>, html: string, draft: boolean) {
  const repo = useContext(Repo);
  useLayoutEffect(() => {
    const el = root.current;
    if (!el) return;
    unlink(el);
    if (repo && !draft && QUICK.test(el.textContent ?? "")) link(el, repo);
  }, [root, html, draft, repo]);
}

function anchor(ref: RepoRef): HTMLAnchorElement {
  const a = document.createElement("a");
  a.href = ref.url;
  a.className = "cv-ref";
  a.dataset.repoRef = ref.kind;
  return a;
}

function link(root: HTMLElement, repo: string) {
  const outside = (n: Node) => {
    const hit = n.parentElement?.closest(SKIP);
    return !hit || !root.contains(hit);
  };
  for (const code of root.querySelectorAll("code")) {
    if (code.hasAttribute("data-file-ref") || !outside(code)) continue;
    const ref = wholeRef(code.textContent ?? "", repo);
    if (!ref) continue;
    const a = anchor(ref);
    code.replaceWith(a);
    a.append(code);
  }
  const texts: Text[] = [];
  const walk = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  for (let n = walk.nextNode(); n; n = walk.nextNode()) if (outside(n)) texts.push(n as Text);
  for (const text of texts) {
    const refs = findRefs(text.data, repo);
    if (!refs.length) continue;
    const parts = document.createDocumentFragment();
    let at = 0;
    for (const ref of refs) {
      if (ref.start > at) parts.append(text.data.slice(at, ref.start));
      const a = anchor(ref);
      a.textContent = text.data.slice(ref.start, ref.end);
      // A hash in the text reads as code, as it would in inline code.
      if (ref.kind === "commit") a.classList.add("cv-ref-hash");
      parts.append(a);
      at = ref.end;
    }
    if (at < text.data.length) parts.append(text.data.slice(at));
    text.replaceWith(parts);
  }
}

// unlink takes back the links made before, for a repository that changed.
function unlink(root: HTMLElement) {
  const made = root.querySelectorAll("a.cv-ref");
  if (!made.length) return;
  for (const a of made) a.replaceWith(...a.childNodes);
  root.normalize();
}
