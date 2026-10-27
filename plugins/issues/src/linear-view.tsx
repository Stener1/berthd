import { type BerthPluginContext, type LinearIssue, type LinearTeam, type Project, type Session, useCurrentWorktree, useProjects, useStorage } from "@berth/plugin";
import {
  AlertDialog,
  AlertDialogClose,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogPopup,
  AlertDialogTitle,
  Button,
  Checkbox,
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
  FilterChip,
  Icon,
  Input,
  Kbd,
  Menu,
  MenuGroup,
  MenuGroupLabel,
  MenuItem,
  MenuPopup,
  MenuSeparator,
  MenuTrigger,
  PickOne,
  Skeleton,
  Spinner,
  Textarea,
  Tip,
  Tooltip,
  TooltipPopup,
  TooltipTrigger,
  ViewHeader,
  cn,
} from "@berth/plugin/ui";
import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";

import { Avatar, LabelChip, Post, Runs, StatePill } from "./detail";
import { since } from "./gh";
import { DEFAULT_TEMPLATE, PRIORITY_ICON, STATE_ICON, TEMPLATE_VARIABLES, priorityRank, renderPrompt, runsByIdentifier, worktreeOf } from "./linear";
import { MINE, type Problem, type Scope, describeProblem, detailOf, listOf, loadDetail, loadList, loadStatus, loadTeams, postComment, statusOf, teamsOf, useLinearStore } from "./linear-store";
import { Markdown } from "./markdown";
import { KeyHints, RowSkeleton, SessionFeed, useListKeys } from "./shared";
import { StartSheet, type Target } from "./start-sheet";
import type { Run } from "./store";

// The Linear side of Issues: what is assigned to you, or a team's open
// issues, read by the laptop with your API key; and an agent on any of them
// in the project you choose, on the branch Linear suggests for the issue.

type Who = "all" | "mine" | "unassigned";
type Sort = "updated" | "newest" | "priority";
type Row = LinearIssue & { key: string };

// Linear's colours come with their #; a label chip wants them without.
const chip = (l: { name: string; color: string }) => ({ name: l.name, color: l.color.replace(/^#/, "") });

export function LinearIssues({ berth, source }: { berth: BerthPluginContext; source: ReactNode }) {
  useLinearStore();
  useEffect(() => {
    // Asked again each time the screen opens: the key is added, changed and
    // removed in Settings, which this screen doesn't hear about.
    void loadStatus(berth, true);
  }, [berth]);
  const load = statusOf();
  const status = load?.status === "ok" ? load.value : load?.status === "loading" ? load.prev : undefined;

  if (!status?.connected) {
    const waiting = !load || load.status === "loading";
    const problem: Problem = load?.status === "problem" ? load.problem : { kind: "off" };
    const d = describeProblem(problem);
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <ViewHeader title="Issues" description="Your Linear issues, and an agent on any of them in one step." actions={source} />
        {waiting ? null : (
          <Empty className="flex-1">
            <EmptyHeader>
              <Icon name={problem.kind === "off" ? "Cable" : d.icon} className="mb-2 size-6 text-muted-foreground" />
              <EmptyTitle>{problem.kind === "off" ? "Connect Linear" : d.title}</EmptyTitle>
              <EmptyDescription>{d.body}</EmptyDescription>
            </EmptyHeader>
            {problem.kind !== "error" && (
              <Button size="sm" onClick={() => berth.openSettings("integrations")}>
                <Icon name="Settings" className="size-3.5" />
                Open Settings
              </Button>
            )}
          </Empty>
        )}
      </div>
    );
  }
  return <Connected berth={berth} source={source} viewer={status.viewer} workspace={status.workspace?.name} />;
}

function Connected({ berth, source, viewer, workspace }: { berth: BerthPluginContext; source: ReactNode; viewer?: { id: string; name: string }; workspace?: string }) {
  const projects = useProjects();
  const current = useCurrentWorktree();

  useEffect(() => {
    void loadTeams(berth);
  }, [berth]);
  const teamsLoad = teamsOf();
  const teams = useMemo(() => (teamsLoad?.status === "ok" ? teamsLoad.value : teamsLoad?.status === "loading" ? (teamsLoad.prev ?? []) : []), [teamsLoad]);

  // Which issues: yours, or one team's.
  const [storedScope, setScope] = useStorage<Scope>("linear.scope", MINE);
  const scope = storedScope === MINE || teams.some((t) => t.key === storedScope) || teamsLoad?.status !== "ok" ? storedScope : MINE;
  const team = teams.find((t) => t.key === scope);

  // Which project an agent starts in: Linear's issues belong to teams, not
  // to repositories, so the person says. The one in front is the first guess.
  const [storedProject, setProject] = useStorage("linear.project", "");
  const fromCurrent = current && projects.find((p) => p.members.some((m) => m.box === current.box && m.location.name === current.location))?.id;
  const project = projects.find((p) => p.id === storedProject) ?? projects.find((p) => p.id === fromCurrent) ?? (projects.length === 1 ? projects[0] : undefined);

  const [who, setWho] = useStorage<Who>("linear.who", "all");
  const [sort, setSort] = useStorage<Sort>("linear.sort", "updated");
  const [labels, setLabels] = useState<string[]>([]);
  const [query, setQuery] = useState("");
  const search = useRef<HTMLInputElement>(null);

  useEffect(() => {
    void loadList(berth, scope);
  }, [berth, scope]);
  const refresh = () => void loadList(berth, scope, true);
  const load = listOf(scope);
  const loading = !load || load.status === "loading";
  const problem = load?.status === "problem" ? load.problem : undefined;
  const at = load?.status === "ok" ? load.at : load?.status;
  const rows: Row[] = useMemo(
    () => (load?.status === "ok" ? load.value : load?.status === "loading" ? (load.prev ?? []) : []).map((i) => ({ ...i, key: i.identifier })),
    // The list is replaced whole; its time says when.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [at, scope],
  );

  // Agents on issues: every box any project is on, since an issue's
  // worktree can be in any of them.
  const boxes = useMemo(() => [...new Set(projects.flatMap((p) => p.members.map((m) => m.box)))].sort(), [projects]);
  const [sessions, setSessions] = useState<Record<string, Session[] | undefined>>({});
  const onSessions = useCallback((box: string, s?: Session[]) => setSessions((prev) => (prev[box] === s ? prev : { ...prev, [box]: s })), []);
  const teamKeys = useMemo(() => [...new Set([...teams.map((t) => t.key), ...rows.map((r) => r.team.key)])], [teams, rows]);
  const runs = useMemo(() => runsByIdentifier(projects, sessions, teamKeys), [projects, sessions, teamKeys]);

  const labelCounts = useMemo(() => {
    const m = new Map<string, { name: string; color: string; n: number }>();
    for (const r of rows) for (const l of r.labels) m.set(l.name, { ...l, n: (m.get(l.name)?.n ?? 0) + 1 });
    return [...m.values()].sort((a, b) => b.n - a.n || a.name.localeCompare(b.name));
  }, [rows]);
  useEffect(() => setLabels((ls) => ls.filter((l) => labelCounts.some((c) => c.name === l))), [labelCounts]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const out = rows.filter(
      (r) =>
        (scope === MINE || who === "all" || (who === "mine" ? !!viewer && r.assignee?.id === viewer.id : !r.assignee)) &&
        labels.every((l) => r.labels.some((x) => x.name === l)) &&
        (!q || `${r.identifier} ${r.title} ${r.state.name} ${r.assignee?.name ?? ""} ${r.labels.map((l) => l.name).join(" ")}`.toLowerCase().includes(q)),
    );
    const by: Record<Sort, (a: Row, b: Row) => number> = {
      updated: (a, b) => b.updated_at.localeCompare(a.updated_at),
      newest: (a, b) => b.created_at.localeCompare(a.created_at),
      priority: (a, b) => priorityRank(a.priority) - priorityRank(b.priority) || b.updated_at.localeCompare(a.updated_at),
    };
    return out.sort(by[sort]);
  }, [rows, scope, who, viewer, labels, query, sort]);

  // Selection: the issue in the right-hand pane, and the ones ticked for a batch.
  const [selectedKey, setSelectedKey] = useState<string>();
  const selected = filtered.find((r) => r.key === selectedKey) ?? filtered[0];
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const checkedRows = filtered.filter((r) => checked.has(r.key));
  const toggle = (key: string) =>
    setChecked((s) => {
      const n = new Set(s);
      if (!n.delete(key)) n.add(key);
      return n;
    });

  const [targets, setTargets] = useState<Target[]>([]);
  // The issues waiting on "which project?": starting with none chosen opens
  // the project menu, and the choice carries on to the sheet.
  const [asking, setAsking] = useState<Row[]>();
  const startOn = (rs: Row[], inProject = project) => {
    if (!rs.length || projects.length === 0) return;
    if (!inProject) return setAsking(rs);
    setTargets(
      rs.map((r) => ({
        project: inProject,
        key: `linear:${r.identifier}`,
        ref: r.identifier,
        title: r.title,
        runs: runs.get(r.identifier) ?? [],
        worktree: worktreeOf(r),
        template: { key: "linear.template", initial: DEFAULT_TEMPLATE, variables: TEMPLATE_VARIABLES },
        render: async (template) => renderPrompt(template, { ...r, description: (await loadDetail(berth, r.id))?.description }),
      })),
    );
  };

  useListKeys<Row>({
    list: filtered,
    selected,
    ticked: checkedRows,
    blocked: targets.length > 0 || !!asking,
    search,
    select: setSelectedKey,
    toggle,
    start: startOn,
    open: (r) => berth.openUrl(r.url),
    clear: () => setChecked(new Set()),
  });

  const busyRuns = useMemo(() => [...runs.values()].filter((rs) => rs[0]?.session && rs[0].session.agent_state !== "finished").length, [runs]);
  const noProject = projects.length === 0 ? "Add a project to a box first: an agent needs a repository to work in." : undefined;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {boxes.map((b) => (
        <SessionFeed key={b} box={b} onSessions={onSessions} />
      ))}
      <ViewHeader
        title="Issues"
        description={busyRuns > 0 ? `${busyRuns} agent${busyRuns === 1 ? " is" : "s are"} on Linear issues right now.` : `Your Linear issues${workspace ? ` in ${workspace}` : ""}, and an agent on any of them in one step.`}
        actions={
          <>
            {source}
            <ScopePicker teams={teams} scope={scope} onChange={(s) => (setScope(s), setChecked(new Set()), setSelectedKey(undefined))} />
            <StartInPicker
              projects={projects}
              project={project}
              open={asking ? true : undefined}
              onOpenChange={(o) => !o && setAsking(undefined)}
              onChange={(p) => {
                setProject(p.id);
                if (asking) startOn(asking, p);
                setAsking(undefined);
              }}
            />
            <Tooltip>
              <TooltipTrigger render={<Button size="icon-sm" variant="ghost" aria-label="Refresh" onClick={refresh} />}>
                <Icon name="RefreshCw" className={cn("size-3.5", loading && "animate-spin")} />
              </TooltipTrigger>
              <TooltipPopup>Refresh</TooltipPopup>
            </Tooltip>
          </>
        }
      />

      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b px-4 py-2">
        <div className="relative">
          <Icon name="Search" className="pointer-events-none absolute top-1/2 left-2 z-10 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input ref={search} size="sm" className="w-52 [&_input]:pl-7" placeholder="Search issues" value={query} onChange={(e: React.ChangeEvent<HTMLInputElement>) => setQuery(e.target.value)} />
          {!query && <Kbd className="pointer-events-none absolute top-1/2 right-1.5 h-4.5 -translate-y-1/2 text-[10px]">/</Kbd>}
        </div>
        {scope !== MINE && (
          <PickOne
            label="Assignee"
            value={who}
            onChange={(v: string) => setWho(v as Who)}
            options={[
              { value: "all", label: "All" },
              { value: "mine", label: "Mine" },
              { value: "unassigned", label: "Unassigned" },
            ]}
          />
        )}
        <SortMenu sort={sort} onChange={setSort} />
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1">
          {labelCounts.slice(0, 10).map((l) => (
            <FilterChip key={l.name} className="text-[11px]" pressed={labels.includes(l.name)} onPressedChange={(on: boolean) => setLabels((ls) => (on ? [...ls, l.name] : ls.filter((x) => x !== l.name)))}>
              <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: l.color }} />
              <span className="truncate">{l.name}</span>
            </FilterChip>
          ))}
        </div>
      </div>

      <div className="flex min-h-0 flex-1">
        <aside className="flex w-[25rem] shrink-0 flex-col border-r max-xl:w-[21rem]">
          <ul className="min-h-0 flex-1 overflow-y-auto p-2" aria-label={team ? `${team.name}'s open issues` : "Issues assigned to you"}>
            {rows.length === 0 && loading ? (
              Array.from({ length: 7 }, (_, i) => <RowSkeleton key={i} />)
            ) : rows.length === 0 && problem ? (
              <ProblemNote berth={berth} problem={problem} />
            ) : filtered.length === 0 ? (
              <p className="px-3 py-8 text-center text-muted-foreground text-xs">
                {rows.length === 0 ? (scope === MINE ? "Nothing is assigned to you. Nice." : "No open issues. Nice.") : "No issues match."}
                {rows.length > 0 && (
                  <button
                    type="button"
                    className="ml-1 font-medium text-foreground hover:underline"
                    onClick={() => {
                      setQuery("");
                      setLabels([]);
                      setWho("all");
                    }}
                  >
                    Clear filters
                  </button>
                )}
              </p>
            ) : (
              filtered.map((r) => (
                <IssueRow
                  key={r.key}
                  row={r}
                  viewer={viewer?.id}
                  run={runs.get(r.key)?.[0]}
                  active={r.key === selected?.key}
                  checked={checked.has(r.key)}
                  selecting={checked.size > 0}
                  onSelect={() => setSelectedKey(r.key)}
                  onCheck={() => toggle(r.key)}
                  onOpenRun={(run) => berth.openWorktree({ box: run.box, location: run.location, worktree: run.worktree, path: run.path })}
                />
              ))
            )}
          </ul>
          {checkedRows.length > 0 ? (
            <footer className="flex shrink-0 items-center gap-2 border-t bg-muted/40 px-3 py-2 text-xs">
              <span className="font-medium">{checkedRows.length} selected</span>
              <button type="button" className="text-muted-foreground hover:text-foreground" onClick={() => setChecked(new Set())}>
                Clear
              </button>
              <Tip label={noProject}>
                <Button size="xs" className="ml-auto" disabled={!!noProject} onClick={() => startOn(checkedRows)}>
                  <Icon name="Bot" className="size-3.5" />
                  Start {checkedRows.length} agent{checkedRows.length === 1 ? "" : "s"}
                </Button>
              </Tip>
            </footer>
          ) : (
            <KeyHints where="Linear" count={filtered.length === rows.length ? `${rows.length}` : `${filtered.length} of ${rows.length}`} />
          )}
        </aside>
        <section className="min-w-0 flex-1 overflow-y-auto">
          {selected ? (
            <Detail key={selected.key} berth={berth} row={selected} runs={runs.get(selected.key) ?? []} viewer={viewer} noProject={noProject} onStart={() => startOn([selected])} />
          ) : (
            <div className="grid h-full place-items-center text-muted-foreground text-xs">{loading ? "" : "Pick an issue to read it."}</div>
          )}
        </section>
      </div>
      <StartSheet berth={berth} targets={targets} onClose={() => setTargets([])} onStarted={() => setChecked(new Set())} />
    </div>
  );
}

function ProblemNote({ berth, problem }: { berth: BerthPluginContext; problem: Problem }) {
  const d = describeProblem(problem);
  return (
    <div className="m-1 flex items-start gap-3 rounded-lg border bg-muted/30 px-4 py-3">
      <Icon name={d.icon} className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0">
        <p className="font-medium text-[13px]">{d.title}</p>
        <p className="mt-0.5 whitespace-pre-wrap text-muted-foreground text-xs">{d.body}</p>
        {problem.kind !== "error" && (
          <Button size="xs" variant="outline" className="mt-2" onClick={() => berth.openSettings("integrations")}>
            Open Settings
          </Button>
        )}
      </div>
    </div>
  );
}

// StateIcon is an issue's workflow state: the glyph says how far along it
// is, the colour is the one the team gave the state in Linear.
function StateIcon({ state, className }: { state: LinearIssue["state"]; className?: string }) {
  return (
    <span role="img" aria-label={state.name} className={cn("inline-flex", className)} style={{ color: state.color }}>
      <Icon name={STATE_ICON[state.type] ?? "Circle"} className="size-3.5" />
    </span>
  );
}

function Priority({ issue }: { issue: LinearIssue }) {
  const icon = PRIORITY_ICON[issue.priority];
  if (!icon) return null;
  const label = `${issue.priority_label ?? "Priority"} priority`;
  return (
    <Tip label={label}>
      <span role="img" aria-label={label} className={cn("inline-flex", issue.priority === 1 ? "text-warning-foreground" : "text-muted-foreground")}>
        <Icon name={icon} className="size-3.5" />
      </span>
    </Tip>
  );
}

function IssueRow({
  row,
  viewer,
  run,
  active,
  checked,
  selecting,
  onSelect,
  onCheck,
  onOpenRun,
}: {
  row: Row;
  viewer?: string;
  run?: Run;
  active: boolean;
  checked: boolean;
  selecting: boolean;
  onSelect(): void;
  onCheck(): void;
  onOpenRun(run: Run): void;
}) {
  return (
    <li id={`issue-${row.key}`}>
      {/* biome-ignore lint/a11y/useSemanticElements: the row holds a checkbox, so it can't be a button. */}
      <div
        role="button"
        tabIndex={-1}
        onClick={onSelect}
        onKeyDown={undefined}
        aria-current={active || undefined}
        className={cn("group flex w-full cursor-default gap-2.5 rounded-lg px-2.5 py-2 text-left outline-none hover:bg-accent/50", active && "bg-accent hover:bg-accent")}
      >
        <span className="relative mt-[3px] grid size-4 shrink-0 place-items-center">
          <StateIcon state={row.state} className={cn((selecting || checked) && "hidden", "group-hover:hidden")} />
          {/* biome-ignore lint/a11y/noStaticElementInteractions: stops the row's click. */}
          <span className={cn("hidden", (selecting || checked) && "flex", "group-hover:flex")} onClick={(e) => e.stopPropagation()} onKeyDown={undefined}>
            <Checkbox checked={checked} onCheckedChange={onCheck} aria-label={`Select ${row.identifier}`} />
          </span>
        </span>
        <span className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="line-clamp-2 font-medium text-[13px] leading-snug">{row.title}</span>
          <span className="flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
            <span className="shrink-0 font-mono tabular-nums">{row.identifier}</span>
            <span className="truncate">
              · {row.state.name} · {since(row.updated_at)}
            </span>
          </span>
          {(row.labels.length > 0 || run) && (
            <span className="flex min-w-0 items-center gap-1 overflow-hidden">
              {run && (
                // biome-ignore lint/a11y/useKeyWithClickEvents: the issue's own keys reach it.
                <Tip label={`${run.worktree} on ${run.box}: open it`}>
                  <span
                    className="inline-flex shrink-0 items-center gap-1 rounded-full border bg-background px-1.5 py-px hover:bg-accent"
                    onClick={(e) => {
                      e.stopPropagation();
                      onOpenRun(run);
                    }}
                  >
                    <StatePill run={run} compact />
                    <span className="max-w-28 truncate text-[11px] text-muted-foreground">{run.session ? run.box : run.worktree}</span>
                  </span>
                </Tip>
              )}
              {row.labels.slice(0, 3).map((l) => (
                <LabelChip key={l.name} label={chip(l)} />
              ))}
              {row.labels.length > 3 && <span className="shrink-0 text-[10px] text-muted-foreground">+{row.labels.length - 3}</span>}
            </span>
          )}
        </span>
        <span className="flex shrink-0 flex-col items-end gap-1.5 pt-px text-[11px] text-muted-foreground">
          {row.assignee && <Avatar login={row.assignee.name} className={cn("size-4 text-[8px] ring-2 ring-background", row.assignee.id === viewer && "ring-primary/60")} />}
          <Priority issue={row} />
        </span>
      </div>
    </li>
  );
}

function Detail({
  berth,
  row,
  runs,
  viewer,
  noProject,
  onStart,
}: {
  berth: BerthPluginContext;
  row: Row;
  runs: Run[];
  viewer?: { id: string; name: string };
  // Why no agent can start (there is no project at all), when that is so.
  noProject?: string;
  onStart(): void;
}) {
  useLinearStore();
  const load = detailOf(row.id);
  useEffect(() => {
    void loadDetail(berth, row.id);
  }, [berth, row.id]);
  const detail = load?.status === "ok" ? load.value : load?.status === "loading" ? load.prev : undefined;
  const open = (u: string) => berth.openUrl(u);

  return (
    <article className="mx-auto flex max-w-3xl flex-col gap-4 px-6 py-5">
      <header className="flex flex-col gap-2">
        <p className="flex flex-wrap items-center gap-x-1.5 text-muted-foreground text-xs">
          <span className="inline-flex items-center gap-1 rounded-full border px-1.5 py-px font-medium text-[11px] text-foreground">
            <StateIcon state={row.state} />
            {row.state.name}
          </span>
          <span className="font-mono">{row.identifier}</span>
          <span>·</span>
          <span>{row.team.name}</span>
          <span>·</span>
          <span>
            opened {since(row.created_at)}
            {row.creator && (
              <>
                {" "}
                by <b className="font-medium text-foreground">{row.creator}</b>
              </>
            )}
          </span>
        </p>
        <h2 className="font-semibold text-lg leading-snug tracking-tight">{row.title}</h2>
        <div className="flex flex-wrap items-center gap-1.5">
          {row.labels.map((l) => (
            <LabelChip key={l.name} label={chip(l)} />
          ))}
          <span className="ml-1 inline-flex items-center gap-1.5 text-muted-foreground text-xs">
            <Icon name="UserRound" className="size-3" />
            {row.assignee ? (row.assignee.id === viewer?.id ? "you" : row.assignee.name) : "Unassigned"}
          </span>
          {row.priority > 0 && (
            <span className="ml-1 inline-flex items-center gap-1 text-muted-foreground text-xs">
              <Priority issue={row} />
              {row.priority_label}
            </span>
          )}
        </div>
        <div className="mt-1 flex flex-wrap items-center gap-2">
          <Tip label={noProject}>
            <Button size="sm" onClick={onStart} disabled={!!noProject}>
              <Icon name="Bot" className="size-3.5" />
              Start an agent
              <Kbd className="ml-1 h-4.5 bg-primary-foreground/15 text-[10px] text-primary-foreground">S</Kbd>
            </Button>
          </Tip>
          <Button size="sm" variant="outline" onClick={() => open(row.url)}>
            <Icon name="ExternalLink" className="size-3.5" />
            Open in Linear
          </Button>
          <Tooltip>
            <TooltipTrigger render={<Button size="icon-sm" variant="ghost" aria-label="Refresh this issue" onClick={() => void loadDetail(berth, row.id, true)} />}>
              <Icon name="RefreshCw" className={cn("size-3.5", load?.status === "loading" && "animate-spin")} />
            </TooltipTrigger>
            <TooltipPopup>Refresh this issue</TooltipPopup>
          </Tooltip>
        </div>
      </header>

      {runs.length > 0 && <Runs berth={berth} runs={runs} />}

      {detail && detail.links.length > 0 && (
        <section className="flex flex-col rounded-lg border">
          <h3 className="border-b px-3 py-1.5 font-medium text-[11px] text-muted-foreground">Linked to it</h3>
          {detail.links.map((l) => (
            <button key={l.url} type="button" onClick={() => open(l.url)} className="flex items-center gap-2 px-3 py-2 text-left text-[13px] hover:bg-accent/50">
              <Icon name={l.source === "github" ? "GitPullRequest" : "Link"} className="size-3.5 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1 truncate">{l.title}</span>
            </button>
          ))}
        </section>
      )}

      {load?.status === "problem" && !detail ? (
        <ProblemNote berth={berth} problem={load.problem} />
      ) : !detail ? (
        <div className="flex flex-col gap-2 rounded-lg border p-4">
          <Skeleton className="h-3.5 w-1/3" />
          <Skeleton className="h-3.5 w-full" />
          <Skeleton className="h-3.5 w-5/6" />
          <Skeleton className="h-3.5 w-2/3" />
        </div>
      ) : (
        <>
          <Post author={row.creator} at={row.created_at} viewer={viewer?.name} badge="Author">
            {detail.description.trim() ? <Markdown source={detail.description} repo="" onLink={open} /> : <p className="text-muted-foreground text-[13px] italic">No description.</p>}
          </Post>
          {detail.comments.map((c, i) => (
            <Post key={c.url || `${c.created_at}-${i}`} author={c.author} at={c.created_at} viewer={viewer?.name}>
              <Markdown source={c.body} repo="" onLink={open} />
            </Post>
          ))}
          <Composer berth={berth} row={row} viewer={viewer?.name} />
        </>
      )}
    </article>
  );
}

// Comments go to Linear under your name and notify everyone subscribed to
// the issue, so they are always confirmed first.
function Composer({ berth, row, viewer }: { berth: BerthPluginContext; row: Row; viewer?: string }) {
  const [text, setText] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [posting, setPosting] = useState(false);
  const [error, setError] = useState<string>();

  async function post() {
    setPosting(true);
    setError(undefined);
    try {
      await postComment(berth, row.id, text.trim());
      setText("");
      setConfirming(false);
      berth.notify(`Commented on ${row.identifier}`, row.title);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setConfirming(false);
    } finally {
      setPosting(false);
    }
  }

  return (
    <div className="flex gap-3">
      <Avatar login={viewer} className="mt-0.5 size-6 text-[10px]" />
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <Textarea
          className="min-h-20 text-[13px]"
          placeholder="Leave a comment (Markdown)"
          value={text}
          onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => setText(e.target.value)}
          onKeyDown={(e: React.KeyboardEvent) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && text.trim()) {
              e.preventDefault();
              setConfirming(true);
            }
          }}
        />
        {error && <p className="text-destructive text-xs">{error}</p>}
        <div className="flex items-center justify-end gap-2">
          <span className="mr-auto text-muted-foreground text-xs">{viewer ? `Posts to Linear as ${viewer}.` : null}</span>
          <Button size="sm" variant="outline" disabled={!text.trim() || posting} onClick={() => setConfirming(true)}>
            {posting ? <Spinner className="size-3.5" /> : <Icon name="MessageSquare" className="size-3.5" />}
            Comment
          </Button>
        </div>
      </div>
      <AlertDialog open={confirming} onOpenChange={(o: boolean) => !posting && setConfirming(o)}>
        <AlertDialogPopup>
          <AlertDialogHeader>
            <AlertDialogTitle>Comment on {row.identifier}?</AlertDialogTitle>
            <AlertDialogDescription>
              It's posted to Linear{viewer ? ` as ${viewer}` : ""}, and everyone subscribed to the issue is notified.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <div className="mx-6 mb-5 max-h-48 overflow-y-auto rounded-md border bg-muted/40 px-3 py-2">
            <Markdown source={text} repo="" onLink={() => undefined} />
          </div>
          <AlertDialogFooter>
            <AlertDialogClose render={<Button variant="ghost" disabled={posting} />}>Cancel</AlertDialogClose>
            <Button onClick={() => void post()} disabled={posting}>
              {posting && <Spinner className="size-3.5" />}
              Post comment
            </Button>
          </AlertDialogFooter>
        </AlertDialogPopup>
      </AlertDialog>
    </div>
  );
}

function ScopePicker({ teams, scope, onChange }: { teams: LinearTeam[]; scope: Scope; onChange(s: Scope): void }) {
  const team = teams.find((t) => t.key === scope);
  return (
    <Menu>
      <MenuTrigger render={<Button size="sm" variant="outline" className="max-w-56" aria-label="Which issues" />}>
        <Icon name={team ? "Users" : "UserRound"} className="size-3.5" />
        <span className="truncate">{team ? team.name : "Assigned to me"}</span>
        <Icon name="ChevronsUpDown" className="size-3 opacity-60" />
      </MenuTrigger>
      <MenuPopup align="end" className="min-w-56">
        <MenuItem onClick={() => onChange(MINE)}>
          <Icon name="UserRound" className="size-3.5" />
          Assigned to me
          {scope === MINE && <Icon name="Check" className="ml-auto size-3.5" />}
        </MenuItem>
        {teams.length > 0 && (
          <>
            <MenuSeparator />
            <MenuGroup>
              <MenuGroupLabel>Teams</MenuGroupLabel>
              {teams.map((t) => (
                <MenuItem key={t.id} onClick={() => onChange(t.key)}>
                  <Icon name="Users" className="size-3.5" />
                  <span className="truncate">{t.name}</span>
                  <span className="font-mono text-[11px] text-muted-foreground">{t.key}</span>
                  {scope === t.key && <Icon name="Check" className="ml-auto size-3.5" />}
                </MenuItem>
              ))}
            </MenuGroup>
          </>
        )}
      </MenuPopup>
    </Menu>
  );
}

// StartInPicker is the project an agent works in. A Linear issue names no
// repository, so this is the one thing the person has to say.
function StartInPicker({ projects, project, open, onOpenChange, onChange }: { projects: Project[]; project?: Project; open?: boolean; onOpenChange(open: boolean): void; onChange(p: Project): void }) {
  if (projects.length === 0) return null;
  return (
    <Menu open={open} onOpenChange={onOpenChange}>
      <MenuTrigger render={<Button size="sm" variant="outline" className="max-w-56" aria-label="Project agents start in" />}>
        <Icon name="FolderGit2" className="size-3.5" />
        <span className="truncate">{project ? project.name : "Choose a project"}</span>
        <Icon name="ChevronsUpDown" className="size-3 opacity-60" />
      </MenuTrigger>
      <MenuPopup align="end" className="min-w-56">
        <MenuGroup>
          <MenuGroupLabel>Agents start in</MenuGroupLabel>
          {projects.map((p) => (
            <MenuItem key={p.id} onClick={() => onChange(p)}>
              <Icon name="FolderGit2" className="size-3.5" />
              <span className="flex min-w-0 flex-col">
                <span className="truncate">{p.name}</span>
                {p.slug && <span className="truncate text-[11px] text-muted-foreground">{p.slug}</span>}
              </span>
              {project?.id === p.id && <Icon name="Check" className="ml-auto size-3.5" />}
            </MenuItem>
          ))}
        </MenuGroup>
      </MenuPopup>
    </Menu>
  );
}

const SORTS: Record<Sort, string> = { updated: "Recently updated", newest: "Newest", priority: "Priority" };

function SortMenu({ sort, onChange }: { sort: Sort; onChange(s: Sort): void }) {
  return (
    <Menu>
      <MenuTrigger render={<Button size="sm" variant="ghost" className="text-muted-foreground" />}>
        <Icon name="ArrowDownWideNarrow" className="size-3.5" />
        {SORTS[sort]}
      </MenuTrigger>
      <MenuPopup align="start">
        {(Object.keys(SORTS) as Sort[]).map((s) => (
          <MenuItem key={s} onClick={() => onChange(s)}>
            {SORTS[s]}
            {s === sort && <Icon name="Check" className="ml-auto size-3.5" />}
          </MenuItem>
        ))}
      </MenuPopup>
    </Menu>
  );
}
