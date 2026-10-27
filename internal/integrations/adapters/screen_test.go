package adapters

import (
	"strings"
	"testing"
)

func TestScreenStateReadsQuestions(t *testing.T) {
	rule := strings.Repeat("─", 60)
	form := func(footer string) string {
		return strings.Join([]string{
			"❯ Ask me tea or coffee",
			rule,
			" ☐ Drink",
			"",
			"Tea or coffee?",
			"",
			"❯ 1. Tea",
			"  2. Coffee",
			"  3. Type something.",
			rule,
			"  4. Chat about this",
			"",
			footer,
			"",
		}, "\n")
	}
	for _, c := range []struct {
		name, screen, want string
	}{
		{"question form, Tab/Arrow footer", form("Enter to select · Tab/Arrow keys to navigate · Esc to cancel"), "waiting"},
		{"question form, ↑/↓ footer", form("Enter to select · ↑/↓ to navigate · Esc to cancel"), "waiting"},
		{"question form, with an editor hint", form("Enter to select · Tab/Arrow keys to navigate · ctrl+g to edit in Vim · Esc to cancel"), "waiting"},
		{"question form's review page", strings.Join([]string{rule, "Review your answers", "", " ● Tea or coffee?", "   → Tea", "", "Ready to submit your answers?", "", "❯ 1. Submit answers", "  2. Cancel"}, "\n"), "waiting"},
		{"permission prompt", "Bash command\n  rm -rf build\nDo you want to proceed?\n❯ 1. Yes\n  2. No\n\nEsc to cancel", "waiting"},
		{"folder trust", " Quick safety check: Is this a project you created or one you trust?\n\n ❯ No, exit\n   Yes, I trust this folder\n\n Enter to confirm · Esc to cancel", "waiting"},

		{"a reply that mentions the keys", "⏺ Press Enter to select the default, then Esc to cancel the dialog.\n" + rule + "\n❯ \n" + rule + "\n  ? for shortcuts", ""},
		{"a reply that says press Enter", "⏺ Run it, then press Enter when the build is done.\n" + rule + "\n❯ \n" + rule, ""},
		{"Claude at its prompt", "⏺ Done: the flag is in.\n✻ Worked for 12s\n" + rule + "\n❯ \n" + rule + "\n  Opus 5.5 · context 5%\n  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents", ""},
		{"Claude working", "⏺ Adding the flag…\n✻ Thinking… (esc to interrupt)\n" + rule + "\n❯ \n" + rule + "\n  ? for shortcuts", ""},
		{"empty", "", ""},
	} {
		if got := ScreenState(c.screen); got != c.want {
			t.Errorf("%s: ScreenState = %q, want %q", c.name, got, c.want)
		}
	}
}
