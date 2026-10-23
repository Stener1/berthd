package adapters

import "strings"

// Claude is Claude Code, through the hooks in ~/.claude/settings.json.
var Claude = register(&Adapter{
	Name: "claude",
	Caps: Caps{Ready: true, Started: true, Waiting: true, Finished: true, FinalMessage: true, Via: "hooks"},
	Translate: func(hook string, in Payload) (string, map[string]any, bool) {
		d := map[string]any{"path": in.Str("cwd"), "agent_session_id": in.Str("session_id"), "session_id": in.Str("session_id")}
		switch hook {
		case "SessionStart":
			// A new session sits at its prompt until someone types.
			return Ready, d, true
		case "UserPromptSubmit":
			d["signal"] = "prompt"
			// Only its short title: the box names an untitled session
			// after it, and drops it before the event is published.
			d["title"] = Title(in.Str("prompt"))
			return Started, d, true
		case "PostToolUse":
			// No hook fires when a permission is granted, but the tool it
			// allowed then runs: the agent is working again.
			d["signal"] = "tool"
			// A helper's (subagent's) tool says whose it is: one left
			// working in the background goes on after the turn ends.
			if in.Str("agent_id") != "" {
				d["helper"] = true
			}
			return Started, d, true
		case "PermissionRequest":
			d["reason"] = "permission"
			if ask := claudeAsk(in); ask != nil {
				d[AskKey] = ask
			}
			return Waiting, d, true
		case "Notification":
			reason, ok := claudeNotification(in)
			if !ok {
				return "", nil, false
			}
			d["reason"] = reason
			// The message ("Claude needs your permission to use Bash", or
			// a question) goes with the ask, never into the event.
			if msg := clip(in.Str("message")); msg != "" {
				d[AskKey] = map[string]any{"message": msg}
			}
			return Waiting, d, true
		case "Stop":
			return Finished, d, true
		case "StopFailure":
			d["status"] = "error"
			return Finished, d, true
		case "SessionEnd":
			return Exited, d, true
		}
		return "", nil, false
	},
})

// claudeNotification says whether a Notification means the agent needs
// someone. idle_prompt comes about a minute after every finished turn and
// auth_success after a login: neither is a question.
func claudeNotification(in Payload) (reason string, ok bool) {
	switch in.Str("notification_type") {
	case "permission_prompt":
		return "permission", true
	case "elicitation_dialog", "agent_needs_input":
		return "question", true
	case "idle_prompt", "auth_success":
		return "", false
	case "":
		// Older versions send no type; their idle reminder reads "Claude is
		// waiting for your input". The message is read here, and kept
		// only with the ask (AskKey), never in the event.
		msg := lower(in.Str("message"))
		if strings.Contains(msg, "waiting for your input") {
			return "", false
		}
		return "permission", true
	}
	return "", false
}
