import type { Client } from "@/lib/api";

// Turn check (see internal/box/turncheck.go): when on, a box asks the model
// you choose on Vercel AI Gateway (or the Jev-compatible server url names)
// whether a finished turn needs you. A pasted key is kept on the box and
// never comes back; key is only ever a secret reference. Only a paired
// laptop reads or sets it.

export interface TurnCheckConfig {
  enabled: boolean;
  key?: string;
  url?: string;
  model?: string;
}

export interface TurnCheckStatus extends TurnCheckConfig {
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

const gateway = "https://ai-gateway.vercel.sh/v1/evaluate";

// ownServer says whether a check asks a server other than AI Gateway, which
// gets no key: by its URL, as the box decides.
export function ownServer(c: TurnCheckConfig) {
  return !!c.url && c.url !== gateway;
}
