// The card format: everything Needs me draws comes through this one shape, a
// card's kind, title, why, blocks and actions. The catalog is the safety
// model. A block can only be one of the types below, agent text is drawn as
// text, and an action is a verb from a fixed list that the app maps to what it
// does, so a card can't bring a URL or a command of its own for a button.
//
// Validation is strict: a key the format doesn't have is an error, not ignored,
// so a button with a URL tucked in is refused rather than quietly drawn without
// it. Shipyard builds every card from known data for now; workers composing
// their own (at the stop gate) go through this same door.
//
// The shape stays close to A2UI's, flat and whole, so a later move stays cheap.

import type { Question } from "./questions.ts";

const CARD_KINDS = ["decision", "task", "result", "close-out", "group", "plan", "pr-approval", "credentials", "ui-edit"] as const;
type CardKind = (typeof CARD_KINDS)[number];

const VERBS = ["answer", "reply", "done", "approve", "ask-for-changes", "read", "not-now", "waiting-on", "close", "pin", "park", "still-needed", "accept", "reject"] as const;
export type Verb = (typeof VERBS)[number];

export type Block =
  | { type: "text"; text: string }
  // The agent's own form (tool and questions), or the answers its screen
  // offers by the key that picks each (choices).
  | { type: "question"; tool?: string; questions?: Question[]; choices?: { key: string; label: string; title?: string }[] }
  | { type: "table"; columns: string[]; rows: string[][] }
  | { type: "chart"; title?: string; labels: string[]; series: { name: string; values: number[] }[] }
  | { type: "image"; src: string; alt: string }
  | { type: "before-after"; before: string; after: string }
  | { type: "diff-summary"; files: { path: string; added: number; removed: number }[] }
  | { type: "checks"; items: { name: string; state: "passed" | "failed" | "pending" }[] }
  | { type: "checklist"; items: { text: string; done?: boolean }[] }
  | { type: "command"; command: string; label?: string }
  | { type: "secret"; name: string; why?: string }
  | { type: "link"; url: string; label?: string }
  // Read-only: drawn in a sandbox with no network and no way into Shipyard.
  | { type: "page"; html: string; title?: string };

type BlockType = Block["type"];

export interface Card {
  kind: CardKind;
  title: string;
  why?: string;
  blocks: Block[];
  actions: Verb[];
}

type Validated = { ok: true; card: Card } | { ok: false; errors: string[] };

export const TITLE_MAX = 200;
const WHY_MAX = 500;
const TEXT_MAX = 20_000;
const LIST_MAX = 100;

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);
const isString = (v: unknown): v is string => typeof v === "string";
const isNumber = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v);

// hostOf is the host a link really goes to, shown beside its label so a label
// can't pretend. undefined for anything but http and https.
export function hostOf(url: string): string | undefined {
  try {
    const u = new URL(url);
    return u.protocol === "http:" || u.protocol === "https:" ? u.host : undefined;
  } catch {
    return undefined;
  }
}

// An image comes from https or is carried in the card; never http, or a
// script's scheme.
const safeImage = (src: string) => src.startsWith("https://") || /^data:image\/(png|jpeg|gif|webp);base64,/.test(src);

// Each block's own keys, and what each must be. A key not listed is an error.
type Rule = { required?: boolean; check(v: unknown): boolean; says: string };
const req = (check: Rule["check"], says: string): Rule => ({ required: true, check, says });
const opt = (check: Rule["check"], says: string): Rule => ({ check, says });

const aString = req(isString, "a string");
const maybeString = opt(isString, "a string");
const text = (max = TEXT_MAX) => (v: unknown) => isString(v) && v.length <= max;
const list = (item: (v: unknown) => boolean) => (v: unknown) => Array.isArray(v) && v.length <= LIST_MAX && v.every(item);
const shape = (rules: Record<string, Rule>) => (v: unknown) => isRecord(v) && objectErrors(v, rules, "").length === 0;

const question = shape({
  question: req(text(), "a string"),
  header: maybeString,
  multi: opt((v) => typeof v === "boolean", "true or false"),
  id: maybeString,
  options: req(list(shape({ label: aString, description: maybeString })), "a list of options"),
});

const BLOCKS: Record<BlockType, Record<string, Rule>> = {
  text: { text: req(text(), "a string") },
  question: {
    tool: maybeString,
    questions: opt(list(question), "a list of questions"),
    choices: opt(list(shape({ key: aString, label: aString, title: maybeString })), "a list of key and label"),
  },
  table: { columns: req(list(isString), "a list of strings"), rows: req(list(list(isString)), "a list of rows of strings") },
  chart: {
    title: maybeString,
    labels: req(list(isString), "a list of strings"),
    series: req(list(shape({ name: aString, values: req(list(isNumber), "a list of numbers") })), "a list of series"),
  },
  image: { src: req((v) => isString(v) && safeImage(v), "an https or data:image address"), alt: aString },
  "before-after": { before: req(text(), "a string"), after: req(text(), "a string") },
  "diff-summary": { files: req(list(shape({ path: aString, added: req(isNumber, "a number"), removed: req(isNumber, "a number") })), "a list of files") },
  checks: { items: req(list(shape({ name: aString, state: req((v) => v === "passed" || v === "failed" || v === "pending", "passed, failed or pending") })), "a list of checks") },
  checklist: { items: req(list(shape({ text: aString, done: opt((v) => typeof v === "boolean", "true or false") })), "a list of steps") },
  command: { command: req(text(4000), "a string"), label: maybeString },
  secret: { name: aString, why: maybeString },
  link: { url: req((v) => isString(v) && !!hostOf(v), "an http or https address"), label: maybeString },
  page: { html: aString, title: maybeString },
};

function objectErrors(o: Record<string, unknown>, rules: Record<string, Rule>, at: string): string[] {
  const errors: string[] = [];
  for (const k of Object.keys(o)) {
    if (k !== "type" && !rules[k]) errors.push(`${at}${k}: the format has no such field`);
  }
  for (const [k, r] of Object.entries(rules)) {
    if (o[k] === undefined) {
      if (r.required) errors.push(`${at}${k}: is required`);
    } else if (!r.check(o[k])) {
      errors.push(`${at}${k}: must be ${r.says}`);
    }
  }
  return errors;
}

function blockErrors(b: unknown, i: number): string[] {
  const at = `blocks[${i}].`;
  if (!isRecord(b)) return [`blocks[${i}]: must be an object`];
  const rules = isString(b.type) && Object.hasOwn(BLOCKS, b.type) ? BLOCKS[b.type as BlockType] : undefined;
  if (!rules) return [`${at}type: ${String(b.type)} isn't a block the format has`];
  const errors = objectErrors(b, rules, at);
  if (b.type === "question" && !b.questions && !b.choices) errors.push(`${at}questions: a question block needs questions or choices`);
  if (b.type === "question" && b.questions && !b.tool) errors.push(`${at}tool: a form says which call it answers`);
  return errors;
}

const CARD_KEYS = new Set(["kind", "title", "why", "blocks", "actions"]);

// validateCard says whether v is a card, and why not. It never throws, whatever
// it is given, because in the end a worker writes this JSON.
export function validateCard(v: unknown): Validated {
  if (!isRecord(v)) return { ok: false, errors: ["a card must be an object"] };
  const errors: string[] = [];
  for (const k of Object.keys(v)) if (!CARD_KEYS.has(k)) errors.push(`${k}: the format has no such field`);
  if (!CARD_KINDS.includes(v.kind as CardKind)) errors.push(`kind: ${String(v.kind)} isn't a kind of card`);
  if (!isString(v.title) || !v.title.trim()) errors.push("title: is required");
  else if (v.title.length > TITLE_MAX) errors.push(`title: is longer than ${TITLE_MAX} characters`);
  if (v.why !== undefined && (!isString(v.why) || v.why.length > WHY_MAX)) errors.push(`why: must be a string of at most ${WHY_MAX} characters`);
  if (!Array.isArray(v.blocks)) errors.push("blocks: must be a list");
  else v.blocks.forEach((b, i) => errors.push(...blockErrors(b, i)));
  if (!Array.isArray(v.actions)) errors.push("actions: must be a list");
  else
    v.actions.forEach((a, i) => {
      // The one rule the whole format turns on: a button is a verb.
      if (!isString(a)) errors.push(`actions[${i}]: an action is one of the fixed verbs, so it can't carry a URL or a command`);
      else if (!VERBS.includes(a as Verb)) errors.push(`actions[${i}]: ${a} isn't one of the fixed verbs`);
    });
  return errors.length ? { ok: false, errors } : { ok: true, card: v as unknown as Card };
}
