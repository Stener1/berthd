// How a box starts an agent when what starts it names no driver: another
// agent through the berth tools, the CLI, a run or an automation (GET and
// PUT agents/defaults, also in info as agent_defaults, on boxes with
// "agents.defaults"). The app's own starts name theirs (lib/structured.ts);
// it keeps each box's default to the same prefs so the rest follow them too.

export interface AgentDefaults {
  structured: boolean;
  // "bypassPermissions" or "default"; the box gives ACP agents no mode
  // unless it is bypass, as structuredLaunch does.
  mode?: string;
}

// The prefs structuredLaunch reads: only the permissions, since agents run
// structured wherever the box can.
export interface DefaultsPrefs {
  approvals: "bypass" | "ask";
}

// agentDefaultsFor is the default the prefs ask for: always structured, in
// the mode Permissions names.
export function agentDefaultsFor(p: DefaultsPrefs): AgentDefaults {
  return { structured: true, mode: p.approvals === "bypass" ? "bypassPermissions" : "default" };
}

// Off, the mode is never read; on, none is "default".
function same(a: AgentDefaults, b: AgentDefaults): boolean {
  if (a.structured !== b.structured) return false;
  return !a.structured || (a.mode || "default") === (b.mode || "default");
}

// defaultsToPut is what to PUT to a box so its default follows the prefs:
// nothing when it already does, or when the box keeps none (an older
// berthd). A box that reports none is taken as off, so it is sent one.
export function defaultsToPut(p: DefaultsPrefs, caps: string[] | undefined, current: AgentDefaults | undefined): AgentDefaults | undefined {
  if (!caps?.includes("agents.defaults")) return undefined;
  const want = agentDefaultsFor(p);
  return same(want, current ?? { structured: false }) ? undefined : want;
}
