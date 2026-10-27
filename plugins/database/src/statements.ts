// What the console runs, and what it remembers. The helper takes one
// statement at a time, so the console has to say which of the editor's it
// means.

export interface Statement {
  // The statement as it is sent: no white space around it, no closing semicolon.
  text: string;
  // Where it is in the editor's text.
  from: number;
  to: number;
}

interface Piece extends Statement {
  // Where its stretch of the text ends: after its semicolon.
  end: number;
}

// split cuts text at the semicolons that end statements: not the ones in a
// string, a quoted name, a dollar-quoted body or a comment. A stretch with
// nothing in it but white space and comments is not a statement.
export function split(sql: string): Piece[] {
  const out: Piece[] = [];
  let start = 0;
  let solid = false;
  const close = (end: number, semi: boolean) => {
    if (solid) {
      const raw = sql.slice(start, end);
      const from = start + (raw.length - raw.trimStart().length);
      const text = raw.trim();
      out.push({ text, from, to: from + text.length, end: semi ? end + 1 : end });
    }
    start = semi ? end + 1 : end;
    solid = false;
  };
  let i = 0;
  while (i < sql.length) {
    const c = sql[i];
    const two = sql.slice(i, i + 2);
    if (two === "--") {
      const nl = sql.indexOf("\n", i);
      i = nl < 0 ? sql.length : nl;
    } else if (two === "/*") {
      const end = sql.indexOf("*/", i + 2);
      i = end < 0 ? sql.length : end + 2;
    } else if (c === "'" || c === '"') {
      // A doubled quote inside is the quote itself.
      solid = true;
      i++;
      while (i < sql.length) {
        if (sql[i] === c) {
          if (sql[i + 1] === c) i++;
          else break;
        }
        i++;
      }
      i++;
    } else if (c === "$") {
      // $$ … $$ or $tag$ … $tag$: a function's body, semicolons and all.
      const tag = /^\$([A-Za-z_][A-Za-z0-9_]*)?\$/.exec(sql.slice(i, i + 66));
      solid = true;
      if (tag) {
        const end = sql.indexOf(tag[0], i + tag[0].length);
        i = end < 0 ? sql.length : end + tag[0].length;
      } else i++;
    } else if (c === ";") {
      close(i, true);
      i++;
    } else {
      if (!/\s/.test(c)) solid = true;
      i++;
    }
  }
  close(sql.length, false);
  return out;
}

// statementAt is what Run means. With text selected, it is the selection,
// whatever it holds. Otherwise it is the statement the cursor is in or
// touching; in the gap after a statement, the one just finished; before the
// first, the first.
export function statementAt(sql: string, selection: { from: number; to: number }): Statement | undefined {
  if (selection.to > selection.from) {
    const raw = sql.slice(selection.from, selection.to);
    const lead = raw.length - raw.trimStart().length;
    const text = raw.trim().replace(/;\s*$/, "").trimEnd();
    return text ? { text, from: selection.from + lead, to: selection.from + lead + text.length } : undefined;
  }
  const pieces = split(sql);
  if (pieces.length === 0) return undefined;
  const at = selection.from;
  let found = pieces[0];
  for (const p of pieces) {
    if (p.from > at) break;
    found = p;
  }
  return { text: found.text, from: found.from, to: found.to };
}

// markAt is where in the editor a refused statement stopped. The helper
// says where in what it was sent, as a character counted from 1 (an error's
// `position`); what was sent is the statement, or the statement after a
// word the console put in front of it (EXPLAIN). Undefined when the place
// is not in the statement itself.
export function markAt(statement: Statement, sent: string, position: number | undefined): number | undefined {
  if (position === undefined || position < 1) return undefined;
  const lead = sent.length - statement.text.length;
  const at = statement.from + (position - 1) - lead;
  return at >= statement.from && at <= statement.to ? at : undefined;
}

// A place in the editor in words: "line 2, column 15".
export function lineAndColumn(text: string, at: number): string {
  const before = text.slice(0, at).split("\n");
  return `line ${before.length}, column ${before[before.length - 1].length + 1}`;
}

// What the console remembers of a run: the statement and when. Never what
// it returned.
export interface Ran {
  sql: string;
  at: number;
}

export const HISTORY = 50;

// remember puts a statement at the top of the history, once: running it
// again moves it up.
export function remember(history: Ran[], sql: string, at: number, max = HISTORY): Ran[] {
  const text = sql.trim();
  if (!text) return history;
  return [{ sql: text, at }, ...history.filter((h) => h.sql !== text)].slice(0, max);
}

// A statement on one line, for a list.
export const oneLine = (sql: string, max = 90) => {
  const line = sql.replace(/\s+/g, " ").trim();
  return line.length > max ? `${line.slice(0, max)}…` : line;
};
