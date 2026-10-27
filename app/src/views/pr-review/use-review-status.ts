import { useEffect } from "react";

import { refreshReviewStatus, reviewOf, statusKey, usePrReview } from "@/lib/pr-review";
import { useStore } from "@/lib/store";

// Each review worktree's PR may move on: how many commits it gained since
// the review was opened, asked of GitHub through the laptop's gh now and
// then, and as soon as a new review worktree shows up.

const EVERY = 3 * 60_000;

function reviewWorktrees() {
  const out = [];
  for (const [box, data] of Object.entries(useStore.getState().boxes)) {
    for (const loc of data?.locations ?? []) {
      for (const wt of loc.worktrees ?? []) if (reviewOf(wt)) out.push({ box, loc, wt });
    }
  }
  return out;
}

// refreshAllReviewStatuses asks again for every review worktree.
export async function refreshAllReviewStatuses() {
  const c = useStore.getState().client;
  if (!c) return;
  await Promise.all(reviewWorktrees().map(({ box, loc, wt }) => refreshReviewStatus(c, box, loc, wt)));
}

export function useReviewStatuses() {
  const connected = useStore((s) => !!s.client);
  // The review worktrees' identities: a new one is asked about at once.
  const keys = useStore((s) => {
    const out: string[] = [];
    for (const [box, data] of Object.entries(s.boxes))
      for (const loc of data?.locations ?? []) for (const wt of loc.worktrees ?? []) if (reviewOf(wt)) out.push(`${statusKey(box, wt.path)}@${reviewOf(wt)!.sha}`);
    return out.join("|");
  });
  useEffect(() => {
    if (!connected || !keys) return;
    void refreshAllReviewStatuses();
    const t = window.setInterval(() => void refreshAllReviewStatuses(), EVERY);
    return () => window.clearInterval(t);
  }, [connected, keys]);
}

export const useReviewStatus = (box: string, path: string) => usePrReview((s) => s.statuses[statusKey(box, path)]);
