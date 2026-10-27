import type { BerthLinear, LinearIssue, LinearIssueDetail, LinearStatus, LinearTeam } from "@berth/plugin";

import type { Client } from "@/lib/api";

// Linear through the laptop agent (internal/agent/linear.go): it keeps the
// API key and makes every call, so the key never reaches the webview.

export const linearApi = {
  status: (c: Client) => c.laptop<LinearStatus>("GET", "/v1/linear"),
  // connect resolves once Linear has accepted the key and the laptop has
  // kept it; a refused key rejects with code "linear_auth".
  connect: (c: Client, apiKey: string) => c.laptop<LinearStatus>("PUT", "/v1/linear", { api_key: apiKey }),
  disconnect: (c: Client) => c.laptop<LinearStatus>("DELETE", "/v1/linear"),
  teams: async (c: Client) => (await c.laptop<{ teams: LinearTeam[] | null }>("GET", "/v1/linear/teams")).teams ?? [],
  issues: async (c: Client, opts: { team?: string; mine?: boolean } = {}) => {
    const q = new URLSearchParams();
    if (opts.team) q.set("team", opts.team);
    if (opts.mine) q.set("mine", "1");
    const qs = q.toString();
    return (await c.laptop<{ issues: LinearIssue[] | null }>("GET", `/v1/linear/issues${qs ? `?${qs}` : ""}`)).issues ?? [];
  },
  issue: (c: Client, id: string) => c.laptop<LinearIssueDetail>("GET", `/v1/linear/issues/${encodeURIComponent(id)}`),
  comment: async (c: Client, id: string, body: string) => {
    await c.laptop("POST", `/v1/linear/issues/${encodeURIComponent(id)}/comments`, { body });
  },
};

// makeLinearApi is Linear as plugins see it (berth.linear): everything but
// the key.
export function makeLinearApi(client: Client): BerthLinear {
  return {
    status: () => linearApi.status(client),
    teams: () => linearApi.teams(client),
    issues: (opts) => linearApi.issues(client, opts),
    issue: (id) => linearApi.issue(client, id),
    comment: (id, body) => linearApi.comment(client, id, body),
  };
}
