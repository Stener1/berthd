import type { Cell } from "../api.ts";

// A family is a kind of value, whatever an engine calls its types: it
// decides how a cell is shown in the grid and what may be typed into it.
// Engine.family maps a type's name to one; "other" is a type the plugin has
// no rule for, shown and sent back as the text the database wrote.
export type Family = "text" | "integer" | "decimal" | "boolean" | "time" | "json" | "binary" | "uuid" | "array" | "other";

// How a cell is drawn. NULL and the empty string are words of their own,
// never an empty cell: one is no value, the other is a value with nothing
// in it, and a grid that draws both as blank can't be trusted.
export interface Shown {
  kind: "null" | "empty" | "value";
  text: string;
  // Numbers line up on the right.
  right: boolean;
}

// The most of a value a cell draws; all of it is in the sheet.
const CELL = 240;

export function show(cell: Cell, family: Family): Shown {
  if (cell === null) return { kind: "null", text: "NULL", right: false };
  const right = family === "integer" || family === "decimal";
  if (typeof cell === "boolean") return { kind: "value", text: cell ? "true" : "false", right: false };
  if (typeof cell === "number") return { kind: "value", text: String(cell), right };
  if (cell === "") return { kind: "empty", text: "empty", right: false };
  // One line: a break is drawn, not obeyed.
  const line = (cell.length > CELL ? cell.slice(0, CELL) : cell).replace(/\r?\n/g, "↵").replace(/\t/g, "→");
  return { kind: "value", text: line, right };
}

// Whether a value is more than a cell can show: it opens in the sheet.
export const isLong = (cell: Cell) => typeof cell === "string" && (cell.length > 80 || cell.includes("\n"));

// What an editor starts with for a cell. NULL starts empty: typing replaces it.
export function editText(cell: Cell): string {
  if (cell === null) return "";
  return typeof cell === "string" ? cell : String(cell);
}

// A value as the sheet shows it whole: JSON laid out when it parses.
export function pretty(cell: Cell, family: Family): string {
  if (cell === null) return "NULL";
  if (typeof cell !== "string") return String(cell);
  if (family === "json") {
    try {
      return JSON.stringify(JSON.parse(cell), null, 2);
    } catch {
      return cell;
    }
  }
  return cell;
}

export type Parsed = { ok: true; value: Cell } | { ok: false; why: string };

const no = (why: string): Parsed => ({ ok: false, why });
const TRUE = new Set(["true", "t", "yes", "y", "on", "1"]);
const FALSE = new Set(["false", "f", "no", "n", "off", "0"]);

// parseInput turns what was typed into the value to send. It never makes
// NULL: NULL is asked for by name (Set NULL), so an empty box in a text
// column is the empty string, and in any other column is not a value yet.
// What it lets through goes as a JSON scalar, or as a string the database
// casts to the column's type.
export function parseInput(text: string, family: Family): Parsed {
  if (family === "text") return { ok: true, value: text };
  const t = text.trim();
  if (!t) return no("Type a value, or set NULL: only text can be empty.");
  switch (family) {
    case "integer":
      if (!/^[+-]?\d+$/.test(t)) return no("Type a whole number.");
      // Past what JSON's numbers hold exactly, it goes as its digits.
      return { ok: true, value: Number.isSafeInteger(Number(t)) ? Number(t) : t.replace(/^\+/, "") };
    case "decimal":
      if (!/^[+-]?(\d+\.?\d*|\.\d+)(e[+-]?\d+)?$/i.test(t) && !/^[+-]?(nan|infinity)$/i.test(t)) return no("Type a number.");
      return { ok: true, value: t };
    case "boolean":
      if (TRUE.has(t.toLowerCase())) return { ok: true, value: true };
      if (FALSE.has(t.toLowerCase())) return { ok: true, value: false };
      return no("Type true or false.");
    case "uuid":
      if (!/^[0-9a-f]{8}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{12}$/i.test(t)) return no("Type a UUID: 32 hex digits.");
      return { ok: true, value: t };
    case "json":
      try {
        JSON.parse(t);
      } catch {
        return no("That isn't JSON yet.");
      }
      // As typed: its spacing and key order are the person's.
      return { ok: true, value: text };
    case "binary":
      if (!/^\\x([0-9a-f]{2})*$/i.test(t)) return no("Type bytes as \\x and pairs of hex digits.");
      return { ok: true, value: t };
    default:
      // A time, an array, a type with no rule: as written, for the database to read.
      return { ok: true, value: text };
  }
}

// Whether two cells are the same value. A number and its digits are (a
// float arrives as a number and is typed as text); NULL and "" never are.
export function sameCell(a: Cell, b: Cell): boolean {
  if (a === b) return true;
  if (a === null || b === null) return false;
  if (typeof a === "number" && typeof b === "string") return String(a) === b;
  if (typeof b === "number" && typeof a === "string") return String(b) === a;
  return false;
}

// A cell in a sentence: NULL bare, text in quotes (so '' is seen), cut short.
export function quote(cell: Cell, max = 60): string {
  if (cell === null) return "NULL";
  if (typeof cell !== "string") return String(cell);
  const line = cell.replace(/\s+/g, " ");
  return `'${line.length > max ? `${line.slice(0, max)}…` : line}'`;
}
