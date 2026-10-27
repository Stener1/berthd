import type { Client } from "@/lib/api";

// Questions an agent asks with a form of its own: Claude Code's
// AskUserQuestion (one to four questions, each with its options, some
// taking several picks, each with room for the person's own words) and
// Codex's request_user_input. The box reads them from the agent's record
// (a "question" item) and, for Claude Code, fills in its form with keys
// from the whole answer set (POST …/answer, the "answer" capability),
// checking its screen after every key.

export interface Question {
  question: string;
  header?: string;
  // Takes several picks (checkboxes) rather than one (radios).
  multi?: boolean;
  options: { label: string; description?: string }[];
  // Codex's name for the question.
  id?: string;
}

// One question's answer: the options picked, by label, and the person's
// own words (Other).
export interface QuestionAnswer {
  picks: string[];
  other?: string;
}

export const answerQuestions = (c: Client, box: string, session: string, req: { tool: string; answers: QuestionAnswer[] }) =>
  c.box<{ answered: string[] }>(box, "POST", `sessions/${encodeURIComponent(session)}/answer`, req);

// shownAnswer is an answer as the agent shows it: the options picked, in
// their order, then the person's own words.
export function shownAnswer(q: Question, a: QuestionAnswer | undefined): string {
  if (!a) return "";
  const parts = q.options.filter((o) => a.picks.includes(o.label)).map((o) => o.label);
  const other = (a.other ?? "").replace(/\s+/g, " ").trim();
  if (other) parts.push(other);
  return parts.join(", ");
}

// complete says a question has its answer: one pick or the person's words
// for a single choice, at least one of either for several.
export function complete(q: Question, a: QuestionAnswer | undefined): boolean {
  if (!a) return false;
  const n = a.picks.length + ((a.other ?? "").trim() ? 1 : 0);
  return q.multi ? n > 0 : n === 1;
}

// answersText is every answer as plain text, one line each under its
// question's name, for the person to paste into the agent's own screen
// when the box couldn't fill in its form.
export function answersText(questions: Question[], answers: (QuestionAnswer | undefined)[]): string {
  return questions.map((q, i) => `${q.header || q.question.split("\n")[0]}: ${shownAnswer(q, answers[i]) || "—"}`).join("\n");
}
