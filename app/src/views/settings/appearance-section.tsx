import { CheckIcon, SearchIcon } from "lucide-react";
import { useState } from "react";

import { SimpleSelect } from "@/components/simple-select";
import { Input } from "@/components/ui/input";
import { SYSTEM_THEME, useThemes } from "@/hooks/use-theme";
import type { Theme } from "@/lib/api";
import { setPrefs, usePrefs } from "@/lib/prefs";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { berthThemes } from "@/themes/builtin";
import { ChatBackgroundSettings, ChatWidthSettings } from "@/views/settings/chat-background-section";
import { Segmented } from "@/views/settings/controls";
import { HeaderPictureSettings } from "@/views/settings/header-picture-section";
import { Code, SettingsGroup, SettingsPage, SettingsRow } from "@/views/settings/rows";
import { IS_LINUX } from "@/lib/platform";

const BERTH = new Set(berthThemes.map((t) => t.id));

export function AppearanceSection() {
  const themes = useThemes();
  const themeId = useStore((s) => s.themeId);
  const density = usePrefs((p) => p.density);
  const uiFontSize = usePrefs((p) => p.uiFontSize);
  const [query, setQuery] = useState("");

  // Shipyard's own, then every other theme (built-in ports, yours, plugins')
  // by appearance; a search narrows them by name.
  const q = query.trim().toLowerCase();
  const shown = q ? themes.filter((t) => t.name.toLowerCase().includes(q) || t.id.includes(q)) : themes;
  const groups = [
    { title: "Shipyard", themes: shown.filter((t) => BERTH.has(t.id)) },
    { title: "Dark", themes: shown.filter((t) => !BERTH.has(t.id) && t.appearance === "dark") },
    { title: "Light", themes: shown.filter((t) => !BERTH.has(t.id) && t.appearance !== "dark") },
  ].filter((g) => g.themes.length);

  return (
    <SettingsPage title="Appearance">
      <section>
        <div className="mb-2 flex items-end gap-3">
          <div className="min-w-0 flex-1">
            <h2 className="font-medium text-[13px] text-muted-foreground">Theme</h2>
            <p className="mt-0.5 text-muted-foreground text-xs">
              Add your own as JSON in <Code>~/.berth/themes/</Code>, or install a plugin that ships one.
            </p>
          </div>
          <div className="relative w-48 shrink-0">
            <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 z-10 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input size="sm" type="search" value={query} onChange={(e) => setQuery(e.currentTarget.value)} placeholder="Find a theme" aria-label="Find a theme" className="[&_input]:pl-7.5" />
          </div>
        </div>
        {!q && <SystemCard selected={themeId === SYSTEM_THEME} themes={themes} />}
        {groups.map((g) => (
          <div key={g.title} className="mt-4">
            <h3 className="mb-2 font-medium text-[11px] text-muted-foreground uppercase tracking-wide">{g.title}</h3>
            <div className="grid grid-cols-3 gap-3 max-[1100px]:grid-cols-2 max-[700px]:grid-cols-1">
              {g.themes.map((t) => (
                <ThemeCard key={t.id} theme={t} selected={t.id === themeId} onSelect={() => useStore.getState().setTheme(t.id)} />
              ))}
            </div>
          </div>
        ))}
        {!groups.length && <p className="mt-4 text-muted-foreground text-xs">No theme is called “{query.trim()}”.</p>}
      </section>

      <SettingsGroup title="Interface">
        <SettingsRow label="Text size" description="Everything outside the terminal. ⌘+ and ⌘− step it from 11 to 20px.">
          <Segmented
            value={String(uiFontSize)}
            options={[
              { value: "12", label: "Small" },
              { value: "13", label: "Default" },
              { value: "14", label: "Large" },
              // A size zoomed to (⌘+, ⌘−) that none of these is.
              ...(uiFontSize < 12 || uiFontSize > 14 ? [{ value: String(uiFontSize), label: `${uiFontSize}px` }] : []),
            ]}
            onChange={(v) => setPrefs({ uiFontSize: Number(v) })}
          />
        </SettingsRow>
        <SettingsRow label="Density" description="How tightly the sidebar and lists are packed.">
          <Segmented
            value={density}
            options={[
              { value: "compact", label: "Compact" },
              { value: "comfortable", label: "Comfortable" },
            ]}
            onChange={(density) => setPrefs({ density })}
          />
        </SettingsRow>
      </SettingsGroup>

      <ChatWidthSettings />

      <ChatBackgroundSettings />

      <HeaderPictureSettings />
    </SettingsPage>
  );
}

// SystemCard follows the computer's appearance: one theme by day, another
// when macOS is dark (Shipyard Light and Shipyard Dark unless picked here). Its
// preview is half of each.
function SystemCard({ selected, themes }: { selected: boolean; themes: Theme[] }) {
  const picks = usePrefs((p) => p.systemThemes);
  const light = themes.find((t) => t.id === picks.light) ?? themes.find((t) => t.id === "berth-light");
  const dark = themes.find((t) => t.id === picks.dark) ?? themes.find((t) => t.id === "berth-dark");
  if (!light || !dark) return null;
  const pick = (side: "light" | "dark", id: string) => {
    setPrefs({ systemThemes: { ...picks, [side]: id } });
    useStore.getState().setTheme(SYSTEM_THEME);
  };
  const options = (appearance: "light" | "dark") => themes.filter((t) => (t.appearance === "dark") === (appearance === "dark")).map((t) => ({ value: t.id, label: t.name }));
  return (
    <div className={cn("flex items-center overflow-hidden rounded-xl border transition-[box-shadow,border-color]", selected ? "border-ring ring-1 ring-ring" : "hover:border-foreground/25")}>
      <button
        type="button"
        onClick={() => useStore.getState().setTheme(SYSTEM_THEME)}
        aria-pressed={selected}
        className="flex min-w-0 flex-1 items-center self-stretch text-left outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
      >
        <div className="relative h-14 w-28 shrink-0 border-r">
          <Swatch theme={light} />
          <div className="absolute inset-0 [clip-path:polygon(100%_0,100%_100%,0_100%)]">
            <Swatch theme={dark} />
          </div>
        </div>
        <span className="flex min-w-0 flex-1 items-center gap-2 px-3.5 text-[13px]">
          <span className="min-w-0 flex-1">
            <span className="block">Match system</span>
            <span className="block truncate text-[11px] text-muted-foreground">{IS_LINUX ? "Follows your desktop" : "Follows macOS"}</span>
          </span>
          {selected && <CheckIcon className="size-3.5" />}
        </span>
      </button>
      <div className="flex shrink-0 items-center gap-2 px-3 max-[800px]:hidden">
        <SimpleSelect size="sm" className="w-40" aria-label="Theme by day" value={light.id} options={options("light")} onChange={(id) => id && pick("light", id)} />
        <SimpleSelect size="sm" className="w-40" aria-label={IS_LINUX ? "Theme when your desktop is dark" : "Theme when macOS is dark"} value={dark.id} options={options("dark")} onChange={(id) => id && pick("dark", id)} />
      </div>
    </div>
  );
}

// ThemeCard previews a theme with its own colors: a sidebar beside a
// terminal, whatever theme the app itself is in.
function ThemeCard({ theme, selected, onSelect }: { theme: Theme; selected: boolean; onSelect(): void }) {
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-pressed={selected}
      data-testid="theme-option"
      data-theme-id={theme.id}
      className={cn("group overflow-hidden rounded-xl border text-left outline-none transition-[box-shadow,border-color] focus-visible:ring-2 focus-visible:ring-ring", selected ? "border-ring ring-1 ring-ring" : "hover:border-foreground/25")}
    >
      {/* A picture of the theme (its name says which): its sample text is in
          the theme's terminal colours, which the axe spec leaves out. */}
      <div aria-hidden data-a11y-skip className="relative h-[88px]">
        <Preview theme={theme} />
        <Dots theme={theme} />
      </div>
      <div className="flex items-center gap-2 border-t bg-card px-3 py-2 text-[13px]">
        <span className="min-w-0 flex-1 truncate">{theme.name}</span>
        {selected && <CheckIcon className="size-3.5 shrink-0" />}
      </div>
    </button>
  );
}

// Preview draws a theme's sidebar beside its terminal, in its own colors.
function Preview({ theme }: { theme: Theme }) {
  const c = theme.colors;
  const t = theme.terminal;
  return (
    <div className="flex h-full" style={{ background: c.background }}>
      <div className="flex w-[38%] flex-col gap-1.5 border-r px-2.5 py-3" style={{ background: c.sidebar, borderColor: c.border }}>
        <div className="h-1.5 w-4/5 rounded-full" style={{ background: c.sidebarForeground, opacity: 0.55 }} />
        <div className="h-1.5 w-3/5 rounded-full" style={{ background: c.sidebarForeground, opacity: 0.3 }} />
        <div className="flex items-center gap-1">
          <span className="size-1.5 rounded-full" style={{ background: c.warning }} />
          <div className="h-1.5 flex-1 rounded-full" style={{ background: c.accent }} />
        </div>
        <div className="h-1.5 w-2/3 rounded-full" style={{ background: c.sidebarForeground, opacity: 0.3 }} />
      </div>
      <div className="flex-1 px-2.5 py-2.5 font-mono text-[10px] leading-[1.5]" style={{ background: t.background, color: t.foreground }}>
        <div>
          <span style={{ color: t.green }}>●</span> tests pass
        </div>
        <div>
          <span style={{ color: t.yellow }}>●</span> <span style={{ color: t.blue }}>~/work/shop</span>
        </div>
        <div>
          <span style={{ color: t.magenta }}>❯</span> <span style={{ color: t.cyan }}>berth</span>
          <span className="ml-0.5 inline-block h-2.5 w-1 align-middle" style={{ background: t.cursor }} />
        </div>
      </div>
    </div>
  );
}

// Swatch is a theme's colours without text, for a thumbnail too small to
// read: sidebar, terminal, and an accent and a cursor.
function Swatch({ theme }: { theme: Theme }) {
  const c = theme.colors;
  const t = theme.terminal;
  return (
    <div className="flex h-full">
      <div className="flex w-2/5 flex-col gap-1 px-1.5 py-2" style={{ background: c.sidebar }}>
        <div className="h-1 w-4/5 rounded-full" style={{ background: c.sidebarForeground, opacity: 0.5 }} />
        <div className="h-1 w-3/5 rounded-full" style={{ background: c.accent }} />
      </div>
      <div className="flex-1 px-1.5 py-2" style={{ background: t.background }}>
        <div className="h-1 w-3/4 rounded-full" style={{ background: t.green }} />
        <div className="mt-1 h-1.5 w-1 rounded-[1px]" style={{ background: t.cursor }} />
      </div>
    </div>
  );
}

// Dots are a theme's colours at a glance, in the corner of its preview: its
// primary, then the terminal's red, yellow, green, blue and magenta.
function Dots({ theme }: { theme: Theme }) {
  const t = theme.terminal;
  return (
    <span className="absolute right-2 bottom-2 flex items-center gap-[3px] rounded-full border px-1.5 py-1" style={{ background: theme.colors.background, borderColor: theme.colors.border }} aria-hidden>
      {[theme.colors.primary, t.red, t.yellow, t.green, t.blue, t.magenta].map((c, i) => (
        <span key={i} className="size-2 rounded-full" style={{ background: c }} />
      ))}
    </span>
  );
}
