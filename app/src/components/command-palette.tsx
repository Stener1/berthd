import {
  BellIcon,
  Columns2Icon,
  HouseIcon,
  KeyboardIcon,
  Minimize2Icon,
  BookMarkedIcon,
  CodeXmlIcon,
  ArrowUpRightIcon,
  CheckIcon,
  CodeIcon,
  FolderPlusIcon,
  GitBranchIcon,
  GitBranchPlusIcon,
  GitCompareArrowsIcon,
  GlobeIcon,
  LayoutDashboardIcon,
  PanelTopIcon,
  PuzzleIcon,
  RefreshCwIcon,
  ServerIcon,
  SettingsIcon,
  SlidersHorizontalIcon,
  SquareTerminalIcon,
  PackageIcon,
  PackagePlusIcon,
  UsersIcon,
  WorkflowIcon,
} from "lucide-react";
import { isMock } from "@/hooks/use-berth-connection";
import { openAddKit } from "@/views/kits/kits-store";
import { useMemo, useRef, useState } from "react";

import { AgentIcon, StateGlyph } from "@/components/agent-glyph";
import {
  Command,
  CommandCollection,
  CommandDialog,
  CommandDialogPopup,
  CommandEmpty,
  CommandFooter,
  CommandGroup,
  CommandGroupLabel,
  CommandInput,
  CommandItem,
  CommandList,
  CommandPanel,
} from "@/components/ui/command";
import { openEditor } from "@/components/editors/open";
import { Kbd } from "@/components/ui/kbd";
import { useAllSessions } from "@/hooks/use-agent-counts";
import { sessionWord } from "@/lib/state-model";
import { useThemes } from "@/hooks/use-theme";
import { openBrowserAt, resolveUrl } from "@/lib/actions";
import { agentOf, sessionAgent, sessionName, sortedWorktrees, worktreeOf } from "@/lib/derive";
import { openBroadcast, openPromptPicker } from "@/lib/prompts";
import { setNotificationsOpen } from "@/lib/notifications";
import { usePrefs } from "@/lib/prefs";
import { useStore } from "@/lib/store";
import { isLink, teamRef } from "@/lib/team-ref";
import { focusedPane, focusSession, goHome, hereRef, recentWorktrees, refOf, selectWorktree, useWorkspaces } from "@/lib/workspaces";
import { openShortcuts } from "@/components/shortcuts-sheet";
import { openWorktreePicker } from "@/components/workspace/worktree-picker";
import { newTerminal } from "@/components/box-picker";
import { openCustomize, useArrangedNav } from "@/components/sidebar/nav";
import { loadPlugins } from "@/plugins/host";
import { useRegistry } from "@/plugins/registry";
import { openAddBox } from "@/views/onboarding/add-box-dialog";
import { openAttempts, openComposer } from "@/lib/composer";
import { defaultScope } from "@/views/automations/flows/project-label";

// defaultScopeRef is a project to try things in when no worktree is open.
function defaultScopeRef(): { box: string; location: string } | undefined {
  const at = defaultScope();
  return at?.scope.startsWith("repo:") ? { box: at.box, location: at.scope.slice(5) } : undefined;
}

interface Item {
  value: string;
  label: string;
  detail?: string;
  // Also matched when searching, without being shown.
  search?: string;
  icon?: React.ReactNode;
  // Shown at the right: a shortcut, or a theme's swatches.
  shortcut?: string;
  trailing?: React.ReactNode;
  // Highlighting a theme previews it.
  theme?: string;
  run(): void;
}

interface Group {
  value: string;
  items: Item[];
}

const slot = (icon: React.ReactNode) => <span className="flex size-4 shrink-0 items-center justify-center text-muted-foreground [&_svg]:size-4">{icon}</span>;

// CommandPalette (⌘K) jumps anywhere: sessions, worktrees, views, themes,
// and whatever commands plugins add. Empty, it shows what needs you and
// where you were; with a query that matches nothing, it offers to make it.
export function CommandPalette() {
  const open = useStore((s) => s.paletteOpen);
  const setOpen = useStore((s) => s.setPaletteOpen);
  const sessions = useAllSessions();
  const boxes = useStore((s) => s.boxes);
  const status = useStore((s) => s.status);
  const themeId = useStore((s) => s.themeId);
  const spaces = useWorkspaces((s) => s.spaces);
  const themes = useThemes();
  const pluginCommands = useRegistry((s) => s.commands);
  const nav = useArrangedNav();
  const [query, setQuery] = useState("");
  // The theme in use when the palette opened, to put back after a preview.
  const before = useRef<string | undefined>(undefined);

  const close = (keepTheme = false) => {
    if (!keepTheme && before.current && useStore.getState().themeId !== before.current) useStore.getState().setTheme(before.current);
    before.current = undefined;
    setQuery("");
    setOpen(false);
  };

  const groups = useMemo<Group[]>(() => {
    const st = useStore.getState();
    const go = (fn: () => void) => () => {
      close(true);
      fn();
    };
    const q = query.trim();

    const actions: Item[] = [
      { value: "new-worktree task start agent", label: "New task…", icon: slot(<GitBranchPlusIcon />), shortcut: "⌘N", run: go(() => {
        const at = hereRef();
        st.openNewWorktree(at ? { box: at.box, location: at.location } : {});
      }) },
      { value: "new worktree only no agent", label: "New worktree (no agent)…", icon: slot(<GitBranchPlusIcon />), run: go(() => {
        const at = hereRef();
        openComposer({ noAgent: true, ...(at ? { box: at.box, location: at.location } : {}) });
      }) },
      { value: "new-terminal", label: "New terminal", icon: slot(<SquareTerminalIcon />), shortcut: "⌘T", run: go(newTerminal) },
      { value: "new-browser", label: "New browser tab", icon: slot(<GlobeIcon />), shortcut: "⌘⇧B", run: go(() => openBrowserAt("")) },
      { value: "open in editor cursor vscode zed", label: "Open in editor", icon: slot(<CodeXmlIcon />), shortcut: "⌘⇧O", run: go(() => {
        const at = hereRef();
        if (at) void openEditor({ box: at.box, path: at.path });
      }) },
      { value: "new-tab", label: "New tab…", icon: slot(<PanelTopIcon />), run: go(() => st.setNewTabMenuOpen(true)) },
      ...(usePrefs.getState().labs && st.view.kind === "workspace" && focusedPane()
        ? [{ value: "split right with another worktree side by side guest pane", label: "Split right with another worktree…", icon: slot(<Columns2Icon />), shortcut: "⌘⌥D", run: go(() => openWorktreePicker({ kind: "split" })) }]
        : []),
      { value: "send saved prompt library", label: "Send a saved prompt…", icon: slot(<BookMarkedIcon />), run: go(() => openPromptPicker()) },
      { value: "broadcast prompt several agents running", label: "Send a prompt to several agents…", icon: slot(<UsersIcon />), run: go(() => openBroadcast()) },
      {
        value: "try n ways attempts best of several agents compare",
        label: "Try N ways…",
        icon: slot(<GitCompareArrowsIcon />),
        run: go(() => {
          const at = hereRef() ?? defaultScopeRef();
          if (at) openAttempts({ box: at.box, location: at.location });
        }),
      },
      { value: "add-location", label: "Add a project…", icon: slot(<FolderPlusIcon />), run: go(() => st.openAddProject()) },
      ...(usePrefs.getState().labs
        ? [{ value: "zen focus calm hide sidebar labs", label: usePrefs.getState().zen ? "Leave zen" : "Zen: only the agents", icon: slot(<Minimize2Icon />), shortcut: "⌘.", run: go(() => usePrefs.setState((p) => ({ zen: !p.zen }))) }]
        : []),
      ...(usePrefs.getState().labs ? [{ value: "home harbour start", label: "Home", icon: slot(<HouseIcon />), run: go(goHome) }] : []),
      { value: "dashboard", label: "Agent Dashboard", icon: slot(<LayoutDashboardIcon />), shortcut: "⌘J", run: go(() => st.setView({ kind: "dashboard" })) },
      { value: "keyboard shortcuts keys help", label: "Keyboard shortcuts", icon: slot(<KeyboardIcon />), shortcut: "⌘/", run: go(openShortcuts) },
      { value: "notifications inbox bell", label: "Notifications", icon: slot(<BellIcon />), shortcut: "⌘⇧N", run: go(() => setNotificationsOpen(true)) },
      { value: "notification settings do not disturb", label: "Notification settings", icon: slot(<BellIcon />), run: go(() => st.setView({ kind: "settings", section: "notifications" })) },
      { value: "worktrees", label: "Worktrees", icon: slot(<GitBranchIcon />), run: go(() => st.setView({ kind: "worktrees" })) },
      { value: "automations", label: "Automations", icon: slot(<WorkflowIcon />), run: go(() => st.setView({ kind: "automations" })) },
      { value: "kits", label: "Kits", icon: slot(<PackageIcon />), run: go(() => st.setView({ kind: "kits" })) },
      { value: "add kit from link", label: "Add a kit from a link…", icon: slot(<PackagePlusIcon />), run: go(() => openAddKit()) },
      { value: "settings", label: "Settings", icon: slot(<SettingsIcon />), run: go(() => st.setView({ kind: "settings" })) },
      { value: "add-box", label: "Add a box…", icon: slot(<ServerIcon />), run: go(openAddBox) },
      { value: "team setup github org berth workspace kit onboarding", label: "Team setup…", icon: slot(<UsersIcon />), run: go(() => st.setView({ kind: "team", from: "palette" })) },
      // A link to a team setup pasted here opens it.
      ...(q.includes("/") && teamRef(q) && isLink(teamRef(q)!)
        ? [{ value: `team setup link ${q}`, label: "Open team setup from link", detail: teamRef(q), search: q, icon: slot(<UsersIcon />), run: go(() => st.setView({ kind: "team", org: teamRef(q), from: "palette" })) }]
        : []),
      { value: "settings-developer", label: "Developer settings", icon: slot(<CodeIcon />), run: go(() => st.setView({ kind: "settings", section: "developer" })) },
      { value: "customize-sidebar", label: "Customize sidebar…", icon: slot(<SlidersHorizontalIcon />), run: go(() => openCustomize()) },
      { value: "refresh", label: "Refresh everything", icon: slot(<RefreshCwIcon />), run: go(() => void st.refreshAll()) },
      { value: "reload-plugins", label: "Reload plugins", icon: slot(<PuzzleIcon />), run: go(() => st.client && void loadPlugins(st.client)) },
    ];
    // Demo mode plays what agents on boxes do, to see the app react.
    const hereAt = hereRef();
    const ws = hereAt ? { ref: hereAt } : undefined;
    if (isMock()) {
      actions.push({ value: "mock demo notifications every kind", label: "Demo: one of every notification", icon: slot(<BellIcon />), run: go(() => void import("@/lib/mock").then((m) => m.mockNotifications())) });
    }
    if (isMock() && ws) {
      const where = ws.ref.main ? ws.ref.location : `${ws.ref.location}/${ws.ref.worktree}`;
      for (const how of ["split", "tab"] as const) {
        actions.push({
          value: `mock agent opens ${how}`,
          label: `Demo: an agent opens Claude Code in a ${how} here`,
          icon: slot(<CodeIcon />),
          run: go(() => void import("@/lib/mock").then((m) => m.mockAgentOpens(ws.ref.box, where, ws.ref.path, how))),
        });
      }
    }

    // Sessions are named by where they run, so two "main" branches in
    // different repositories cannot be confused.
    const sessionItem = ({ box, session, state }: (typeof sessions)[number]): Item => {
      const where = worktreeOf(boxes[box]?.locations, session);
      const agent = agentOf(session);
      return {
        value: `session:${box}/${session.name}`,
        label: sessionName(session, { sessions: boxes[box]?.sessions, locations: boxes[box]?.locations, place: true }),
        // Its state in the same words as everywhere else, then where.
        detail: [sessionAgent(session), state !== "idle" && sessionWord(state), where?.worktree.branch, box].filter(Boolean).join(" · "),
        search: session.name,
        icon: (
          <span className="flex w-8 shrink-0 items-center gap-1">
            <AgentIcon agent={agent} />
            <StateGlyph state={state} className="size-3" />
          </span>
        ),
        run: go(() => void focusSession(box, session.name)),
      };
    };

    const online = status?.boxes.filter((b) => b.state === "online").map((b) => b.name) ?? [];
    const worktreeItems: Item[] = online.flatMap((box) =>
      (boxes[box]?.locations ?? []).flatMap((loc) =>
        sortedWorktrees(loc).map((wt) => ({
          value: `wt:${box}:${wt.path}`,
          label: wt.main ? loc.name : `${loc.name} / ${wt.name}`,
          detail: [wt.branch, box].filter(Boolean).join(" · "),
          icon: slot(<GitBranchIcon />),
          run: go(() => selectWorktree(refOf(box, loc, wt))),
        })),
      ),
    );

    const themeItems: Item[] = themes.map((t) => ({
      value: `theme:${t.id}`,
      label: t.name,
      search: "theme",
      theme: t.id,
      icon: slot(t.id === (before.current ?? themeId) ? <CheckIcon /> : null),
      trailing: (
        <span className="ml-auto flex shrink-0 overflow-hidden rounded-sm border">
          {[t.colors.background, t.colors.sidebar, t.terminal.blue, t.terminal.green].map((c, i) => (
            <span key={i} className="size-3" style={{ background: c }} />
          ))}
        </span>
      ),
      run: go(() => st.setTheme(t.id)),
    }));

    const pluginItems: Item[] = pluginCommands.map(({ plugin, item }) => ({
      value: `plugin:${plugin}:${item.id}`,
      label: item.title,
      detail: item.group ?? plugin,
      icon: slot(<PuzzleIcon />),
      shortcut: item.shortcut,
      run: go(() => {
        try {
          void Promise.resolve(item.run()).catch((err) => console.error(`plugin ${plugin}: ${item.id} failed`, err));
        } catch (err) {
          console.error(`plugin ${plugin}: ${item.id} failed`, err);
        }
      }),
    }));

    // Places hidden from the sidebar stay reachable here.
    const hiddenPlaces: Item[] = nav.hidden.map((n) => ({
      value: `place:${n.id}`,
      label: n.label,
      detail: "Hidden from sidebar",
      icon: slot(n.icon),
      run: go(n.go),
    }));

    if (!q) {
      const waiting = sessions.filter((s) => s.state === "waiting").map(sessionItem);
      const recent: Item[] = recentWorktrees(spaces, 5).map((w) => ({
        value: `recent:${w.ref.box}:${w.ref.path}`,
        label: w.ref.main ? w.ref.location : `${w.ref.location} / ${w.ref.worktree}`,
        detail: w.ref.box,
        icon: slot(<GitBranchIcon />),
        run: go(() => selectWorktree(w.ref)),
      }));
      return [
        { value: "Needs you", items: waiting },
        { value: "Recent", items: recent },
        { value: "Actions", items: actions },
      ].filter((g) => g.items.length);
    }

    // Searching: everything, and ways to make what is not there.
    const url = resolveUrl(q);
    const make: Item[] = [
      ...(url ? [{ value: `open:${url}`, label: `Open ${q} in a browser tab`, detail: url, icon: slot(<ArrowUpRightIcon />), run: go(() => openBrowserAt(url)) }] : []),
      { value: `new-worktree:${q}`, label: `New task from "${q}"`, icon: slot(<GitBranchPlusIcon />), run: go(() => st.openNewWorktree({ name: q })) },
    ];
    return [
      { value: "Sessions", items: sessions.map(sessionItem) },
      { value: "Worktrees", items: worktreeItems },
      { value: "Actions", items: actions },
      { value: "Plugins", items: pluginItems },
      { value: "Themes", items: themeItems },
      { value: "Hidden from sidebar", items: hiddenPlaces },
      { value: "Make it", items: make },
    ].filter((g) => g.items.length);
    // close is stable enough: it only reads refs and store setters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessions, boxes, status, themes, themeId, spaces, pluginCommands, query, nav.hidden]);

  return (
    <CommandDialog
      open={open}
      onOpenChange={(o) => {
        if (o) setOpen(true);
        else close();
      }}
    >
      <CommandDialogPopup>
        <Command
          items={groups}
          value={query}
          onValueChange={setQuery}
          itemToStringValue={(i: unknown) => `${(i as Item).label} ${(i as Item).detail ?? ""} ${(i as Item).search ?? ""}`}
          onItemHighlighted={(i: unknown) => {
            // Highlighting a theme previews it; closing without choosing puts
            // the old one back.
            const t = (i as Item | undefined)?.theme;
            if (!t) return;
            before.current ??= useStore.getState().themeId;
            useStore.getState().setTheme(t);
          }}
        >
          <CommandInput placeholder="Jump to a session, worktree, or command…" />
          <CommandPanel>
            <CommandEmpty>Nothing matches.</CommandEmpty>
            <CommandList>
              {(group: Group) => (
                <CommandGroup key={group.value} items={group.items}>
                  <CommandGroupLabel>{group.value}</CommandGroupLabel>
                  <CommandCollection>
                    {(item: Item) => (
                      <CommandItem key={item.value} value={item} className="gap-2" onClick={() => item.run()}>
                        {item.icon}
                        <span className="truncate">{item.label}</span>
                        {item.detail && <span className="ml-auto min-w-0 shrink truncate text-muted-foreground text-xs">{item.detail}</span>}
                        {item.trailing}
                        {item.shortcut && <Kbd className={item.detail ? "" : "ml-auto"}>{item.shortcut}</Kbd>}
                      </CommandItem>
                    )}
                  </CommandCollection>
                </CommandGroup>
              )}
            </CommandList>
          </CommandPanel>
          <CommandFooter className="text-[11px] text-muted-foreground">
            <span className="flex items-center gap-1">
              <Kbd>↑</Kbd>
              <Kbd>↓</Kbd> to move, <Kbd>↵</Kbd> to open
            </span>
            <span className="flex items-center gap-1">
              <Kbd>esc</Kbd> to close
            </span>
          </CommandFooter>
        </Command>
      </CommandDialogPopup>
    </CommandDialog>
  );
}
