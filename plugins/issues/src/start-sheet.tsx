import { type AgentPreset, type BerthPluginContext, type Project, useStorage } from "@berth/plugin";
import {
  AgentIcon,
  AgentPicker,
  Button,
  Icon,
  Input,
  Kbd,
  PickOne,
  Sheet,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetPanel,
  SheetPopup,
  SheetTitle,
  Skeleton,
  Spinner,
  Textarea,
  cn,
} from "@berth/plugin/ui";
import { useEffect, useState } from "react";

import { type Run, runnerOf } from "./store";

// The sheet behind "Start an agent": for one issue, a worktree named by the
// box's resolver, a box, an agent and the first prompt, all editable; for
// several, one agent each, started one after another with a summary.

// An issue to start on, wherever it is from (GitHub or Linear).
export interface Target {
  project: Project;
  // One issue, one key: "acme/shop#12", "linear:WEB-12".
  key: string;
  // As people say it: "#12", "WEB-12".
  ref: string;
  title: string;
  runs: Run[];
  // What the box's resolver names the worktree from (a GitHub issue's URL).
  // Without it the worktree below is used as it is.
  resolve?: string;
  // The worktree when the box has no better name for it.
  worktree: { name: string; branch: string };
  // The first prompt: where its template is kept, what it is until the
  // person changes it, and the {{variables}} it can use.
  template: { key: string; initial: string; variables: string[] };
  // render fills a template in from the issue, reading its body first.
  render(template: string): Promise<string>;
}

interface Resolution {
  name: string;
  branch: string;
  base?: string;
  exists?: boolean;
  note?: string;
}

type Step = { state: "waiting" } | { state: "working"; what: string } | { state: "started"; worktree: string; box: string } | { state: "skipped"; why: string } | { state: "failed"; why: string };

const enc = encodeURIComponent;
const message = (err: unknown) => (err instanceof Error ? err.message : String(err));

export function StartSheet({ berth, targets, onClose, onStarted }: { berth: BerthPluginContext; targets: Target[]; onClose(): void; onStarted?(): void }) {
  const open = targets.length > 0;
  return (
    <Sheet open={open} onOpenChange={(o: boolean) => !o && onClose()}>
      <SheetPopup side="right" className="max-w-lg">
        {open && <StartForm key={targets.map((t) => t.key).join(",")} berth={berth} targets={targets} onClose={onClose} onStarted={onStarted} />}
      </SheetPopup>
    </Sheet>
  );
}

function StartForm({ berth, targets, onClose, onStarted }: { berth: BerthPluginContext; targets: Target[]; onClose(): void; onStarted?(): void }) {
  const single = targets.length === 1 ? targets[0] : undefined;
  const project = targets.every((t) => t.project.id === targets[0].project.id) ? targets[0].project : undefined;
  // One batch is one kind of issue, so the first target's template is everyone's.
  const [template, setTemplate] = useStorage(targets[0].template.key, targets[0].template.initial);
  const [lastAgent, setLastAgent] = useStorage("agent", "claude");

  // The box: the project's default unless another is picked. Several
  // projects at once each go to their own.
  const online = project?.members.filter((m) => m.online) ?? [];
  const [box, setBox] = useState(() => (project ? runnerOf(project)?.box : undefined) ?? "");
  const member = project?.members.find((m) => m.box === box);
  const infoBox = box || (targets[0] && runnerOf(targets[0].project)?.box) || "";

  const [presets, setPresets] = useState<AgentPreset[]>();
  const repoAgents = JSON.stringify(member?.location.agents ?? []);
  useEffect(() => {
    let live = true;
    setPresets(undefined);
    if (!infoBox) return;
    berth.api.info(infoBox).then(
      (info) => {
        if (!live) return;
        const repo = JSON.parse(repoAgents) as AgentPreset[];
        const all = [...repo, ...(info.agents ?? []).filter((a) => !repo.some((r) => r.id === a.id))];
        setPresets(all.filter((a) => a.command && a.id !== "shell"));
      },
      () => live && setPresets([]),
    );
    return () => {
      live = false;
    };
  }, [berth, infoBox, repoAgents]);
  const [agent, setAgent] = useState(lastAgent);
  useEffect(() => {
    if (presets?.length && !presets.some((p) => p.id === agent)) setAgent(presets[0].id);
  }, [presets, agent]);

  // One issue: what the box would call its worktree, and the prompt with
  // the issue's body in it.
  const [resolution, setResolution] = useState<Resolution>();
  const [name, setName] = useState("");
  const [nameEdited, setNameEdited] = useState(false);
  useEffect(() => {
    if (!single || !member) return;
    let live = true;
    setResolution(undefined);
    resolveName(berth, box, member.location.name, single)
      .then((r) => {
        if (!live) return;
        setResolution(r);
        if (!nameEdited) setName(unique(r.name, member.location.worktrees?.map((w) => w.name) ?? []));
      });
    return () => {
      live = false;
    };
    // nameEdited is read once, when the answer arrives.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [berth, single?.key, box, member?.location.name]);

  const [prompt, setPrompt] = useState<string>();
  const [promptEdited, setPromptEdited] = useState(false);
  useEffect(() => {
    if (!single) return;
    let live = true;
    void single.render(template).then((text) => {
      if (live && !promptEdited) setPrompt(text);
    });
    return () => {
      live = false;
    };
    // Rendered once from the template; edits after that are the person's.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [berth, single?.key]);

  const [steps, setSteps] = useState<Step[]>();
  const [error, setError] = useState<string>();
  const busy = !!steps && steps.some((s) => s.state === "waiting" || s.state === "working");
  const finished = !!steps && !busy;
  const agentName = presets?.find((p) => p.id === agent)?.name ?? agent;

  const ready = !!agent && !busy && (single ? !!member && !!name.trim() && prompt !== undefined : targets.length > 0);

  async function start() {
    if (!ready) return;
    setError(undefined);
    setLastAgent(agent);
    if (!single) setTemplate(template);
    const next: Step[] = targets.map(() => ({ state: "waiting" }));
    const show = () => setSteps([...next]);
    show();
    for (const [i, t] of targets.entries()) {
      const where = project && member ? { box, member } : runnerOf(t.project);
      if (!where) {
        next[i] = { state: "failed", why: `None of ${t.project.name}'s boxes is online.` };
        show();
        continue;
      }
      if (!single && t.runs.length) {
        next[i] = { state: "skipped", why: `already has ${t.runs[0].worktree}` };
        show();
        continue;
      }
      const loc = where.member.location;
      try {
        let wt: Resolution;
        if (single) {
          const n = name.trim();
          wt = { name: n, branch: nameEdited ? n : (resolution?.branch ?? n), base: resolution?.base };
        } else {
          next[i] = { state: "working", what: "Naming the worktree" };
          show();
          const r = await resolveName(berth, where.box, loc.name, t);
          const n = unique(r.name, loc.worktrees?.map((w) => w.name) ?? []);
          wt = { name: n, branch: n === r.name ? r.branch : n, base: r.base };
        }
        let text = prompt ?? "";
        if (!single) {
          next[i] = { state: "working", what: "Reading the issue" };
          show();
          text = await t.render(template);
        }
        next[i] = { state: "working", what: `Starting ${agentName}` };
        show();
        await berth.api.createTask(where.box, {
          location: loc.name,
          name: wt.name,
          branch: wt.branch,
          base: wt.base || loc.default_branch,
          agent,
          prompt: text,
          // One agent comes to the front (or offers to); a batch would bury
          // the person in tabs, so it only reports.
          open: single ? "tab" : undefined,
        });
        next[i] = { state: "started", worktree: wt.name, box: where.box };
      } catch (err) {
        next[i] = { state: "failed", why: message(err) };
      }
      show();
    }
    const started = next.filter((s) => s.state === "started").length;
    if (started) onStarted?.();
    const failed = next.filter((s) => s.state === "failed").length;
    if (single) {
      const s = next[0];
      if (s.state === "started") {
        berth.notify(`${agentName} is on ${single.ref}`, `${s.worktree} on ${s.box}`);
        onClose();
      } else if (s.state === "failed") {
        setError(s.why);
        setSteps(undefined);
      }
      return;
    }
    berth.notify(
      started ? `Started ${started} agent${started === 1 ? "" : "s"}` : "No agents started",
      [failed && `${failed} failed`, next.length - started - failed && `${next.length - started - failed} skipped`].filter(Boolean).join(", ") || `${agentName} on each issue`,
    );
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      void start();
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col" onKeyDown={onKeyDown}>
      <SheetHeader className="gap-1.5 pb-3">
        <SheetTitle className="flex items-center gap-2 text-base">
          <Icon name="Bot" className="size-4 text-muted-foreground" />
          {single ? `Start an agent on ${single.ref}` : `Start ${targets.length} agents`}
        </SheetTitle>
        <SheetDescription className="line-clamp-2 text-[13px]">{single ? single.title : project ? `One worktree and agent per issue in ${project.name}, one after another.` : "One worktree and agent per issue, each on its project's default box."}</SheetDescription>
      </SheetHeader>
      <SheetPanel className="flex flex-col gap-5">
        {single && single.runs.length > 0 && (
          <div className="flex items-start gap-2 rounded-lg border border-warning/30 bg-warning/8 px-3 py-2 text-xs">
            <Icon name="TriangleAlert" className="mt-px size-3.5 shrink-0 text-warning" />
            <span className="min-w-0 flex-1">
              <b className="font-medium">{single.runs[0].worktree}</b> on {single.runs[0].box} is already for this issue. This makes another worktree beside it.
            </span>
          </div>
        )}

        {project && online.length > 0 && (
          <Field label="Box" hint={box === project.defaultBox ? "The project's default box." : undefined}>
            <PickOne
              label="Box"
              value={box}
              onChange={setBox}
              options={online.map((m) => ({ value: m.box, label: m.box, icon: <Icon name="Server" className="size-3.5" /> }))}
            />
          </Field>
        )}

        <Field label="Agent">
          {presets ? (
            presets.length ? (
              <AgentPicker presets={presets} value={agent} onChange={(id: string) => !steps && setAgent(id)} />
            ) : (
              <p className="text-muted-foreground text-xs">{infoBox} has no agents installed.</p>
            )
          ) : (
            <Skeleton className="h-7 w-56 rounded-lg" />
          )}
        </Field>

        {single && (
          <Field
            label="Worktree"
            hint={
              resolution ? (
                <>
                  Branch <span className="font-mono">{nameEdited ? name.trim() : resolution.branch}</span>
                  {resolution.exists && !nameEdited ? ", which already exists: the worktree checks it out." : ` from ${resolution.base || member?.location.default_branch || "the default branch"}.`}
                  {resolution.note && <span className="block">{resolution.note}</span>}
                </>
              ) : (
                "Asking the box what to call it…"
              )
            }
          >
            <div className="relative">
              <Input
                size="sm"
                className="font-mono"
                value={name}
                disabled={!resolution}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
                  setName(e.target.value);
                  setNameEdited(true);
                }}
              />
              {!resolution && <Spinner className="absolute top-1/2 right-2 size-3.5 -translate-y-1/2" />}
            </div>
          </Field>
        )}

        <Field
          label={single ? "First prompt" : "Prompt for each"}
          hint={
            single ? undefined : (
              <>
                {targets[0].template.variables.map((v, i, all) => (
                  <span key={v}>
                    {i > 0 && (i === all.length - 1 ? " and " : ", ")}
                    <Code>{`{{${v}}}`}</Code>
                  </span>
                ))}{" "}
                are filled in from each issue.
              </>
            )
          }
        >
          {single ? (
            prompt === undefined ? (
              <Skeleton className="h-40 w-full rounded-lg" />
            ) : (
              <Textarea
                className="min-h-40 font-mono text-xs [&_textarea]:max-h-[45vh]"
                value={prompt}
                onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => {
                  setPrompt(e.target.value);
                  setPromptEdited(true);
                }}
              />
            )
          ) : (
            <Textarea className="min-h-32 font-mono text-xs" disabled={!!steps} value={template} onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => setTemplate(e.target.value)} />
          )}
        </Field>

        {!single && (
          <ol className="flex flex-col divide-y rounded-lg border text-[13px]">
            {targets.map((t, i) => (
              <li key={t.key} className="flex items-center gap-2.5 px-3 py-2">
                <StepIcon step={steps?.[i]} />
                <span className="w-14 shrink-0 font-mono text-[11px] text-muted-foreground tabular-nums">{t.ref}</span>
                <span className="min-w-0 flex-1 truncate">{t.title}</span>
                <StepNote step={steps?.[i]} pending={!steps && t.runs.length ? `has ${t.runs[0].worktree}` : undefined} />
              </li>
            ))}
          </ol>
        )}

        {error && <pre className="whitespace-pre-wrap rounded-md bg-destructive/8 p-2 font-mono text-[11px] text-destructive">{error}</pre>}
      </SheetPanel>
      <SheetFooter className="items-center sm:justify-between">
        <span className="hidden text-muted-foreground text-xs sm:flex sm:items-center sm:gap-1.5">
          {finished ? summary(steps!) : agent && <><AgentIcon agent={agent} /> {single ? `${agentName} opens in a new tab` : `${agentName} on each, one at a time`}</>}
        </span>
        <div className="flex gap-2">
          <Button variant="ghost" size="sm" onClick={onClose}>
            {finished ? "Close" : "Cancel"}
          </Button>
          {!finished && (
            <Button size="sm" onClick={() => void start()} disabled={!ready}>
              {busy ? <Spinner className="size-3.5" /> : <Icon name="Play" className="size-3.5" />}
              {single ? "Start agent" : `Start ${targets.length}`}
              <Kbd className="ml-1 h-4.5 bg-primary-foreground/15 text-[10px] text-primary-foreground">⌘↵</Kbd>
            </Button>
          )}
        </div>
      </SheetFooter>
    </div>
  );
}

function Field({ label, hint, children }: { label: string; hint?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <span className="font-medium text-xs">{label}</span>
      {children}
      {hint && <span className="text-muted-foreground text-xs">{hint}</span>}
    </div>
  );
}

const Code = ({ children }: { children: React.ReactNode }) => <code className="rounded bg-muted px-1 font-mono text-[11px]">{children}</code>;

function StepIcon({ step }: { step?: Step }) {
  if (!step || step.state === "waiting") return <Icon name="Circle" className="size-3.5 shrink-0 text-muted-foreground/50" />;
  if (step.state === "working") return <Spinner className="size-3.5 shrink-0" />;
  if (step.state === "started") return <Icon name="CircleCheck" className="size-3.5 shrink-0 text-success" />;
  if (step.state === "skipped") return <Icon name="CircleMinus" className="size-3.5 shrink-0 text-muted-foreground" />;
  return <Icon name="CircleX" className="size-3.5 shrink-0 text-destructive" />;
}

function StepNote({ step, pending }: { step?: Step; pending?: string }) {
  const text = !step ? pending : step.state === "working" ? step.what : step.state === "started" ? step.worktree : step.state === "skipped" || step.state === "failed" ? step.why : undefined;
  if (!text) return null;
  return <span className={cn("max-w-[45%] shrink-0 truncate text-[11px]", step?.state === "failed" ? "text-destructive" : "text-muted-foreground", step?.state === "started" && "font-mono")} title={text}>{text}</span>;
}

function summary(steps: Step[]) {
  const n = (s: Step["state"]) => steps.filter((x) => x.state === s).length;
  return [`${n("started")} started`, n("skipped") && `${n("skipped")} skipped`, n("failed") && `${n("failed")} failed`].filter(Boolean).join(" · ");
}

// resolveName asks the box what to call an issue's worktree, when the issue
// is one its resolver knows (GitHub's); otherwise the target's own name.
async function resolveName(berth: BerthPluginContext, box: string, location: string, t: Target): Promise<Resolution> {
  if (!t.resolve) return t.worktree;
  return berth.api.request<Resolution>(box, "POST", `locations/${enc(location)}/resolve`, { input: t.resolve, kind: "smart" }).catch((): Resolution => t.worktree);
}

// unique keeps a new worktree's name clear of the location's others.
function unique(name: string, taken: string[]) {
  if (!taken.includes(name)) return name;
  for (let i = 2; ; i++) if (!taken.includes(`${name}-${i}`)) return `${name}-${i}`;
}

