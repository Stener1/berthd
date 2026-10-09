import { DitherBand } from "@/components/art/dither-band";
import { HARBOUR, HARBOUR_MUTE, useHarbourLight } from "@/components/art/harbour-art";
import { Scene } from "@/components/art/scenes";
import { TaskComposer } from "@/components/conversation/task-composer";
import { cn } from "@/lib/utils";
import { usePrefs } from "@/lib/prefs";
import { HomeGrid } from "@/views/home/widgets/grid";
import { TeamSuggestCards } from "@/views/home/team-suggest-card";

// HomeView is the workspace before any worktree is open: one composer to
// start work (lib/composer), then the person's own grid of widgets
// (views/home/widgets): the agents that need them, pull requests, their
// boxes, whatever they put there. Labs puts the harbour across the top,
// dissolving into the page, with the composer at the seam.
const BAND = "clamp(220px, 50vh, 500px)";

export function HomeView() {
  const labs = usePrefs((p) => p.labs);
  const light = useHarbourLight();
  return (
    <div className="absolute inset-0 overflow-y-auto bg-background">
      <div className="relative min-h-full">
        {labs && (
          <div aria-hidden className="absolute inset-x-0 top-0" style={{ height: BAND }}>
            <DitherBand src={HARBOUR[light]} position={0.42} fade={0.45} mute={HARBOUR_MUTE[light]} className="size-full" />
          </div>
        )}
        <div className="relative mx-auto flex w-full max-w-[640px] flex-col items-center px-6 pb-10" style={{ paddingTop: labs ? `calc(${BAND} - 84px)` : "clamp(48px, 16vh, 160px)" }}>
          {!labs && (
            <div aria-hidden className="mb-2">
              <Scene name="dawn" width={136} />
            </div>
          )}
          <h1 className={cn("mb-4 text-balance text-center font-heading font-semibold text-2xl tracking-tight", labs && "[text-shadow:0_0_6px_var(--background),0_0_16px_var(--background)]")}>What should your agents work on?</h1>
          <TaskComposer autoFocus />
          <TeamSuggestCards />
        </div>
        <div className="relative mx-auto w-full max-w-[1180px] px-6 pb-12">
          <HomeGrid />
        </div>
      </div>
    </div>
  );
}
