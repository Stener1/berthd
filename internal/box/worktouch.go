package box

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/cosscom/shipyard/internal/events"
	"github.com/cosscom/shipyard/internal/integrations/adapters"
)

// A work record's last_seen is the last time a person prompted a session,
// answered it or visited it. An agent's traffic, a /loop wakeup and a daemon
// restart all look like activity, and state_since moves on all of them, so
// the box reads its journal for the few events a person causes:
//
//   - a send or an answer from the app, the phone or a laptop's berth;
//   - a prompt typed into a terminal: the agent's hook says a prompt started
//     with no send near it, and its transcript shows a person wrote it;
//   - an answer given at a terminal: a wait for a person that ended with no
//     send or answer near it.
//
// The reader's Seq and what it holds (waits open, prompts not yet settled) are
// saved in one write, so a restart goes on where it left off. If it stopped
// without saving, it repeats events from the last save, and a repeat of a time
// already noted moves nothing.

const (
	// touchSentWindow is how close to a send a hook's prompt is that send's.
	touchSentWindow = 15 * time.Second
	// touchPromptMatch is how close a transcript's line is to the hook.
	touchPromptMatch = 10 * time.Second
	// touchSettle is how long a candidate waits: the hook comes before the
	// transcript's line, and before an answer's own event.
	touchSettle = 8 * time.Second
	// touchMinAnswer is the least a person takes to answer. Quicker is a hook
	// or a rule allowing a permission.
	touchMinAnswer = 2 * time.Second
)

// humanSend says a person caused a send or an answer: it came from the app,
// the phone or a laptop. "berth" alone is a session, a flow or a script on
// the box, and "mcp" an agent.
func humanSend(e events.Event) bool {
	from, _ := e.Data["from"].(string)
	return e.Origin == "app" || e.Origin == "phone" || strings.HasPrefix(e.Origin, "laptop:") || strings.HasPrefix(from, "laptop:") || from == "phone"
}

// A touchNote is a person's prompt or answer: source is send, start, prompt
// or answer.
type touchNote struct {
	session string
	at      time.Time
	source  string
}

// touchTracker turns the journal's events into notes.
type touchTracker struct {
	sent    map[string]time.Time
	waits   map[string]time.Time
	pending []touchNote
	// typed says a person wrote the prompt a session started at that time.
	typed func(session string, at time.Time) bool
}

// touchState is what a touchTracker holds, as the store keeps it.
type touchState struct {
	Sent    map[string]time.Time `json:"sent,omitempty"`
	Waits   map[string]time.Time `json:"waits,omitempty"`
	Pending []touchPending       `json:"pending,omitempty"`
}

type touchPending struct {
	Session string    `json:"session"`
	At      time.Time `json:"at"`
	Source  string    `json:"source"`
}

// touchWaitMax is the longest a wait is remembered: the journal's own reach.
const touchWaitMax = 14 * 24 * time.Hour

// state is what to keep at now. A send too old to matter goes, and so does a
// wait for a session that ended with no word of it.
func (t *touchTracker) state(now time.Time) *touchState {
	maps.DeleteFunc(t.sent, func(_ string, at time.Time) bool { return now.Sub(at) > 2*touchSentWindow })
	maps.DeleteFunc(t.waits, func(_ string, at time.Time) bool { return now.Sub(at) > touchWaitMax })
	st := &touchState{Sent: maps.Clone(t.sent), Waits: maps.Clone(t.waits)}
	for _, p := range t.pending {
		st.Pending = append(st.Pending, touchPending{p.session, p.at, p.source})
	}
	return st
}

// restore takes up what an earlier reader kept.
func (t *touchTracker) restore(st *touchState) {
	if st == nil {
		return
	}
	maps.Copy(t.sent, st.Sent)
	maps.Copy(t.waits, st.Waits)
	for _, p := range st.Pending {
		t.pending = append(t.pending, touchNote{p.Session, p.At, p.Source})
	}
}

func newTouchTracker(typed func(string, time.Time) bool) *touchTracker {
	return &touchTracker{sent: map[string]time.Time{}, waits: map[string]time.Time{}, typed: typed}
}

func near(a, b time.Time, within time.Duration) bool {
	d := a.Sub(b)
	return d >= -within && d <= within
}

// sentNear says a send or answer of any origin came close to at.
func (t *touchTracker) sentNear(name string, at time.Time) bool {
	s, ok := t.sent[name]
	return ok && near(at, s, touchSentWindow)
}

// feed reads one event and returns what it settles at once.
func (t *touchTracker) feed(e events.Event) []touchNote {
	str := func(k string) string { s, _ := e.Data[k].(string); return s }
	switch e.Type {
	case "session.sent":
		name := str("name")
		if name == "" {
			return nil
		}
		t.sent[name] = e.Time
		delete(t.waits, name)
		if humanSend(e) {
			return []touchNote{{name, e.Time, "send"}}
		}
	case "session.started":
		// Started by a person, with or without a first prompt.
		if name := str("name"); name != "" && humanSend(e) {
			return []touchNote{{name, e.Time, "start"}}
		}
	case adapters.Waiting:
		name := str("session")
		if name == "" {
			return nil
		}
		if r := str("reason"); e.Origin != "screen" && (r == "permission" || r == "question") {
			t.waits[name] = e.Time
		} else {
			delete(t.waits, name)
		}
	case adapters.Finished, adapters.Exited:
		delete(t.waits, str("session"))
	case adapters.Started:
		name := str("session")
		if name == "" || e.Origin == "screen" || str("source") == "send" {
			return nil
		}
		switch str("signal") {
		case "prompt":
			delete(t.waits, name)
			if !t.sentNear(name, e.Time) {
				t.pending = append(t.pending, touchNote{name, e.Time, "prompt"})
			}
		case "tool":
			if helper, _ := e.Data["helper"].(bool); helper {
				return nil
			}
			w, ok := t.waits[name]
			delete(t.waits, name)
			if ok && e.Time.Sub(w) >= touchMinAnswer && !t.sentNear(name, e.Time) {
				t.pending = append(t.pending, touchNote{name, e.Time, "answer"})
			}
		}
	}
	return nil
}

// due settles the candidates old enough at now.
func (t *touchTracker) due(now time.Time) []touchNote {
	var out []touchNote
	keep := t.pending[:0]
	for _, c := range t.pending {
		if now.Sub(c.at) < touchSettle {
			keep = append(keep, c)
			continue
		}
		// A send or answer that came just after it was what caused it.
		if t.sentNear(c.session, c.at) {
			continue
		}
		if c.source == "prompt" && t.typed != nil && !t.typed(c.session, c.at) {
			continue
		}
		out = append(out, c)
	}
	t.pending = keep
	return out
}

// typedPromptAt is when a Claude Code transcript line was typed by a person:
// not a meta line (a /loop wakeup, a skill's injected text), and a message
// firstPrompt takes for a person's.
func typedPromptAt(line []byte) (time.Time, bool) {
	if !bytes.Contains(line, []byte(`"user"`)) || bytes.Contains(line, []byte(`"isMeta":true`)) {
		return time.Time{}, false
	}
	var e struct {
		Type      string          `json:"type"`
		Timestamp time.Time       `json:"timestamp"`
		Message   json.RawMessage `json:"message"`
	}
	if json.Unmarshal(line, &e) != nil || e.Type != "user" || e.Timestamp.IsZero() || firstPrompt(e.Message) == "" {
		return time.Time{}, false
	}
	return e.Timestamp, true
}

// promptIndex keeps each transcript's person-typed times, reading only what
// was added since the last look. Only RunWork's goroutine uses it.
type promptIndex struct {
	files map[string]*promptFile
}

type promptFile struct {
	offset int64
	times  []time.Time
}

// readNew reads the whole lines added since the last look: the last may still
// be being written, and waits for next time.
func (f *promptFile) readNew(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	if _, err := file.Seek(f.offset, io.SeekStart); err != nil {
		return
	}
	r := bufio.NewReaderSize(file, 64*1024)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		f.offset += int64(len(line))
		if at, ok := typedPromptAt(line); ok {
			f.times = append(f.times, at)
		}
	}
}

func (x *promptIndex) has(path string, at time.Time) bool {
	if x.files == nil {
		x.files = map[string]*promptFile{}
	}
	f := x.files[path]
	if f == nil {
		f = &promptFile{}
		x.files[path] = f
	}
	f.readNew(path)
	return slices.ContainsFunc(f.times, func(t time.Time) bool { return near(t, at, touchPromptMatch) })
}

// promptWasSent says the prompt that started at at came from a send, whatever
// the time between them: a new agent can take longer than touchSentWindow to
// read the prompt it started with. Either the ledger paired the two, or the
// turn counted as started at its send (the agent hadn't been heard from yet)
// and was still open.
func (b *Box) promptWasSent(session string, at time.Time) bool {
	if b.Turns == nil {
		return false
	}
	return slices.ContainsFunc(b.Turns.List(session, 20), func(tr Turn) bool {
		if tr.SentSeq == 0 {
			return false
		}
		open := !at.Before(tr.Sent) && (tr.Ended.IsZero() || !at.After(tr.Ended))
		return near(tr.Started, at, 2*time.Second) || (tr.Started.Equal(tr.Sent) && open)
	})
}

// notClaude stands for a session whose agent keeps no such transcript.
const notClaude = "\x00"

// typedByPerson says a person wrote the prompt a session's hook reported.
// Only Claude Code keeps what can tell (and only it wakes itself): another
// agent's prompt with no send near it is someone at its terminal. One whose
// transcript can't be found doesn't count: a touch missed brings the work
// back sooner, a touch made up hides it.
func (b *Box) typedByPerson(ctx context.Context, index *promptIndex, paths map[string]string, session string, at time.Time) bool {
	if !b.Work.holds(session) || b.promptWasSent(session, at) {
		return false
	}
	path, known := paths[session]
	if !known {
		// Remembered even when there's none, so a catch-up asks tmux once.
		if sess, all, err := b.Sessions.getWithAll(ctx, session); err == nil {
			agent, p, _ := b.transcriptFile(ctx, sess, all)
			switch {
			case agent == "claude":
				path = p
			case agent != "":
				path = notClaude
			}
		}
		paths[session] = path
	}
	if path == notClaude {
		return true
	}
	return path != "" && index.has(path, at)
}

// RunWork forms records and keeps last_seen until ctx ends: first the
// journal it hasn't read (all of it the first time, so work that's already
// running starts with a true last_seen), then each event as it comes.
func (b *Box) RunWork(ctx context.Context) {
	s := b.Work
	if s == nil {
		return
	}
	reconcile := func() {
		if err := b.reconcileWork(ctx); err != nil && ctx.Err() == nil {
			log.Printf("work: %v", err)
		}
	}
	reconcile()
	from, kept := s.resume()
	head := b.Events.Head()
	catchingUp := from < head
	index := &promptIndex{}
	paths := map[string]string{}
	tracker := newTouchTracker(func(session string, at time.Time) bool {
		// Only a catch-up's many questions share an answer: a live session
		// can move to another transcript when it resumes.
		if !catchingUp {
			clear(paths)
		}
		return b.typedByPerson(ctx, index, paths, session, at)
	})
	tracker.restore(kept)
	cursor := b.Events.SubscribeFrom(from).Named("work")
	defer cursor.Close()

	var last, flushed int64
	flushedAt, reconciled := time.Now(), time.Now()
	stale := false
	flush := func(now time.Time) {
		flushed, flushedAt = last, now
		if err := s.Flush(last, tracker.state(now)); err != nil {
			log.Printf("work: %v", err)
		}
	}
	keep := func(notes []touchNote) {
		for _, n := range notes {
			if catchingUp {
				s.touchQuietly(n.session, n.at)
				continue
			}
			// A person's first prompt can beat the session's record.
			if !s.holds(n.session) {
				reconcile()
			}
			id, err := s.Touch(n.session, n.at)
			if err != nil {
				log.Printf("work: %v", err)
			}
			if id != "" {
				b.publishWork("work", []workEvent{{"work.seen", map[string]any{"id": id, "session": n.session, "source": n.source}}})
			}
		}
	}
	for {
		next, cancel := context.WithTimeout(ctx, 2*time.Second)
		e, err := cursor.Next(next)
		cancel()
		now := time.Now()
		if ctx.Err() != nil {
			if last > flushed {
				flush(now)
			}
			return
		}
		got := err == nil
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			log.Printf("work: %v", err)
		}
		if got {
			last = e.Seq
			if !strings.HasPrefix(e.Type, "work.") {
				keep(tracker.feed(e))
				switch e.Type {
				case "session.started", "session.stopped", "session.renamed", "session.switched":
					stale = true
				}
			}
			if catchingUp {
				keep(tracker.due(e.Time))
			}
		}
		if catchingUp && (!got || e.Seq >= head) {
			// Caught up: everything old enough is settled, and kept at once.
			keep(tracker.due(now))
			catchingUp = false
			flush(now)
			stale = true
		}
		if catchingUp {
			continue
		}
		keep(tracker.due(now))
		b.syncDeliveries()
		// Soon after what's known to change a session, and every so often for
		// what isn't announced: a session that just ended by itself.
		if (stale && now.Sub(reconciled) >= 2*time.Second) || now.Sub(reconciled) >= 10*time.Second {
			stale, reconciled = false, now
			reconcile()
		}
		// Now and then: a restart that couldn't say goodbye reads back only
		// that far.
		if last > flushed && now.Sub(flushedAt) > 5*time.Minute {
			flush(now)
		}
	}
}
