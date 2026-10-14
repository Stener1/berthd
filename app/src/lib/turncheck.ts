import type { Client } from "@/lib/api";

// Turn check (see internal/box/turncheck.go): when on, a box asks the
// decision model you chose whether a finished turn needs you: Jev on Vercel
// AI Gateway, OpenAI's Decisions API, or Laya on a server you run. A pasted
// key is kept on the box, one per provider, and never comes back; key is
// only ever a secret reference. Only a paired laptop reads or sets it.

export type TurnCheckProvider = "gateway" | "openai" | "laya";

export interface TurnCheckConfig {
  enabled: boolean;
  provider?: TurnCheckProvider;
  key?: string;
  url?: string;
  model?: string;
}

export interface TurnCheckStatus extends TurnCheckConfig {
  // Whether a key is kept for the chosen provider.
  key_set: boolean;
}

export interface TurnCheckChange extends TurnCheckConfig {
  key_value?: string;
  remove_key?: boolean;
}

export const turnCheckApi = {
  get: (c: Client, box: string) => c.box<TurnCheckStatus>(box, "GET", "turncheck"),
  put: (c: Client, box: string, change: TurnCheckChange) => c.box<TurnCheckStatus>(box, "PUT", "turncheck", change),
};

// What Settings shows for each provider. model is the one it serves today,
// filled in when you pick the provider; keyName and keyHelp are for the
// hosted ones, which take a key. Laya takes none.
export const turnCheckProviders: Record<TurnCheckProvider, { label: string; model?: string; keyName?: string; keyHelp?: string; keyLink?: string }> = {
  gateway: { label: "Jev on AI Gateway", model: "typesafe-ai/jev", keyName: "AI Gateway", keyHelp: "Create an API key in Vercel's AI Gateway", keyLink: "https://vercel.com/docs/ai-gateway" },
  openai: { label: "OpenAI Decisions", model: "gpt-6-luna", keyName: "OpenAI", keyHelp: "Create an OpenAI API key", keyLink: "https://platform.openai.com/api-keys" },
  laya: { label: "Laya, your own server" },
};

export const layaPlaceholder = "http://127.0.0.1:8000/v1/systemone";

// turnCheckReady says whether a box has all it needs to be turned on, as
// the box decides.
export function turnCheckReady(st: TurnCheckStatus) {
  if (st.provider === "laya") return !!st.url;
  if (st.provider === "gateway" || st.provider === "openai") return !!st.model && (st.key_set || !!st.key);
  return false;
}
