import { type HomeWidgetProps, type LinearIssue, type LinearStatus, useWidgetData } from "@berth/plugin";
import { Icon, WidgetEmpty, WidgetRow, WidgetSkeleton, cn } from "@berth/plugin/ui";

import { PRIORITY_ICON, STATE_ICON, priorityRank } from "./linear";

// Home's Linear widget: what is assigned to you and not done, the most
// urgent first, read by the laptop with your API key and only while the
// widget is on screen.

interface Assigned {
  status: LinearStatus;
  issues: LinearIssue[];
}

export function LinearWidget({ berth, size }: HomeWidgetProps) {
  const { data, error, loading } = useWidgetData<Assigned>(
    "linear.assigned",
    async () => {
      const status = await berth.linear.status();
      return { status, issues: status.connected ? await berth.linear.issues({ mine: true }) : [] };
    },
    { every: 3 * 60_000 },
  );
  const tall = size === "l" || size === "t";
  if (!data && loading) return <WidgetSkeleton rows={tall ? 5 : 3} />;
  if (!data) return <WidgetEmpty scene="storm" title="Couldn't ask Linear" hint={error ?? "Linear didn't answer"} compact />;
  if (!data.status.connected) return <WidgetEmpty scene="anchor" title="Linear isn't connected" hint="Add an API key and what is assigned to you shows here." action="Open Settings" onAction={() => berth.openSettings("integrations")} />;
  if (!data.issues.length) return <WidgetEmpty scene="calm" title="Nothing assigned to you" hint="Issues Linear has for you show here." />;
  const issues = [...data.issues].sort((a, b) => priorityRank(a.priority) - priorityRank(b.priority) || b.updated_at.localeCompare(a.updated_at)).slice(0, tall ? 8 : 3);
  return (
    <div className="flex flex-col">
      {issues.map((i) => (
        <WidgetRow key={i.id} onClick={() => berth.openUrl(i.url)} label={`${i.title}, ${i.identifier}, ${i.state.name}`}>
          <span className="inline-flex shrink-0" style={{ color: i.state.color }}>
            <Icon name={STATE_ICON[i.state.type] ?? "Circle"} className="size-3.5" />
          </span>
          <span className="min-w-0 flex-1 truncate">{i.title}</span>
          {PRIORITY_ICON[i.priority] && (
            <span className={cn("inline-flex shrink-0", i.priority === 1 ? "text-warning-foreground" : "text-muted-foreground")}>
              <Icon name={PRIORITY_ICON[i.priority]} className="size-3.5" />
            </span>
          )}
          <span className="w-16 shrink-0 text-right font-mono text-[11px] text-muted-foreground @max-[300px]:hidden">{i.identifier}</span>
        </WidgetRow>
      ))}
    </div>
  );
}
