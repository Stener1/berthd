// Team suggestions, as the app shows them (lib/team-suggest.ts): the laptop
// agent notices that an org a project comes from publishes a team setup
// (internal/agent/teamsuggest.go), and the app says so quietly in the
// sidebar, Project settings and once on Home. Kept apart so it tests
// without the app.

import type { TeamSuggestion, TeamSuggestProject } from "@berth/plugin";

export type { TeamSuggestion, TeamSuggestProject };

const low = (s: string) => s.toLowerCase();

// visibleSuggestions leaves out orgs whose setup this laptop accepted and
// those turned down a moment ago, before the agent says so itself.
export function visibleSuggestions(list: TeamSuggestion[] | undefined, accepted: string[], dismissed: string[]): TeamSuggestion[] {
  if (!list?.length) return [];
  const skip = new Set([...accepted, ...dismissed].map(low));
  const out = list.filter((s) => !skip.has(low(s.org)) && s.projects.length > 0);
  return out.length === list.length ? list : out;
}

// suggestionFor is the suggestion a project is part of, if any.
export function suggestionFor(list: TeamSuggestion[], box: string, location: string): { suggestion: TeamSuggestion; project: TeamSuggestProject } | undefined {
  for (const suggestion of list) {
    const project = suggestion.projects.find((p) => p.box === box && p.location === location);
    if (project) return { suggestion, project };
  }
  return undefined;
}

// SuggestPrefs is what this computer remembers: the orgs whose Home card
// was put away (Not now, or Review…). The sidebar and Project settings
// keep their hints.
export interface SuggestPrefs {
  seen: string[];
}

export function homeCards(list: TeamSuggestion[], prefs: SuggestPrefs): TeamSuggestion[] {
  const seen = new Set(prefs.seen.map(low));
  return list.filter((s) => !seen.has(low(s.org)));
}

export function putAway(prefs: SuggestPrefs, org: string): SuggestPrefs {
  return prefs.seen.some((o) => low(o) === low(org)) ? prefs : { seen: [...prefs.seen, low(org)] };
}

// sentence ends s with a full stop, unless it ends with one already (or
// with "Log in as…").
export const sentence = (s: string) => (/[.…!?]$/.test(s) ? s : `${s}.`);

// orgName is how the org is named in a sentence: its setup's own name.
export const orgName = (s: Pick<TeamSuggestion, "org" | "name">) => s.name || s.org;

// teamLink is the link that opens an org's team setup in Shipyard.
export const teamLink = (org: string) => `berth://team?org=${encodeURIComponent(org)}`;

// setsUpLine is Project settings' line: what the setup brings this
// project, from what it declares for the repo, else what it is.
export function setsUpLine(s: TeamSuggestion, p: TeamSuggestProject): string {
  if (p.listed && p.sets_up.length) return `${s.repo} sets this project up: ${p.sets_up.join(", ")}`;
  if (p.listed) return `${s.repo} lists this project, and sets your box up the way ${orgName(s)}'s are`;
  return `${s.repo} doesn't list this repo, but sets your box up the way ${orgName(s)}'s are`;
}

// projectsLine names a suggestion's projects: "shop on devl", "shop and web
// on devl", "shop on devl and web on gpu", "shop, web and 2 more".
export function projectsLine(s: TeamSuggestion): string {
  const boxes = new Set(s.projects.map((p) => p.box));
  const names = s.projects.map((p) => (boxes.size > 1 ? `${p.location} on ${p.box}` : p.location));
  const shown = names.length > 3 ? [...names.slice(0, 2), `${names.length - 2} more`] : names;
  const list = shown.length > 1 ? `${shown.slice(0, -1).join(", ")} and ${shown[shown.length - 1]}` : shown[0];
  return boxes.size === 1 ? `${list} on ${[...boxes][0]}` : list;
}

// reviewBox is the box the Team setup page opens on: the project's own,
// so the page finds the clone there and uses it ("Use this").
export function reviewBox(s: TeamSuggestion, box?: string): string | undefined {
  if (box && s.projects.some((p) => p.box === box)) return box;
  const counts = new Map<string, number>();
  for (const p of s.projects) counts.set(p.box, (counts.get(p.box) ?? 0) + 1);
  return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))[0]?.[0];
}
