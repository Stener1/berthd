import { Icon, Tip, cn } from "@berth/plugin/ui";
import type { ReactNode } from "react";

import { stripComments } from "./gh";

// A small GitHub-flavoured Markdown renderer for issue bodies and comments.
// It builds React elements and never sets HTML, so whatever an issue says is
// only ever text: raw HTML tags are dropped (their text kept), and only
// http(s) links become links, opened outside the app with onLink.

interface Ctx {
  repo: string;
  onLink(url: string): void;
}

export function Markdown({ source, repo, onLink, className }: { source: string; repo: string; onLink(url: string): void; className?: string }) {
  const ctx = { repo, onLink };
  return <div className={cn("flex flex-col gap-3 text-[13px] leading-relaxed [overflow-wrap:anywhere]", className)}>{blocks(clean(source), ctx)}</div>;
}

// clean drops comments and turns the little HTML GitHub bodies use into
// Markdown, or into nothing.
function clean(s: string) {
  return stripComments(s.replace(/\r\n?/g, "\n"))
    .replace(/<br\s*\/?>/gi, "\n")
    .replace(/<\/?(details|summary|p|div|span|sub|sup|kbd|picture|source|center)[^>]*>/gi, "")
    .replace(/<img\b[^>]*?(?:alt="([^"]*)")?[^>]*?src="([^"]+)"[^>]*>/gi, (_, alt: string | undefined, src: string) => `![${alt ?? "image"}](${src})`)
    .replace(/<\/?[a-z][^>]*>/gi, "");
}

const fence = /^\s{0,3}(`{3,}|~{3,})\s*([\w+-]*)/;
const heading = /^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/;
const rule = /^\s{0,3}([-*_])(\s*\1){2,}\s*$/;
const item = /^(\s*)([-*+]|\d+[.)])\s+(.*)$/;
const quoteLine = /^\s{0,3}>\s?(.*)$/;
const tableSep = /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/;

function blocks(src: string, ctx: Ctx): ReactNode[] {
  const lines = src.split("\n");
  const out: ReactNode[] = [];
  let i = 0;
  let key = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) {
      i++;
      continue;
    }
    const f = fence.exec(line);
    if (f) {
      const code: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trimStart().startsWith(f[1])) code.push(lines[i++]);
      i++;
      out.push(
        <pre key={key++} className="overflow-x-auto rounded-md border bg-muted/50 px-3 py-2 font-mono text-[12px] leading-normal">
          <code>{code.join("\n")}</code>
        </pre>,
      );
      continue;
    }
    const h = heading.exec(line);
    if (h) {
      const size = h[1].length <= 2 ? "text-[15px]" : "text-[13px]";
      out.push(
        <p key={key++} role="heading" aria-level={h[1].length} className={cn("font-semibold", size, h[1].length <= 2 && "border-b pb-1")}>
          {inline(h[2], ctx)}
        </p>,
      );
      i++;
      continue;
    }
    if (rule.test(line)) {
      out.push(<hr key={key++} className="border-border" />);
      i++;
      continue;
    }
    if (quoteLine.test(line)) {
      const q: string[] = [];
      while (i < lines.length && lines[i].trim() && quoteLine.test(lines[i])) q.push(quoteLine.exec(lines[i++])![1]);
      out.push(
        <blockquote key={key++} className="flex flex-col gap-2 border-l-2 pl-3 text-muted-foreground">
          {blocks(q.join("\n"), ctx)}
        </blockquote>,
      );
      continue;
    }
    if (line.includes("|") && i + 1 < lines.length && tableSep.test(lines[i + 1])) {
      const rows: string[][] = [cells(line)];
      i += 2;
      while (i < lines.length && lines[i].includes("|") && lines[i].trim()) rows.push(cells(lines[i++]));
      out.push(
        <div key={key++} className="overflow-x-auto">
          <table className="w-full border-collapse text-[12px]">
            <thead>
              <tr>
                {rows[0].map((c, j) => (
                  <th key={j} className="border px-2 py-1 text-left font-medium">
                    {inline(c, ctx)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.slice(1).map((r, ri) => (
                <tr key={ri}>
                  {r.map((c, j) => (
                    <td key={j} className="border px-2 py-1 align-top">
                      {inline(c, ctx)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>,
      );
      continue;
    }
    if (item.test(line)) {
      const items: { depth: number; ordered: boolean; text: string }[] = [];
      while (i < lines.length) {
        const m = item.exec(lines[i]);
        if (m) {
          items.push({ depth: Math.floor(m[1].replace(/\t/g, "  ").length / 2), ordered: /\d/.test(m[2]), text: m[3] });
          i++;
        } else if (lines[i].trim() && /^\s{2,}/.test(lines[i]) && items.length) {
          items[items.length - 1].text += ` ${lines[i++].trim()}`;
        } else break;
      }
      out.push(list(items, ctx, key++));
      continue;
    }
    const para: string[] = [];
    while (i < lines.length && lines[i].trim() && !fence.test(lines[i]) && !heading.test(lines[i]) && !quoteLine.test(lines[i]) && !(para.length && item.test(lines[i]))) para.push(lines[i++]);
    out.push(
      <p key={key++} className="whitespace-pre-line">
        {inline(para.join("\n"), ctx)}
      </p>,
    );
  }
  return out;
}

function cells(row: string) {
  return row
    .trim()
    .replace(/^\||\|$/g, "")
    .split("|")
    .map((c) => c.trim());
}

function list(items: { depth: number; ordered: boolean; text: string }[], ctx: Ctx, key: number) {
  const ordered = items[0]?.ordered;
  const Tag = ordered ? "ol" : "ul";
  return (
    <Tag key={key} className={cn("flex flex-col gap-1 pl-5", ordered ? "list-decimal" : "list-disc")}>
      {items.map((it, j) => {
        const task = /^\[([ xX])\]\s+(.*)$/.exec(it.text);
        return (
          <li key={j} style={{ marginLeft: `${it.depth * 1.1}rem` }} className={cn(task && "-ml-5 list-none")}>
            {task ? (
              <span className="flex items-start gap-2">
                <span className={cn("mt-[3px] grid size-3.5 shrink-0 place-items-center rounded-[4px] border", task[1] !== " " && "border-primary bg-primary text-primary-foreground")}>
                  {task[1] !== " " && <Icon name="Check" className="size-2.5" />}
                </span>
                <span className={cn(task[1] !== " " && "text-muted-foreground line-through decoration-muted-foreground/50")}>{inline(task[2], ctx)}</span>
              </span>
            ) : (
              inline(it.text, ctx)
            )}
          </li>
        );
      })}
    </Tag>
  );
}

// One pass over a line's inline syntax; the earliest match wins, and its
// inside is rendered again (bold inside a link, and so on).
const INLINE =
  /(`+)([^`][\s\S]*?)\1|!\[([^\]]*)\]\(([^)\s]+)[^)]*\)|\[([^\]]+)\]\(([^)\s]+)[^)]*\)|\*\*([^*][\s\S]*?)\*\*|__([^_][\s\S]*?)__|~~([\s\S]+?)~~|(?<![\w*])\*(?![\s*])([^*\n]+?)\*(?![\w*])|(?<![\w_])_(?![\s_])([^_\n]+?)_(?![\w_])|(https?:\/\/[^\s<>()[\]]+[^\s<>()[\].,;:!?'"])|(?<![\w/&#])#(\d+)\b|(?<![\w`/])@([A-Za-z0-9](?:[A-Za-z0-9-]{0,38}))\b/g;

const safe = (url: string) => /^https?:\/\//i.test(url);

function inline(text: string, ctx: Ctx): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  let key = 0;
  for (const m of text.matchAll(INLINE)) {
    const at = m.index ?? 0;
    if (at > last) out.push(text.slice(last, at));
    last = at + m[0].length;
    const k = key++;
    if (m[2] !== undefined) {
      out.push(
        <code key={k} className="rounded bg-muted px-1 py-px font-mono text-[12px]">
          {m[2].trim()}
        </code>,
      );
    } else if (m[4] !== undefined) {
      const url = m[4];
      out.push(safe(url) ? <Link key={k} url={url} ctx={ctx} icon="Image">{m[3] || "image"}</Link> : <span key={k}>{m[3]}</span>);
    } else if (m[6] !== undefined) {
      out.push(safe(m[6]) ? <Link key={k} url={m[6]} ctx={ctx}>{inline(m[5], ctx)}</Link> : <span key={k}>{inline(m[5], ctx)}</span>);
    } else if (m[7] !== undefined || m[8] !== undefined) {
      out.push(
        <strong key={k} className="font-semibold">
          {inline(m[7] ?? m[8], ctx)}
        </strong>,
      );
    } else if (m[9] !== undefined) {
      out.push(<s key={k}>{inline(m[9], ctx)}</s>);
    } else if (m[10] !== undefined || m[11] !== undefined) {
      out.push(<em key={k}>{inline(m[10] ?? m[11], ctx)}</em>);
    } else if (m[12] !== undefined) {
      out.push(
        <Link key={k} url={m[12]} ctx={ctx}>
          {shortUrl(m[12])}
        </Link>,
      );
    } else if (m[13] !== undefined) {
      // Only a GitHub issue's text has a repository for #12 to point into.
      out.push(
        ctx.repo ? (
          <Link key={k} url={`https://github.com/${ctx.repo}/issues/${m[13]}`} ctx={ctx}>
            #{m[13]}
          </Link>
        ) : (
          `#${m[13]}`
        ),
      );
    } else if (m[14] !== undefined) {
      out.push(
        <span key={k} className="font-medium text-foreground">
          @{m[14]}
        </span>,
      );
    }
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

// github.com/owner/repo/pull/12 reads as owner/repo#12, as GitHub shows it.
function shortUrl(url: string) {
  const m = /^https:\/\/github\.com\/([^/]+\/[^/]+)\/(?:issues|pull)\/(\d+)\/?$/.exec(url);
  return m ? `${m[1]}#${m[2]}` : url.replace(/^https?:\/\//, "");
}

function Link({ url, ctx, icon, children }: { url: string; ctx: Ctx; icon?: string; children: ReactNode }) {
  return (
    <Tip label={<span className="break-all font-mono">{url}</span>} className="max-w-md">
      <a
        href={url}
        onClick={(e) => {
          e.preventDefault();
          ctx.onLink(url);
        }}
        className="inline-flex items-baseline gap-1 text-info-foreground underline-offset-2 hover:underline"
      >
        {icon && <Icon name={icon} className="size-3 self-center" />}
        {children}
      </a>
    </Tip>
  );
}
