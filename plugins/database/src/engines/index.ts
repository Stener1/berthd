// What the plugin has to know about a database engine that the helper
// can't tell it. Most of what differs between engines arrives with each
// connection (its `capabilities`: the dialect, whether it has schemas, the
// filter operators, the kinds of relation, whether it takes writes), and the
// panel is built from that. What is left is here: a name and an icon, how
// the engine names its types, and how it quotes a name.
//
// To add an engine:
//   1. Write engines/<id>.ts exporting an Engine (postgres.ts is the model).
//      `id` is what a connection's `engine` says.
//   2. Add it to ENGINES below.
//   3. Add its type names to its `family` and a case to
//      app/src/lib/database.test.ts.
// Nothing else in the plugin names an engine. One this file has never heard
// of still shows: engineOf falls back to GENERIC, which reads every value as
// plain text and offers no editing in the grid, so nothing is written in a
// form the engine may not take. Its console still runs what you type.

import type { Family } from "./families.ts";
import { postgres } from "./postgres.ts";

export interface Engine {
  // What a connection's `engine` says: "postgres".
  id: string;
  // What people call it: "PostgreSQL".
  name: string;
  // A lucide icon.
  icon: string;
  // Whether the plugin knows the engine's types well enough to edit its rows
  // in the grid: what a cell shows can be sent back as it is.
  edits: boolean;
  // The family of one of the engine's types, as the schema names it
  // ("timestamp with time zone", "numeric(10,2)", "text[]"). A family
  // decides how a value is shown and edited (families.ts).
  family(type: string): Family;
  // A name as the engine quotes it in SQL the plugin writes for the console.
  ident(name: string): string;
  // The schema a table's name can leave out, when the engine has one.
  defaultSchema?: string;
  // The statement that shows another's plan without running it, when the
  // engine has one.
  explain?(statement: string): string;
}

const ENGINES: Record<string, Engine> = { [postgres.id]: postgres };

// GENERIC is an engine the plugin doesn't know, by the name the helper gave.
export const generic = (id: string): Engine => ({
  id,
  name: id || "Database",
  icon: "Database",
  edits: false,
  family: () => "other",
  ident: (name) => `"${name.replaceAll('"', '""')}"`,
});

export function engineOf(id: string): Engine {
  return ENGINES[id] ?? generic(id);
}

// A table's name for a sentence and for SQL: without its schema where the
// engine lets that be left out.
export function tableName(engine: Engine, schema: string, table: string, quoted = false): string {
  const q = (s: string) => (quoted && !/^[a-z_][a-z0-9_]*$/.test(s) ? engine.ident(s) : s);
  return !schema || schema === engine.defaultSchema ? q(table) : `${q(schema)}.${q(table)}`;
}

export type { Family } from "./families.ts";
