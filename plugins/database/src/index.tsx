import { definePlugin, type ScreenProps } from "@berth/plugin";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle, Icon } from "@berth/plugin/ui";

import { DatabasePanel } from "./panel";
import { DatabaseScreen } from "./screen";

// Database: a worktree's own development database, as a tab on the
// worktree. A helper on the box (berth-database, which this plugin's
// manifest carries) notes the database each worktree's services are started
// with, and runs what the panel asks of it: the tables, a page of rows, a
// statement, an edit. It opens read-only; writes are unlocked for fifteen
// minutes at a time. Which engine a connection is, and what that engine can
// do, comes from the helper with the connection (src/engines).

export default definePlugin((berth) => {
  // A Shipyard from before plugins could carry a helper: say so, add nothing else.
  if (!berth.helper) {
    berth.addScreen({ id: "database", title: "Database", Component: TooOld });
    berth.addSidebarItem({ id: "database", title: "Database", icon: "table-2", screen: "database" });
    return;
  }
  berth.addWorktreePanel({ id: "database", title: "Database", icon: "table-2", Component: DatabasePanel });
  berth.addScreen({ id: "database", title: "Database", description: "A worktree's own development database: tables, rows and a SQL console, read-only until you unlock it.", Component: DatabaseScreen });
  berth.addSidebarItem({ id: "database", title: "Database", icon: "table-2", screen: "database" });
  berth.addCommand({ id: "open", title: "Show Database", group: "Database", run: () => berth.openScreen("database") });
  berth.addCommand({ id: "panel", title: "Show this worktree's database", group: "Database", run: () => berth.openPanel("database") });
});

function TooOld(_: ScreenProps) {
  return (
    <Empty className="py-16">
      <EmptyHeader>
        <Icon name="table-2" className="mx-auto mb-2 size-5 text-muted-foreground" />
        <EmptyTitle>Database needs a newer Shipyard</EmptyTitle>
        <EmptyDescription>This version can't install a plugin's program on a box. Update Shipyard, then reload plugins in Settings → Plugins.</EmptyDescription>
      </EmptyHeader>
    </Empty>
  );
}
