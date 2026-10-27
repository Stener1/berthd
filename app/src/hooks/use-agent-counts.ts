import { useMemo } from "react";

import type { Session, Status } from "@/lib/api";
import { offBoard, type SessionState, sessionState } from "@/lib/derive";
import { lastOf } from "@/lib/last-of";
import { useBoardHidden } from "@/lib/project-groups";
import { type BoxData, useStore } from "@/lib/store";

export interface SessionEntry {
  box: string;
  session: Session;
  state: SessionState;
}

// useAllSessions lists every session on every online box with its state.
// Every caller gets the same list for the same store (Home has a dozen), so
// what is derived from it can be worked out once (lastOf).
export function useAllSessions(): SessionEntry[] {
  const boxes = useStore((s) => s.boxes);
  const status = useStore((s) => s.status);
  return allSessions(boxes, status);
}

const allSessions = lastOf((boxes: Record<string, BoxData>, status: Status | undefined): SessionEntry[] => {
  const online = new Set(status?.boxes.filter((b) => b.state === "online").map((b) => b.name));
  return Object.entries(boxes)
    .filter(([box]) => online.has(box))
    .flatMap(([box, d]) => (d.sessions ?? []).map((session) => ({ box, session, state: sessionState(session, d.stats) })));
});

// useBoardSessions is useAllSessions without the projects kept off the
// Agent Dashboard (Settings → Agents): the board and its counts.
export function useBoardSessions(): SessionEntry[] {
  const all = useAllSessions();
  const boxes = useStore((s) => s.boxes);
  const hidden = useBoardHidden();
  return useMemo(() => (hidden.size ? all.filter((e) => !offBoard(boxes[e.box]?.locations, e.box, e.session, hidden)) : all), [all, boxes, hidden]);
}

// useAgentCounts is the board's counts, for the sidebar and the status bar.
export function useAgentCounts() {
  const all = useBoardSessions();
  return useMemo(
    () => ({
      waiting: all.filter((e) => e.state === "waiting").length,
      running: all.filter((e) => e.state === "running").length,
      finished: all.filter((e) => e.state === "finished").length,
    }),
    [all],
  );
}
