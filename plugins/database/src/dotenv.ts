import type { HelperEnvLook } from "@berth/plugin";

import type { Fault } from "./api";

// A worktree's database, found in its own .env files.
//
// The helper learns of a database when Shipyard starts one of the worktree's
// services. A project whose dev servers Shipyard does not start never gets
// there: its DATABASE_URL is in the worktree's .env, which the dev server
// loads itself. So when the tab opens and the helper knows no database for
// the worktree, the panel has the box look in those files
// (berth.helper.attachEnv) and tell the helper what it found. This file is
// the rules of that, with no React in them: what a look's answer means, and
// what to say of one that found no database to show.

// The files the box reads, in the order it reads them: a later one wins.
export const FILES = [".env", ".env.development", ".env.local", ".env.development.local"];

// What berth-database answers to being told of a worktree: the name it files
// the worktree under, and the ids of the databases it took.
export interface Told {
  source?: string;
  connections?: string[];
}

export type Look = HelperEnvLook<Told>;

// How a look went: it was made; it can't be, on this box or in this
// Shipyard, and the panel is as it was before there were looks; or it was
// tried and went wrong.
export type Looked = { kind: "looked"; look: Look } | { kind: "old" } | { kind: "failed"; message: string };

// A box whose berthd is from before it could look says it has no such route
// ("box_outdated", a 404); a helper that is not told of worktrees at all
// says "helper_no_attach". Neither is something that went wrong.
export const cannotLook = (f: Fault) => f.code === "box_outdated" || f.code === "helper_no_attach" || f.status === 404 || f.status === 405;

// What is kept of a look that found a database: the connections it gave,
// and the file each variable was read from. It is how the panel can say,
// the next time it opens, that a connection came from the worktree's .env:
// the helper lists a connection by its variable, and not by who said so.
export interface Origin {
  ids: string[];
  files: Record<string, string>;
}

export function originOf(look: Look): Origin | undefined {
  const ids = (look.answer?.connections ?? []).filter((id): id is string => typeof id === "string");
  if (ids.length === 0) return undefined;
  const files: Record<string, string> = {};
  for (const v of look.variables ?? []) if (v.sent) files[v.name] = v.file;
  return { ids, files };
}

// fromFile is the .env file a connection was read from, when the last look
// found it there.
export function fromFile(origin: Origin | null | undefined, conn: { id: string; label: string }): string | undefined {
  if (!origin?.ids?.includes(conn.id)) return undefined;
  return origin.files?.[conn.label] ?? FILES[0];
}

// lookAgain says whether reading the tables again should look in the .env
// files again too: when the worktree has no database, has one the files
// gave, or has only what was connected by hand. One that a service gave is
// the service's: the files are not asked, so a .env the service's
// environment overrides adds nothing here.
export function lookAgain(origin: Origin | null | undefined, conns: { id: string }[], byHand: string[] = []): boolean {
  return conns.every((c) => byHand.includes(c.id)) || conns.some((c) => !!origin?.ids?.includes(c.id));
}

// ---- A database connected by hand ----
//
// When nothing says which database a worktree has, the person can: they
// paste its address, the box hands it to the helper (the same route, with
// ".manual" where a look says ".env"), and the helper keeps it as it keeps
// the others. One for a worktree: giving another replaces it, and giving
// none forgets it. The address is a secret. It is in the dialog's field
// until it is sent, and nowhere in the plugin after that: what is kept here
// is the id of the connection it became.

// The variable a pasted address is given as. The helper is only told the
// variables the manifest names, and labels a connection by its variable.
export const BY_HAND_VARIABLE = "DATABASE_URL";

// What the box answers "command" with for a database given by hand. A box
// that answers another did something else with the request: its berthd is
// from before it took an address.
export const BY_HAND = ".manual";

// What the helper opens, in words, for where the address is asked for.
export const EXAMPLE = "postgres://user:password@localhost:5432/database";

// addressProblem is what is plainly wrong with what was typed as an
// address, before it is sent anywhere: nothing, or not a Postgres address at
// all. Whether it is a database on the box is the helper's to say; this
// only saves sending something that could never be one. It never repeats
// what was typed.
export function addressProblem(text: string): string | undefined {
  const address = text.trim();
  if (!address) return "Paste the database's address.";
  if (/\s/.test(address)) return "An address has no spaces or line breaks in it. Paste the one address alone.";
  if (!/^(jdbc:)?postgres(ql)?(\+[a-z0-9]+)?:\/\//i.test(address)) return `That isn't a Postgres address. One looks like ${EXAMPLE}.`;
  return undefined;
}

// connected is the connection a given address became, by the box's answer:
// its id, or why there is none. "old" is a box that cannot take an address.
export function connected(look: Look): { id: string } | { refused: true } | { old: true } {
  if (look.command !== BY_HAND) return { old: true };
  const id = (look.answer?.connections ?? []).find((x): x is string => typeof x === "string");
  return id ? { id } : { refused: true };
}

// list writes names as words do: "a", "a and b", "a, b and c".
export function list(names: string[]): string {
  if (names.length < 2) return names.join("");
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

const SKIPPED: Record<string, string> = {
  outside: "is a link to a file outside the worktree, which isn't followed",
  not_a_file: "isn't a plain file",
  too_large: "is larger than 256 KB, so it wasn't read",
  not_text: "isn't text",
  unreadable: "couldn't be read",
};

// said is what a look that found no database to show found instead, as
// sentences: the files it read, and for each variable that is there, why it
// is not a database shown here. Never a value: the box sends none.
export function said(look: Look, box: string): string[] {
  const out: string[] = [];
  const files = look.files ?? [];
  const skipped = look.skipped ?? [];
  const variables = look.variables ?? [];
  if (files.length === 0 && skipped.length === 0) {
    out.push(`Looked for ${list(FILES)} in this worktree on ${box}: there is none.`);
  } else if (files.length > 0 && variables.length === 0) {
    out.push(`Looked in ${list(files)}: no DATABASE_URL or POSTGRES_URL in ${files.length === 1 ? "it" : "them"}.`);
  }
  for (const v of variables) {
    if (v.sent) out.push(`${v.name} in ${v.file} doesn't point at a Postgres on ${box} itself, so it isn't opened.`);
    else if (v.reason === "secret") out.push(`${v.name} in ${v.file} is a reference to a secret, not an address, so it isn't read.`);
    else if (v.reason === "unresolved") out.push(`${v.name} in ${v.file} refers to a variable that these files don't set.`);
    else if (v.reason === "empty") out.push(`${v.name} in ${v.file} is empty.`);
    else out.push(`${v.name} in ${v.file} is too long to be an address.`);
  }
  for (const s of skipped) out.push(`${s.file} ${SKIPPED[s.reason] ?? SKIPPED.unreadable}.`);
  return out;
}
