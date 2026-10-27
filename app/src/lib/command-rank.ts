import type { AgentCommand, CommandKind } from "@/lib/commands";

// The person's own come first: their commands and skills, then the agent's
// commands, then plugins'.
const KIND_ORDER: Record<CommandKind, number> = { custom: 0, skill: 1, builtin: 2, plugin: 3, mcp: 4 };

// rankCommands is the commands matching what follows the "/", best first: a
// name that is it, starts with it, has a word starting with it, contains it,
// has its words' initials ("fcc" for figma-code-connect) or its letters close
// together; an alias counts as the name. Without a query it is all of them,
// by kind: the list scrolls, and a cap there would cut whole kinds off its
// end (an agent has more commands of its own than the cap).
export function rankCommands(list: AgentCommand[], query: string, limit = 60): AgentCommand[] {
  const q = query.toLowerCase().replace(/^\//, "");
  const scored: { c: AgentCommand; s: number }[] = [];
  for (const c of list) {
    const names = [c.name, ...(c.aliases ?? [])].map((n) => n.slice(1).toLowerCase());
    let s = 0;
    if (!q) s = 1;
    else
      for (const n of names) {
        const v = n === q ? 100 : n.startsWith(q) ? 80 - Math.min(n.length, 30) / 2 : n.split(/[:\-_]/).some((w) => w.startsWith(q)) ? 55 : n.includes(q) ? 40 : initials(n).startsWith(q) ? 30 : close(n, q) ? 15 : 0;
        s = Math.max(s, v);
      }
    if (!s && q.length > 3 && c.description?.toLowerCase().includes(q)) s = 5;
    if (s) scored.push({ c, s });
  }
  scored.sort((a, b) => b.s - a.s || KIND_ORDER[a.c.kind] - KIND_ORDER[b.c.kind] || a.c.name.localeCompare(b.c.name));
  return (q ? scored.slice(0, limit) : scored).map((x) => x.c);
}

// initials is a name's words' first letters: "figma:figma-code-connect" is
// "ffcc".
const initials = (n: string) =>
  n
    .split(/[:\-_]/)
    .map((w) => w[0] ?? "")
    .join("");

// close says q's letters are in n in order, not spread over all of it.
function close(n: string, q: string): boolean {
  if (q.length < 2) return false;
  for (let start = n.indexOf(q[0]); start >= 0; start = n.indexOf(q[0], start + 1)) {
    let i = 1;
    let j = start + 1;
    for (; j < n.length && i < q.length; j++) if (n[j] === q[i]) i++;
    if (i === q.length && j - start <= q.length + 2) return true;
  }
  return false;
}
