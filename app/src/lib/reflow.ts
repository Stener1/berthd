// reflowScreen turns lines an agent's terminal drew into Markdown that reads
// as prose: the terminal broke its sentences at its own width, so the lines
// of a paragraph join back up and wrap to wherever they are shown. List
// items and numbered options keep their own lines, an empty line still
// ends a paragraph, as does a line ending in a colon, and a table drawn
// with box lines stays as drawn. Pure, so the Review page and its tests
// share it.

const ITEM = /^(?:[❯›>]\s*)?([-*•]|\d+[.)])\s+(.*)$/;
const BOXED = /[│┃]/;

type Block = { kind: "p" | "list"; lines: string[] } | { kind: "drawn"; lines: string[] };

// GFM reads one ~ as strikethrough ("from ~180ms to ~45ms"); the terminal
// already drew whatever formatting there was, so a tilde is only a tilde.
const plain = (s: string) => s.replace(/(`+[^`]*`+)|~/g, (_, code?: string) => code ?? "\\~");

export function reflowScreen(lines: string[]): string {
  const blocks: Block[] = [];
  let open: Block | undefined;
  const close = () => {
    if (open) blocks.push(open);
    open = undefined;
  };
  for (const raw of lines) {
    const line = raw.trim();
    if (!line) {
      close();
      continue;
    }
    if (BOXED.test(line)) {
      if (open?.kind !== "drawn") close();
      open ??= { kind: "drawn", lines: [] };
      open.lines.push(raw.trimEnd());
      continue;
    }
    if (open?.kind === "drawn") close();
    const item = ITEM.exec(line);
    if (item) {
      if (open?.kind !== "list") close();
      open ??= { kind: "list", lines: [] };
      const marker = /\d/.test(item[1]) ? item[1].replace(")", ".") : "-";
      open.lines.push(`${marker} ${plain(item[2])}`);
      continue;
    }
    const last = open?.lines.at(-1);
    if (open && last !== undefined && !last.endsWith(":")) {
      open.lines[open.lines.length - 1] = `${last} ${plain(line)}`;
      continue;
    }
    // The first line, or the one after a line ending in a colon.
    close();
    open = { kind: "p", lines: [plain(line)] };
  }
  close();
  return blocks
    .map((b) => (b.kind === "drawn" ? `\`\`\`text\n${b.lines.join("\n")}\n\`\`\`` : b.lines.join("\n")))
    .join("\n\n");
}
