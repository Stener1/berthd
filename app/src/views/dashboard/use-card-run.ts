import { type RefObject, useEffect, useMemo, useState } from "react";

import type { SessionEntry } from "@/hooks/use-agent-counts";
import { isMock } from "@/hooks/use-berth-connection";
import { runOf, type Run } from "@/lib/agent-run";
import { useChatSignals } from "@/lib/chat-controls";
import { keyOf, useConversations } from "@/lib/conversation-store";
import { worktreeOf } from "@/lib/derive";
import { useStore } from "@/lib/store";
import { useTranscriptFeed } from "@/lib/transcript-feed";

const mockConversation = () => import("@/lib/mock-conversation");

// useCardRun is a card's turn, read from the agent's record the way its
// chat reads it (lib/transcript-feed), so a chat opened from the card has
// it already. Only while the card is on screen: a board of finished agents
// scrolled away reads nothing. An agent without a record (a shell, a box
// too old to send one) has no run; the card shows its screen instead.
export function useCardRun(entry: SessionEntry, card: RefObject<HTMLElement | null>): { run?: Run; model?: string } {
  const { box, session, state } = entry;
  const mock = isMock();
  const key = keyOf(box, session.name);
  const shown = useOnScreen(card);
  const live = state === "running" || state === "waiting" || state === "finished";
  // A working agent's card follows it as its chat does; one that waits or
  // is done reads on its events, and only every 30s besides.
  useTranscriptFeed(box, session.name, session.dir, live && shown && !mock, 0, state === "running" ? undefined : 30_000);
  const locations = useStore((s) => s.boxes[box]?.locations);

  // The demo makes up the same conversation its chat would.
  useEffect(() => {
    if (!mock || !live || useConversations.getState().items[key]) return;
    const wt = worktreeOf(locations, session);
    void mockConversation().then((m) => {
      if (!useConversations.getState().items[key]) m.seedTranscript(box, session.name, state, wt ? (wt.worktree.main ? wt.location.name : wt.worktree.name) : session.name);
    });
  }, [mock, live, key, box, session, state, locations]);

  const items = useConversations((s) => s.items[key]);
  const wrote = useConversations((s) => s.last[key]);
  const signals = useChatSignals(box, session.name);
  const since = Date.parse(session.state_since ?? session.created) || 0;
  // Its turn ended on work that runs without it (turn check): nothing of
  // its own is under way.
  const working = state === "running" && !session.unfinished;
  const run = useMemo(() => (live && items?.length ? runOf(items, signals?.todos, { working, waiting: state === "waiting", since, wrote }) : undefined), [live, items, signals?.todos, working, state, since, wrote]);
  return { run, model: signals?.model };
}

function useOnScreen(ref: RefObject<HTMLElement | null>): boolean {
  const [on, setOn] = useState(false);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof IntersectionObserver === "undefined") {
      setOn(true);
      return;
    }
    const io = new IntersectionObserver(([e]) => setOn(e.isIntersecting), { rootMargin: "200px" });
    io.observe(el);
    return () => io.disconnect();
  }, [ref]);
  return on;
}
