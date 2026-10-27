package adapters

// Omp reports through a small extension berth installs into oh-my-pi, which
// runs the hook for the events below with {"cwd", "session_id"}.
var Omp = register(&Adapter{
	Name: "omp",
	Caps: Caps{Ready: true, Started: true, Waiting: true, Finished: true, Via: "extension"},
	Translate: func(hook string, in Payload) (string, map[string]any, bool) {
		d := map[string]any{"path": in.Str("cwd"), "agent_session_id": in.Str("session_id")}
		switch hook {
		case "session_start":
			return Ready, d, true
		case "agent_start":
			d["signal"] = "prompt"
			return Started, d, true
		case "tool_approval_requested":
			d["reason"] = "permission"
			return Waiting, d, true
		case "tool_approval_resolved":
			d["signal"] = "tool"
			return Started, d, true
		case "agent_end":
			// An agent_end that will continue (a queued message, a retry)
			// is not the turn's end.
			if b, _ := in["will_continue"].(bool); b {
				return "", nil, false
			}
			return Finished, d, true
		case "session_shutdown":
			return Exited, d, true
		}
		return "", nil, false
	},
})

// OmpExtension is the extension file, for the berth binary at bin. oh-my-pi
// loads every file in ~/.omp/agent/extensions. A subagent has no UI and is
// not a session someone watches, so it reports nothing.
func OmpExtension(bin string) string {
	return `// Installed by berth: tells berth (hook omp) when an oh-my-pi turn starts, needs you, or ends.
// Only the session ID and the working directory leave oh-my-pi.
// @ts-nocheck
const BERTH = ` + jsString(bin) + `;

export default function (pi) {
  const report = (event, ctx, extra) => {
    if (!ctx || !ctx.hasUI) return;
    let session_id = "";
    try { session_id = ctx.sessionManager.getSessionId() || ""; } catch {}
    const payload = JSON.stringify({ cwd: ctx.cwd || process.cwd(), session_id, ...extra });
    try {
      const proc = Bun.spawn({ cmd: [BERTH, "hook", "omp", event, payload], stdin: "ignore", stdout: "ignore", stderr: "ignore" });
      proc.unref?.();
    } catch {}
  };
  pi.on("session_start", (_e, ctx) => report("session_start", ctx));
  pi.on("agent_start", (_e, ctx) => report("agent_start", ctx));
  pi.on("tool_approval_requested", (_e, ctx) => report("tool_approval_requested", ctx));
  pi.on("tool_approval_resolved", (_e, ctx) => report("tool_approval_resolved", ctx));
  pi.on("agent_end", (e, ctx) => report("agent_end", ctx, { will_continue: !!(e && e.willContinue) }));
  pi.on("session_shutdown", (_e, ctx) => report("session_shutdown", ctx));
}
`
}
