import { type AgentPreset, sessionName, useSessions, useStorage } from "@berth/plugin";
import { AgentPicker, Button, Icon, PickOne, Sheet, SheetDescription, SheetFooter, SheetHeader, SheetPanel, SheetPopup, SheetTitle, Skeleton, Textarea, Tip } from "@berth/plugin/ui";
import { useEffect, useMemo, useState } from "react";

import { type Brief, endpointBrief, failuresOf, type Failure, queryBrief } from "./brief.ts";
import { bytes, span } from "./format.ts";
import type { PanelContext } from "./ui";

// Send to the agent: a slow query, an N+1 or a failing endpoint written up
// (brief.ts) and handed to the worktree's agent. The sheet shows exactly
// the text that will be sent, and it can be changed first. Nothing goes
// anywhere until Send is pressed.

export type Ask = { kind: "query"; db_system: string; fingerprint: string } | { kind: "endpoint"; service: string; name: string };

// The most a session takes as one prompt.
const MAX_BYTES = 64 * 1024;

// write reads what the brief is about, as of now, and writes it up.
async function write(ctx: PanelContext, ask: Ask): Promise<Brief> {
  const scope = ctx.scope();
  const base = { source: ctx.source, windowMs: ctx.window.ms };
  // Where the helper answers on the box, so the agent can check its work.
  const api = await ctx.api.sources(ctx.box, ctx.source).then(
    (s) => s.otlp.endpoints[0],
    () => undefined,
  );
  // How it failed is in a trace: the newest that shows it.
  const failures = (traceId: string | undefined, shape?: string): Promise<Failure[]> => (traceId ? ctx.api.trace(ctx.box, traceId).then((t) => failuresOf(t, shape), () => []) : Promise.resolve([]));
  let brief: Brief | undefined;
  if (ask.kind === "query") {
    const d = await ctx.api.query(scope, ask);
    brief = queryBrief(d, { ...base, api }, d.stats?.errors ? await failures(d.traces.find((t) => t.errors > 0)?.trace_id, ask.fingerprint) : []);
  } else {
    const d = await ctx.api.endpoint(scope, ask);
    brief = endpointBrief(d, { ...base, api }, await failures(d.failing[0]?.trace_id));
  }
  if (!brief) throw new Error(`It didn't run in the last ${span(ctx.window.ms)}, so there are no numbers to send. Pick a wider window.`);
  return brief;
}

// SendButton opens the sheet for one problem. With words it is a button of
// its own (a detail's header); without, an icon on a row.
export function SendButton({ ctx, ask, words, what }: { ctx: PanelContext; ask: Ask; words?: boolean; what: string }) {
  const [open, setOpen] = useState(false);
  const label = `Send ${what} to the agent`;
  return (
    <>
      {words ? (
        <Button size="xs" variant="outline" onClick={() => setOpen(true)}>
          <Icon name="Send" className="size-3.5" />
          Send to the agent…
        </Button>
      ) : (
        <Tip label={label}>
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label={label}
            onClick={(e: React.MouseEvent) => {
              // On a row that opens its detail: this is not that.
              e.stopPropagation();
              setOpen(true);
            }}
          >
            <Icon name="Send" className="size-3.5" />
          </Button>
        </Tip>
      )}
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetPopup side="right" className="max-w-2xl">
          {open && <SendForm ctx={ctx} ask={ask} onClose={() => setOpen(false)} />}
        </SheetPopup>
      </Sheet>
    </>
  );
}

function SendForm({ ctx, ask, onClose }: { ctx: PanelContext; ask: Ask; onClose(): void }) {
  const { berth, box } = ctx;
  const [brief, setBrief] = useState<Brief>();
  const [text, setText] = useState("");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    write(ctx, ask).then(
      (b) => {
        if (!live) return;
        setBrief(b);
        setText(b.text);
      },
      (err) => live && setError(String((err as Error)?.message ?? err)),
    );
    return () => {
      live = false;
    };
    // Written once, when the sheet opens: it must not change under the person reading it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // The agents in this worktree; with none, one is started on the brief.
  const sessions = useSessions(box);
  const agents = useMemo(() => (sessions ?? []).filter((s) => s.agent && !s.exited && s.dir === ctx.path), [sessions, ctx.path]);
  const [picked, setPicked] = useState<string>();
  const target = agents.find((a) => a.name === picked) ?? agents[0];

  const [presets, setPresets] = useState<AgentPreset[]>();
  const [lastAgent, setLastAgent] = useStorage("agent", "claude");
  const [agent, setAgent] = useState(lastAgent);
  const none = sessions !== undefined && agents.length === 0;
  useEffect(() => {
    if (!none) return;
    let live = true;
    berth.api.info(box).then(
      (info) => live && setPresets((info.agents ?? []).filter((a) => a.command && a.id !== "shell")),
      () => live && setPresets([]),
    );
    return () => {
      live = false;
    };
  }, [berth, box, none]);
  useEffect(() => {
    if (presets?.length && !presets.some((p) => p.id === agent)) setAgent(presets[0].id);
  }, [presets, agent]);
  const agentName = presets?.find((p) => p.id === agent)?.name ?? agent;

  const size = new TextEncoder().encode(text).length;
  const fault = !text.trim() ? "There is nothing to send." : size > MAX_BYTES ? `This is ${bytes(size)}, and ${bytes(MAX_BYTES)} is the most that can be sent.` : undefined;

  const send = async () => {
    setBusy(true);
    setError(undefined);
    try {
      if (target) {
        const r = await berth.orchestrate.send(box, target.name, text, { when: "idle" });
        berth.notify(`Sent to ${sessionName(target, { sessions })}`, r.queued ? "It's busy, so the box holds it until it is idle." : (brief?.title ?? ""));
      } else {
        setLastAgent(agent);
        await berth.api.request(box, "POST", "sessions", { location: ctx.where, agent, prompt: text, title: brief?.title, open: "tab" });
        berth.notify(`${agentName} is on it`, `${ctx.worktree} on ${box}`);
      }
      onClose();
    } catch (err) {
      setError(String((err as Error)?.message ?? err));
    } finally {
      setBusy(false);
    }
  };
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      berth.notify("Copied", "Paste it to any agent.");
    } catch {
      berth.notify("Couldn't copy", "The clipboard refused it.");
    }
  };

  return (
    <div
      className="flex min-h-0 flex-1 flex-col"
      data-testid="leadline-send"
      onKeyDown={(e) => {
        if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && brief && !fault && !busy) {
          e.preventDefault();
          void send();
        }
      }}
    >
      <SheetHeader className="gap-1.5 pb-3">
        <SheetTitle className="flex items-center gap-2 text-base">
          <Icon name="Send" className="size-4 text-muted-foreground" />
          Send to the agent
        </SheetTitle>
        <SheetDescription className="text-[13px]">
          {brief ? brief.title : "Writing it up…"}
        </SheetDescription>
      </SheetHeader>
      <SheetPanel className="flex min-h-0 flex-1 flex-col gap-3">
        {!brief && !error ? (
          <div className="space-y-2" aria-busy="true">
            <Skeleton className="h-5 w-2/3" />
            <Skeleton className="h-40 w-full" />
            <Skeleton className="h-24 w-full" />
          </div>
        ) : brief ? (
          <>
            <label htmlFor="leadline-brief" className="text-muted-foreground text-xs">
              This is exactly what is sent. Change anything before it goes. Statements are written by their shape, with <code>?</code> where a value was.
            </label>
            <Textarea id="leadline-brief" value={text} onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => setText(e.target.value)} spellCheck={false} className="min-h-40 font-mono text-xs [&_textarea]:max-h-[58vh]" />
            <div className="flex flex-wrap items-center gap-2 text-xs">
              {target ? (
                agents.length > 1 ? (
                  <>
                    <span className="text-muted-foreground">To</span>
                    <PickOne label="Which agent" value={target.name} options={agents.map((a) => ({ value: a.name, label: sessionName(a, { sessions }) }))} onChange={setPicked} />
                  </>
                ) : (
                  <span className="text-muted-foreground">
                    To <b className="font-medium text-foreground">{sessionName(target, { sessions, agent: true })}</b> in {ctx.worktree}. If it's busy, the box holds this until it is idle.
                  </span>
                )
              ) : none ? (
                <>
                  <span className="text-muted-foreground">No agent runs in {ctx.worktree}. Start</span>
                  {presets === undefined ? <Skeleton className="h-7 w-32" /> : presets.length ? <AgentPicker presets={presets} value={agent} onChange={setAgent} /> : <span className="text-destructive-foreground">no agent is installed on {box}</span>}
                  <span className="text-muted-foreground">on it.</span>
                </>
              ) : null}
              <span className="ml-auto text-muted-foreground tabular-nums">{bytes(size)}</span>
            </div>
          </>
        ) : null}
        {(error || (brief && fault)) && <p className="whitespace-pre-wrap rounded-md bg-destructive/8 p-2 text-destructive-foreground text-xs">{error ?? fault}</p>}
      </SheetPanel>
      <SheetFooter>
        <Button variant="ghost" onClick={onClose}>
          Cancel
        </Button>
        <Button variant="outline" disabled={!brief} onClick={() => void copy()}>
          <Icon name="Copy" className="size-3.5" />
          Copy
        </Button>
        <Button loading={busy} disabled={!brief || !!fault || (!target && (!none || !presets?.length))} onClick={() => void send()}>
          <Icon name="Send" className="size-3.5" />
          {target ? "Send" : `Start ${agentName}`}
        </Button>
      </SheetFooter>
    </div>
  );
}
