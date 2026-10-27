package adapters

import (
	"regexp"
	"strings"
)

// Screen is the fallback for sessions whose agent has no hooks: berth reads
// the pane every couple of seconds while a turn is open. Output that keeps
// changing is working; an approval question is waiting; a screen that has
// not changed for a while is a finished turn.
var Screen = register(&Adapter{
	Name: "screen",
	Caps: Caps{Ready: true, Started: true, Waiting: true, Finished: true, Via: "screen"},
	Translate: func(string, Payload) (string, map[string]any, bool) {
		return "", nil, false
	},
})

// approval matches the questions agents stop at for a person. Claude
// Code's question form (AskUserQuestion) asks in the agent's own words, so
// it is known by its footer, a line of its own ("Enter to select · ↑/↓ to
// navigate · Esc to cancel"), and by its last page, which reviews them.
var approval = regexp.MustCompile(`(?im)(do you want to (proceed|make this edit|allow)|allow (once|always)|\(y/n\)|\[y/n\]|approve\b.*\?|yes, (and )?(allow|proceed|i trust)|trust (this folder|the files)|press enter to confirm|waiting for (your )?(approval|permission)|^[ \t]*enter to select\b.*\besc to cancel[ \t]*$|ready to submit your answers\?)`)

// ScreenState reads the bottom of a pane: "waiting" when it shows an
// approval question, "" otherwise. Quiescence is the caller's to judge.
func ScreenState(screen string) string {
	lines := strings.Split(strings.TrimRight(screen, "\n "), "\n")
	if len(lines) > 15 {
		lines = lines[len(lines)-15:]
	}
	if approval.MatchString(strings.Join(lines, "\n")) {
		return "waiting"
	}
	return ""
}
