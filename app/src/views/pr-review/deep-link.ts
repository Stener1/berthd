import { useEffect } from "react";

import { isTauri } from "@/lib/api";
import { openInvalidReviewLink, openReviewSheet } from "@/lib/pr-review";
import { parseReviewLink } from "@/lib/review-link";

// A review link someone pasted in a PR or a chat, berth://review?repo=…&pr=…,
// opens the review sheet, and only the sheet: nothing is fetched from the
// PR or run until Review. A link with anything else in it opens a sheet
// that says so. In the browser build ?review-link=<link> does the same, for
// trying it out.

export function openReviewLink(url: string) {
  if (!/^berth:\/\/review\b/i.test(url.trim())) return;
  const ref = parseReviewLink(url);
  if (ref) openReviewSheet(ref);
  else openInvalidReviewLink(url);
}

function takeParam(name: string): string | null {
  const params = new URLSearchParams(window.location.search);
  const v = params.get(name);
  if (v === null) return null;
  params.delete(name);
  const rest = params.toString();
  window.history.replaceState(null, "", `${window.location.pathname}${rest ? `?${rest}` : ""}${window.location.hash}`);
  return v;
}

export function useReviewDeepLinks() {
  useEffect(() => {
    const link = takeParam("review-link");
    if (link !== null) openReviewLink(link.startsWith("berth://") ? link : `berth://review?${link}`);
    if (!isTauri()) return;
    let stop: (() => void) | undefined;
    let cancelled = false;
    void (async () => {
      try {
        const dl = await import("@tauri-apps/plugin-deep-link");
        for (const u of (await dl.getCurrent()) ?? []) openReviewLink(u);
        const unlisten = await dl.onOpenUrl((urls) => urls.forEach(openReviewLink));
        if (cancelled) unlisten();
        else stop = unlisten;
      } catch (err) {
        console.warn("review links are not available", err);
      }
    })();
    return () => {
      cancelled = true;
      stop?.();
    };
  }, []);
}
