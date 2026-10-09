import { useCallback, useEffect, useState } from "react";

import { useEventLog } from "@/lib/events";
import { flowsApi, type LocationConfig, type RepoConfig } from "@/lib/flows";
import { isSecretRef } from "@/lib/api";
import { errorMessage } from "@/lib/format";
import { explainConfigError, refProblem } from "@/lib/secret-ref";
import { useStore } from "@/lib/store";

// clean drops what is empty, so the box stores only what this box sets and
// the committed config shows through everywhere else.
export function clean(c: RepoConfig): RepoConfig {
  const out: RepoConfig = {};
  if (c.setup?.trim()) out.setup = c.setup;
  if (c.archive?.trim()) out.archive = c.archive;
  if (c.ports) out.ports = c.ports;
  if (c.env && Object.keys(c.env).length) out.env = c.env;
  if (c.services?.length) out.services = c.services;
  if (c.agents?.length) out.agents = c.agents;
  if (c.hooks?.length) out.hooks = c.hooks;
  if (c.flows?.length) out.flows = c.flows;
  // Not edited here, but kept: saving must not drop the box's own login.
  if (c.login) out.login = c.login;
  return out;
}

// useProjectConfig loads a location's config on a box and keeps a draft of
// this box's layer, which save writes back.
export function useProjectConfig(box: string, location: string) {
  const client = useStore((s) => s.client);
  const [config, setConfig] = useState<LocationConfig>();
  const [draft, setDraft] = useState<RepoConfig>({});
  const [error, setError] = useState<string>();
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    if (!client) return;
    try {
      const c = await flowsApi.config(client, box, location);
      setConfig(c);
      setDraft(c.local ?? {});
      setError(undefined);
    } catch (err) {
      setError(errorMessage(err));
    }
  }, [client, box, location]);

  useEffect(() => {
    setConfig(undefined);
    void load();
  }, [load]);

  // Someone else changed it: reload, unless there are edits to keep.
  const changed = useEventLog((s) => s.events.find((e) => e.type === "config.changed" && e.box === box && e.data?.location === location));
  const dirty = !!config && JSON.stringify(clean(draft)) !== JSON.stringify(clean(config.local ?? {}));
  useEffect(() => {
    if (changed && !dirty) void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [changed]);

  const save = useCallback(async () => {
    if (!client) return;
    // A reference the box would refuse: say which, before it says it raw.
    const bad = Object.entries(draft.env ?? {}).find(([, v]) => isSecretRef(v) && refProblem(v));
    if (bad) {
      setError(`${bad[0]} isn't a valid secret reference. ${refProblem(bad[1])}`);
      return;
    }
    setSaving(true);
    try {
      // Flows and hooks are edited elsewhere (the flow editor saves into this
      // same config), so they always come from the box, never a stale draft.
      const fresh = await flowsApi.config(client, box, location);
      const c = await flowsApi.saveConfig(client, box, location, clean({ ...draft, flows: fresh.local?.flows, hooks: fresh.local?.hooks }));
      setConfig(c);
      setDraft(c.local ?? {});
      setError(undefined);
    } catch (err) {
      setError(explainConfigError(errorMessage(err)));
    } finally {
      setSaving(false);
    }
  }, [client, box, location, draft]);

  const discard = () => {
    setDraft(config?.local ?? {});
    setError(undefined);
  };

  return { config, draft, setDraft, dirty, save, saving, discard, error, reload: load };
}
