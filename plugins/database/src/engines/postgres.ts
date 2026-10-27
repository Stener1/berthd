import type { Family } from "./families.ts";
import type { Engine } from "./index.ts";

// PostgreSQL's type names, both as the catalog has them ("int4", "bpchar",
// "_text") and as SQL writes them ("integer", "character(3)", "text[]").
const FAMILIES: Record<string, Family> = {
  int2: "integer",
  int4: "integer",
  int8: "integer",
  smallint: "integer",
  integer: "integer",
  int: "integer",
  bigint: "integer",
  smallserial: "integer",
  serial: "integer",
  bigserial: "integer",
  oid: "integer",
  numeric: "decimal",
  decimal: "decimal",
  float4: "decimal",
  float8: "decimal",
  real: "decimal",
  "double precision": "decimal",
  bool: "boolean",
  boolean: "boolean",
  text: "text",
  varchar: "text",
  "character varying": "text",
  character: "text",
  char: "text",
  bpchar: "text",
  citext: "text",
  name: "text",
  date: "time",
  time: "time",
  timetz: "time",
  timestamp: "time",
  timestamptz: "time",
  "time without time zone": "time",
  "time with time zone": "time",
  "timestamp without time zone": "time",
  "timestamp with time zone": "time",
  interval: "time",
  json: "json",
  jsonb: "json",
  bytea: "binary",
  uuid: "uuid",
};

export const postgres: Engine = {
  id: "postgres",
  name: "PostgreSQL",
  icon: "Database",
  edits: true,
  family(type) {
    const t = type.trim().toLowerCase();
    // An array, however it is written: "text[]", "_text", "integer ARRAY".
    if (t.endsWith("[]") || t.startsWith("_") || t.endsWith(" array")) return "array";
    // "numeric(10,2)", "character varying(255)", "timestamp(3) with time zone".
    return FAMILIES[t.replace(/\s*\([^)]*\)/g, "")] ?? "other";
  },
  ident: (name) => `"${name.replaceAll('"', '""')}"`,
  defaultSchema: "public",
  // Without ANALYZE: the plan, and nothing is run.
  explain: (statement) => `EXPLAIN ${statement}`,
};
