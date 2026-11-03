import { CheckIcon, ChevronDownIcon, CircleSlashIcon, ClockIcon, ExternalLinkIcon, GroupIcon, HourglassIcon, MessageSquareIcon, PinIcon, PinOffIcon, SendHorizontalIcon, SquareTerminalIcon, UngroupIcon, XIcon } from "lucide-react";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";

import { IssueText } from "@/components/conversation/issue-text";
import { QuestionCard, QuestionsContext } from "@/components/conversation/question-form";
import { toastError } from "@/components/error-note";
import { Tip } from "@/components/tip";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogDescription, DialogFooter, DialogHeader, DialogPanel, DialogPopup, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Kbd } from "@/components/ui/kbd";
import { Menu, MenuItem, MenuPopup, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { QUESTION_TOOLS } from "@/lib/chat-card";
import { type Block, type Card, hostOf, validateCard, type Verb } from "@/lib/cards";
import { errorMessage } from "@/lib/format";
import { buildCard, type CardFacts, type Member, type QueueItem, repoOf, sessionTree, whoOf, whyOf, type WorkRecord } from "@/lib/needs-me";
import { openUrl } from "@/lib/open-url";
import type { Question, QuestionAnswer } from "@/lib/questions";
import { permissionChoices } from "@/lib/screen";
import { snoozeUntil } from "@/lib/skills-notice-model";
import { sessionWord } from "@/lib/state-model";
import { useStore } from "@/lib/store";
import type { TranscriptItem } from "@/lib/transcript";
import { type TranscriptResult, useAsk } from "@/lib/transcript-feed";
import { cn } from "@/lib/utils";
import { isViewKey } from "@/lib/view-keys";
import { focusSession } from "@/lib/workspaces";
import { accept, answerForm, choose, closeWork, groupWith, markSeen, notNow, park, pin, read, reject, reply, ungroup, useNeedsMeStore, waitOn } from "@/views/needs-me/needs-me-store";

// A piece of work, filling the screen: drawn from its card (lib/cards), so what
// a card can show and do is only what the format has. Its actions are verbs,
// and this file is what each verb does.

const KIND_LABEL: Record<string, string> = { decision: "Decision", task: "Task for you", result: "Result to read", "close-out": "Close-out", group: "Suggested group" };

// The verbs that are buttons, in the order they show, with the key that presses
// each. answer and reply draw their own controls on the card.
const BUTTONS: { verb: Verb; label: string; icon: ReactNode; key?: string }[] = [
  { verb: "done", label: "Done", icon: <CheckIcon /> },
  { verb: "read", label: "Read", key: "e", icon: <CheckIcon /> },
  { verb: "not-now", label: "Not now", key: "s", icon: <ClockIcon /> },
  { verb: "waiting-on", label: "Waiting on", key: "w", icon: <HourglassIcon /> },
  { verb: "accept", label: "Accept", icon: <GroupIcon /> },
  { verb: "reject", label: "Reject", icon: <CircleSlashIcon /> },
  { verb: "still-needed", label: "Still needed", icon: <CheckIcon /> },
  { verb: "park", label: "Park", icon: <ClockIcon /> },
  { verb: "pin", label: "Pin", key: "p", icon: <PinIcon /> },
  { verb: "close", label: "Close", key: "d", icon: <XIcon /> },
];

// The last thing it said, and its open form, from its record: the same read a
// chat makes (transcript?since=0, the recent window). The record can trail its
// waiting state by a moment, so a wait with no form yet is read again. Leaving a
// card drops its read.
function useRead(m: Member | undefined): TranscriptItem[] | undefined {
  const client = useStore((s) => s.client);
  const [read, setRead] = useState<{ key: string; items: TranscriptItem[] }>();
  const key = m?.session ? `${m.box}/${m.name}` : "";
  const since = m?.session?.state_since;
  const waiting = m?.state === "waiting" && !m.noForm;
  useEffect(() => {
    if (!client || !key || !m) return;
    const stop = new AbortController();
    let timer = 0;
    let tries = 0;
    const load = async () => {
      const r = await client.box<TranscriptResult>(m.box, "GET", `sessions/${encodeURIComponent(m.name)}/transcript?since=0`, undefined, stop.signal).catch(() => undefined);
      if (stop.signal.aborted) return;
      const items = r?.items ?? [];
      setRead({ key, items });
      if (waiting && !openForm(items) && ++tries < 4) timer = window.setTimeout(() => void load(), 800 * 2 ** tries);
    };
    void load();
    return () => {
      stop.abort();
      window.clearTimeout(timer);
    };
    // m follows key.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, key, since, waiting]);
  return read?.key === key ? read.items : undefined;
}

// As the chat finds it: the last question, unless something the person said
// came after.
function openForm(items: TranscriptItem[]) {
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (it.kind === "question") return it.done || !it.tool ? undefined : it;
    if (it.kind === "user") return undefined;
  }
}

function lastSaid(items: TranscriptItem[]) {
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (it.kind === "text" && it.text.trim()) return it.text.trim();
  }
}

function asksFor(m: Member | undefined): string | undefined {
  const ask = m?.state === "waiting" ? m.session?.ask : undefined;
  if (!ask || QUESTION_TOOLS.test(ask.tool ?? "")) return undefined;
  return [ask.tool ? `Wants to use ${ask.tool}` : "", ask.input ?? "", ask.why || ask.message || ""].filter(Boolean).join("\n") || undefined;
}

export function NeedsCard({ item, onNext, onBack }: { item: QueueItem; onNext(): void; onBack(): void }) {
  // A worker's question in a group is answered directly: pick its session.
  const [picked, setPicked] = useState<string>();
  const focus = item.members.find((m) => m.name === picked && m.need) ?? item.focus;
  const items = useRead(focus);
  const waiting = focus?.state === "waiting" && !focus.noForm;
  const form = waiting && items ? openForm(items) : undefined;
  const asks = useAsk(focus?.box ?? "", focus?.name ?? "", !!waiting && !!items && !form, focus?.session?.state_since, focus?.session?.ask);
  const facts: CardFacts = form ? { form: { tool: form.tool ?? "", questions: form.questions } } : { said: items && lastSaid(items), choices: asks?.choices.length ? (permissionChoices(asks.choices) ?? asks.choices) : undefined, asks: asksFor(focus) };
  const reading = !!focus?.session && item.kind !== "group" && !items;
  const card = validateCard(buildCard(picked && focus?.need ? { ...item, focus, why: whyOf(focus) } : item, facts));
  if (!card.ok) return <Broken errors={card.errors} item={item} onNext={onNext} />;
  return <Drawn key={focus?.name ?? ""} card={card.card} item={item} focus={focus} reading={reading} onPick={setPicked} onNext={onNext} onBack={onBack} />;
}

function Broken({ errors, item, onNext }: { errors: string[]; item: QueueItem; onNext(): void }) {
  return (
    <article data-testid="needs-me-card" data-key={item.key} className="flex flex-col gap-3">
      <Alert variant="error">
        <AlertTitle>This card can't be drawn</AlertTitle>
        <AlertDescription>{errors.join(". ")}</AlertDescription>
      </Alert>
      <div>
        <Button size="sm" variant="outline" onClick={onNext}>
          Next
        </Button>
      </div>
    </article>
  );
}

const dateField = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
const inDays = (n: number) => {
  const d = new Date();
  d.setDate(d.getDate() + n);
  return dateField(d);
};
// A date picked is that morning, where the person is; the box keeps it in UTC.
const morningOf = (field: string) => new Date(`${field}T09:00:00`).toISOString();

function Drawn({ card, item, focus, reading, onPick, onNext, onBack }: { card: Card; item: QueueItem; focus?: Member; reading: boolean; onPick(name: string): void; onNext(): void; onBack(): void }) {
  const [asking, setAsking] = useState<"not-now" | "waiting-on">();
  const replyBox = useRef<HTMLTextAreaElement>(null);
  // Each new card takes the keyboard from whatever brought it up (the menu, the
  // list, the last card's button), so its keys work at once. What arrives later
  // doesn't remount a card, so it never takes the focus from what's being typed.
  const self = useRef<HTMLElement>(null);
  useEffect(() => self.current?.focus({ preventScroll: true }), []);
  const has = (v: Verb) => card.actions.includes(v);
  const record = item.record;
  const box = item.box;
  // Does something to the record, and the queue moves on as soon as the store has it.
  const onRecord = (f: (box: string, id: string) => unknown) => () => {
    if (!record) return;
    void f(box, record.id);
    onNext();
  };
  const run: Partial<Record<Verb, () => void>> = {
    read: onRecord((_, id) => read(item, id)),
    "not-now": () => setAsking("not-now"),
    "waiting-on": () => setAsking("waiting-on"),
    close: () => void closeWork(item, onNext),
    pin: () => {
      if (record) void pin(box, "work", record.id, !record.pinned);
    },
    park: onRecord(park),
    "still-needed": onRecord(markSeen),
    accept: onRecord(accept),
    reject: onRecord(reject),
    done: () => {
      if (!focus) return;
      void reply(focus, "Done.").catch((err) => toastError(err, { title: "Couldn't tell it", box: focus.box }));
      onNext();
    },
  };
  // A form has Enter and the number keys for itself.
  const hasForm = card.blocks.some((b) => b.type === "question" && b.questions);

  // The keys, as Review's go: j and k move, e marks read, d closes, Enter opens.
  useEffect(() => {
    if (asking) return;
    const onKey = (e: KeyboardEvent) => {
      if (!isViewKey(e)) return;
      if (e.key === "Enter" && (e.target as HTMLElement | null)?.closest("button, a, [role=link]")) return;
      const keys: Record<string, (() => void) | undefined> = {
        j: onNext,
        n: onNext,
        ArrowDown: onNext,
        k: onBack,
        ArrowUp: onBack,
        r: has("reply") ? () => replyBox.current?.focus() : undefined,
        Enter: focus?.session && !hasForm ? () => void focusSession(focus.box, focus.name) : undefined,
      };
      for (const b of BUTTONS) if (b.key && has(b.verb)) keys[b.key] = run[b.verb];
      const go = keys[e.key];
      if (!go) return;
      e.preventDefault();
      go();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  return (
    <article ref={self} tabIndex={-1} data-testid="needs-me-card" data-key={item.key} data-kind={card.kind} className="flex min-w-0 flex-col gap-5 outline-none">
      <header className="flex flex-col gap-1.5">
        <div className="flex flex-wrap items-center gap-2 text-meta text-muted-foreground">
          <span data-testid="needs-me-kind">{KIND_LABEL[card.kind] ?? card.kind}</span>
          {item.pinned && (
            <Badge variant="outline" size="sm" data-testid="needs-me-pinned">
              <PinIcon />
              Pinned
            </Badge>
          )}
        </div>
        <h2 data-testid="needs-me-title" data-selectable className="break-words text-title">
          {card.title}
        </h2>
        {card.why && (
          <p data-testid="needs-me-why" className="text-body text-muted-foreground">
            {card.why}
          </p>
        )}
      </header>

      {reading ? (
        <div className="flex flex-col gap-2">
          <Skeleton className="h-4 w-2/3" />
          <Skeleton className="h-16 w-full" />
        </div>
      ) : (
        card.blocks.map((b, i) => <BlockView key={i} block={b} focus={focus} onAnswered={onNext} />)
      )}

      {has("reply") && focus && (
        <ReplyBox
          ref={replyBox}
          who={whoOf(focus)}
          onSend={async (text) => {
            await reply(focus, text);
            onNext();
          }}
        />
      )}

      <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="What to do with it">
        {BUTTONS.filter((b) => has(b.verb)).map((b) =>
          b.verb === "pin" ? (
            record && <PinMenu key={b.verb} item={item} record={record} />
          ) : (
            <Button key={b.verb} size="sm" variant="outline" onClick={run[b.verb]}>
              {b.icon}
              {b.label}
              {b.key && <KeyHint k={b.key} />}
            </Button>
          ),
        )}
        {focus?.session && (
          <Button size="sm" variant="ghost" onClick={() => void focusSession(focus.box, focus.name)}>
            <SquareTerminalIcon />
            Open session
          </Button>
        )}
      </div>

      {item.kind !== "group" && <Sessions item={item} focus={focus} onPick={onPick} />}

      {asking === "not-now" && record && (
        <NotNowDialog
          onClose={() => setAsking(undefined)}
          onPick={(until) => {
            setAsking(undefined);
            void notNow(box, record.id, until);
            onNext();
          }}
        />
      )}
      {asking === "waiting-on" && record && (
        <WaitingDialog
          initial={record.waiting_on}
          onClose={() => setAsking(undefined)}
          onSave={(who, at) => {
            setAsking(undefined);
            void waitOn(box, record.id, who, at);
            onNext();
          }}
        />
      )}
    </article>
  );
}

const KeyHint = ({ k }: { k: string }) => (
  <Kbd aria-hidden className="ml-0.5 h-4 min-w-4 px-1 text-tag uppercase">
    {k}
  </Kbd>
);

function PinMenu({ item, record }: { item: QueueItem; record: WorkRecord }) {
  const pins = useNeedsMeStore((s) => s.byBox[item.box]?.pins);
  const repo = repoOf(item.members.find((m) => m.session)?.session?.location);
  const project = record.project;
  const on = (kind: "repo" | "project", target: string) => !!pins?.some((p) => p.kind === kind && p.target === target);
  return (
    <Menu>
      <MenuTrigger render={<Button size="sm" variant="outline" />}>
        {record.pinned ? <PinOffIcon /> : <PinIcon />}
        Pin
        <KeyHint k="p" />
        <ChevronDownIcon />
      </MenuTrigger>
      <MenuPopup align="start">
        <MenuItem onClick={() => void pin(item.box, "work", record.id, !record.pinned)}>{record.pinned ? "Unpin this work" : "Pin this work"}</MenuItem>
        {(repo || project) && <MenuSeparator />}
        {repo && <MenuItem onClick={() => void pin(item.box, "repo", repo, !on("repo", repo))}>{on("repo", repo) ? `Unpin ${repo}` : `Pin all of ${repo}`}</MenuItem>}
        {project && <MenuItem onClick={() => void pin(item.box, "project", project, !on("project", project))}>{on("project", project) ? `Unpin ${project}` : `Pin all of ${project}`}</MenuItem>}
      </MenuPopup>
    </Menu>
  );
}

// Blocks this slice doesn't draw yet are still valid: a card that has one says
// so, rather than dropping part of what it means.
function BlockView({ block, focus, onAnswered }: { block: Block; focus?: Member; onAnswered(): void }) {
  switch (block.type) {
    case "text":
      return (
        <div data-testid="needs-me-text" data-selectable className="max-h-[50vh] overflow-y-auto whitespace-pre-wrap break-words rounded-lg border bg-muted/20 px-4 py-3 text-body">
          <IssueText text={block.text} />
        </div>
      );
    case "question":
      if (block.questions && block.tool && focus) return <Form tool={block.tool} questions={block.questions} focus={focus} onAnswered={onAnswered} />;
      if (block.choices && focus) return <KeyChoices choices={block.choices} focus={focus} onAnswered={onAnswered} />;
      return null;
    case "table":
      return (
        <Table>
          <TableHeader>
            <TableRow>
              {block.columns.map((c) => (
                <TableHead key={c}>{c}</TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {block.rows.map((r, i) => (
              <TableRow key={i}>
                {r.map((c, j) => (
                  <TableCell key={j}>{c}</TableCell>
                ))}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      );
    case "link":
      return (
        // The real host, whatever the label says.
        <Button size="sm" variant="outline" className="self-start" onClick={() => void openUrl(block.url)}>
          <ExternalLinkIcon />
          {block.label ?? block.url}
          <span className="text-meta text-muted-foreground">{hostOf(block.url)}</span>
        </Button>
      );
    default:
      return <p className="text-desc text-muted-foreground">This card has a {block.type} block, which this version doesn't draw yet.</p>;
  }
}

// The agent's own form, on the app's own card. The box fills it in with keys.
function Form({ tool, questions, focus, onAnswered }: { tool: string; questions: Question[]; focus: Member; onAnswered(): void }) {
  const canAnswer = useStore((s) => !!s.boxes[focus.box]?.info?.capabilities?.includes("answer"));
  const [stuck, setStuck] = useState<string>();
  const who = whoOf(focus);
  const value = {
    live: tool,
    canAnswer: canAnswer && focus.session?.agent === "claude",
    stuck,
    keys: true,
    where: `${who}'s session`,
    submit: async (t: string, answers: QuestionAnswer[]) => {
      try {
        await answerForm(focus, t, answers);
        setStuck(undefined);
        onAnswered();
      } catch (err) {
        setStuck(errorMessage(err));
        throw err;
      }
    },
  };
  return (
    <QuestionsContext value={value}>
      <QuestionCard it={{ kind: "question", id: tool, tool, questions }} who={who} />
    </QuestionsContext>
  );
}

// The answers a permission offers, by the key that picks one: one press sends
// it, as the board's chips do. Ported from the Intake plugin.
function KeyChoices({ choices, focus, onAnswered }: { choices: { key: string; label: string; title?: string }[]; focus: Member; onAnswered(): void }) {
  const [sent, setSent] = useState<string>();
  const pick = async (key: string) => {
    if (sent) return;
    setSent(key);
    try {
      await choose(focus, key);
      onAnswered();
    } catch (err) {
      setSent(undefined);
      toastError(err, { title: "Couldn't answer", box: focus.box });
    }
  };
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!isViewKey(e) || !choices.some((c) => c.key === e.key)) return;
      e.preventDefault();
      void pick(e.key);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
  return (
    <div className="flex flex-col gap-1.5" data-testid="needs-me-choices">
      {choices.map((c) => (
        <Button key={c.key} size="sm" variant={sent === c.key ? "secondary" : "outline"} disabled={!!sent} className="justify-start" onClick={() => void pick(c.key)}>
          {sent === c.key ? <Spinner className="size-3.5" /> : <Kbd aria-hidden className="h-4 min-w-4 px-1 text-tag">{c.key}</Kbd>}
          {c.label}
          {c.title && <span className="min-w-0 truncate font-normal text-meta text-muted-foreground">{c.title}</span>}
        </Button>
      ))}
    </div>
  );
}

function ReplyBox({ ref, who, onSend }: { ref: React.Ref<HTMLTextAreaElement>; who: string; onSend(text: string): Promise<void> }) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const send = async () => {
    const t = text.trim();
    if (!t || busy) return;
    setBusy(true);
    setError(undefined);
    try {
      await onSend(t);
    } catch (err) {
      setError(errorMessage(err));
    }
    setBusy(false);
  };
  return (
    <div className="flex flex-col gap-2">
      <Textarea
        ref={ref}
        rows={3}
        value={text}
        disabled={busy}
        aria-label="Reply"
        placeholder={`Reply to ${who}…`}
        onChange={(e) => setText(e.currentTarget.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            void send();
          }
        }}
      />
      <div className="flex items-center gap-2">
        <Button size="sm" disabled={busy || !text.trim()} onClick={() => void send()}>
          {busy ? <Spinner className="size-3.5" /> : <SendHorizontalIcon />}
          Reply
        </Button>
        {error && (
          <span role="alert" className="text-desc text-destructive-foreground">
            {error}
          </span>
        )}
      </div>
    </div>
  );
}

// The sessions of a piece of work, as a tree from who started whom. They stay
// separate sessions: grouping only draws them together.
function Sessions({ item, focus, onPick }: { item: QueueItem; focus?: Member; onPick(name: string): void }) {
  const tree = useMemo(() => sessionTree(item.members), [item.members]);
  const record = item.record;
  const records = useNeedsMeStore((s) => s.byBox[item.box]?.records);
  const others = useMemo(() => records?.filter((r) => r.id !== record?.id && r.status === "open") ?? [], [records, record?.id]);
  if (!tree.length) return null;
  return (
    <section aria-label="Sessions in this work" className="flex flex-col gap-1 border-t pt-4">
      <div className="flex items-center gap-2">
        <h3 className="min-w-0 flex-1 text-section text-muted-foreground">{tree.length === 1 ? "Session" : `${tree.length} sessions`}</h3>
        {record && others.length > 0 && (
          <Menu>
            <MenuTrigger render={<Button size="xs" variant="ghost" />}>
              <GroupIcon />
              Group with
            </MenuTrigger>
            <MenuPopup align="end" className="max-h-80 overflow-y-auto">
              {others.map((r) => (
                <MenuItem key={r.id} onClick={() => void groupWith(item.box, record.id, r.id)}>
                  {r.title || r.id}
                </MenuItem>
              ))}
            </MenuPopup>
          </Menu>
        )}
      </div>
      <ul className="flex flex-col gap-px" data-testid="needs-me-tree">
        {tree.map(({ member: m, depth }) => (
          <li key={`${m.box}/${m.name}`} data-testid="needs-me-session" data-session={m.name} data-depth={depth} data-focus={m === focus || undefined} style={{ paddingLeft: depth * 16 }} className="group/row flex min-w-0 items-center gap-2 rounded-md">
            <button type="button" disabled={!m.session} onClick={() => (m.need ? onPick(m.name) : void focusSession(m.box, m.name))} className={cn("flex h-8 min-w-0 flex-1 items-center gap-2 rounded-md px-2 text-left text-body outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring enabled:hover:bg-accent/60", m === focus && "bg-accent/50", !m.session && "text-muted-foreground")}>
              {m.need ? <MessageSquareIcon className="size-3.5 shrink-0 text-warning-foreground" /> : <SquareTerminalIcon className="size-3.5 shrink-0 text-muted-foreground" />}
              <span className="min-w-0 flex-1 truncate">{m.title}</span>
              <span className="shrink-0 text-meta text-muted-foreground">{m.state ? sessionWord(m.state) : "Ended"}</span>
            </button>
            {record && tree.length > 1 && (
              <Tip label={`Take ${m.title} out of this work`}>
                <Button size="icon-xs" variant="ghost" aria-label={`Ungroup ${m.title}`} onClick={() => void ungroup(item.box, record.id, m.name)}>
                  <UngroupIcon />
                </Button>
              </Tip>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

function NotNowDialog({ onClose, onPick }: { onClose(): void; onPick(until: string): void }) {
  const [date, setDate] = useState(() => inDays(1));
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogPopup className="max-w-sm" data-testid="needs-me-not-now">
        <DialogHeader>
          <DialogTitle>Not now</DialogTitle>
          <DialogDescription>It leaves the queue and comes back by itself.</DialogDescription>
        </DialogHeader>
        <DialogPanel className="flex flex-col gap-3">
          <Button autoFocus onClick={() => onPick(snoozeUntil("tomorrow", new Date()).toISOString())}>
            Tomorrow
          </Button>
          <label className="flex flex-col gap-1.5 text-desc text-muted-foreground">
            Or on a date
            <Input type="date" min={inDays(0)} value={date} aria-label="Back on" onChange={(e) => setDate(e.currentTarget.value)} />
          </label>
        </DialogPanel>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="outline" disabled={!date} onClick={() => onPick(morningOf(date))}>
            Back on this date
          </Button>
        </DialogFooter>
      </DialogPopup>
    </Dialog>
  );
}

function WaitingDialog({ initial, onClose, onSave }: { initial?: string; onClose(): void; onSave(who: string, at: string): void }) {
  const [who, setWho] = useState(initial ?? "");
  const [date, setDate] = useState(() => inDays(3));
  const ok = !!who.trim() && !!date;
  const save = () => ok && onSave(who.trim(), morningOf(date));
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogPopup className="max-w-sm" data-testid="needs-me-waiting-on">
        <DialogHeader>
          <DialogTitle>Waiting on</DialogTitle>
          <DialogDescription>It leaves the queue, and comes back on the date to check.</DialogDescription>
        </DialogHeader>
        <DialogPanel className="flex flex-col gap-3">
          <label className="flex flex-col gap-1.5 text-desc text-muted-foreground">
            Who or what
            <Input autoFocus value={who} aria-label="Who or what" placeholder="A teammate, a reply, a deploy…" onChange={(e) => setWho(e.currentTarget.value)} onKeyDown={(e) => e.key === "Enter" && save()} />
          </label>
          <label className="flex flex-col gap-1.5 text-desc text-muted-foreground">
            Check back
            <Input type="date" min={inDays(0)} value={date} aria-label="Check back" onChange={(e) => setDate(e.currentTarget.value)} />
          </label>
        </DialogPanel>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={!ok} onClick={save}>
            Wait
          </Button>
        </DialogFooter>
      </DialogPopup>
    </Dialog>
  );
}
