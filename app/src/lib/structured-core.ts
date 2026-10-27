// How an agent the app starts runs, kept apart from lib/structured.ts so it
// can be tested on its own (structured-core.test.ts).

export type StructuredDriver = "claude" | "codex" | "acp";

// launchFor is how an agent starts: structured wherever the box can, Claude
// Code through its stream ("driver.claude"), Codex through its app server
// ("driver.codex"), and an agent whose preset speaks ACP through that
// ("driver.acp"). Empty when it runs in its terminal: an agent with no
// driver, or a box too old to have one. There is no setting for it; only
// the mode follows Permissions (approvals).
export function launchFor(agent: string, acp: boolean, caps: readonly string[], approvals: "bypass" | "ask"): { driver?: StructuredDriver; mode?: string } {
  const driver: StructuredDriver | undefined = agent === "claude" || agent === "codex" ? agent : acp ? "acp" : undefined;
  if (!driver || !caps.includes(`driver.${driver}`)) return {};
  // ACP agents have modes of their own: bypass, or theirs as they start.
  const mode = approvals === "bypass" ? "bypassPermissions" : driver === "acp" ? undefined : "default";
  return { driver, mode };
}
