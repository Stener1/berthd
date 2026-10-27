// A small SQL editor for plugins (@berth/plugin/ui's SqlEditor): CodeMirror
// in the File tab's type and colours, with SQL highlighting, completion of
// tables and columns from a schema the plugin gives, and a key to run. It is
// its own chunk, loaded the first time a plugin draws one, so a plugin gets
// an editor without carrying CodeMirror in its bundle.
import { autocompletion, closeCompletion } from "@codemirror/autocomplete";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { Cassandra, MariaSQL, MSSQL, MySQL, PLSQL, PostgreSQL, type SQLDialect, SQLite, sql, StandardSQL } from "@codemirror/lang-sql";
import { bracketMatching, indentOnInput, syntaxHighlighting } from "@codemirror/language";
import { Compartment, EditorState, type Extension, Prec, StateEffect, StateField } from "@codemirror/state";
import { Decoration, type DecorationSet, drawSelection, EditorView, keymap, placeholder as placeholderOf } from "@codemirror/view";
import { type CSSProperties, useEffect, useMemo, useRef } from "react";

import { chrome, highlight, useSyntaxVars } from "@/components/files/code-editor";
import { useActiveTheme } from "@/hooks/use-theme";
import { patchText } from "@/lib/file-marks";

export interface SqlRange {
  from: number;
  to: number;
}

export interface SqlEditorProps {
  value: string;
  onChange(value: string): void;
  // ⌘↵ (Ctrl+↵ elsewhere): the selection as it is then, from === to for a cursor.
  onRun?(selection: SqlRange): void;
  // The selection whenever it moves, for a Run button outside the editor.
  onSelect?(selection: SqlRange): void;
  // What to complete: each table (or "schema.table") and its columns.
  schema?: Record<string, string[]>;
  // The schema whose tables are completed without its name.
  defaultSchema?: string;
  // "postgresql", "mysql", "mariadb", "sqlite", "mssql", "plsql",
  // "cassandra"; anything else is standard SQL.
  dialect?: string;
  // Where a statement failed: underlined from `from` to `to`, or to the end
  // of the word there.
  error?: { from: number; to?: number };
  // What a screen reader calls the editor.
  label?: string;
  placeholder?: string;
  readOnly?: boolean;
  className?: string;
}

const DIALECTS: Record<string, SQLDialect> = {
  postgresql: PostgreSQL,
  postgres: PostgreSQL,
  mysql: MySQL,
  mariadb: MariaSQL,
  sqlite: SQLite,
  mssql: MSSQL,
  plsql: PLSQL,
  cassandra: Cassandra,
};

const setError = StateEffect.define<{ from: number; to?: number } | undefined>();

const errorMark = Decoration.mark({ class: "cm-sql-error" });

const errorField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(marks, tr) {
    for (const e of tr.effects) {
      if (!e.is(setError)) continue;
      const at = e.value;
      const size = tr.state.doc.length;
      if (!at || at.from < 0 || at.from > size) return Decoration.none;
      let to = at.to;
      if (to === undefined) {
        // The word the database stopped at, and the rest of a dotted name
        // with it: for o.statuss or analytics.page_views a database points
        // at where the whole name starts, and one letter underlined is easy
        // to miss.
        to = tr.state.wordAt(at.from)?.to ?? at.from + 1;
        while (to + 1 < size && tr.state.sliceDoc(to, to + 1) === ".") {
          const next = tr.state.wordAt(to + 1);
          if (!next || next.from !== to + 1) break;
          to = next.to;
        }
      }
      to = Math.min(size, Math.max(to, at.from + 1));
      return at.from >= to ? Decoration.none : Decoration.set([errorMark.range(at.from, to)]);
    }
    // The mark is of the text as it was run: typing takes it away.
    return tr.docChanged ? Decoration.none : marks;
  },
  provide: (f) => EditorView.decorations.from(f),
});

// The console's own chrome over the File tab's: no gutter, less room under
// the last line, and the completion list in the app's popover colours.
const consoleChrome = EditorView.theme({
  ".cm-content": { padding: "8px 0 16px" },
  ".cm-line": { padding: "0 12px" },
  ".cm-placeholder": { color: "var(--muted-foreground)" },
  ".cm-sql-error": { textDecoration: "underline wavy var(--destructive)", textDecorationSkipInk: "none", textUnderlineOffset: "3px" },
  ".cm-tooltip": { border: "1px solid var(--border)", borderRadius: "8px", backgroundColor: "var(--popover)", color: "var(--popover-foreground)", boxShadow: "0 4px 16px rgb(0 0 0 / 0.12)", overflow: "hidden" },
  ".cm-tooltip.cm-tooltip-autocomplete > ul": { fontFamily: "var(--font-mono)", fontSize: "12px", maxHeight: "14em" },
  ".cm-tooltip.cm-tooltip-autocomplete > ul > li": { padding: "2px 8px", lineHeight: "1.6" },
  ".cm-tooltip-autocomplete ul li[aria-selected]": { backgroundColor: "var(--accent)", color: "var(--accent-foreground)" },
  ".cm-completionIcon": { display: "none" },
  ".cm-completionDetail": { color: "var(--muted-foreground)", fontStyle: "normal", marginLeft: "1em" },
  ".cm-completionMatchedText": { textDecoration: "none", fontWeight: "600" },
});

const languageOf = (p: Pick<SqlEditorProps, "schema" | "defaultSchema" | "dialect">) => sql({ dialect: DIALECTS[(p.dialect ?? "").toLowerCase()] ?? StandardSQL, schema: p.schema, defaultSchema: p.defaultSchema, upperCaseKeywords: true });

export function SqlEditor({ value, onChange, onRun, onSelect, schema, defaultSchema, dialect, error, label = "SQL", placeholder, readOnly, className }: SqlEditorProps) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const cb = useRef({ onChange, onRun, onSelect });
  cb.current = { onChange, onRun, onSelect };
  const lang = useRef(new Compartment());
  const theme = useActiveTheme();
  const syntax = useSyntaxVars(theme);

  useEffect(() => {
    if (!host.current) return;
    const run = (v: EditorView) => {
      // The list of completions is about what was being typed, not what ran.
      closeCompletion(v);
      const s = v.state.selection.main;
      cb.current.onRun?.({ from: s.from, to: s.to });
      return true;
    };
    const v = new EditorView({
      parent: host.current,
      state: EditorState.create({
        doc: value,
        extensions: [
          // Before the default keymap, where ⌘↵ opens a line.
          Prec.highest(keymap.of([{ key: "Mod-Enter", preventDefault: true, run }, { key: "Ctrl-Enter", preventDefault: true, run }])),
          errorField,
          history(),
          drawSelection(),
          indentOnInput(),
          bracketMatching(),
          autocompletion({ icons: false }),
          syntaxHighlighting(highlight),
          lang.current.of(languageOf({ schema, defaultSchema, dialect })),
          EditorView.lineWrapping,
          // Before the File tab's, so its rules win where both speak.
          consoleChrome,
          chrome,
          placeholder ? placeholderOf(placeholder) : [],
          EditorState.readOnly.of(!!readOnly),
          EditorView.editable.of(!readOnly),
          // Tab is left alone, so it still leaves the editor.
          EditorView.contentAttributes.of({ "aria-label": label, tabindex: "0" }),
          keymap.of([...defaultKeymap, ...historyKeymap]),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) cb.current.onChange(u.state.doc.toString());
            if (u.selectionSet || u.docChanged) {
              const s = u.state.selection.main;
              cb.current.onSelect?.({ from: s.from, to: s.to });
            }
          }),
        ] satisfies Extension[],
      }),
    });
    view.current = v;
    return () => {
      v.destroy();
      view.current = null;
    };
    // One editor for the component's life; what changes follows by transaction.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [readOnly, label, placeholder]);

  // Text from outside (a statement from the history): only what differs is
  // replaced, the cursor goes to its end, and the editor takes the keyboard,
  // so it can be run or changed at once.
  useEffect(() => {
    const v = view.current;
    if (!v) return;
    const p = patchText(v.state.doc.toString(), value);
    if (!p) return;
    v.dispatch({ changes: p, selection: { anchor: value.length } });
    v.focus();
  }, [value]);

  const names = useMemo(() => JSON.stringify([schema, defaultSchema, dialect]), [schema, defaultSchema, dialect]);
  useEffect(() => {
    view.current?.dispatch({ effects: lang.current.reconfigure(languageOf({ schema, defaultSchema, dialect })) });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [names]);

  useEffect(() => {
    view.current?.dispatch({ effects: setError.of(error) });
  }, [error?.from, error?.to]);

  const vars = useMemo(
    () =>
      ({
        ...syntax,
        "--ed-selection": theme.terminal.selectionBackground ?? "color-mix(in oklab, var(--ring) 40%, transparent)",
        "--ed-font": "12.5px",
      }) as CSSProperties,
    [syntax, theme],
  );

  return <div ref={host} className={className} style={vars} data-testid="sql-editor" />;
}

export default SqlEditor;
