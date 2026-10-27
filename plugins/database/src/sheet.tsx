import { Badge, Button, Sheet, SheetDescription, SheetFooter, SheetHeader, SheetPanel, SheetPopup, SheetTitle, Textarea } from "@berth/plugin/ui";
import { useState } from "react";

import { type Cell, CUT_AT } from "./api";
import { editText, type Family, parseInput, pretty } from "./engines/families.ts";

// A cell's whole value, beside the grid: a long text, a JSON document, a
// value that arrived cut. Where the cell can be edited it is edited here
// too, with room for more than a line.

export interface Viewed {
  // The column, and the row in words ("id = 12").
  column: string;
  type: string;
  family: Family;
  row: string;
  cell: Cell;
  // Only its first 4,000 characters were read.
  cut?: boolean;
  nullable?: boolean;
  // Nothing was typed in a new row's cell yet.
  unset?: boolean;
  // Stages a new value; absent where the cell can't be edited.
  onStage?(value: Cell): void;
}

export function ValueSheet({ viewed, onClose }: { viewed?: Viewed; onClose(): void }) {
  // The value stays in the sheet while it closes.
  const [last, setLast] = useState(viewed);
  if (viewed && viewed !== last) setLast(viewed);
  const v = viewed ?? last;
  return (
    <Sheet open={!!viewed} onOpenChange={(open: boolean) => !open && onClose()}>
      <SheetPopup side="right" className="max-w-xl">
        {v && <Value key={`${v.row}\n${v.column}`} v={v} onClose={onClose} />}
      </SheetPopup>
    </Sheet>
  );
}

function Value({ v, onClose }: { v: Viewed; onClose(): void }) {
  const editable = !!v.onStage && !v.cut;
  const [text, setText] = useState(() => (v.unset ? "" : v.family === "json" && v.cell !== null ? pretty(v.cell, v.family) : editText(v.cell)));
  const [why, setWhy] = useState<string>();
  const [copied, setCopied] = useState(false);
  // JSON opens laid out: staged as it stands, that alone would be a change.
  const [opened] = useState(text);
  const stage = () => {
    if (text === opened && !v.unset && v.cell !== null) return onClose();
    const parsed = parseInput(text, v.family);
    if (!parsed.ok) return setWhy(parsed.why);
    v.onStage?.(parsed.value);
    onClose();
  };
  const size = typeof v.cell === "string" ? v.cell.length : undefined;
  return (
    <div className="flex min-h-0 flex-1 flex-col" data-testid="database-value">
      <SheetHeader className="gap-1.5 pb-3">
        <SheetTitle className="flex items-center gap-2 text-base">
          <span className="truncate font-mono">{v.column}</span>
          <Badge variant="outline" size="sm" className="shrink-0 font-mono font-normal">
            {v.type}
          </Badge>
        </SheetTitle>
        <SheetDescription className="text-[13px]">
          {v.row}
          {size !== undefined && !v.cut && ` · ${size.toLocaleString("en-US")} characters`}
        </SheetDescription>
      </SheetHeader>
      <SheetPanel className="flex min-h-0 flex-1 flex-col gap-3">
        {v.cut && (
          <p role="status" className="rounded-md bg-muted px-3 py-2 text-pretty text-foreground text-xs" data-testid="database-cut">
            Only the first {CUT_AT.toLocaleString("en-US")} characters were read: the value is longer. It can't be edited here, because saving would keep only this much. Read or change the rest from the console.
          </p>
        )}
        {editable ? (
          <>
            <Textarea
              aria-label={`${v.column}, ${v.type}`}
              aria-invalid={!!why}
              spellCheck={false}
              className="min-h-48 flex-1 font-mono text-xs"
              value={text}
              placeholder={v.cell === null || v.unset ? (v.unset ? "Nothing typed: the column's default" : "NULL") : v.family === "text" ? "The empty string" : undefined}
              onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => (setText(e.target.value), setWhy(undefined))}
            />
            {why && (
              <p role="alert" className="text-destructive-foreground text-xs">
                {why}
              </p>
            )}
            <p className="text-muted-foreground text-xs">{v.family === "text" ? "Left empty, this is the empty string. NULL is its own button." : "NULL is its own button: only text can be empty."}</p>
          </>
        ) : v.cell === null ? (
          <p className="rounded-lg border border-dashed px-3 py-6 text-center text-muted-foreground text-sm italic">{v.unset ? "Nothing typed: the column's default" : "NULL"}</p>
        ) : v.cell === "" ? (
          <p className="rounded-lg border border-dashed px-3 py-6 text-center text-muted-foreground text-sm italic">The empty string</p>
        ) : (
          // The sheet's panel scrolls it; nothing here takes the keyboard
          // first, so a long value opens at its top.
          <pre className="whitespace-pre-wrap break-words rounded-lg border bg-muted/40 p-3 font-mono text-xs">
            {pretty(v.cell, v.cut ? "other" : v.family)}
          </pre>
        )}
      </SheetPanel>
      <SheetFooter>
        {v.cell !== null && (
          <Button
            variant="ghost"
            className="mr-auto"
            onClick={() => {
              void navigator.clipboard?.writeText(editText(v.cell)).then(() => setCopied(true));
            }}
          >
            {copied ? "Copied" : "Copy"}
          </Button>
        )}
        {editable && v.nullable && (
          <Button
            variant="outline"
            onClick={() => {
              v.onStage?.(null);
              onClose();
            }}
          >
            Set NULL
          </Button>
        )}
        {editable ? (
          <Button onClick={stage}>Stage change</Button>
        ) : (
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        )}
      </SheetFooter>
    </div>
  );
}
