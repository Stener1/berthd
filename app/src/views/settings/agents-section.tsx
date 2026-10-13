import { CheckIcon, MinusIcon } from "lucide-react";
import { useEffect, useState } from "react";

import { AgentIcon } from "@/components/agent-glyph";
import { useMinute } from "@/components/notifications/notification-center";
import { SkillsPanel } from "@/components/skills/skills-panel";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Tooltip, TooltipPopup, TooltipTrigger } from "@/components/ui/tooltip";
import { SimpleSelect } from "@/components/simple-select";
import type { AgentPreset } from "@/lib/api";
import { plainError } from "@/lib/errors";
import { type Project, projectActions, useProjects } from "@/lib/project-groups";
import { endSnooze, setRemind, useSkillsNotice } from "@/lib/skills-notice";
import { neverRemind, untilText } from "@/lib/skills-notice-model";
import { NONE, useStore } from "@/lib/store";
import { Segmented } from "@/views/settings/controls";
import { Code, SettingsGroup, SettingsPage, SettingsRow } from "@/views/settings/rows";

// How to put each built-in agent on a box's PATH.
const INSTALL: Record<string, string> = {
  claude: "npm install -g @anthropic-ai/claude-code",
  codex: "npm install -g @openai/codex",
  opencode: "npm install -g opencode-ai",
  gemini: "npm install -g @google/gemini-cli",
  cursor: "curl https://cursor.com/install -fsS | bash",
};

const BUILTIN: Pick<AgentPreset, "id" | "name">[] = [
  { id: "claude", name: "Claude Code" },
  { id: "codex", name: "Codex" },
  { id: "opencode", name: "OpenCode" },
  { id: "gemini", name: "Gemini CLI" },
  { id: "cursor", name: "Cursor Agent" },
];

export function AgentsSection() {
  const boxes = useStore((s) => s.status?.boxes ?? NONE);
  const data = useStore((s) => s.boxes);
  const online = boxes.filter((b) => b.state === "online");
  // The sidebar's notice opens this page on its box ("Open Settings"), each
  // time it is asked, whichever box was picked here since.
  const view = useStore((s) => s.view);
  const asked = view.kind === "settings" ? view.box : undefined;
  const [skillsBox, setSkillsBox] = useState(asked);
  useEffect(() => {
    if (asked) setSkillsBox(asked);
  }, [asked, view]);
  const shownSkills = online.some((b) => b.name === skillsBox) ? skillsBox! : online[0]?.name;

  // Every agent any box has, built-ins first, then each box's own.
  const found = new Map<string, AgentPreset>();
  for (const b of online) for (const a of data[b.name]?.info?.agents ?? []) if (!found.has(a.id)) found.set(a.id, a);
  const rows = [...BUILTIN.map((a) => found.get(a.id) ?? { ...a, command: "" }), ...[...found.values()].filter((a) => !BUILTIN.some((x) => x.id === a.id))];

  return (
    <SettingsPage
      title="Agents"
      description={
        <>
          The agent CLIs each box can start, and the skills that teach them to use Shipyard. A repository adds its own agents, or changes how one starts, in <Code>.berth/config.json</Code>.
        </>
      }
    >
      {online.length === 0 ? (
        <p className="rounded-xl border px-4 py-6 text-center text-muted-foreground text-sm">No box is online. Agents are listed once one connects.</p>
      ) : (
        <SettingsGroup title="Agent CLIs" description="Found on each box's PATH when berthd checks.">
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b text-[11px] text-muted-foreground">
                  <th className="px-4 py-2 text-left font-normal">Agent</th>
                  {online.map((b) => (
                    <th key={b.name} className="w-24 px-2 py-2 text-center font-normal">
                      {b.name}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((a) => (
                  <tr key={a.id} className="border-b last:border-b-0">
                    <td className="px-4 py-2.5">
                      <div className="flex items-center gap-2">
                        <AgentIcon agent={a.id} className="size-3.5" />
                        {a.name}
                      </div>
                      <div className="mt-0.5 pl-5.5 font-mono text-[11px] text-muted-foreground">{!found.has(a.id) ? "not found on any box" : a.command ? a.command + (a.prompt_flag ? ` ${a.prompt_flag} …` : "") : "$SHELL -l"}</div>
                    </td>
                    {online.map((b) => {
                      const has = data[b.name]?.info?.agents?.some((x) => x.id === a.id);
                      return (
                        <td key={b.name} className="px-2 py-2.5 text-center">
                          {has ? (
                            <CheckIcon className="mx-auto size-4 text-success-foreground" aria-label={`On ${b.name}`} />
                          ) : (
                            <Tooltip>
                              <TooltipTrigger render={<button type="button" className="inline-flex cursor-help rounded text-muted-foreground/50 outline-none focus-visible:ring-2 focus-visible:ring-ring" />} aria-label={`Not on ${b.name}`}>
                                <MinusIcon className="size-4" />
                              </TooltipTrigger>
                              <TooltipPopup className="max-w-72">
                                Not on {b.name}'s PATH.{INSTALL[a.id] && <span className="mt-1 block font-mono text-[11px]">{INSTALL[a.id]}</span>}
                              </TooltipPopup>
                            </Tooltip>
                          )}
                        </td>
                      );
                    })}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </SettingsGroup>
      )}

      {shownSkills && (
        <section>
          <div className="mb-2 flex items-end gap-3">
            <div className="min-w-0 flex-1">
              <h2 className="font-medium text-[13px] text-muted-foreground">Skills{online.length === 1 ? ` on ${shownSkills}` : ""}</h2>
            </div>
            {online.length > 1 && <Segmented label="Box" value={shownSkills} options={online.map((b) => ({ value: b.name, label: b.name }))} onChange={setSkillsBox} />}
          </div>
          <SkillsPanel key={shownSkills} box={shownSkills} hideTitle />
          <SkillsReminder key={`remind-${shownSkills}`} box={shownSkills} />
        </section>
      )}

      <OffBoard />

      {boxes.length > online.length && <p className="text-muted-foreground text-xs">Offline boxes are listed once they reconnect.</p>}
    </SettingsPage>
  );
}

// SkillsReminder is where the sidebar's notice about a box's skills
// (lib/skills-notice.ts) is seen and undone: a snooze ends with Remind me
// now, and "Don't remind me for this box" is the switch, off.
function SkillsReminder({ box }: { box: string }) {
  const prefs = useSkillsNotice((s) => s.prefs);
  const loaded = useSkillsNotice((s) => s.loaded);
  const off = neverRemind(prefs, box);
  const snooze = prefs.snoozed[box];
  const until = snooze ? Date.parse(snooze.until) : 0;
  // Looked at again each minute, so a snooze that ends says so.
  const now = useMinute();
  // A snooze holds for the state it was made in; Settings says which.
  const snoozed = !off && until > now;
  const description = off
    ? `Off. The sidebar says nothing about ${box}'s skills until you turn this back on.`
    : snoozed
      ? `Snoozed until ${untilText(until, now)}, while its skills ${snooze.kind === "outdated" ? "are out of date" : "aren't installed"}.`
      : `The sidebar says so when ${box}'s skills aren't installed or are out of date, with Install now and Snooze.`;
  return (
    <div data-testid="skills-reminder" className="mt-3 overflow-hidden rounded-xl border bg-card/40">
      <SettingsRow label={`Remind me about ${box}'s skills`} description={description}>
        {snoozed && (
          <Button size="sm" variant="outline" onClick={() => void endSnooze(box)}>
            Remind me now
          </Button>
        )}
        <Switch checked={!off} disabled={!loaded} onCheckedChange={(on) => void setRemind(box, on)} />
      </SettingsRow>
    </div>
  );
}

// OffBoard is the projects whose agents stay off the Agent Dashboard and
// its counts: work followed somewhere else, such as a plugin's own board.
// The choice is the project's, on every box, and kept on the laptop agent
// with its name and section.
function OffBoard() {
  const { projects } = useProjects();
  const [error, setError] = useState<string>();
  const off = projects.filter((p) => p.dashboardHidden);
  const on = projects.filter((p) => !p.dashboardHidden);
  const set = (p: Project, hidden: boolean) => {
    setError(undefined);
    projectActions.setDashboardHidden(p, hidden).catch((err) => setError(plainError(err)));
  };
  if (!projects.length) return null;
  return (
    <SettingsGroup title="Agent Dashboard" description="Projects kept off the Agent Dashboard and its counts in the sidebar and status bar. Their agents still show in the sidebar, on Home and in notifications.">
      {off.map((p) => (
        <SettingsRow key={p.id} label={p.name} description={p.members.length > 1 ? `Kept off, on ${p.members.map((m) => m.box.name).join(", ")}` : "Kept off"}>
          <Button size="sm" variant="outline" onClick={() => set(p, false)}>
            Show
          </Button>
        </SettingsRow>
      ))}
      <SettingsRow label="Keep a project off" description={error ?? (off.length ? undefined : "Every project shows on the dashboard.")}>
        <SimpleSelect
          size="sm"
          className="w-48"
          placeholder="Choose a project…"
          value=""
          options={on.map((p) => ({ value: p.id, label: p.name }))}
          onChange={(id) => {
            const p = on.find((x) => x.id === id);
            if (p) set(p, true);
          }}
        />
      </SettingsRow>
    </SettingsGroup>
  );
}
