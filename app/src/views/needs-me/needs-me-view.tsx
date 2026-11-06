import { PinIcon } from "lucide-react";
import { useEffect, useMemo, useRef } from "react";

import { Scene } from "@/components/art/scenes";
import { Button } from "@/components/ui/button";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { Kbd } from "@/components/ui/kbd";
import { Spinner } from "@/components/ui/spinner";
import { laterList, leftLine, passLabel, type QueueItem, walkOrder } from "@/lib/needs-me";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { NeedsCard } from "@/views/needs-me/card-view";
import { IssueChips } from "@/views/needs-me/issue-chip";
import { bringBack, lookSoon, useNeedsMe, useNeedsMeStore, walkBack, walkOn, walkTo } from "@/views/needs-me/needs-me-store";
import { ViewHeader } from "@/views/view-header";

// Needs me: one piece of work fills the screen, and answering or closing it
// brings up the next. The list beside it is for jumping; you never need it.

const LEGEND = [
  [["1", "2", "3"], "pick"],
  [["J", "K"], "move"],
  [["E"], "read"],
  [["S"], "not now"],
  [["W"], "waiting on"],
  [["D"], "close"],
] as const;

export function NeedsMeView() {
  const items = useNeedsMe();
  const current = useNeedsMeStore((s) => s.current);
  const skipped = useNeedsMeStore((s) => s.skipped);
  const loaded = useNeedsMeStore((s) => s.loaded);
  const byBox = useNeedsMeStore((s) => s.byBox);
  const now = useNeedsMeStore((s) => s.now);
  const order = useMemo(() => walkOrder(items, current, skipped), [items, current, skipped]);
  const front = order[0];
  const later = useMemo(() => laterList(byBox, now), [byBox, now]);
  const boxes = useStore((s) => s.status?.boxes.filter((b) => b.state === "online").length ?? 0);

  // Whatever is in front stays in front, so a card that sorts ahead of it later
  // never takes its place. And the view reads afresh when it opens.
  useEffect(() => {
    if (front?.key !== current) useNeedsMeStore.setState({ current: front?.key });
  }, [front?.key, current]);
  useEffect(() => lookSoon(), []);

  // The list as it stands when something moves on, not as it was when bound.
  const latest = useRef(items);
  latest.current = items;

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="needs-me">
      <ViewHeader title="Needs me" description={<span data-testid="needs-me-left">{leftLine(items)}</span>} />
      {!loaded && items.length === 0 ? (
        <div className="flex flex-1 items-center justify-center gap-2 text-muted-foreground text-body">
          <Spinner className="size-4" /> Reading each box's work…
        </div>
      ) : !front ? (
        <div className="flex min-h-0 flex-1 flex-col">
          <Empty className="flex-1" data-testid="needs-me-empty">
            <EmptyHeader>
              <EmptyMedia>
                <Scene name="calm" />
              </EmptyMedia>
              <EmptyTitle>Nothing needs you</EmptyTitle>
              <EmptyDescription>{boxes ? "Questions and finished work come here by themselves." : "No box is online right now."}</EmptyDescription>
            </EmptyHeader>
          </Empty>
          <div className="mx-auto w-full max-w-md px-page pb-6">
            <Later later={later} />
          </div>
        </div>
      ) : (
        <div className="flex min-h-0 flex-1">
          <aside aria-label="What needs you" className="flex w-80 shrink-0 flex-col border-r max-xl:w-64">
            <div className="min-h-0 flex-1 overflow-y-auto px-edge py-2">
              <SideList order={order} />
              <Later later={later} />
            </div>
            <footer className="flex shrink-0 flex-wrap items-center gap-x-2.5 gap-y-1 border-t px-edge py-2 text-tag text-muted-foreground">
              {LEGEND.map(([keys, label]) => (
                <span key={label} className="inline-flex items-center gap-1">
                  {keys.map((k) => (
                    <Kbd key={k} className="h-4 min-w-4 px-1 text-tag">
                      {k}
                    </Kbd>
                  ))}
                  {label}
                </span>
              ))}
            </footer>
          </aside>
          <main className="min-h-0 min-w-0 flex-1 overflow-y-auto">
            <div className="mx-auto w-full max-w-3xl px-page py-6">
              <IssueChips>
                <NeedsCard key={front.key} item={front} onNext={() => walkOn(latest.current, front.key)} onBack={() => walkBack(latest.current)} />
              </IssueChips>
            </div>
          </main>
        </div>
      )}
    </div>
  );
}

// The list in walk order: what's in front, then each pass under its name.
function SideList({ order }: { order: QueueItem[] }) {
  const multiBox = useMemo(() => new Set(order.map((i) => i.box)).size > 1, [order]);
  const [front, ...rest] = order;
  // Passes in the order the walk meets them, each once, however a passed-over
  // card has moved.
  const sections: { label: string; items: QueueItem[] }[] = [];
  for (const item of rest) {
    const label = passLabel(item.pass);
    const last = sections[sections.length - 1];
    if (last?.label === label) last.items.push(item);
    else sections.push({ label, items: [item] });
  }
  return (
    <nav aria-label="Jump to" className="flex flex-col gap-3">
      <Section label="Now">
        <Row item={front} active multiBox={multiBox} />
      </Section>
      {sections.map((s, i) => (
        <Section key={`${s.label}${i}`} label={s.label}>
          {s.items.map((item) => (
            <Row key={item.key} item={item} active={false} multiBox={multiBox} />
          ))}
        </Section>
      ))}
    </nav>
  );
}

function Section({ label, className, children }: { label: string; className?: string; children: React.ReactNode }) {
  return (
    <section aria-label={label} className={cn("flex flex-col gap-px", className)}>
      <h3 className="px-2 pb-0.5 text-tag text-muted-foreground">{label}</h3>
      <ul className="flex flex-col gap-px">{children}</ul>
    </section>
  );
}

function Row({ item, active, multiBox }: { item: QueueItem; active: boolean; multiBox: boolean }) {
  return (
    <li>
      <button type="button" data-testid="needs-me-row" data-key={item.key} aria-current={active ? "true" : undefined} onClick={() => walkTo(item.key)} className={cn("flex w-full min-w-0 flex-col gap-0.5 rounded-md px-2 py-1.5 text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring", active ? "bg-accent" : "hover:bg-accent/60")}>
        <span className="flex min-w-0 items-center gap-1.5">
          {item.pinned && <PinIcon aria-label="Pinned" className="size-3 shrink-0 text-muted-foreground" />}
          <span className="min-w-0 flex-1 truncate text-row">{item.title}</span>
        </span>
        <span className="truncate text-meta text-muted-foreground">
          {multiBox ? `${item.box} · ` : ""}
          {item.why}
        </span>
      </button>
    </li>
  );
}

// What's put off: out of the count, and still somewhere to be found, saying
// whom it waits for rather than an internal status.
function Later({ later }: { later: ReturnType<typeof laterList> }) {
  if (!later.length) return null;
  return (
    <div data-testid="needs-me-later">
      <Section label="Later" className="mt-4">
        {later.map(({ box, record, line }) => (
          <li key={`${box}/${record.id}`} data-testid="needs-me-later-row" data-id={record.id} className="flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5">
            <span className="flex min-w-0 flex-1 flex-col gap-0.5">
              <span className="truncate text-row">{record.title || record.id}</span>
              <span className="truncate text-meta text-muted-foreground">{line}</span>
            </span>
            <Button size="xs" variant="ghost" onClick={() => void bringBack(box, record.id)}>
              Bring back
            </Button>
          </li>
        ))}
      </Section>
    </div>
  );
}
