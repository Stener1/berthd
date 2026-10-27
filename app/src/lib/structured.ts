import type { AgentPreset } from "@/lib/api";
import { usePrefs } from "@/lib/prefs";
import { useStore } from "@/lib/store";
import { launchFor, type StructuredDriver } from "@/lib/structured-core";

// structuredLaunch is how an agent starts on box: structured wherever the
// box has the driver, else in its terminal (lib/structured-core.ts).
export function structuredLaunch(box: string, agent: string, preset?: AgentPreset): { driver?: StructuredDriver; mode?: string } {
  const caps = useStore.getState().boxes[box]?.info?.capabilities ?? [];
  return launchFor(agent, !!preset?.acp, caps, usePrefs.getState().approvals);
}

// isStructured is whether a session's agent runs structured (Session.driver):
// its chat is its stream, and the pane's terminal shows only the host that
// runs it, never a screen of the agent's own to read or answer.
export function isStructured(driver?: string): boolean {
  return driver === "claude" || driver === "codex" || driver === "acp";
}
