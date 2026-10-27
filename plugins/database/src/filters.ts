import type { Cell, Filter, ForeignKey, Order } from "./api.ts";
import { type Family, parseInput } from "./engines/families.ts";

// How a table's rows are narrowed and ordered: what the panel keeps while
// someone types, and what db/{conn}/rows is sent.

// Every operator the plugin has words for. Which of them a filter offers is
// the connection's to say (its capabilities' operators): one that is not
// here is offered by its own name, and takes a value.
export const OPERATORS: Record<string, { label: string; value: boolean }> = {
  "=": { label: "=", value: true },
  "!=": { label: "≠", value: true },
  "<": { label: "<", value: true },
  "<=": { label: "≤", value: true },
  ">": { label: ">", value: true },
  ">=": { label: "≥", value: true },
  like: { label: "like", value: true },
  ilike: { label: "like, any case", value: true },
  is_null: { label: "is NULL", value: false },
  not_null: { label: "is not NULL", value: false },
};

export const opLabel = (op: string) => OPERATORS[op]?.label ?? op;
export const takesValue = (op: string) => OPERATORS[op]?.value ?? true;

const PATTERNS = new Set(["like", "ilike"]);
const ORDERED = new Set(["<", "<=", ">", ">="]);

// opsFor is the operators a column can be filtered by: the connection's,
// less the ones that mean nothing for its family (a pattern on a number, an
// order on JSON).
export function opsFor(operators: string[], family: Family): string[] {
  return operators.filter((op) => {
    if (PATTERNS.has(op)) return family === "text" || family === "other" || family === "uuid";
    if (ORDERED.has(op)) return family !== "json" && family !== "boolean" && family !== "binary" && family !== "array";
    return true;
  });
}

// A filter as it is being written. value is undefined until it has been
// entered once: a filter someone has only just added narrows nothing, and
// one entered empty means the empty string.
export interface Draft {
  id: number;
  column: string;
  op: string;
  value?: string;
}

// filtersOf is what the drafts ask the helper for. A value goes as the
// column's family reads it (a number, true), or as typed for the database
// to cast; a pattern always goes as text.
export function filtersOf(drafts: Draft[], family: (column: string) => Family): Filter[] {
  const out: Filter[] = [];
  for (const d of drafts) {
    if (!takesValue(d.op)) {
      out.push({ column: d.column, op: d.op });
      continue;
    }
    if (d.value === undefined) continue;
    const parsed = PATTERNS.has(d.op) ? undefined : parseInput(d.value, family(d.column));
    out.push({ column: d.column, op: d.op, value: parsed?.ok ? parsed.value : d.value });
  }
  return out;
}

// nextOrder is the order after a click on a column's heading: ascending,
// then descending, then as the table gives them. One column at a time.
export function nextOrder(order: Order[], column: string): Order[] {
  const now = order.find((o) => o.column === column);
  if (!now) return [{ column }];
  return now.desc ? [] : [{ column, desc: true }];
}

// jumpFilters finds the row a foreign key points at: its referenced columns
// equal to this row's. Undefined when the key holds a NULL: it points at
// nothing.
export function jumpFilters(fk: ForeignKey, row: Record<string, Cell>): Draft[] | undefined {
  const out: Draft[] = [];
  for (let i = 0; i < fk.columns.length; i++) {
    const v = row[fk.columns[i]];
    if (v === null || v === undefined) return undefined;
    out.push({ id: i + 1, column: fk.ref_columns[i], op: "=", value: String(v) });
  }
  return out;
}
