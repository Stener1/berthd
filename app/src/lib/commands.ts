import { useEffect, useState } from "react";

import { isMock } from "@/hooks/use-berth-connection";
import type { Client } from "@/lib/api";
import { useStore } from "@/lib/store";

export { rankCommands } from "@/lib/command-rank";

// What an agent takes after a "/" and an "@", for the chat's composer. A
// box with the "commands" capability lists them per session: GET
// sessions/{name}/commands (its own commands, the person's and the repo's
// custom commands, skills and plugins) and GET sessions/{name}/files?q= (the
// worktree's files). What is typed goes to the agent exactly as typed.

export type CommandKind = "builtin" | "custom" | "skill" | "plugin" | "mcp";

export interface AgentCommand {
  name: string;
  description?: string;
  kind: CommandKind;
  args?: string;
  aliases?: string[];
  // Handled by the agent's own program: no turn starts.
  local?: boolean;
  // Opens the agent's own picker or dialog.
  screen?: boolean;
  // Answered by the app itself, never sent: a structured chat's /resume.
  app?: boolean;
  source?: string;
}

export interface CommandCatalog {
  agent: string;
  version?: string;
  commands: AgentCommand[];
  // "!" and "#": what each does at the start of a prompt.
  prefixes?: Record<string, string>;
  notes?: string[];
  truncated?: boolean;
}

export const hasCommands = (box: string) => !!useStore.getState().boxes[box]?.info?.capabilities?.includes("commands");

export const commandsApi = {
  list: (c: Client, box: string, session: string) => c.box<CommandCatalog>(box, "GET", `sessions/${encodeURIComponent(session)}/commands`),
  files: (c: Client, box: string, session: string, q: string) => c.box<{ files: string[]; truncated?: boolean }>(box, "GET", `sessions/${encodeURIComponent(session)}/files?q=${encodeURIComponent(q)}&limit=30`),
};

// Read once a minute at most per session, shared by every composer on it.
const cache = new Map<string, { at: number; p: Promise<CommandCatalog | undefined> }>();

export function useCommandCatalog(box: string | undefined, session: string | undefined): CommandCatalog | undefined {
  const client = useStore((s) => s.client);
  const supported = useStore((s) => !!box && !!s.boxes[box]?.info?.capabilities?.includes("commands"));
  const mock = isMock();
  const [cat, setCat] = useState<CommandCatalog>();
  useEffect(() => {
    if (!box || !session) return;
    if (mock) {
      const structured = !!useStore.getState().boxes[box]?.sessions?.find((s) => s.name === session)?.driver;
      setCat(structured ? withResumePicker(MOCK_CATALOG) : MOCK_CATALOG);
      return;
    }
    if (!client || !supported) {
      setCat(undefined);
      return;
    }
    const key = `${box}/${session}`;
    let alive = true;
    let retry: number | undefined;
    const load = (wait: number) => {
      let hit = cache.get(key);
      if (!hit || Date.now() - hit.at > 60_000) {
        const p = commandsApi.list(client, box, session).catch(() => undefined);
        hit = { at: Date.now(), p };
        cache.set(key, hit);
        // A read that failed (the box was reconnecting, the session not up
        // yet) is not kept: the next one asks again.
        void p.then((c) => !c && cache.get(key)?.p === p && cache.delete(key));
      }
      void hit.p.then((c) => {
        if (!alive) return;
        setCat(c);
        if (!c) retry = window.setTimeout(() => load(Math.min(wait * 2, 30_000)), wait);
      });
    };
    load(2_000);
    return () => {
      alive = false;
      window.clearTimeout(retry);
    };
  }, [client, supported, mock, box, session]);
  return cat;
}

export async function searchFiles(client: Client | undefined, box: string, session: string, q: string): Promise<string[]> {
  if (isMock()) return MOCK_FILES.filter((f) => !q || f.toLowerCase().includes(q.toLowerCase()) || subsequence(f.toLowerCase(), q.toLowerCase())).slice(0, 30);
  if (!client) return [];
  return (await commandsApi.files(client, box, session, q)).files ?? [];
}

function subsequence(s: string, q: string): boolean {
  let i = 0;
  for (let j = 0; j < s.length && i < q.length; j++) if (s[j] === q[i]) i++;
  return i === q.length;
}

// commandIn is the catalogue's command a prompt starts with ("/model opus"
// is /model), by name or alias.
export function commandIn(cat: CommandCatalog | undefined, text: string): AgentCommand | undefined {
  const m = text.trim().match(/^\/([^\s/]+)(?:\s|$)/);
  if (!m || !cat) return undefined;
  const name = `/${m[1]}`;
  return cat.commands.find((c) => c.name === name || c.aliases?.includes(name));
}

// canPickPast says whether a box lists a structured session's past
// conversations (berthd new enough).
export const canPickPast = (box: string) => !!useStore.getState().boxes[box]?.info?.capabilities?.includes("sessions.past");

// withResumePicker is a structured chat's catalogue, as the box gives it
// (internal/box/commands.go): its /resume is the app's picker.
function withResumePicker(cat: CommandCatalog): CommandCatalog {
  const resume: AgentCommand = { name: "/resume", description: "Pick a past conversation and carry it on here", kind: "builtin", args: "[conversation]", local: true, app: true, aliases: ["/continue"] };
  return { ...cat, commands: [resume, ...cat.commands.filter((c) => c.name !== "/resume")] };
}

export const KIND_LABEL: Record<CommandKind, string> = { builtin: "Built-in", custom: "Custom", skill: "Skill", plugin: "Plugin", mcp: "MCP" };

// The demo's agent, with a few of each kind.
export const MOCK_CATALOG: CommandCatalog = {
  agent: "claude",
  version: "2.1.289",
  prefixes: { "!": "Runs in the shell", "#": "Sent as a prompt: Claude Code 2.1 has no # memory (use /memory)" },
  commands: [
    { name: "/compact", description: "Free up context by summarizing the conversation so far", kind: "builtin", args: "[instructions]", local: true },
    { name: "/context", description: "Visualize current context usage", kind: "builtin", local: true },
    { name: "/usage", description: "Show session cost, plan usage, and activity stats", kind: "builtin", aliases: ["/cost", "/stats"], local: true, screen: true },
    { name: "/model", description: "Set the AI model for Claude Code", kind: "builtin", args: "[model]", local: true, screen: true },
    { name: "/config", description: "Open settings", kind: "builtin", aliases: ["/settings"], local: true, screen: true },
    { name: "/clear", description: "Start a new session with empty context; the previous one stays resumable", kind: "builtin", aliases: ["/reset", "/new"], local: true },
    { name: "/resume", description: "Resume a previous conversation", kind: "builtin", local: true, screen: true },
    { name: "/permissions", description: "Manage allow and deny tool permission rules", kind: "builtin", local: true, screen: true },
    { name: "/init", description: "Initialize a new CLAUDE.md file with codebase documentation", kind: "builtin" },
    { name: "/simplify", description: "Review the changed code for reuse, simplification and efficiency, then fix it", kind: "builtin" },
    { name: "/ship", description: "Run the checks, write the changelog line and open a PR", kind: "custom", args: "[branch]", source: "project" },
    { name: "/front:test", description: "Run the storefront's tests and fix what fails", kind: "custom", source: "user" },
    { name: "/seed-data", description: "Fill the local database with demo orders and customers", kind: "skill", source: "project" },
    { name: "/tidy", description: "Tidy the files you touched without changing behaviour", kind: "skill", source: "user" },
    { name: "/vercel:deploy", description: "Deploy the current project to Vercel", kind: "plugin", args: "[prod]", source: "vercel" },
  ],
};

const MOCK_FILES = ["README.md", "package.json", "apps/web/src/checkout/webhook.ts", "apps/web/src/checkout/order.ts", "apps/web/src/checkout/retry.test.ts", "apps/web/src/lib/stripe.ts", "packages/db/schema.prisma", "docs/payments.md"];
