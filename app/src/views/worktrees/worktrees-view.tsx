import { SearchIcon } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { Scene } from "@/components/art/scenes";
import { BoxFilter, shownBoxes as shownOf } from "@/components/box-filter";
import { FilterChip } from "@/components/filter-chip";
import { SimpleSelect } from "@/components/simple-select";
import { Button } from "@/components/ui/button";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { queueRemoval } from "@/lib/remove-worktree";
import { load, save } from "@/lib/storage";
import { ViewHeader } from "@/views/view-header";
import { BulkBar } from "@/views/worktrees/bulk-bar";
import { ArchiveDialog } from "@/views/worktrees/archive-dialog";
import { HistorySheet } from "@/views/worktrees/history-sheet";
import { type BulkAction, useBulk } from "@/views/worktrees/use-bulk";
import { type Row, useWorktrees } from "@/views/worktrees/use-worktrees";
import { type Group, WorktreeTable } from "@/views/worktrees/worktree-table";
import { BoxError } from "@/components/upgrade-box";

type Flag = "behind" | "changes" | "paused";
type Sort = "recent" | "behind" | "changes" | "name";

const SORTS: { value: Sort; label: string }[] = [
  { value: "recent", label: "Last commit" },
  { value: "behind", label: "Most behind" },
  { value: "changes", label: "Most changes" },
  { value: "name", label: "Name" },
];

// WorktreesView is every worktree on every box in one table, to see where
// each stands against its base and act on many at once: sync, pause, stop,
// delete. A row opens its commit history.
export function WorktreesView() {
  const { rows, boxes, errors, loaded, patch } = useWorktrees();
  const onRowDone = useCallback((r: Row, p: Partial<Row>) => patch(r.box, r.location, r.name, p), [patch]);
  const { progress, summary, run, cancel, clear } = useBulk(onRowDone);

  const [query, setQuery] = useState("");
  // Boxes turned off; every box is on until one is turned off, as on the
  // Agent Dashboard.
  const [hiddenBoxes, setHiddenBoxes] = useState<string[]>(() => load("berth.worktrees.hiddenBoxes", []));
  const [project, setProject] = useState("");
  const [flags, setFlags] = useState<Flag[]>([]);
  const [sort, setSort] = useState<Sort>(() => load("berth.worktrees.sort", "recent"));
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [anchor, setAnchor] = useState<string>();
  const [openKey, setOpenKey] = useState<string>();
  const [archiving, setArchiving] = useState<Row[]>();
  // The row order when a bulk action started: rows stay put under the
  // pointer while their commits change, until its summary is dismissed.
  const [frozen, setFrozen] = useState<string[]>();

  const shownBoxes = shownOf(boxes, hiddenBoxes);
  const hideBoxes = (next: string[]) => {
    setHiddenBoxes(next);
    save("berth.worktrees.hiddenBoxes", next);
  };

  const projects = useMemo(() => [...new Set(rows.map((r) => r.location))].sort(), [rows]);
  const q = query.trim().toLowerCase();
  const visible = useMemo(() => {
    const keep = rows.filter(
      (r) =>
        shownBoxes.includes(r.box) &&
        (!project || r.location === project) &&
        (!flags.includes("behind") || r.behind > 0) &&
        (!flags.includes("changes") || r.changed + r.untracked > 0) &&
        (!flags.includes("paused") || r.paused) &&
        (!q || [r.name, r.branch, r.location, r.box, r.last_commit?.subject].some((s) => s?.toLowerCase().includes(q))),
    );
    const by: Record<Sort, (a: Row, b: Row) => number> = {
      recent: (a, b) => (b.last_commit?.time ?? "").localeCompare(a.last_commit?.time ?? ""),
      behind: (a, b) => b.behind - a.behind,
      changes: (a, b) => b.changed + b.untracked - (a.changed + a.untracked),
      name: (a, b) => a.name.localeCompare(b.name),
    };
    const at = new Map(frozen?.map((k, i) => [k, i]));
    const pos = (r: Row) => at.get(r.key) ?? Number.MAX_SAFE_INTEGER;
    // The main checkout heads its project; the rest follow the sort.
    return keep.sort((a, b) => Number(!!b.main) - Number(!!a.main) || (frozen ? pos(a) - pos(b) : 0) || by[sort](a, b));
  }, [rows, shownBoxes.join(","), project, flags, q, sort, frozen]); // eslint-disable-line react-hooks/exhaustive-deps

  const groups = useMemo(() => {
    const map = new Map<string, Group>();
    for (const r of visible) {
      const key = `${r.box}/${r.location}`;
      if (!map.has(key)) map.set(key, { key, box: r.box, location: r.location, rows: [] });
      map.get(key)!.rows.push(r);
    }
    return [...map.values()].sort((a, b) => a.location.localeCompare(b.location) || a.box.localeCompare(b.box));
  }, [visible]);
  const flat = useMemo(() => groups.flatMap((g) => g.rows), [groups]);

  // Selection follows what exists: deleted worktrees drop out.
  useEffect(() => {
    setSelected((s) => {
      const keys = new Set(rows.map((r) => r.key));
      const next = new Set([...s].filter((k) => keys.has(k)));
      return next.size === s.size ? s : next;
    });
  }, [rows]);

  const toggle = (r: Row, shift: boolean) => {
    setSelected((s) => {
      const next = new Set(s);
      if (shift && anchor) {
        const [i, j] = [flat.findIndex((x) => x.key === anchor), flat.findIndex((x) => x.key === r.key)];
        if (i >= 0 && j >= 0) {
          const on = !s.has(r.key);
          for (const x of flat.slice(Math.min(i, j), Math.max(i, j) + 1)) on ? next.add(x.key) : next.delete(x.key);
          return next;
        }
      }
      next.has(r.key) ? next.delete(r.key) : next.add(r.key);
      return next;
    });
    setAnchor(r.key);
  };
  const toggleGroup = (g: Group, on: boolean) =>
    setSelected((s) => {
      const next = new Set(s);
      for (const r of g.rows) on ? next.add(r.key) : next.delete(r.key);
      return next;
    });

  // Esc clears the selection; ⌘A selects every visible worktree.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = e.target instanceof HTMLElement && (e.target.closest("input, textarea, [role=dialog]") || e.target.isContentEditable);
      if (typing) return;
      if (e.key === "Escape" && selected.size && !openKey) setSelected(new Set());
      if (e.key === "a" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        setSelected(new Set(flat.map((r) => r.key)));
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [flat, selected.size, openKey]);

  const selectedRows = rows.filter((r) => selected.has(r.key));
  const busy = !!summary && !summary.done;
  const act = (a: BulkAction, list: Row[]) => {
    if (busy) return;
    setFrozen(flat.map((r) => r.key));
    void run(a, list);
  };
  // Delete asks nothing: each worktree shows "Removing…" until its box has
  // removed it, one at a time per box (lib/remove-worktree.ts).
  const remove = (list: Row[]) => {
    for (const r of list) if (!r.main) queueRemoval(r.box, r.location, { name: r.name, path: r.path });
  };
  const dismiss = () => {
    clear();
    setFrozen(undefined);
  };
  const open = rows.find((r) => r.key === openKey);
  const filtered = !!(q || shownBoxes.length < boxes.length || project || flags.length);
  const flip = (f: Flag) => setFlags((fs) => (fs.includes(f) ? fs.filter((x) => x !== f) : [...fs, f]));
  const clearFilters = () => {
    setQuery("");
    hideBoxes([]);
    setProject("");
    setFlags([]);
  };
  const behind = rows.filter((r) => r.behind > 0).length;
  const paused = rows.filter((r) => r.paused).length;

  return (
    <div className="relative flex h-full flex-col">
      <ViewHeader
        title="Worktrees"
        description={loaded ? [`${rows.length} on ${boxes.length} box${boxes.length === 1 ? "" : "es"}`, behind && `${behind} behind their base`, paused && `${paused} paused`].filter(Boolean).join(" · ") : undefined}
      />

      {/* One row of filters, search first; it stays one row down to 1100px. */}
      <div className="@container/toolbar flex shrink-0 items-center gap-2 border-b px-page py-2">
        <div className="relative w-56 min-w-36 shrink">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 z-10 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input size="sm" value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Search" className="[&_input]:pl-8" aria-label="Search worktrees" />
        </div>
        <BoxFilter boxes={boxes} hidden={hiddenBoxes} onChange={hideBoxes} />
        <div className="w-40 min-w-28 shrink">
          <SimpleSelect aria-label="Project" size="sm" className="min-w-0" value={project} onChange={setProject} options={[{ value: "", label: "All projects" }, ...projects.map((p) => ({ value: p, label: p }))]} />
        </div>
        <div className="flex shrink-0 items-center gap-1" role="group" aria-label="Show only">
          <FilterChip pressed={flags.includes("behind")} onPressedChange={() => flip("behind")}>
            {/* One flex item, or the chip's gap doubles the space before "base". */}
            <span>
              Behind<span className="@max-[56rem]/toolbar:hidden"> base</span>
            </span>
          </FilterChip>
          <FilterChip pressed={flags.includes("changes")} onPressedChange={() => flip("changes")}>
            <span className="@max-[56rem]/toolbar:hidden">Has changes</span>
            <span className="@min-[56rem]/toolbar:hidden">Changed</span>
          </FilterChip>
          <FilterChip pressed={flags.includes("paused")} onPressedChange={() => flip("paused")}>
            Paused
          </FilterChip>
        </div>
        {filtered && (
          <Button size="xs" variant="ghost" className="shrink-0" onClick={clearFilters}>
            Clear filters
          </Button>
        )}
        <div className="ml-auto w-44 min-w-32 shrink">
          <SimpleSelect
            size="sm"
            className="min-w-0"
            value={sort}
            onChange={(v) => {
              setSort(v as Sort);
              save("berth.worktrees.sort", v);
            }}
            options={SORTS}
          />
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-auto pb-24">
        {Object.entries(errors).map(([box, err]) => (
          <div key={box} className="border-b px-page py-2">
            <BoxError box={box} error={err} what="its worktrees" />
          </div>
        ))}
        {!loaded ? (
          <div className="space-y-2 px-page pt-4">
            {[0, 1, 2, 3, 4, 5].map((i) => (
              <Skeleton key={i} className="h-10" />
            ))}
          </div>
        ) : flat.length === 0 ? (
          <Empty className="mt-16">
            <EmptyHeader>
              <EmptyMedia>
                {/* Worktrees are berths: none yet is an empty one; none
                    passing the filters, a beam finding nothing. */}
                <Scene name={filtered ? "lighthouse" : "ended"} />
              </EmptyMedia>
              <EmptyTitle>{filtered ? "No worktrees match" : "No worktrees yet"}</EmptyTitle>
              <EmptyDescription>{filtered ? "Nothing here passes every filter." : "Worktrees you make on any box show up here."}</EmptyDescription>
            </EmptyHeader>
            {filtered && (
              <Button size="sm" variant="outline" onClick={clearFilters}>
                Clear filters
              </Button>
            )}
          </Empty>
        ) : (
          <WorktreeTable
            groups={groups}
            selected={selected}
            progress={progress}
            onToggle={toggle}
            onToggleGroup={toggleGroup}
            onToggleAll={(on) => setSelected(on ? new Set(flat.map((r) => r.key)) : new Set())}
            onOpen={(r) => setOpenKey(r.key)}
            openKey={openKey}
          />
        )}
      </div>

      <BulkBar
        selected={selectedRows}
        summary={summary}
        progress={progress}
        onSync={(mode, paused) => act({ kind: "sync", mode, paused }, selectedRows)}
        onPause={() => act({ kind: "pause" }, selectedRows)}
        onResume={() => act({ kind: "resume" }, selectedRows)}
        onStop={() => act({ kind: "stop" }, selectedRows)}
        onDelete={() => {
          remove(selectedRows);
          setSelected(new Set());
        }}
        onArchive={() => setArchiving(selectedRows)}
        onClear={() => setSelected(new Set())}
        onCancel={cancel}
        onDismiss={dismiss}
        onRetry={(a, list) => {
          dismiss();
          act(a, list);
        }}
      />

      <HistorySheet
        row={open}
        progress={open ? progress[open.key] : undefined}
        busy={busy}
        onClose={() => setOpenKey(undefined)}
        onAction={(a) => open && act(a, [open])}
        onDelete={() => {
          if (open) remove([open]);
          setOpenKey(undefined);
        }}
        onArchive={() => open && setArchiving([open])}
      />

      <ArchiveDialog
        rows={archiving}
        progress={progress}
        running={busy && summary?.action.kind === "archive"}
        onRun={() => archiving && act({ kind: "archive" }, archiving)}
        onClose={() => {
          if (summary?.action.kind === "archive") {
            clear();
            setSelected(new Set());
            setOpenKey(undefined);
          }
          setArchiving(undefined);
        }}
      />
    </div>
  );
}
