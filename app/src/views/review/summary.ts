import { meaningfulTail } from "@/lib/screen";

// lastMessage is what the agent said last: Claude Code starts each message
// with ●, so it is the text from the last ● to the input box. Other agents
// get the last few meaningful lines. Continuation lines lose the indent the
// terminal gave them; an empty line between paragraphs stays.
export function lastMessage(screen: string): string[] {
  const lines = meaningfulTail(screen, 60, { blanks: true });
  let start = -1;
  for (let i = lines.length - 1; i >= 0; i--) {
    if (/^\s*●\s/.test(lines[i])) {
      start = i;
      break;
    }
  }
  const msg = (start >= 0 ? lines.slice(start) : lines.slice(-8)).filter((l) => !/^\s*[✻✽✳✶*]\s+\S+ for \d.*·/.test(l)); // "✻ Brewed for 1s · done 7:14 PM"
  while (msg.at(-1)?.trim() === "") msg.pop();
  if (!msg.length) return [];
  const first = msg[0].replace(/^\s*●\s*/, "");
  const rest = msg.slice(1);
  // A selected option ("❯ 1. Yes") sits left of the text; it sets no indent.
  const indent = Math.min(...rest.filter((l) => l.trim() && !/^[❯›>]/.test(l)).map((l) => l.length - l.trimStart().length), Infinity);
  const dedent = (l: string) => (Number.isFinite(indent) && l.slice(0, indent).trim() === "" ? l.slice(indent) : l);
  return [first, ...rest.map(dedent)].map((l) => l.trimEnd());
}

const OPTION = /^\s*(?:[❯›>]\s*)?\d[.)]\s/;
const LIST = /^\s*(?:[-*•]|\d+[.)])\s/;

// commitMessage drafts a commit message from the agent's summary: its first
// sentence as the subject, the rest as the body, without the questions it
// asked or the options it offered. Without a summary, the branch name.
export function commitMessage(summary: string[], branch?: string): string {
  // The terminal wrapped the agent's sentences; join them back up, keeping
  // list items on their own lines.
  const lines: string[] = [];
  for (const l of summary) {
    if (OPTION.test(l) || !l.trim()) continue;
    const prev = lines.at(-1);
    if (prev !== undefined && !LIST.test(l) && !/[:]$/.test(prev)) lines[lines.length - 1] = `${prev} ${l.trim()}`;
    else lines.push(l.trim());
  }
  const text = lines
    .map((l) => l.replace(/[^.!?]*\?(?=\s|$)/g, "").replace(/\s+/g, " ").trim())
    .filter(Boolean)
    .join("\n");
  if (!text) return branch ? humanize(branch) : "";
  // The first sentence ends at . or ! followed by a space or the end.
  const m = /^(.{8,}?[.!])(?=\s|$)/s.exec(text);
  const first = m ? m[1] : text.split("\n")[0];
  const sentence = first.replace(/\s+/g, " ").trim();
  let subject = sentence.replace(/[.!]$/, "");
  if (subject.length > 72) {
    // Prefer ending at a clause before ellipsising.
    const comma = subject.lastIndexOf(", ", 72);
    subject = comma >= 24 ? subject.slice(0, comma) : `${subject.slice(0, 71).replace(/\s+\S*$/, "")}…`;
  }
  // A shortened subject leaves its whole sentence for the body.
  const rest = text.slice(first.length).trim();
  const body = subject === sentence.replace(/[.!]$/, "") ? rest : [sentence, rest].filter(Boolean).join("\n\n");
  return body ? `${subject}\n\n${body}` : subject;
}

function humanize(branch: string): string {
  const leaf = branch.split("/").pop() ?? branch;
  const words = leaf.replace(/[-_]+/g, " ").trim();
  return words.charAt(0).toUpperCase() + words.slice(1);
}
