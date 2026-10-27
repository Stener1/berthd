import { boxApi, type BoxInfo } from "@/lib/api";
import { type AgentDefaults, defaultsToPut } from "@/lib/box-defaults-core";
import { usePrefs } from "@/lib/prefs";
import { useStore } from "@/lib/store";

// Keeps each box's default for agents started without the app (another
// agent, the CLI, a run: lib/box-defaults-core.ts) structured, in the mode
// Settings › Agents › Permissions names: when a box's info loads (it comes
// online, the app reconnects) and when Permissions changes.

type Info = BoxInfo & { agent_defaults?: AgentDefaults };

// startBoxDefaultsSync starts keeping them; the function it returns stops.
export function startBoxDefaultsSync(): () => void {
  // What was last sent to each box, and the info it was sent against: a
  // refresh that brings the same info back, or a box that refused, isn't
  // sent it again until the prefs or the box's info change.
  const sent = new Map<string, { key: string; info: Info }>();
  const sync = () => {
    const { client, boxes } = useStore.getState();
    if (!client) return;
    const prefs = usePrefs.getState();
    for (const [box, data] of Object.entries(boxes)) {
      const info = data.info as Info | undefined;
      const put = info && defaultsToPut(prefs, info.capabilities, info.agent_defaults);
      if (!info || !put) {
        sent.delete(box);
        continue;
      }
      const key = JSON.stringify(put);
      const last = sent.get(box);
      if (last && last.key === key && last.info === info) continue;
      sent.set(box, { key, info });
      boxApi.putAgentDefaults(client, box, put).then(
        // What the box holds now, so the next look finds them alike.
        (now) =>
          useStore.setState((s) => {
            const d = s.boxes[box];
            if (!d?.info) return s;
            return { boxes: { ...s.boxes, [box]: { ...d, info: { ...d.info, agent_defaults: now } as Info } } };
          }),
        () => {},
      );
    }
  };
  const unsub = useStore.subscribe((s, prev) => {
    if (s.boxes !== prev.boxes || s.client !== prev.client) sync();
  });
  const unsubPrefs = usePrefs.subscribe((p, prev) => {
    if (p.approvals !== prev.approvals) sync();
  });
  sync();
  return () => {
    unsub();
    unsubPrefs();
  };
}
