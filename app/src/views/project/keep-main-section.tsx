import { useContext } from "react";

import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import type { RepoConfig } from "@/lib/flows";
import { KitLayer, Section, SourceBadge } from "@/views/project/parts";

// KeepMainSection is whether a new branch from the default branch fetches
// it first and fast-forwards the local one (box/freshbase.go). It is on
// unless the committed config or the kit turns it off; this box can turn it
// on or off for itself.
export function KeepMainSection({ repo, draft, setDraft, box }: { repo: RepoConfig | null; draft: RepoConfig; setDraft(c: RepoConfig): void; box: string }) {
  const kit = useContext(KitLayer);
  const below = repo?.keep_main;
  const inherited = below ?? true;
  const own = draft.keep_main;
  const on = own ?? inherited;
  const source = own !== undefined ? (below !== undefined ? "override" : "box") : below !== undefined ? (kit.config?.keep_main !== undefined ? "kit" : "repo") : undefined;
  return (
    <Section id="main" title="Main branch" description="New worktrees start from what origin has now, not from whenever this box last pulled.">
      <div className="flex items-center gap-4 px-4 py-3.5">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 text-row">
            Keep main up to date
            {source && <SourceBadge source={source} box={box} />}
          </div>
          <p className="mt-0.5 text-desc text-muted-foreground">
            Before a new branch is made from the default branch, Shipyard fetches it and fast-forwards the local one, so diffs against main compare with current history. A main checkout with uncommitted changes, or with commits origin doesn't have, is left as it is.
          </p>
        </div>
        {own !== undefined && below !== undefined ? (
          <Button size="xs" variant="ghost" onClick={() => setDraft({ ...draft, keep_main: undefined })}>
            Use the {kit.config?.keep_main !== undefined ? "kit's" : "repo's"} setting
          </Button>
        ) : null}
        <Switch checked={on} onCheckedChange={(v) => setDraft({ ...draft, keep_main: v === inherited ? undefined : v })} aria-label="Keep main up to date" data-testid="keep-main-switch" />
      </div>
    </Section>
  );
}
