// What a box needs before it can run an agent: tmux, which every session
// runs in, and an agent CLI. A fresh Mac usually has neither, and berthd
// started by launchd can't see a Homebrew tmux on its PATH, so the box says
// what it found (GET /v1/requirements) and the app shows a card with the
// command to install what's missing, before anything is created. Older
// boxes don't have the route: their requirements are unknown, and the app
// goes ahead as it always did. Kept free of the app's imports, so node can
// test it as it is.

export interface ToolRequirement {
  found: boolean;
  path?: string;
  // The command that installs it on this box, to type, not run.
  install?: string;
  // The package manager the command uses: brew, apt, dnf, pacman, apk, zypper.
  manager?: string;
  // A Mac without Homebrew: install it first (help is where).
  manager_missing?: boolean;
  help?: string;
}

export interface AgentRequirement {
  id: string;
  name: string;
  command: string;
  found: boolean;
  path?: string;
  install?: string;
}

export interface Requirements {
  os: string;
  tmux: ToolRequirement;
  agents: AgentRequirement[];
}

// Homebrew's own installer, from brew.sh.
export const BREW_INSTALL = '/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"';
export const BREW_HELP = "https://brew.sh";

// parseRequirements reads the box's answer, or undefined for anything that
// isn't one (an older box's 404 never gets here).
export function parseRequirements(v: unknown): Requirements | undefined {
  if (!v || typeof v !== "object") return undefined;
  const o = v as Record<string, unknown>;
  const t = o.tmux as Record<string, unknown> | undefined;
  if (!t || typeof t !== "object" || typeof t.found !== "boolean") return undefined;
  const str = (x: unknown) => (typeof x === "string" && x.trim() ? x.trim() : undefined);
  const agents = Array.isArray(o.agents)
    ? o.agents
        .filter((a): a is Record<string, unknown> => !!a && typeof a === "object" && typeof (a as Record<string, unknown>).id === "string")
        .map((a) => ({
          id: a.id as string,
          name: str(a.name) ?? (a.id as string),
          command: str(a.command) ?? (a.id as string),
          found: a.found === true,
          path: str(a.path),
          install: str(a.install),
        }))
    : [];
  return {
    os: str(o.os) ?? "",
    tmux: { found: t.found, path: str(t.path), install: str(t.install), manager: str(t.manager), manager_missing: t.manager_missing === true, help: str(t.help) },
    agents,
  };
}

// What the card shows: nothing (all there, or unknown), tmux missing, or no
// agent CLI at all. tmux comes first: without it nothing starts.
export type RequirementsCard = "hidden" | "tmux" | "agent";

// requirementsCard decides the card. agent is the one about to start, when
// known; without one, any agent found is enough. noAgent is a worktree
// alone, which needs neither (worktrees are git's, not tmux's).
export function requirementsCard(r: Requirements | undefined, o: { agent?: string; noAgent?: boolean } = {}): RequirementsCard {
  if (!r || o.noAgent) return "hidden";
  if (!r.tmux.found) return "tmux";
  // Unknown agents (a repository's own command) are the box's business.
  if (!r.agents.length) return "hidden";
  if (o.agent) {
    const a = r.agents.find((x) => x.id === o.agent);
    return a && !a.found ? "agent" : "hidden";
  }
  return r.agents.some((a) => a.found) ? "hidden" : "agent";
}

// blocksStart is whether the card stops Start: tmux missing does; a missing
// agent the box reported does too, since its session would only fail.
export const blocksStart = (card: RequirementsCard) => card !== "hidden";

export interface RequirementsCopy {
  title: string;
  body: string;
  // The command to type, and where to get the tool when there isn't one.
  command?: string;
  // A second command to run first (Homebrew itself on a bare Mac).
  first?: string;
  help?: { label: string; url: string };
  // The terminal to run it in, in words: "Terminal on this Mac", "a terminal on devl".
  where: string;
}

const isMac = (r: Requirements) => r.os === "darwin";

// The agent the card offers: the one asked for, else Claude Code, else the
// first the box listed.
export function agentToInstall(r: Requirements, agent?: string): AgentRequirement | undefined {
  return r.agents.find((a) => a.id === agent) ?? r.agents.find((a) => a.id === "claude") ?? r.agents[0];
}

// requirementsCopy is what the card says, in plain words. box is how the
// box is named to the person ("this Mac" for the local one).
// team is a team setup about to run, whose steps run in a terminal there.
export function requirementsCopy(card: RequirementsCard, r: Requirements | undefined, box: string, o: { local?: boolean; agent?: string; team?: boolean } = {}): RequirementsCopy | undefined {
  if (!r || card === "hidden") return undefined;
  const where = o.local ? "Terminal on this Mac" : `a terminal on ${box} (over SSH)`;
  if (card === "tmux") {
    const t = r.tmux;
    const why = o.team ? "Berth runs the team setup's steps, and later your agents, in tmux on the box." : "Berth runs agents in tmux, so they keep going when you close it.";
    if (isMac(r) && t.manager_missing)
      return {
        title: `Install tmux on ${box}`,
        body: `${why} tmux comes from Homebrew, which isn't on this Mac yet: install Homebrew, then tmux.`,
        first: BREW_INSTALL,
        command: t.install ?? "brew install tmux",
        help: { label: "brew.sh", url: t.help ?? BREW_HELP },
        where,
      };
    if (isMac(r))
      return {
        title: `Install tmux on ${box}`,
        body: `${why} Install it with Homebrew; it takes a minute.`,
        command: t.install ?? "brew install tmux",
        where,
      };
    return {
      title: `Install tmux on ${box}`,
      body: t.install
        ? `${why} Install it with the box's package manager; it asks for your password.`
        : `${why} Install it with the box's package manager.`,
      command: t.install,
      where,
    };
  }
  const a = agentToInstall(r, o.agent);
  return {
    title: a ? `Install ${a.name} on ${box}` : `No agent CLI on ${box}`,
    body: a?.install
      ? `Berth starts ${a.name} in the worktree, so it needs the ${a.command} command there. Install it, sign in once by running ${a.command}, then check again.`
      : "Berth starts a coding agent's CLI in the worktree, such as Claude Code or Codex. Install one there, then check again.",
    command: a?.install,
    where,
  };
}
