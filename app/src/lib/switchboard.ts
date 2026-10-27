import { create } from "zustand";

import { load, save } from "@/lib/storage";

// A person's lightweight choices for the Switchboard. The underlying work
// stays on its box; these labels can be rebuilt or changed without moving it.
interface SwitchboardDoc {
  focus: string[];
  areas: string[];
  assigned: Record<string, string>;
  notes: Record<string, string>;
}

const KEY = "berth.switchboard";
const empty: SwitchboardDoc = { focus: [], areas: [], assigned: {}, notes: {} };
const saved = load<Partial<SwitchboardDoc>>(KEY, empty);
const initial: SwitchboardDoc = {
  focus: Array.isArray(saved.focus) ? saved.focus.filter((x): x is string => typeof x === "string") : [],
  areas: Array.isArray(saved.areas) ? saved.areas.filter((x): x is string => typeof x === "string") : [],
  assigned: saved.assigned && typeof saved.assigned === "object" ? saved.assigned : {},
  notes: saved.notes && typeof saved.notes === "object" ? saved.notes : {},
};

export const useSwitchboard = create<SwitchboardDoc>()(() => initial);
export const useSwitchboardOpen = create<{ open: boolean }>()(() => ({ open: false }));
export const setSwitchboardOpen = (open: boolean) => useSwitchboardOpen.setState({ open });

function update(fn: (doc: SwitchboardDoc) => SwitchboardDoc) {
  useSwitchboard.setState((doc) => {
    const next = fn(doc);
    if (next !== doc) save(KEY, next);
    return next;
  });
}

export const switchboardActions = {
  toggleFocus(id: string) {
    update((d) => ({ ...d, focus: d.focus.includes(id) ? d.focus.filter((x) => x !== id) : [...d.focus, id] }));
  },
  addArea(name: string) {
    const area = name.trim();
    if (area && !["all", "unsorted"].includes(area.toLowerCase())) update((d) => ({ ...d, areas: d.areas.some((a) => a.toLowerCase() === area.toLowerCase()) ? d.areas : [...d.areas, area] }));
  },
  assign(id: string, area?: string) {
    update((d) => {
      const assigned = { ...d.assigned };
      if (area) assigned[id] = area;
      else delete assigned[id];
      return { ...d, assigned };
    });
  },
  note(id: string, value: string) {
    update((d) => {
      const notes = { ...d.notes };
      const note = value.trim();
      if (note) notes[id] = note;
      else delete notes[id];
      return { ...d, notes };
    });
  },
  migrate(from: string, to: string) {
    if (from === to) return;
    update((d) => {
      if (!d.focus.includes(from) && !(from in d.assigned) && !(from in d.notes)) return d;
      const focus = [...new Set(d.focus.map((id) => id === from ? to : id))];
      const assigned = { ...d.assigned };
      const notes = { ...d.notes };
      if (from in assigned) { assigned[to] ??= assigned[from]; delete assigned[from]; }
      if (from in notes) { notes[to] ??= notes[from]; delete notes[from]; }
      return { ...d, focus, assigned, notes };
    });
  },
};
