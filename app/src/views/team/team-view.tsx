import { ArrowLeftIcon, ArrowRightIcon, CopyIcon, ExternalLinkIcon, LockIcon, RotateCwIcon, SearchIcon } from "lucide-react";
import { type ReactNode, useCallback, useEffect, useMemo, useState } from "react";

import { ErrorText } from "@/components/error-note";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { toastManager } from "@/components/ui/toast";
import { copyText } from "@/lib/clipboard";
import { ago, errorMessage } from "@/lib/format";
import { openUrl } from "@/lib/open-url";
import { useStore } from "@/lib/store";
import { type GitHubState, loadTeams, orgFromInput, putRun, refreshGitHub, runFor, sudoCount, teamApi, type TeamStatus, type TeamView as View, useTeam, validOrg } from "@/lib/team";
import { focusSession } from "@/lib/workspaces";
import { finishOnboarding } from "@/views/onboarding/onboarding-state";
import { openAddBox } from "@/views/onboarding/add-box-dialog";
import { TeamDone } from "@/views/team/team-done";
import { TeamFiles } from "@/views/team/team-files";
import { GitHubMark, OrgAvatar, OrgHeader, Section } from "@/views/team/team-parts";
import { Plan } from "@/views/team/team-plan";
import { Checklist, RunCard } from "@/views/team/team-rail";
import { ViewHeader } from "@/views/view-header";

type From = "onboarding" | "addbox" | "link" | "sidebar" | "palette";

// TeamSetupView is Team setup as one page: the org's .berth, read like its
// repo on GitHub, what you'll get with every command a click away, and a
// short checklist that ends in one button. Pressing it turns the same rows
// into progress.
export function TeamSetupView({ org: initial, from, box: wantBox, update, onBack }: { org?: string; from?: From; box?: string; update?: boolean; onBack?(): void }) {
  const [org, setOrg] = useState(initial ?? "");
  useEffect(() => setOrg(initial ?? ""), [initial]);
  const github = useTeam((s) => s.github);
  useEffect(() => {
    void refreshGitHub();
    void loadTeams();
  }, []);

  const firstRun = from === "onboarding" && !useStore.getState().status?.boxes.length;
  const body = !github ? (
    <Loading />
  ) : github.state !== "ready" ? (
    <ConnectGitHub github={github} org={org} />
  ) : !org ? (
    <AskOrg onOrg={setOrg} />
  ) : (
    <OrgPage key={org} org={org} from={from} wantBox={wantBox} wantUpdate={update} github={github} onOrg={setOrg} />
  );

  return (
    <div className="@container flex h-full flex-col bg-background">
      {!firstRun && (
        <ViewHeader
          title="Team setup"
          description={org ? <span className="font-mono">github.com/{org}</span> : "Set a box up the way your team's are, from your GitHub org"}
          actions={
            onBack ? (
              <Button size="sm" variant="ghost" onClick={onBack}>
                <ArrowLeftIcon /> Back
              </Button>
            ) : undefined
          }
        />
      )}
      {firstRun && onBack && (
        <div className="flex h-9 shrink-0 items-center px-3">
          <Button size="sm" variant="ghost" className="text-muted-foreground" onClick={onBack}>
            <ArrowLeftIcon /> Back
          </Button>
        </div>
      )}
      <div data-testid="team-page" className="min-h-0 flex-1 overflow-y-auto">
        {body}
      </div>
    </div>
  );
}

function Loading() {
  return (
    <div className="mx-auto max-w-6xl px-8 pt-8">
      <div className="flex items-center gap-4">
        <Skeleton className="size-13 rounded-xl" />
        <div className="space-y-2">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-6 w-72" />
        </div>
      </div>
      <Skeleton className="mt-8 h-64 rounded-xl" />
    </div>
  );
}

function Centered({ children }: { children: ReactNode }) {
  return <div className="mx-auto w-full max-w-lg px-6 pt-[12vh] pb-16">{children}</div>;
}

// ConnectGitHub comes before anything else: the org's setup (and its
// private repos) are read with this computer's own GitHub CLI.
function ConnectGitHub({ github, org }: { github: GitHubState; org: string }) {
  const client = useStore((s) => s.client);
  const [waiting, setWaiting] = useState(false);
  const [command, setCommand] = useState<string>();
  // Checks again on its own while gh auth login runs in its terminal.
  useEffect(() => {
    if (!waiting) return;
    const t = window.setInterval(() => void refreshGitHub(), 1000);
    return () => window.clearInterval(t);
  }, [waiting]);
  const connect = async () => {
    if (!client) return;
    try {
      const r = await teamApi.githubLogin(client);
      setCommand(r.command);
      setWaiting(true);
      if (!r.opened) toastManager.add({ title: "Run it in a terminal", description: r.error ?? `Berth couldn't open one here: ${r.command}` });
    } catch (err) {
      toastManager.add({ type: "error", title: "Couldn't start the sign-in", description: errorMessage(err) });
    }
  };
  const missing = github.state === "missing";
  return (
    <Centered>
      <div data-testid="team-github" data-state={github.state}>
        <span className="flex size-11 items-center justify-center rounded-xl border bg-card">
          <GitHubMark className="size-5" />
        </span>
        <h1 className="mt-5 font-semibold text-2xl tracking-tight">{missing ? "Install GitHub's CLI first" : "Connect GitHub"}</h1>
        <p className="mt-2 text-muted-foreground leading-relaxed">
          Berth reads {org ? <span className="font-mono text-foreground">{org}/.berth</span> : "your org's team setup"} and the repos it lists with <span className="font-mono text-foreground">gh</span>, GitHub's own CLI, signed in as you. Private setups and private repos need it. Berth has no GitHub app of its own and keeps no token.
        </p>
        {missing ? (
          <div className="mt-6 rounded-xl border bg-card p-4">
            <p className="font-medium text-sm">Install GitHub CLI</p>
            <div className="mt-2 flex items-center gap-2 rounded-lg border bg-background px-3 py-2 font-mono text-sm">
              <span className="text-muted-foreground">$</span>
              <span className="min-w-0 flex-1 truncate">{github.install?.command ?? "brew install gh"}</span>
              <Button size="icon-xs" variant="ghost" aria-label="Copy the command" onClick={() => void copyText(github.install?.command ?? "brew install gh", "Copied the command")}>
                <CopyIcon />
              </Button>
            </div>
            <p className="mt-2 text-muted-foreground text-xs">
              {github.install?.os === "linux" ? "On Linux, from GitHub's own apt or dnf repository." : github.install?.os === "windows" ? "With winget, in PowerShell." : "With Homebrew, in Terminal."} Other ways:{" "}
              <button type="button" className="underline underline-offset-2 hover:text-foreground" onClick={() => void openUrl(github.install?.url ?? "https://cli.github.com")}>
                cli.github.com
              </button>
            </p>
            <Button className="mt-4" variant="outline" onClick={() => void refreshGitHub()}>
              <RotateCwIcon /> I've installed it
            </Button>
          </div>
        ) : (
          <div className="mt-6 rounded-xl border bg-card p-4">
            {!waiting ? (
              <>
                <Button data-testid="connect-github" onClick={connect}>
                  <GitHubMark /> Connect GitHub
                </Button>
                <p className="mt-2.5 text-muted-foreground text-xs leading-relaxed">
                  Opens a terminal running <span className="font-mono">gh auth login</span>. Sign in there in your browser; this page carries on by itself.
                </p>
              </>
            ) : (
              <>
                <p className="flex items-center gap-2 font-medium text-sm">
                  <Spinner className="size-3.5" /> Waiting for gh auth login to finish
                </p>
                <p className="mt-1.5 text-muted-foreground text-xs leading-relaxed">
                  A terminal is running <span className="font-mono">{command}</span>. Follow it there; this page checks again on its own.
                </p>
                <Button className="mt-3" size="sm" variant="ghost" onClick={connect}>
                  Open the terminal again
                </Button>
              </>
            )}
          </div>
        )}
        {github.error && <ErrorText className="mt-3 text-destructive-foreground text-xs" text={github.error} />}
      </div>
    </Centered>
  );
}

function AskOrg({ onOrg, initial = "", note }: { onOrg(org: string): void; initial?: string; note?: ReactNode }) {
  const [v, setV] = useState(initial);
  const org = orgFromInput(v);
  return (
    <Centered>
      {note}
      <h1 className="font-semibold text-2xl tracking-tight">Which GitHub org?</h1>
      <p className="mt-2 text-muted-foreground leading-relaxed">If your team publishes a team setup (a repo called .berth), Berth sets your box up the same way the team's are. If not, you pick its repos.</p>
      <form
        className="mt-5 flex gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (validOrg(org)) onOrg(org);
        }}
      >
        <OrgField value={v} onChange={setV} autoFocus />
        <Button type="submit" disabled={!validOrg(org)}>
          Continue <ArrowRightIcon />
        </Button>
      </form>
    </Centered>
  );
}

export function OrgField({ value, onChange, autoFocus }: { value: string; onChange(v: string): void; autoFocus?: boolean }) {
  return (
    <label className="flex min-w-0 flex-1 items-center rounded-lg border bg-background pl-3 font-mono text-sm shadow-xs/5 focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/24 dark:bg-input/32">
      <span className="text-muted-foreground">github.com/</span>
      <Input unstyled aria-label="GitHub org" autoFocus={autoFocus} spellCheck={false} autoCapitalize="off" placeholder="your-org" value={value} onChange={(e) => onChange(e.target.value)} className="font-mono [&_input]:pl-0.5" />
    </label>
  );
}

function OrgPage({ org, from, wantBox, wantUpdate, github, onOrg }: { org: string; from?: From; wantBox?: string; wantUpdate?: boolean; github: GitHubState; onOrg(org: string): void }) {
  const client = useStore((s) => s.client);
  const status = useStore((s) => s.status);
  const boxData = useStore((s) => s.boxes);
  const online = useMemo(() => status?.boxes.filter((b) => b.state === "online") ?? [], [status]);
  const [view, setView] = useState<View>();
  const [error, setError] = useState<string>();
  const [box, setBox] = useState<string | undefined>(wantBox);
  const [keys, setKeys] = useState<Record<string, string>>({});
  const [picked, setPicked] = useState<Set<string>>();
  const [busy, setBusy] = useState(false);
  const [files, setFiles] = useState(false);
  const [reviewUpdate, setReviewUpdate] = useState(!!wantUpdate);
  const [showPlan, setShowPlan] = useState(false);
  const [checks, setChecks] = useState(0);

  // The box: the one asked for, the one it was set up on, else the online
  // box with the fewest projects (a new one, usually). Chosen again as the
  // boxes' projects come in, until someone picks one.
  const [chosen, setChosen] = useState(!!wantBox);
  useEffect(() => {
    if (chosen && box && online.some((b) => b.name === box)) return;
    const count = (name: string) => boxData[name]?.locations?.length ?? 99;
    const pick = (view?.accepted && online.find((b) => b.name === view.accepted!.box)?.name) ?? [...online].sort((a, b) => count(a.name) - count(b.name))[0]?.name;
    if (pick && pick !== box) setBox(pick);
  }, [online, box, chosen, view?.accepted, boxData]);
  const chooseBox = (b: string) => {
    setChosen(true);
    setBox(b);
  };

  const load = useCallback(async () => {
    if (!client) return;
    setError(undefined);
    try {
      const v = await teamApi.view(client, org, { box, from: from === "link" ? "link" : undefined });
      setView(v);
      setPicked((p) => p ?? new Set(v.state === "none" ? (v.repos ?? []).filter((r) => r.has_berth).map((r) => r.full_name) : v.projects.filter((x) => x.access).map((x) => x.id)));
    } catch (err) {
      setError(errorMessage(err));
      if (/gh_/.test(String((err as { code?: string }).code))) void refreshGitHub();
    }
  }, [client, org, box, from]);
  useEffect(() => void load(), [load, checks]);

  const run = useTeam((s) => (view?.setup ? runFor(s, view.setup.id, box) : runFor(s, org, box)));
  const updating = reviewUpdate && !!view?.update;

  if (error) {
    return (
      <Centered>
        <ErrorText className="rounded-xl border border-destructive/30 bg-destructive/8 px-4 py-3 text-destructive-foreground text-sm" text={error} />
        <Button className="mt-4" variant="outline" onClick={() => setChecks((n) => n + 1)}>
          <RotateCwIcon /> Try again
        </Button>
      </Centered>
    );
  }
  if (!view) return <Loading />;
  if (view.state === "no-org") {
    return <AskOrg onOrg={onOrg} note={<p className="mb-5 rounded-lg border bg-muted/40 px-3 py-2 text-sm">GitHub has no org or user called <span className="font-mono">{org}</span> that {github.login} can see.</p>} />;
  }
  if (view.state === "unreadable") return <Unreadable view={view} github={github} onCheck={() => setChecks((n) => n + 1)} onOrg={onOrg} />;

  const boxes = online.map((b) => {
    const n = boxData[b.name]?.locations?.length ?? 0;
    const os = boxData[b.name]?.info?.os;
    return { name: b.name, detail: [os, n ? `${n} ${n === 1 ? "project" : "projects"}` : "new"].filter(Boolean).join(" · ") };
  });
  const sel = picked ?? new Set<string>();
  const name = view.setup?.name ?? view.org.name;

  const start = async () => {
    if (!client || !box) return;
    setBusy(true);
    try {
      const byProject: Record<string, Record<string, string>> = {};
      for (const [id, v] of Object.entries(keys)) {
        if (!v) continue;
        const [p, k] = id.split("/");
        (byProject[p] ??= {})[k] = v;
      }
      const req =
        view.state === "none"
          ? { box, repos: [...sel] }
          : { box, commit: updating ? view.update!.to : view.commit?.sha, projects: view.projects.filter((x) => x.access && (x.required || sel.has(x.id))).map((x) => x.id), keys: byProject };
      const s = await teamApi.setup(client, org, req);
      putRun(s);
      if (updating) {
        useTeam.setState((t) => {
          const updates = { ...t.updates };
          delete updates[org];
          return { updates };
        });
      }
      setKeys({});
      setReviewUpdate(false);
      setShowPlan(false);
      // From the first run, the app around it comes now: the sidebar and the
      // status bar show the setup as it goes.
      finishOnboarding();
      useStore.getState().setView({ kind: "team", org, box });
      void useStore.getState().refreshBox(box, ["sessions", "locations"]);
    } catch (err) {
      toastManager.add({ type: "error", title: `Couldn't start ${name}'s setup`, description: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  };
  const retry = async (fromStep: string) => {
    if (!client || !run) return;
    try {
      putRun(await teamApi.retry(client, org, run.box, fromStep));
    } catch (err) {
      toastManager.add({ type: "error", title: "Couldn't retry", description: errorMessage(err) });
    }
  };
  const openTerminal = () => run?.session && void focusSession(run.box, run.session);
  const startOn = (project: string) => {
    const r = run?.projects.find((x) => x.id === project);
    if (!run || !r?.location) return;
    useStore.getState().openNewWorktree({ box: run.box, location: r.location });
  };
  const background = () => useStore.getState().setView({ kind: "workspace" });

  if (view.state === "none") {
    return <NoBerth view={view} github={github} boxes={boxes} box={box} onBox={chooseBox} picked={sel} onPick={(id, on) => setPicked((p) => toggled(p, id, on))} busy={busy} onRun={start} run={run} onRetry={retry} onOpenTerminal={openTerminal} onStartOn={startOn} onBackground={background} />;
  }

  const live = run && run.phase !== "done" && !updating ? run : undefined;
  if (run?.phase === "done" && !updating && !showPlan) {
    return <TeamDone view={view} run={run} onShowPlan={() => setShowPlan(true)} onReviewUpdate={() => setReviewUpdate(true)} />;
  }

  const picks = view.projects.filter((x) => x.access && (x.required || sel.has(x.id))).length;
  const blocked = !box ? "Pick or add the box to set up" : view.access.readable === 0 ? "You can't read any of its repos yet" : undefined;
  const action = updating ? `Update ${box}` : `Set up for ${name}`;
  const sudo = updating ? (view.update?.sudo.length ?? 0) : sudoCount(view);
  const rail = (compact: boolean) =>
    live ? (
      <RunCard run={live} github={github} os={boxData[live.box]?.info?.os} compact={compact} onOpenTerminal={openTerminal} onRetry={retry} onBackground={background} />
    ) : (
      <Checklist
        compact={compact}
        view={view}
        github={github}
        boxes={boxes}
        box={box}
        onBox={chooseBox}
        onAddBox={openAddBox}
        keys={keys}
        onKey={(id, v) => setKeys((k) => ({ ...k, [id]: v }))}
        picked={picks}
        sudo={sudo}
        action={action}
        blocked={blocked}
        busy={busy}
        onRun={start}
      />
    );

  return (
    <>
      <OrgHeader view={view} from={from} badge={updating ? <span className="rounded-full bg-info/10 px-2.5 py-1 font-medium text-info-foreground text-xs">Update · {view.update!.from} → {view.update!.to}</span> : undefined} />
      <div className="mx-auto grid max-w-6xl grid-cols-[minmax(0,1fr)_300px] gap-8 px-8 pt-6 pb-16 @max-[819px]:grid-cols-1 @max-[819px]:gap-4 @max-[819px]:px-5 @max-[819px]:pt-4">
        <div className="min-w-0 @max-[819px]:order-2">
          <Plan view={view} run={live} box={box} picked={sel} onPick={(id, on) => setPicked((p) => toggled(p, id, on))} onRetry={retry} onOpenTerminal={openTerminal} onStartOn={startOn} onFiles={() => setFiles(true)} update={updating ? view.update : undefined} />
        </div>
        <div className="@max-[819px]:order-1">
          <div className="sticky top-4 @max-[819px]:hidden">{rail(false)}</div>
          <div className="@min-[820px]:hidden">{rail(true)}</div>
        </div>
      </div>
      <TeamFiles view={view} open={files} onOpenChange={setFiles} />
    </>
  );
}

function toggled(p: Set<string> | undefined, id: string, on: boolean) {
  const n = new Set(p);
  if (on) n.add(id);
  else n.delete(id);
  return n;
}

// Unreadable: a link named this org's setup, but GitHub won't show this
// account its .berth repo. Access is the check: there is no separate
// membership step.
function Unreadable({ view, github, onCheck, onOrg }: { view: View; github: GitHubState; onCheck(): void; onOrg(org: string): void }) {
  const [other, setOther] = useState(false);
  return (
    <Centered>
      <div data-testid="team-unreadable" className="flex items-center gap-3">
        <OrgAvatar org={view.org} size={44} />
        <div>
          <p className="flex items-center gap-1.5 font-mono text-muted-foreground text-sm">
            <GitHubMark className="size-3.5" /> {view.org.login} / <span className="font-semibold text-foreground">.berth</span>
          </p>
          <p className="font-medium">{view.org.name}</p>
        </div>
      </div>
      <h1 className="mt-6 font-semibold text-2xl tracking-tight">You can't read this setup</h1>
      <p className="mt-2 text-muted-foreground leading-relaxed">
        GitHub doesn't show <span className="font-mono text-foreground">{view.org.login}/.berth</span> to <span className="text-foreground">{github.login}</span>. It's private, and this account hasn't been given access, or it isn't there.
      </p>
      <ol className="mt-4 list-decimal space-y-1.5 pl-5 text-sm">
        <li>Ask an admin of {view.org.name} on GitHub for access to its repos (your team's onboarding channel is the place).</li>
        <li>Accept the invitation on GitHub, if one comes by email.</li>
        <li>Check again here.</li>
      </ol>
      <div className="mt-5 flex flex-wrap gap-2">
        <Button onClick={onCheck}>
          <RotateCwIcon /> Check again
        </Button>
        <Button variant="outline" onClick={() => void openUrl(view.org.html_url)}>
          {view.org.login} on GitHub <ExternalLinkIcon />
        </Button>
        <Button variant="ghost" onClick={() => setOther(true)}>
          Another org
        </Button>
      </div>
      <p className="mt-4 text-muted-foreground text-xs">Signed in to gh as {github.login}. To use another account, run gh auth login again in a terminal.</p>
      {other && (
        <div className="mt-6">
          <OrgSwitch onOrg={onOrg} />
        </div>
      )}
    </Centered>
  );
}

function OrgSwitch({ onOrg }: { onOrg(org: string): void }) {
  const [v, setV] = useState("");
  const org = orgFromInput(v);
  return (
    <form
      className="flex gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        if (validOrg(org)) onOrg(org);
      }}
    >
      <OrgField value={v} onChange={setV} autoFocus />
      <Button type="submit" disabled={!validOrg(org)}>
        Continue
      </Button>
    </form>
  );
}

// NoBerth: the org publishes no team setup (or none this account can read).
// Its repos you can reach, the ones that carry their own Berth setup picked.
function NoBerth({
  view,
  github,
  boxes,
  box,
  onBox,
  picked,
  onPick,
  busy,
  onRun,
  run,
  onRetry,
  onOpenTerminal,
  onStartOn,
  onBackground,
}: {
  view: View;
  github: GitHubState;
  boxes: { name: string; detail: string }[];
  box?: string;
  onBox(b: string): void;
  picked: Set<string>;
  onPick(id: string, on: boolean): void;
  busy: boolean;
  onRun(): void;
  run?: TeamStatus;
  onRetry(from: string): void;
  onOpenTerminal(): void;
  onStartOn(p: string): void;
  onBackground(): void;
}) {
  const [filter, setFilter] = useState("");
  const repos = (view.repos ?? []).filter((r) => r.full_name.toLowerCase().includes(filter.toLowerCase()));
  const withBerth = (view.repos ?? []).filter((r) => r.has_berth).length;
  const live = run && run.phase !== "done" ? run : undefined;
  const pseudo: View = { ...view, access: { readable: picked.size, total: picked.size, missing: [] } };
  const rail = (compact: boolean) =>
    live ? (
      <RunCard run={live} github={github} compact={compact} onOpenTerminal={onOpenTerminal} onRetry={onRetry} onBackground={onBackground} />
    ) : (
      <Checklist
        compact={compact}
        view={pseudo}
        github={github}
        boxes={boxes}
        box={box}
        onBox={onBox}
        onAddBox={openAddBox}
        keys={{}}
        onKey={() => {}}
        picked={picked.size}
        sudo={0}
        action={picked.size ? `Set up ${picked.size} ${picked.size === 1 ? "repo" : "repos"}` : "Pick repos to set up"}
        blocked={!box ? "Pick or add the box to set up" : !picked.size ? "Pick at least one repo" : undefined}
        busy={busy}
        onRun={onRun}
      />
    );
  return (
    <>
      <div className="border-b bg-gradient-to-b from-muted/50 to-transparent">
        <div className="mx-auto flex max-w-6xl items-start gap-4 px-8 pt-6 pb-5 @max-[819px]:px-5">
          <OrgAvatar org={view.org} size={48} />
          <div className="min-w-0">
            <p className="flex items-center gap-1.5 font-mono text-muted-foreground text-sm">
              <GitHubMark className="size-3.5" /> {view.org.login}
            </p>
            <h1 data-testid="team-none" className="mt-0.5 font-semibold text-2xl tracking-tight">
              {view.org.name}
            </h1>
            <p className="mt-1 max-w-[70ch] text-muted-foreground text-xs leading-relaxed">
              No team setup here: <span className="font-mono">{view.org.login}/.berth</span> isn't a repo {github.login} can read. You can still set up its repos; an admin can publish one to set up boxes too.
            </p>
          </div>
        </div>
      </div>
      <div className="mx-auto grid max-w-6xl grid-cols-[minmax(0,1fr)_300px] gap-8 px-8 pt-6 pb-16 @max-[819px]:grid-cols-1 @max-[819px]:gap-4 @max-[819px]:px-5 @max-[819px]:pt-4">
        <div className="min-w-0 @max-[819px]:order-2">
          {live ? (
            <Section title="Repos" aside={`${live.projects.length} cloned as you`}>
              <div className="divide-y overflow-hidden rounded-xl border bg-card">
                {live.projects.map((p) => (
                  <div key={p.id} className="flex items-center gap-2.5 px-4 py-2.5">
                    <span className="font-mono text-[13px]">{p.repo}</span>
                    <span className="ml-auto text-muted-foreground text-xs">{p.state === "ready" ? <Button size="xs" onClick={() => onStartOn(p.id)}>Start on {p.id}</Button> : p.state}</span>
                  </div>
                ))}
              </div>
            </Section>
          ) : (
            <Section title="Repos you can reach" aside={`${withBerth} have Berth setup and are picked`}>
              <div className="mb-2">
                <label className="flex items-center gap-2 rounded-lg border bg-background px-2.5 text-sm dark:bg-input/32">
                  <SearchIcon className="size-3.5 text-muted-foreground" />
                  <Input unstyled aria-label="Filter repos" placeholder="Filter" value={filter} onChange={(e) => setFilter(e.target.value)} />
                </label>
              </div>
              <ul className="divide-y overflow-hidden rounded-xl border bg-card">
                {repos.map((r) => (
                  <li key={r.full_name}>
                    <label className="flex cursor-pointer items-center gap-3 px-4 py-2.5 hover:bg-accent/30">
                      <Checkbox className="not-dark:border-foreground/25" checked={picked.has(r.full_name)} onCheckedChange={(v) => onPick(r.full_name, !!v)} />
                      <span className="min-w-0 flex-1">
                        <span className="flex flex-wrap items-center gap-x-2">
                          <span className="font-medium font-mono text-[13px]">{r.full_name}</span>
                          {r.private && <LockIcon className="size-3 text-muted-foreground" />}
                          {r.has_berth && <span className="rounded-md border border-success/35 bg-success/8 px-1.5 py-px font-mono text-[10.5px] text-success-foreground">.berth/config.json</span>}
                        </span>
                        <span className="block text-muted-foreground text-xs">
                          {r.description} · pushed {ago(r.pushed_at)}
                        </span>
                      </span>
                    </label>
                  </li>
                ))}
              </ul>
              <p className="mt-3 text-muted-foreground text-xs leading-relaxed">
                Repos with their own <span className="font-mono">.berth/config.json</span> are set up by it, trusted as it is now. The rest clone as they are; add ports and services later in Project settings. The box signs in to GitHub itself first.
              </p>
            </Section>
          )}
        </div>
        <div className="@max-[819px]:order-1">
          <div className="sticky top-4 @max-[819px]:hidden">{rail(false)}</div>
          <div className="@min-[820px]:hidden">{rail(true)}</div>
        </div>
      </div>
    </>
  );
}
