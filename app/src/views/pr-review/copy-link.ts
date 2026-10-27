import { toastManager } from "@/components/ui/toast";
import type { Location, Worktree } from "@/lib/api";
import { linkFor, reviewOf } from "@/lib/pr-review";
import { parseReviewRef, reviewLink } from "@/lib/review-link";
import { useStore } from "@/lib/store";

// Copy review link: the berth://review link for a worktree's PR, to paste in
// the PR's description or a chat, so a teammate opens it for review on their
// own box. A review worktree knows its PR; any other asks gh on the box which
// PR its branch has.

function copied(link: string) {
  void navigator.clipboard.writeText(link);
  toastManager.add({
    title: "Copied the review link",
    description: link,
    type: "success",
  });
}

// open, when given, makes the link open the review logged in as a dev user
// (as) at a page (path); the default link has neither.
export async function copyReviewLink(box: string, loc: Location, wt: Worktree, open: { as?: string; path?: string } = {}) {
  const r = reviewOf(wt);
  if (r) return copied(linkFor(r, open));
  const client = useStore.getState().client;
  if (!client) return;
  try {
    const res = await client.box<{ exit_code: number; output: string }>(box, "POST", "exec", {
      location: wt.main ? loc.name : `${loc.name}/${wt.name}`,
      command: "gh pr view --json number,url 2>/dev/null",
      timeout: "30s",
    });
    const pr = res.exit_code === 0 ? (JSON.parse(res.output) as { number?: number; url?: string }) : undefined;
    // The repository is the PR's own, from its URL; else the project's.
    const repo = /^https:\/\/github\.com\/([^/]+\/[^/]+)\/pull\/\d+/.exec(pr?.url ?? "")?.[1] ?? loc.slug;
    const ref = pr?.number && repo ? parseReviewRef(`${repo}#${pr.number}`) : undefined;
    if (!ref) {
      toastManager.add({
        title: "No PR for this branch",
        description: wt.branch ? `${wt.branch} has no pull request on GitHub yet.` : undefined,
        type: "info",
      });
      return;
    }
    copied(reviewLink(ref.repo, ref.pr, open));
  } catch {
    toastManager.add({
      title: "No PR for this branch",
      description: "gh on the box couldn't find one.",
      type: "info",
    });
  }
}
