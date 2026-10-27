import type { Cell, Change } from "./api.ts";
import { quote, sameCell } from "./engines/families.ts";

// The changes someone has made to a table and not applied yet. Nothing is
// written as they type: an edit is staged here, shown in the grid, listed in
// Review as old → new, and sent as one db/{conn}/edit, all or nothing.
//
// A row is known by its primary key as it was read, so two edits to one row
// are one update, an edit back to what was there is no change, and deleting
// a row drops what was staged for it.

export type Row = Record<string, Cell>;

export interface Update {
  key: Row;
  // What each changed column held when it was read, for Review.
  was: Row;
  set: Row;
}

export interface Delete {
  key: Row;
}

export interface Insert {
  // The new row's name until it has a key: "new-1".
  id: string;
  // Only what was typed: a column left out takes the table's default.
  set: Row;
}

export interface Staged {
  updates: Record<string, Update>;
  deletes: Record<string, Delete>;
  inserts: Insert[];
  // The next new row's number.
  next: number;
}

export const NOTHING: Staged = { updates: {}, deletes: {}, inserts: [], next: 1 };

// A row's key: its primary key's values, in the key's order.
export function keyOf(row: Row, pk: string[]): Row {
  return Object.fromEntries(pk.map((c) => [c, row[c] ?? null]));
}

// The same, as one string to file the row under.
export function rowId(row: Row, pk: string[]): string {
  return JSON.stringify(pk.map((c) => row[c] ?? null));
}

// A key in a sentence: "id = 12", "product_id = 4, warehouse = 'east'".
export function keyText(key: Row): string {
  return Object.entries(key)
    .map(([c, v]) => `${c} = ${quote(v, 40)}`)
    .join(", ");
}

const without = <T,>(map: Record<string, T>, id: string): Record<string, T> => {
  const { [id]: _, ...rest } = map;
  return rest;
};

// setCell stages one cell of a row that was read. Back to what was read is
// no change; a row staged for deletion takes none.
export function setCell(staged: Staged, pk: string[], row: Row, column: string, value: Cell): Staged {
  const id = rowId(row, pk);
  if (staged.deletes[id]) return staged;
  const had = staged.updates[id] ?? { key: keyOf(row, pk), was: {}, set: {} };
  const { [column]: _w, ...was } = had.was;
  const { [column]: _s, ...set } = had.set;
  if (!sameCell(row[column] ?? null, value)) {
    was[column] = row[column] ?? null;
    set[column] = value;
  }
  if (Object.keys(set).length === 0) return { ...staged, updates: without(staged.updates, id) };
  return { ...staged, updates: { ...staged.updates, [id]: { key: had.key, was, set } } };
}

// addRow stages a new row with nothing typed yet, and says what it is called.
export function addRow(staged: Staged): [Staged, string] {
  const id = `new-${staged.next}`;
  return [{ ...staged, inserts: [...staged.inserts, { id, set: {} }], next: staged.next + 1 }, id];
}

// setNew stages one cell of a new row; undefined takes it back, so the
// column gets its default.
export function setNew(staged: Staged, id: string, column: string, value: Cell | undefined): Staged {
  return {
    ...staged,
    inserts: staged.inserts.map((i) => {
      if (i.id !== id) return i;
      const { [column]: _, ...set } = i.set;
      return { id, set: value === undefined ? set : { ...set, [column]: value } };
    }),
  };
}

// removeRows stages rows that were read for deletion, dropping what was
// staged for them; removeNew takes a new row back.
export function removeRows(staged: Staged, pk: string[], rows: Row[]): Staged {
  let { updates, deletes } = staged;
  for (const row of rows) {
    const id = rowId(row, pk);
    updates = without(updates, id);
    deletes = { ...deletes, [id]: { key: keyOf(row, pk) } };
  }
  return { ...staged, updates, deletes };
}

export function removeNew(staged: Staged, ids: string[]): Staged {
  return { ...staged, inserts: staged.inserts.filter((i) => !ids.includes(i.id)) };
}

// What Review lists: a change, in the order it will be sent.
export interface ReviewLine {
  // What to call undo with.
  id: string;
  kind: "update" | "delete" | "insert";
  // The row: its key, or "New row".
  title: string;
  // Each column old → new. A delete has none; an insert has no old.
  fields: { column: string; was?: Cell; now: Cell }[];
}

export function review(staged: Staged): ReviewLine[] {
  return [
    ...Object.entries(staged.deletes).map(([id, d]): ReviewLine => ({ id, kind: "delete", title: keyText(d.key), fields: [] })),
    ...Object.entries(staged.updates).map(([id, u]): ReviewLine => ({ id, kind: "update", title: keyText(u.key), fields: Object.keys(u.set).map((column) => ({ column, was: u.was[column], now: u.set[column] })) })),
    ...staged.inserts.map((i): ReviewLine => ({ id: i.id, kind: "insert", title: "New row", fields: Object.entries(i.set).map(([column, now]) => ({ column, now })) })),
  ];
}

// undo takes one of Review's lines back.
export function undo(staged: Staged, line: Pick<ReviewLine, "id" | "kind">): Staged {
  if (line.kind === "insert") return removeNew(staged, [line.id]);
  if (line.kind === "delete") return { ...staged, deletes: without(staged.deletes, line.id) };
  return { ...staged, updates: without(staged.updates, line.id) };
}

// How many rows a staged set changes.
export function count(staged: Staged): number {
  return Object.keys(staged.updates).length + Object.keys(staged.deletes).length + staged.inserts.length;
}

// What db/{conn}/edit is sent: deletes, then updates, then inserts, so a
// new row can take the key of one deleted in the same go.
export function changes(staged: Staged): Change[] {
  return [
    ...Object.values(staged.deletes).map((d): Change => ({ op: "delete", key: d.key })),
    ...Object.values(staged.updates).map((u): Change => ({ op: "update", key: u.key, set: u.set })),
    ...staged.inserts.map((i): Change => ({ op: "insert", set: i.set })),
  ];
}

// The rows an edit names by key: the ones a row_conflict can be about.
export function keyed(staged: Staged): { id: string; key: Row }[] {
  return [...Object.entries(staged.deletes), ...Object.entries(staged.updates)].map(([id, c]) => ({ id, key: c.key }));
}

// dropRows forgets what was staged for rows that are no longer there.
export function dropRows(staged: Staged, ids: string[]): Staged {
  let { updates, deletes } = staged;
  for (const id of ids) {
    updates = without(updates, id);
    deletes = without(deletes, id);
  }
  return { ...staged, updates, deletes };
}

// A summary for the bar: "2 changes", and in words what kinds.
export function summary(staged: Staged): string {
  const parts = [
    [Object.keys(staged.updates).length, "edited"],
    [staged.inserts.length, "added"],
    [Object.keys(staged.deletes).length, "deleted"],
  ] as const;
  return parts
    .filter(([n]) => n > 0)
    .map(([n, what]) => `${n} ${what}`)
    .join(", ");
}
