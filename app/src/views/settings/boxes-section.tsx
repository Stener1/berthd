import { ArrowUpCircleIcon, BotIcon, CopyIcon, EllipsisIcon, PlusIcon, RefreshCwIcon, ShieldIcon, Trash2Icon } from "lucide-react";
import { useState } from "react";

import { StatusDot, useBoxState } from "@/components/agent-glyph";
import { BrowserSandboxCard } from "@/components/browser-sandbox";
import { EditorsSettings } from "@/components/editors/editors-settings";
import { ErrorDetails } from "@/components/error-note";
import { GuardDialog } from "@/components/guard-dialog";
import { Tip } from "@/components/tip";
import { Button } from "@/components/ui/button";
import { Menu, MenuItem, MenuPopup, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { Switch } from "@/components/ui/switch";
import { toastManager } from "@/components/ui/toast";
import { OutdatedNotice, UpgradeBox } from "@/components/upgrade-box";
import { type BoxStatus, laptopApi } from "@/lib/api";
import { explain } from "@/lib/errors";
import { errorMessage } from "@/lib/format";
import { updateBoxes, useOutdated } from "@/lib/outdated";
import { usePrefs } from "@/lib/prefs";
import { BOX_WORDS, boxWhy } from "@/lib/state-model";
import { NONE, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { openAddBox } from "@/views/onboarding/add-box-dialog";
import { AddAgents } from "@/views/onboarding/guided-install";
import { CommandLog } from "@/views/settings/command-log";
import { ConfirmDialog } from "@/views/settings/confirm";
import { RemoveLocalBoxDialog } from "@/views/settings/local-box-remove";
import { Code, SettingsGroup, SettingsPage, SettingsRow } from "@/views/settings/rows";

export function BoxesSection() {
  const boxes = useStore((s) => s.status?.boxes ?? NONE);
  const auto = usePrefs((p) => p.autoUpdateBoxes);
  return (
    <SettingsPage
      title="Boxes"
      description={
        <>
          The machines your agents run on. Each runs berthd; this computer only connects to them. <Code>berth boxes</Code> shows the same.
        </>
      }
    >
      <OutdatedNotice className="-mt-2" />
      <SettingsGroup
        title={boxes.length === 1 ? "1 paired" : `${boxes.length} paired`}
        actions={
          <Button size="xs" variant="outline" onClick={openAddBox}>
            <PlusIcon /> Add a box
          </Button>
        }
      >
        {boxes.length === 0 ? (
          <div className="flex flex-col items-center gap-3 px-4 py-8 text-center">
            <p className="text-muted-foreground text-sm">No boxes yet. Agents run on a box: any VPS or dev machine you can SSH into.</p>
            <Button size="sm" onClick={openAddBox}>
              Add your first box
            </Button>
          </div>
        ) : (
          boxes.map((b) => <BoxRow key={b.name} box={b} />)
        )}
      </SettingsGroup>
      {boxes.length > 0 && (
        <SettingsGroup>
          <SettingsRow label="Update boxes automatically when Berth updates" description="Each box gets the berthd this Berth ships as soon as it's online. Agents keep running through an update.">
            <Switch checked={auto} onCheckedChange={(autoUpdateBoxes) => usePrefs.setState({ autoUpdateBoxes })} aria-label="Update boxes automatically when Berth updates" />
          </SettingsRow>
        </SettingsGroup>
      )}
      {boxes.length > 0 && <EditorsSettings />}
    </SettingsPage>
  );
}

// retry asks the agent to check every box now rather than at its next poll.
async function retry() {
  const st = useStore.getState();
  if (!st.client) return;
  try {
    await st.client.laptop("POST", "/v1/refresh");
  } catch {
    // The status refresh below reports what the agent knows either way.
  }
  await st.refreshAll();
}

function BoxRow({ box }: { box: BoxStatus }) {
  const info = useStore((s) => s.boxes[box.name]?.info);
  const update = useOutdated((s) => s.updating[box.name]);
  const check = useOutdated((s) => s.boxes[box.name]);
  const [forgetting, setForgetting] = useState(false);
  const [removingLocal, setRemovingLocal] = useState(false);
  const [guarding, setGuarding] = useState(false);
  const [addingAgents, setAddingAgents] = useState(false);
  const canAddAgents = !!info?.capabilities?.includes("agents.install");
  const [retrying, setRetrying] = useState(false);
  const online = box.state === "online";
  const state = useBoxState(box.name);
  const upgrading = update?.state === "queued" || update?.state === "running";
  // What went wrong reaching it, in plain words, with the raw text behind Details.
  const problem = box.error && !online ? explain(box.error, { box: box.name }) : undefined;

  const build = info?.build && (state === "outdated" && check?.available ? `berthd ${info.build} → ${check.available}` : `berthd ${info.build}`);
  const details = [box.address, box.network && `via ${box.network}`, build, info?.os && info.arch && `${info.os}/${info.arch}`].filter(Boolean);

  return (
    <div className="px-4 py-3">
      <div className="flex items-center gap-3">
        <Tip label={boxWhy(box.name, box, state)}>
          <StatusDot state={state} className="size-2" />
        </Tip>
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline gap-2 text-sm">
            <span>{box.name}</span>
            {box.local && <span className="rounded border px-1 text-[10px] text-muted-foreground uppercase tracking-wide">This Mac</span>}
            <span className={cn("text-xs", state === "online" ? "text-muted-foreground" : state === "outdated" ? "text-info-foreground" : state === "unreachable" ? "text-destructive-foreground" : "text-muted-foreground")}>
              {state === "online" && box.latency_ms != null ? `${box.latency_ms} ms` : BOX_WORDS[state].word}
            </span>
          </div>
          <div className="truncate font-mono text-[11px] text-muted-foreground">{details.join(" · ")}</div>
          {problem && (
            <div className="mt-0.5 text-[11px] text-muted-foreground">
              {problem.message}
              <ErrorDetails text={problem.details} className="text-[11px]" />
            </div>
          )}
        </div>
        {state === "outdated" && <UpgradeBox box={box.name} size="xs" variant="outline" label="Update" />}
        {!online && (
          <Button
            size="xs"
            variant="outline"
            loading={retrying}
            onClick={async () => {
              setRetrying(true);
              await retry();
              setRetrying(false);
            }}
          >
            <RefreshCwIcon /> Retry
          </Button>
        )}
        <Menu>
          <MenuTrigger render={<Button size="icon-xs" variant="ghost" aria-label={`${box.name} actions`} />}>
            <EllipsisIcon />
          </MenuTrigger>
          <MenuPopup align="end" className="min-w-48">
            <MenuItem disabled={!online || upgrading} onClick={() => void updateBoxes([box.name])}>
              <ArrowUpCircleIcon />
              {online ? "Update berthd" : "Update berthd (offline)"}
            </MenuItem>
            <MenuItem
              onClick={() =>
                navigator.clipboard.writeText(box.address).then(
                  () => toastManager.add({ title: "Copied the address", type: "success" }),
                  () => {},
                )
              }
            >
              <CopyIcon />
              Copy address
            </MenuItem>
            {canAddAgents && (
              <MenuItem disabled={!online} onClick={() => setAddingAgents(true)} data-testid="box-add-agents">
                <BotIcon />
                Add agents…
              </MenuItem>
            )}
            <MenuItem disabled={!online} onClick={() => setGuarding(true)}>
              <ShieldIcon />
              Resource guard…
            </MenuItem>
            <MenuSeparator />
            {box.local && (
              <MenuItem variant="destructive" onClick={() => setRemovingLocal(true)}>
                <Trash2Icon />
                Stop using this Mac…
              </MenuItem>
            )}
            <MenuItem variant="destructive" onClick={() => setForgetting(true)}>
              <Trash2Icon />
              Forget…
            </MenuItem>
          </MenuPopup>
        </Menu>
      </div>
      {/* The agent's browser can't start here (Chromium's sandbox), or runs without it. */}
      {online && <BrowserSandboxCard box={box.name} full className="mt-3" />}
      {update && update.state !== "queued" && <CommandLog className="mt-3" lines={update.lines ?? []} done={update.state === "done"} error={update.error} />}
      {update?.state === "queued" && <p className="mt-2 text-muted-foreground text-xs">Waiting for the box before it to finish updating…</p>}
      <GuardDialog box={box.name} open={guarding} onOpenChange={setGuarding} />
      {canAddAgents && <AddAgents box={box.name} open={addingAgents} onClose={() => setAddingAgents(false)} />}
      {box.local && <RemoveLocalBoxDialog box={box.name} open={removingLocal} onOpenChange={setRemovingLocal} />}
      <ConfirmDialog
        open={forgetting}
        onOpenChange={setForgetting}
        destructive
        title={`Forget ${box.name}?`}
        description={
          <>
            This computer stops connecting to {box.name}. Nothing on the box changes: its agents, worktrees and berthd keep running, and you can pair again with <Code>berthd pair</Code>.
          </>
        }
        confirm="Forget"
        onConfirm={async () => {
          const client = useStore.getState().client;
          if (!client) return;
          try {
            await laptopApi.forget(client, box.name);
            await useStore.getState().refreshStatus();
            toastManager.add({ title: `Forgot ${box.name}`, type: "success" });
          } catch (err) {
            toastManager.add({ title: `Couldn't forget ${box.name}`, description: errorMessage(err), type: "error" });
          }
        }}
      />
    </div>
  );
}
