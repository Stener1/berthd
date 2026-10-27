import { definePlugin, type ScreenProps } from "@berth/plugin";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle, Icon } from "@berth/plugin/ui";

import { EndpointsPanel } from "./endpoints";
import { LogsPanel } from "./logs";
import { QueriesPanel } from "./queries";
import { LeadlineScreen } from "./screen";
import { TracesPanel } from "./traces";

// Leadline: what your code did in development, as tabs on a worktree. A
// helper on the box (berth-leadline, which this plugin's manifest carries)
// records every database query, each request's trace and the logs of the
// worktree's dev servers; the panels read it through the box. Nothing it
// records leaves the box except to be shown here.

export default definePlugin((berth) => {
  // A Shipyard from before plugins could carry a helper: say so, add nothing else.
  if (!berth.helper) {
    berth.addScreen({ id: "leadline", title: "Leadline", Component: TooOld });
    berth.addSidebarItem({ id: "leadline", title: "Leadline", icon: "Anchor", screen: "leadline" });
    return;
  }
  berth.addWorktreePanel({ id: "queries", title: "Queries", icon: "Database", Component: QueriesPanel });
  berth.addWorktreePanel({ id: "traces", title: "Traces", icon: "ChartGantt", Component: TracesPanel });
  berth.addWorktreePanel({ id: "logs", title: "Logs", icon: "ScrollText", Component: LogsPanel });
  berth.addWorktreePanel({ id: "endpoints", title: "Endpoints", icon: "Route", Component: EndpointsPanel });
  berth.addScreen({ id: "leadline", title: "Leadline", description: "What your code did in development: queries, traces and logs, recorded on each box.", Component: LeadlineScreen });
  berth.addSidebarItem({ id: "leadline", title: "Leadline", icon: "Anchor", screen: "leadline" });
  berth.addCommand({ id: "open", title: "Show Leadline", group: "Leadline", run: () => berth.openScreen("leadline") });
  berth.addCommand({ id: "queries", title: "Show this worktree's queries", group: "Leadline", run: () => berth.openPanel("queries") });
  berth.addCommand({ id: "traces", title: "Show this worktree's traces", group: "Leadline", run: () => berth.openPanel("traces") });
  berth.addCommand({ id: "logs", title: "Show this worktree's logs", group: "Leadline", run: () => berth.openPanel("logs") });
  berth.addCommand({ id: "endpoints", title: "Show this worktree's endpoints", group: "Leadline", run: () => berth.openPanel("endpoints") });
});

function TooOld(_: ScreenProps) {
  return (
    <Empty className="py-16">
      <EmptyHeader>
        <Icon name="Anchor" className="mx-auto mb-2 size-5 text-muted-foreground" />
        <EmptyTitle>Leadline needs a newer Shipyard</EmptyTitle>
        <EmptyDescription>This version can't install a plugin's program on a box. Update Shipyard, then reload plugins in Settings → Plugins.</EmptyDescription>
      </EmptyHeader>
    </Empty>
  );
}
