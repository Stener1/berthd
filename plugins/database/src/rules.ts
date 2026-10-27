// What may be done to a table's rows from the grid, and why not when not.
// Kept apart from the panel so it can be tested.

export interface Rules {
  // Rows that were read can be edited and deleted.
  rows: boolean;
  // Rows can be added.
  inserts: boolean;
  // Why something above is false, in a sentence. Undefined when all is allowed.
  why?: string;
}

interface Kind {
  label: string;
  // A word for the list of tables, where there is room for one.
  short: string;
  icon: string;
  why?: string;
}

const KINDS: Record<string, Kind> = {
  table: { label: "Table", short: "table", icon: "table-2" },
  view: { label: "View", short: "view", icon: "Eye", why: "is a view: its rows come from a query, so they can't be changed here" },
  materialized_view: { label: "Materialized view", short: "mat. view", icon: "Layers", why: "is a materialized view: its rows are a query's saved result, so they can't be changed here" },
};

// A kind of relation in words, with an icon. One the plugin has no name for
// is called what the helper called it, and is read, not edited.
export function kindOf(kind: string): Kind {
  return KINDS[kind] ?? { label: kind.replaceAll("_", " ") || "Relation", short: kind.replaceAll("_", " ") || "relation", icon: "table-2", why: `is a ${kind.replaceAll("_", " ") || "relation"}, which Shipyard only knows how to read` };
}

export function rulesFor(table: { name: string; kind: string; primary_key: string[] }, engine: { name: string; edits: boolean }, caps: { writes: boolean }): Rules {
  const none = (why: string): Rules => ({ rows: false, inserts: false, why });
  if (!caps.writes) return none(`${engine.name} takes no writes through Shipyard, so ${table.name} can be read and not changed.`);
  if (!engine.edits) return none(`Shipyard doesn't know ${engine.name}'s types well enough to edit rows in the grid. The console still runs what you type.`);
  const kind = kindOf(table.kind);
  if (kind.why) return none(`${table.name} ${kind.why}.`);
  if (table.primary_key.length === 0) return { rows: false, inserts: true, why: `${table.name} has no primary key, so one row can't be told from another like it: rows can be added, not edited or deleted.` };
  return { rows: true, inserts: true };
}
