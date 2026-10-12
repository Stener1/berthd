package box

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/transcript"
)

// fakeForm is Claude Code's AskUserQuestion form (2.1), drawn as its
// screen shows it and moved by the keys it takes: arrows move the cursor
// (Left and Right between questions), Enter picks a single choice or
// ticks a multiple one, Space ticks, typing goes into "Type something",
// Next (Submit for the last) moves on, and a review lists the answers
// before they go.
type fakeForm struct {
	qs      []transcript.Question
	tab     int // the question shown; len(qs) is the review
	cursor  []int
	ticked  []map[int]bool
	other   []string
	picked  []string
	review  int
	sent    bool
	keysLog []string
	// before can change a key or the form before a key acts (a fault).
	before func(f *fakeForm, key string) string
}

func newFakeForm(qs []transcript.Question) *fakeForm {
	f := &fakeForm{qs: qs, review: 1}
	for range qs {
		f.cursor = append(f.cursor, 1)
		f.ticked = append(f.ticked, map[int]bool{})
		f.other = append(f.other, "")
		f.picked = append(f.picked, "")
	}
	return f
}

func (f *fakeForm) reviews() bool { return len(f.qs) > 1 || f.qs[0].Multi }

func (f *fakeForm) keys(k ...string) error {
	if len(k) == 2 && k[0] == "-l" {
		f.keysLog = append(f.keysLog, "type:"+k[1])
		q := f.qs[f.tab]
		if f.cursor[f.tab] == len(q.Options)+1 {
			f.other[f.tab] += k[1]
			if q.Multi {
				f.ticked[f.tab][len(q.Options)+1] = true
			}
		}
		return nil
	}
	for _, key := range k {
		if f.before != nil {
			key = f.before(f, key)
		}
		f.keysLog = append(f.keysLog, key)
		f.press(key)
	}
	return nil
}

func (f *fakeForm) press(key string) {
	if f.sent {
		return
	}
	if f.tab == len(f.qs) {
		switch key {
		case "Up":
			f.review = 1
		case "Down":
			f.review = 2
		case "Left":
			f.tab--
		case "Enter":
			f.sent = f.review == 1
		}
		return
	}
	q := f.qs[f.tab]
	n := len(q.Options)
	rows := []int{}
	for i := 1; i <= n+1; i++ {
		rows = append(rows, i)
	}
	if q.Multi {
		rows = append(rows, 0)
	}
	pos := 0
	for i, r := range rows {
		if r == f.cursor[f.tab] {
			pos = i
		}
	}
	next := func() {
		f.tab++
		if f.tab == len(f.qs) && !f.reviews() {
			f.sent = true
		}
	}
	switch key {
	case "Down":
		f.cursor[f.tab] = rows[min(pos+1, len(rows)-1)]
	case "Up":
		f.cursor[f.tab] = rows[max(pos-1, 0)]
	case "Left":
		f.tab = max(f.tab-1, 0)
	case "Right":
		f.tab++
	case "Space":
		if c := f.cursor[f.tab]; q.Multi && c > 0 && (c <= n || f.other[f.tab] != "") {
			f.ticked[f.tab][c] = !f.ticked[f.tab][c]
		}
	case "Enter":
		c := f.cursor[f.tab]
		switch {
		case q.Multi && c == 0:
			next()
		case q.Multi:
			if c <= n || f.other[f.tab] != "" {
				f.ticked[f.tab][c] = !f.ticked[f.tab][c]
			}
		case c <= n:
			f.picked[f.tab] = q.Options[c-1].Label
			next()
		case f.other[f.tab] != "":
			f.picked[f.tab] = f.other[f.tab]
			next()
		}
	}
}

func (f *fakeForm) answer(i int) string {
	q := f.qs[i]
	if !q.Multi {
		return f.picked[i]
	}
	var parts []string
	for j, o := range q.Options {
		if f.ticked[i][j+1] {
			parts = append(parts, o.Label)
		}
	}
	if f.ticked[i][len(q.Options)+1] {
		parts = append(parts, f.other[i])
	}
	return strings.Join(parts, ", ")
}

const fakeRule = "────────────────────────────────────────────────────────────────────────────────"

func (f *fakeForm) screen() string {
	var b strings.Builder
	w := func(s string) { b.WriteString(s + "\n") }
	w("❯ Use the AskUserQuestion tool to ask me three questions at once: 1. one single-choice")
	w("  2. one multi-select")
	w("")
	if f.sent {
		w("⏺ User answered Claude's questions:")
		for i, q := range f.qs {
			w("  ⎿  · " + q.Question + " → " + f.answer(i))
		}
		w("")
		w("✢ Thinking… (esc to interrupt)")
		w(fakeRule)
		w("❯ ")
		w(fakeRule)
		w("  ⏵⏵ auto mode on (shift+tab to cycle)")
		return b.String()
	}
	w(fakeRule)
	if f.reviews() {
		tabs := "←  "
		for i, q := range f.qs {
			box := "☐"
			if f.answer(i) != "" {
				box = "☒"
			}
			tabs += box + " " + q.Header + "  "
		}
		w(tabs + "✔ Submit  →")
	} else {
		w(" ☐ " + f.qs[0].Header + "            ")
	}
	if f.tab == len(f.qs) {
		w("Review your answers")
		for i, q := range f.qs {
			w(" ● " + q.Question + "       ")
			w("   → " + f.answer(i) + "  ")
		}
		w("Ready to submit your answers?")
		mark := func(n int) string {
			if f.review == n {
				return "❯ "
			}
			return "  "
		}
		w(mark(1) + "1. Submit answers")
		w(mark(2) + "2. Cancel")
		w("")
		w("")
		return b.String()
	}
	q := f.qs[f.tab]
	// A long question wraps.
	words := q.Question
	if len(words) > 50 {
		cut := strings.LastIndex(words[:50], " ")
		w(words[:cut])
		w(words[cut+1:])
	} else {
		w(words)
	}
	mark := func(n int) string {
		if f.cursor[f.tab] == n {
			return "❯ "
		}
		return "  "
	}
	n := len(q.Options)
	for j, o := range q.Options {
		if q.Multi {
			box := "[ ]"
			if f.ticked[f.tab][j+1] {
				box = "[✔]"
			}
			w(mark(j+1) + strconv.Itoa(j+1) + ". " + box + " " + o.Label)
			w("         " + o.Description + "     ")
		} else {
			w(mark(j+1) + strconv.Itoa(j+1) + ". " + o.Label + "   ")
			w("     " + o.Description)
		}
	}
	if q.Multi {
		box, text := "[ ]", "Type something"
		if f.other[f.tab] != "" {
			text = f.other[f.tab]
		}
		if f.ticked[f.tab][n+1] {
			box = "[✔]"
		}
		w(mark(n+1) + strconv.Itoa(n+1) + ". " + box + " " + text + "     ")
		label := "Next"
		if f.tab == len(f.qs)-1 {
			label = "Submit"
		}
		w(mark(0) + "   " + label)
	} else {
		text := "Type something."
		if f.other[f.tab] != "" {
			text = f.other[f.tab]
		}
		w(mark(n+1) + strconv.Itoa(n+1) + ". " + text)
	}
	w(fakeRule)
	w("  " + strconv.Itoa(n+2) + ". Chat about this")
	w("")
	w("Enter to select · Tab/Arrow keys to navigate · Esc to cancel")
	w("")
	return b.String()
}

var threeQs = []transcript.Question{
	{Question: "Pick a colour:", Header: "Colour", Options: []transcript.Option{{Label: "Red", Description: "The colour red"}, {Label: "Green", Description: "The colour green"}, {Label: "Blue", Description: "The colour blue"}}},
	{Question: "Pick your toppings:", Header: "Toppings", Multi: true, Options: []transcript.Option{{Label: "Cheese", Description: "Add cheese"}, {Label: "Ham", Description: "Add ham"}, {Label: "Olives", Description: "Add olives"}, {Label: "Mushrooms", Description: "Add mushrooms"}}},
	{Question: "What's your pet's name? Pick a suggestion or type your own via Other.", Header: "Pet name", Options: []transcript.Option{{Label: "Biscuit", Description: "Suggestion: Biscuit"}, {Label: "Pepper", Description: "Suggestion: Pepper"}}},
}

func fastSteps(t *testing.T) {
	old := answerStep
	answerStep = 150 * time.Millisecond
	t.Cleanup(func() { answerStep = old })
}

func TestDriveThreeQuestions(t *testing.T) {
	fastSteps(t)
	f := newFakeForm(threeQs)
	as := []QuestionAnswer{{Picks: []string{"Green"}}, {Picks: []string{"Olives", "Cheese"}, Other: "anchovies"}, {Other: "Captain Whiskers"}}
	if _, err := checkAnswers(threeQs, as); err != nil {
		t.Fatal(err)
	}
	if err := driveAnswers(context.Background(), f, threeQs, as); err != nil {
		t.Fatalf("drive: %v\nkeys %v\n%s", err, f.keysLog, f.screen())
	}
	if !f.sent || f.answer(0) != "Green" || f.answer(1) != "Cheese, Olives, anchovies" || f.answer(2) != "Captain Whiskers" {
		t.Fatalf("sent %v: %q %q %q", f.sent, f.answer(0), f.answer(1), f.answer(2))
	}
	want := "Down Enter Space Down Down Space Down Down type:anchovies Down Enter Down Down type:Captain Whiskers Enter Enter"
	if got := strings.Join(f.keysLog, " "); got != want {
		t.Errorf("keys\n got %s\nwant %s", got, want)
	}
}

// One question with one answer goes as soon as it is picked; one with
// several picks has a review.
func TestDriveOneQuestion(t *testing.T) {
	fastSteps(t)
	one := []transcript.Question{{Question: "Tea or coffee?", Header: "Drink", Options: []transcript.Option{{Label: "Tea"}, {Label: "Coffee"}}}}
	f := newFakeForm(one)
	if err := driveAnswers(context.Background(), f, one, []QuestionAnswer{{Picks: []string{"Coffee"}}}); err != nil || !f.sent || f.answer(0) != "Coffee" {
		t.Fatalf("single: %v %v %q", err, f.sent, f.answer(0))
	}
	if got := strings.Join(f.keysLog, " "); got != "Down Enter" {
		t.Errorf("keys = %s", got)
	}
	fruit := []transcript.Question{{Question: "Which fruits?", Header: "Fruits", Multi: true, Options: []transcript.Option{{Label: "Apple"}, {Label: "Pear"}, {Label: "Plum"}}}}
	f = newFakeForm(fruit)
	if err := driveAnswers(context.Background(), f, fruit, []QuestionAnswer{{Picks: []string{"Apple", "Plum"}}}); err != nil || !f.sent || f.answer(0) != "Apple, Plum" {
		t.Fatalf("multi: %v %v %q\n%v", err, f.sent, f.answer(0), f.keysLog)
	}
}

// The person had already moved on in the form, and ticked something:
// it goes back to the first question and sets every tick as asked.
func TestDriveFromWhereThePersonLeftIt(t *testing.T) {
	fastSteps(t)
	f := newFakeForm(threeQs)
	f.picked[0] = "Red"
	f.ticked[1][2] = true // Ham
	f.tab = 1
	as := []QuestionAnswer{{Picks: []string{"Blue"}}, {Picks: []string{"Mushrooms"}}, {Picks: []string{"Pepper"}}}
	if err := driveAnswers(context.Background(), f, threeQs, as); err != nil {
		t.Fatalf("drive: %v\n%v", err, f.keysLog)
	}
	if !f.sent || f.answer(0) != "Blue" || f.answer(1) != "Mushrooms" || f.answer(2) != "Pepper" {
		t.Fatalf("sent %v: %q %q %q", f.sent, f.answer(0), f.answer(1), f.answer(2))
	}
}

// Anything unexpected stops it, says what, and never submits.
func TestDriveStopsOnSurprise(t *testing.T) {
	fastSteps(t)
	as := []QuestionAnswer{{Picks: []string{"Green"}}, {Picks: []string{"Ham"}}, {Picks: []string{"Biscuit"}}}
	cases := map[string]func(f *fakeForm, key string) string{
		// A key that does nothing (the agent froze, or another screen).
		"ignored": func(f *fakeForm, key string) string {
			if key == "Down" {
				return "Nothing"
			}
			return key
		},
		// The answer lands elsewhere: the review would show the wrong one.
		"wrong pick": func(f *fakeForm, key string) string {
			if f.tab == 2 && key == "Enter" {
				f.cursor[2] = 2
			}
			return key
		},
		// A different question comes up.
		"other question": func(f *fakeForm, key string) string {
			if f.tab == 1 && key == "Enter" && f.cursor[1] == 0 {
				f.qs = append([]transcript.Question{}, f.qs...)
				f.qs[2].Question = "Something else entirely?"
			}
			return key
		},
	}
	for name, fault := range cases {
		f := newFakeForm(threeQs)
		f.before = fault
		err := driveAnswers(context.Background(), f, threeQs, as)
		var he httpError
		if !errors.As(err, &he) || he.status != http.StatusConflict || !strings.Contains(he.msg, "finish in Claude's screen") {
			t.Errorf("%s: err = %v", name, err)
		}
		if f.sent {
			t.Errorf("%s: submitted anyway", name)
		}
	}
	// Not the form at all.
	f := &plainScreen{s: "❯ \n" + fakeRule + "\n  ⏵⏵ auto mode on"}
	if err := driveAnswers(context.Background(), f, threeQs, as); err == nil || !strings.Contains(err.Error(), "doesn't show its questions") {
		t.Errorf("no form: %v", err)
	}
	if len(f.sent) != 0 {
		t.Errorf("keys sent to a screen without the form: %v", f.sent)
	}
}

type plainScreen struct {
	s    string
	sent []string
}

func (p *plainScreen) keys(k ...string) error { p.sent = append(p.sent, k...); return nil }
func (p *plainScreen) screen() string         { return p.s }

func TestCheckAnswers(t *testing.T) {
	ok := []QuestionAnswer{{Picks: []string{"Green"}}, {Picks: []string{"Ham"}}, {Other: "Rex"}}
	if shown, err := checkAnswers(threeQs, ok); err != nil || shown[2] != "Rex" {
		t.Fatalf("%v %v", shown, err)
	}
	bad := map[string][]QuestionAnswer{
		"too few":           ok[:2],
		"two for a single":  {{Picks: []string{"Green", "Red"}}, ok[1], ok[2]},
		"pick and words":    {{Picks: []string{"Green"}, Other: "teal"}, ok[1], ok[2]},
		"not an option":     {{Picks: []string{"Purple"}}, ok[1], ok[2]},
		"no answer":         {{}, ok[1], ok[2]},
		"words over a line": {ok[0], ok[1], {Other: "Rex\nthe dog"}},
	}
	for name, as := range bad {
		if _, err := checkAnswers(threeQs, as); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// readForm on screens as Claude Code 2.1.289 drew them (captured, wide).
func TestReadForm(t *testing.T) {
	multi := strings.Join([]string{
		"❯ Use the AskUserQuestion tool to ask me three questions at once: one single-choice (pick a colour: red, green, blue), one multi-select (pick toppings: cheese, ham, olives, mushrooms), one where I'd  ",
		"  type my own answer (a pet's name, offer two suggestions). Then repeat my exact answers back to me.",
		"",
		fakeRule,
		"←  ☒ Colour  ☐ Toppings  ☐ Pet name  ✔ Submit  →",
		"",
		"Pick your toppings:",
		"",
		"  1. [✔] Cheese",
		"         Add cheese",
		"  2. [ ] Ham",
		"         Add ham     ",
		"❯ 3. [✔] Olives",
		"         Add olives ",
		"  4. [ ] Mushrooms  ",
		"         Add mushrooms",
		"  5. [✔] anchovies     ",
		"     Next",
		fakeRule,
		"  6. Chat about this",
		"",
		"Enter to select · Tab/Arrow keys to navigate · ctrl+g to edit in Vim · Esc to cancel",
		"", "",
	}, "\n")
	f := readForm(multi)
	if !f.ok || f.review || f.question != "Pick your toppings:" || f.cursor != 3 || len(f.rows) != 6 {
		t.Fatalf("multi: %+v", f)
	}
	if r := f.row(1); !r.box || !r.checked || r.text != "Cheese" {
		t.Errorf("row 1 = %+v", r)
	}
	if r := f.row(2); !r.box || r.checked || r.text != "Ham" {
		t.Errorf("row 2 = %+v", r)
	}
	if r := f.row(5); !r.checked || r.text != "anchovies" {
		t.Errorf("own words = %+v", r)
	}
	if r := f.row(0); r.text != "Next" || f.has(6) {
		t.Errorf("next = %+v, chat counted: %v", r, f.has(6))
	}

	single := strings.Join([]string{
		"❯ Now use AskUserQuestion to ask me ONE single-choice question: tea or coffee (two options). Just that one question.",
		fakeRule,
		" ☐ Drink            ",
		"",
		"Tea or coffee?",
		"",
		"❯ 1. Tea",
		"     A cup of tea",
		"  2. Coffee",
		"     A cup of coffee",
		"  3. Type something.",
		fakeRule,
		"  4. Chat about this",
		"",
		"Enter to select · ↑/↓ to navigate · Esc to cancel",
	}, "\n")
	if f := readForm(single); !f.ok || f.question != "Tea or coffee?" || f.cursor != 1 || !ownWordsEmpty(f.row(3).text) || f.row(2).box {
		t.Fatalf("single: %+v", f)
	}

	bordered := strings.Join([]string{
		fakeRule,
		"←  ☐ Timeout  ☐ Unclear out  ✔ Submit  →",
		"",
		"│ Return portal: on Oct 12 a refund for order #96895 returned a 500 to staff,",
		"│ but Shopify had actually refunded it. How should money mutations",
		"│ behave?",
		"",
		"❯ 1. 60s + idempotency key (Recommended)",
		"     Reads keep 15s.",
		"  2. No client timeout",
		"     Simplest.",
		"  3. Type something.",
		fakeRule,
		"  4. Chat about this",
		"",
		"Enter to select · Tab/Arrow keys to navigate · Esc to cancel",
	}, "\n")
	want := "Return portal: on Oct 12 a refund for order #96895 returned a 500 to staff, but Shopify had actually refunded it. How should money mutations behave?"
	if f := readForm(bordered); !f.ok || f.question != want || f.cursor != 1 {
		t.Fatalf("bordered: %+v", f)
	}

	review := strings.Join([]string{
		fakeRule,
		"←  ☒ Colour  ☒ Toppings  ☒ Pet name  ✔ Submit  →",
		"",
		"Review your answers",
		"",
		" ● Pick a colour:       ",
		"   → Green  ",
		" ● Pick your toppings: ",
		"   → Cheese, Olives, anchovies",
		" ● What's your pet's name? Pick a suggestion or type your own via Other.",
		"   → Captain Whiskers",
		"",
		"Ready to submit your answers?",
		"",
		"❯ 1. Submit answers",
		"  2. Cancel",
		"", "", "",
	}, "\n")
	f = readForm(review)
	if !f.ok || !f.review || f.cursor != 1 || f.row(1).text != "Submit answers" || !strings.Contains(f.reviewText, "→ Cheese, Olives, anchovies") {
		t.Fatalf("review: %+v", f)
	}

	// Its answer in the conversation, and the prompt back: no form.
	done := strings.Join([]string{
		"⏺ User answered Claude's questions:",
		"  ⎿  · Pick a colour: → Green",
		"     · Pick your toppings: → Cheese, Olives, anchovies",
		"⏺ Here are your answers, exactly as you gave them:",
		"✻ Crunched for 4s · done 10:12",
		fakeRule,
		"❯ ",
		fakeRule,
		"  Opus 5.5 · context 5%",
		"  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents",
	}, "\n")
	if f := readForm(done); f.ok {
		t.Fatalf("done: %+v", f)
	}
	// A permission prompt is not a question form.
	perm := strings.Join([]string{
		"Bash command",
		"  rm -rf build",
		"Do you want to proceed?",
		"❯ 1. Yes",
		"  2. No, and tell Claude what to do differently (esc)",
		"",
		"Esc to cancel",
	}, "\n")
	if f := readForm(perm); f.ok {
		t.Fatalf("permission read as a form: %+v", f)
	}
}
