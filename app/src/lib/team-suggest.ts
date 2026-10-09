import { useMemo } from "react";
import { create } from "zustand";

import { toastManager } from "@/components/ui/toast";
import { errorMessage } from "@/lib/format";
import { load, save } from "@/lib/storage";
import { useStore } from "@/lib/store";
import { useTeam } from "@/lib/team";
import { homeCards, orgName, putAway, reviewBox, type SuggestPrefs, suggestionFor, type TeamSuggestion, teamLink, visibleSuggestions } from "@/lib/team-suggest-model";

// Team suggestions in the app: the laptop agent lists, in its status, the
// orgs of the person's projects that publish a team setup they haven't
// accepted (internal/agent/teamsuggest.go). The app mentions them quietly:
// a mark on the project's sidebar row and a line in its menu, a line in
// Project settings, ⌘K, and a card on Home until Not now. Nothing opens by
// itself; Review… opens the Team setup page on the project's box, where the
// project's own clone is used as it is ("Use this").

const KEY = "berth.teamSuggest";

interface SuggestState extends SuggestPrefs {
  // Orgs turned down here, hidden before the agent's status says so.
  dismissed: string[];
}

export const useSuggest = create<SuggestState>(() => ({ seen: load<SuggestPrefs>(KEY, { seen: [] }).seen ?? [], dismissed: [] }));

const NONE: TeamSuggestion[] = [];

function visible(list: TeamSuggestion[] | undefined, accepted: { org: string }[], dismissed: string[]) {
  return visibleSuggestions(list ?? NONE, accepted.map((a) => a.org), dismissed);
}

// useTeamSuggestions is what to suggest now.
export function useTeamSuggestions(): TeamSuggestion[] {
  const list = useStore((s) => s.status?.team_suggestions);
  const accepted = useTeam((s) => s.accepted);
  const dismissed = useSuggest((s) => s.dismissed);
  return useMemo(() => visible(list, accepted, dismissed), [list, accepted, dismissed]);
}

// currentSuggestions is the same, for menus and the palette, which are
// worked out as they open.
export function currentSuggestions(): TeamSuggestion[] {
  return visible(useStore.getState().status?.team_suggestions, useTeam.getState().accepted, useSuggest.getState().dismissed);
}

export function useProjectSuggestion(box: string, location: string) {
  const list = useTeamSuggestions();
  return useMemo(() => suggestionFor(list, box, location), [list, box, location]);
}

export function projectSuggestion(box: string, location: string) {
  return suggestionFor(currentSuggestions(), box, location);
}

export function useHomeSuggestions(): TeamSuggestion[] {
  const list = useTeamSuggestions();
  const seen = useSuggest((s) => s.seen);
  return useMemo(() => homeCards(list, { seen }), [list, seen]);
}

function rememberSeen(org: string) {
  const next = putAway({ seen: useSuggest.getState().seen }, org);
  useSuggest.setState({ seen: next.seen });
  save(KEY, next);
}

// notNow puts the Home card away; the sidebar and Project settings keep
// their hints.
export function notNow(org: string) {
  rememberSeen(org);
}

// reviewSuggestion opens the org's Team setup page on the project's box
// (or the box most of its projects are on). The Home card has done its job.
export function reviewSuggestion(s: TeamSuggestion, box?: string) {
  rememberSeen(s.org);
  useStore.getState().setView({ kind: "team", org: s.org, box: reviewBox(s, box), from: "suggestion" });
}

// dismissSuggestion is "Don't suggest again for acme" (and Project
// settings' Not for me): every hint for the org goes, on every surface,
// and the agent stops asking GitHub about it.
export async function dismissSuggestion(s: TeamSuggestion) {
  const client = useStore.getState().client;
  if (!client) return;
  const org = s.org.toLowerCase();
  useSuggest.setState((st) => ({ dismissed: [...st.dismissed, org] }));
  const undo = () => useSuggest.setState((st) => ({ dismissed: st.dismissed.filter((o) => o !== org) }));
  try {
    await client.laptop("POST", `/v1/team-suggestions/${encodeURIComponent(s.org)}/dismiss`);
    void useStore.getState().refreshStatus();
    toastManager.add({
      title: `No more suggestions for ${orgName(s)}`,
      description: "Its team setup is still a ⌘K away: Team setup…",
      actionProps: {
        children: "Undo",
        onClick: async () => {
          try {
            await client.laptop("DELETE", `/v1/team-suggestions/${encodeURIComponent(s.org)}/dismiss`);
            undo();
            void useStore.getState().refreshStatus();
          } catch (err) {
            toastManager.add({ type: "error", title: "Couldn't undo that", description: errorMessage(err) });
          }
        },
      },
    });
  } catch (err) {
    undo();
    toastManager.add({ type: "error", title: `Couldn't turn off suggestions for ${orgName(s)}`, description: errorMessage(err) });
  }
}

// copyTeamLink copies the link that opens an org's team setup, to send a
// teammate.
export function copyTeamLink(org: string) {
  const link = teamLink(org);
  void navigator.clipboard.writeText(link);
  toastManager.add({ type: "success", title: "Copied the team setup link", description: link });
}
