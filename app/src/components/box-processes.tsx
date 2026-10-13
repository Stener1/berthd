import { GlobeIcon, SettingsIcon } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { SimpleSelect } from "@/components/simple-select";
import { Tip } from "@/components/tip";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Popover, PopoverPopup, PopoverTrigger } from "@/components/ui/popover";
import { Spinner } from "@/components/ui/spinner";
import { toastManager } from "@/components/ui/toast";
import { bytes, errorMessage } from "@/lib/format";
import { plainError } from "@/lib/errors";
import { type BoxBrowser, type BoxProcesses, type BoxSessionProcs, OWNER_WORDS, SESSION_LIMITS, age, busy, cpu, memory, summary } from "@/lib/processes";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

// A box's browsers and what its sessions use (GET /v1/processes): from the
// status bar's memory meter, and in Settings › Boxes. Anything Shipyard or
// its sessions started has Stop; the box user's own browsers are listed
// but left alone.

// boxHasProcesses says the box can list them (berthd with "processes").
export function useBoxHasProcesses(box: string): boolean {
  return useStore((s) => !!s.boxes[box]?.info?.capabilities?.includes("processes"));
}

// useBoxProcesses polls the list every few seconds while on.
export function useBoxProcesses(box: string, on: boolean) {
  const [data, setData] = useState<BoxProcesses>();
  const [error, setError] = useState<string>();
  const load = useCallback(async () => {
    const client = useStore.getState().client;
    if (!client) return;
    try {
      setData(await client.box<BoxProcesses>(box, "GET", "processes"));
      setError(undefined);
    } catch (err) {
      setError(plainError(err));
    }
  }, [box]);
  useEffect(() => {
    if (!on) return;
    void load();
    const t = setInterval(() => void load(), 4000);
    return () => clearInterval(t);
  }, [on, load]);
  return { data, error, reload: load };
}

async function stop(box: string, id: string): Promise<boolean> {
  const client = useStore.getState().client;
  if (!client) return false;
  try {
    const r = await client.box<{ text: string }>(box, "POST", `processes/${encodeURIComponent(id)}/stop`);
    toastManager.add({ title: r.text, type: "success" });
    return true;
  } catch (err) {
    toastManager.add({ title: "Couldn't stop it", description: errorMessage(err), type: "error" });
    return false;
  }
}

// BoxMeter is a box's memory in the status bar; clicked, it lists the box's
// browsers and heavy sessions. A box without the list opens Settings.
export function BoxMeter({ box, mem, route, className }: { box: string; mem: { used: number; total: number }; route?: string; className?: string }) {
  const has = useBoxHasProcesses(box);
  const [open, setOpen] = useState(false);
  const used = mem.used / mem.total;
  const tip = [`${box} memory: ${bytes(mem.used)} of ${bytes(mem.total)} in use`, route].filter(Boolean).join(" · ");
  const body = (
    <>
      {box}
      <span className="relative h-1.5 w-6 overflow-hidden rounded-full bg-muted-foreground/20">
        <span className={cn("absolute inset-y-0 left-0 rounded-full", used > 0.85 ? "bg-warning" : "bg-muted-foreground/60")} style={{ width: `${Math.round(used * 100)}%` }} />
      </span>
      <span className="tabular-nums">{Math.round(used * 100)}%</span>
    </>
  );
  const cls = cn("-mx-1 flex items-center gap-1.5 rounded px-1 hover:bg-accent hover:text-foreground data-popup-open:bg-accent data-popup-open:text-foreground", used > 0.85 && "text-warning-foreground dark:text-warning", className);
  if (!has) {
    return (
      <Tip label={tip}>
        <button type="button" className={cls} onClick={() => useStore.getState().setView({ kind: "settings", section: "boxes" })}>
          {body}
        </button>
      </Tip>
    );
  }
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <Tip label={`${tip}. Click for its browsers and sessions`}>
        <PopoverTrigger render={<button type="button" data-testid={`box-meter-${box}`} className={cls} />}>{body}</PopoverTrigger>
      </Tip>
      <PopoverPopup side="top" align="end" sideOffset={6} className="w-[32rem] max-w-[calc(100vw-2rem)] p-0 [&_[data-slot=popover-viewport]]:p-0">
        <BoxProcessesPopover box={box} mem={mem} on={open} onSettings={() => setOpen(false)} />
      </PopoverPopup>
    </Popover>
  );
}

function BoxProcessesPopover({ box, mem, on, onSettings }: { box: string; mem: { used: number; total: number }; on: boolean; onSettings(): void }) {
  const { data, error, reload } = useBoxProcesses(box, on);
  return (
    <div data-testid="box-processes" className="flex max-h-[min(34rem,70vh)] flex-col">
      <div className="flex items-start gap-2 border-b px-4 pt-3 pb-2.5">
        <div className="min-w-0 flex-1">
          <div className="font-medium text-sm">{box}</div>
          <p className="text-muted-foreground text-xs">
            {bytes(mem.used)} of {bytes(mem.total)} in use{data ? ` · ${summary(data.browsers)}` : ""}
          </p>
        </div>
        <Tip label="Settings › Boxes">
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label="Box settings"
            onClick={() => {
              onSettings();
              useStore.getState().setView({ kind: "settings", section: "boxes" });
            }}
          >
            <SettingsIcon />
          </Button>
        </Tip>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <ProcessesBody box={box} data={data} error={error} reload={reload} />
      </div>
    </div>
  );
}

// ProcessesBody is the list itself, shared by the popover and Settings.
function ProcessesBody({ box, data, error, reload }: { box: string; data?: BoxProcesses; error?: string; reload(): Promise<void> }) {
  if (error && !data) return <p className="px-4 py-6 text-center text-destructive-foreground text-xs">{error}</p>;
  if (!data)
    return (
      <div className="flex justify-center py-6">
        <Spinner className="size-4" />
      </div>
    );
  const sessions = (data.sessions ?? []).slice(0, 5);
  return (
    <>
      <Section title="Browsers" empty={data.browsers.length === 0 ? `No browsers running on ${box}.` : undefined}>
        {data.browsers.map((b) => (
          <BrowserRow key={b.id} box={box} b={b} onStopped={reload} />
        ))}
      </Section>
      {sessions.length > 0 && (
        <Section title="Sessions using the most memory">
          {sessions.map((s) => (
            <SessionRow key={s.id} s={s} />
          ))}
        </Section>
      )}
      <p className="border-t px-4 py-2 text-[11px] text-muted-foreground">
        {data.scopes ? "Each new session runs in a scope of its own: ending it stops everything it started." : "Ending a session stops the processes Shipyard finds for it (this box has no systemd scopes)."}
      </p>
    </>
  );
}

// empty says so in place of the list, which may hold only its rows.
function Section({ title, empty, children }: { title: string; empty?: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="sticky top-0 z-1 border-b bg-popover px-4 py-1 font-medium text-[11px] text-muted-foreground">{title}</div>
      {empty ? <p className="px-4 py-3 text-muted-foreground text-xs">{empty}</p> : <ul className="divide-y">{children}</ul>}
    </div>
  );
}

function BrowserRow({ box, b, onStopped }: { box: string; b: BoxBrowser; onStopped(): Promise<void> }) {
  const [stopping, setStopping] = useState(false);
  const hot = busy(b);
  return (
    <li data-testid="box-browser" data-owner={b.owner} className="flex items-center gap-2.5 px-4 py-1.5 text-xs">
      <GlobeIcon className={cn("size-3.5 shrink-0", hot ? "text-warning-foreground dark:text-warning" : "text-muted-foreground")} aria-hidden />
      <div className="min-w-0 flex-1">
        <div className="truncate">{b.label}</div>
        <div className="flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground tabular-nums">
          <Badge size="sm" variant={b.owner === "orphan" ? "warning" : "outline"} className="shrink-0">
            {OWNER_WORDS[b.owner]}
          </Badge>
          <span className="truncate">
            {b.engine} · {b.processes} {b.processes === 1 ? "process" : "processes"}
            {b.started ? ` · ${age(b.started)} old` : ""}
          </span>
        </div>
      </div>
      <span className={cn("w-12 shrink-0 text-right tabular-nums", b.cpu_percent >= 100 && "font-medium text-warning-foreground dark:text-warning")}>{cpu(b.cpu_percent)}</span>
      <span className="w-14 shrink-0 text-right text-muted-foreground tabular-nums">{memory(b.memory)}</span>
      <div className="flex w-12 shrink-0 justify-end">
        {b.stoppable ? (
          <Button
            size="xs"
            variant="outline"
            loading={stopping}
            aria-label={`Stop ${b.label}`}
            onClick={async () => {
              setStopping(true);
              if (await stop(box, b.id)) await onStopped();
              setStopping(false);
            }}
          >
            Stop
          </Button>
        ) : (
          <Tip label="Shipyard didn't start this browser, so it leaves it alone">
            <span className="text-[11px] text-muted-foreground">—</span>
          </Tip>
        )}
      </div>
    </li>
  );
}

function SessionRow({ s }: { s: BoxSessionProcs }) {
  const u = s.usage;
  return (
    <li data-testid="box-session" className="flex items-center gap-2.5 px-4 py-1.5 text-xs">
      <div className="min-w-0 flex-1">
        <div className="truncate">{s.title || s.name}</div>
        <div className="truncate text-[11px] text-muted-foreground">
          {s.location ?? s.name}
          {u.processes ? ` · ${u.processes} ${u.processes === 1 ? "process" : "processes"}` : ""}
        </div>
      </div>
      <span className="w-12 shrink-0 text-right tabular-nums">{cpu(u.cpu_percent)}</span>
      <span className={cn("w-26 shrink-0 text-right tabular-nums", u.near_limit ? "font-medium text-warning-foreground dark:text-warning" : "text-muted-foreground")}>
        {memory(u.memory)}
        {u.memory_high ? ` of ${memory(u.memory_high)}` : ""}
      </span>
    </li>
  );
}

// BoxProcessesCard is the same list in Settings › Boxes, with the
// per-session memory limit.
export function BoxProcessesCard({ box, className }: { box: string; className?: string }) {
  const has = useBoxHasProcesses(box);
  const { data, error, reload } = useBoxProcesses(box, has);
  if (!has) return null;
  return (
    <div data-testid="box-processes-card" data-box={box} className={cn("rounded-lg border", className)}>
      <div className="flex items-center gap-2 border-b px-4 py-2">
        <div className="min-w-0 flex-1">
          <div className="font-medium text-xs">Browsers and sessions</div>
          <div className="text-[11px] text-muted-foreground">{data ? summary(data.browsers) : " "}</div>
        </div>
        <SessionLimit box={box} scopes={!!data?.scopes} />
      </div>
      <div className="max-h-[28rem] overflow-y-auto [&_.sticky]:bg-background">
        <ProcessesBody box={box} data={data} error={error} reload={reload} />
      </div>
    </div>
  );
}

// SessionLimit is the box's per-session memory ceiling, kept with its
// resource guard (session_memory_gb).
function SessionLimit({ box, scopes }: { box: string; scopes: boolean }) {
  const [gb, setGb] = useState<number>();
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    const client = useStore.getState().client;
    client
      ?.box<{ config: { session_memory_gb?: number } }>(box, "GET", "guard")
      .then((g) => setGb(g.config.session_memory_gb ?? 0))
      .catch(() => setGb(undefined));
  }, [box]);
  if (gb === undefined) return null;
  const save = async (next: number) => {
    const client = useStore.getState().client;
    if (!client) return;
    setSaving(true);
    try {
      const g = await client.box<{ config: Record<string, unknown> }>(box, "GET", "guard");
      await client.box(box, "PUT", "guard", { config: { ...g.config, session_memory_gb: next || undefined } });
      setGb(next);
      toastManager.add({ title: next ? `Sessions on ${box} slow down near ${next} GB` : `Sessions on ${box} have no memory limit`, type: "success" });
    } catch (err) {
      toastManager.add({ title: "Couldn't set the limit", description: errorMessage(err), type: "error" });
    } finally {
      setSaving(false);
    }
  };
  const options = SESSION_LIMITS.map((n) => ({ value: String(n), label: n ? `${n} GB per session` : "No memory limit" }));
  return (
    <Tip label={scopes ? "Near it, a session is slowed down and Shipyard says so in its chat; nothing is stopped" : "Needs a box with systemd (Linux), where each session runs in a scope of its own"}>
      <div className={cn("w-44", !scopes && "opacity-60")}>
        <SimpleSelect aria-label="Memory limit per session" value={String(gb)} onChange={(v) => void save(Number(v))} options={options} size="sm" disabled={!scopes || saving} />
      </div>
    </Tip>
  );
}
