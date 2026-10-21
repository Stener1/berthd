import { ArrowLeftIcon, ArrowRightIcon } from "lucide-react";

import { ink } from "@/components/sidebar/ink";
import { Tip } from "@/components/tip";
import { go, useCanGo } from "@/lib/nav-history";
import { keysFor } from "@/lib/shortcuts";
import { cn } from "@/lib/utils";

// BackForward is the window strip's Back and Forward (lib/nav-history.ts),
// beside the sidebar's own controls. Each is disabled with nowhere to go.
export function BackForward() {
  return (
    <div role="group" aria-label="History" className="flex items-center">
      <Arrow dir={-1} id="back" label="Back" icon={<ArrowLeftIcon strokeWidth={1.8} />} />
      <Arrow dir={1} id="forward" label="Forward" icon={<ArrowRightIcon strokeWidth={1.8} />} />
    </div>
  );
}

function Arrow({ dir, id, label, icon }: { dir: -1 | 1; id: string; label: string; icon: React.ReactNode }) {
  const can = useCanGo(dir);
  return (
    <Tip label={`${label} (${keysFor(id)})`} side="bottom">
      <button
        type="button"
        aria-label={label}
        data-testid={`history-${id}`}
        disabled={!can}
        onClick={() => go(dir)}
        className={cn("inline-flex size-7 items-center justify-center rounded-lg hover:bg-sidebar-accent hover:text-foreground disabled:pointer-events-none disabled:opacity-40 [&_svg]:size-4", ink.glyph)}
      >
        {icon}
      </button>
    </Tip>
  );
}
