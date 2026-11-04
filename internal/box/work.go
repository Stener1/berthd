package box

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cosscom/shipyard/internal/statefile"
)

// A work record is a piece of running work, kept by the box: its sessions,
// status, Not now and Waiting on. It defines its own state rather than
// following its sessions: a session ending changes nothing but that session's
// line, and the record stays until someone closes it. Grouping moves sessions
// between records and never touches the sessions themselves.

const (
	WorkOpen    = "open"
	WorkParked  = "parked"
	WorkWaiting = "waiting"
	WorkClosed  = "closed"

	PinRepo    = "repo"
	PinProject = "project"
	PinWork    = "work"

	// DefaultIssuePattern finds tracker ids when the box names no pattern of
	// its own: uppercase, so a branch's "phase-2" is not an issue.
	DefaultIssuePattern = `\b[A-Z][A-Z0-9]{0,9}-[0-9]+\b`

	// issueRangeMax is the widest range that expands: X-1..100000 in a title
	// is a typo, and keeps only its two ends.
	issueRangeMax = 200
	workTextMax   = 200
)

// notIssuePrefixes look like tracker ids and aren't. Only the default pattern
// skips them: a box's own pattern means what it says.
var notIssuePrefixes = map[string]bool{"UTF": true, "SHA": true, "AES": true, "RSA": true, "ISO": true, "RFC": true, "GPT": true, "HTTP": true, "TLS": true, "ES": true}

// WorkSession is one session of a record. Its box is the box's name, so a
// record can name a session elsewhere; only the box it runs on sees it end.
type WorkSession struct {
	Box  string `json:"box,omitempty"`
	Name string `json:"name"`
	// Title and Worktree are what it was last seen as; Worktree is
	// location/worktree.
	Title    string `json:"title,omitempty"`
	Worktree string `json:"worktree,omitempty"`
	// StartedBy is the session that started it, when one did.
	StartedBy string    `json:"started_by,omitempty"`
	Created   time.Time `json:"created,omitzero"`
	Ended     time.Time `json:"ended,omitzero"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
	// Conversation is the structured conversation it ran, kept for when the
	// session has gone.
	Conversation string `json:"conversation,omitempty"`
	// Claims are the thread keys it took on (workthreads.go). Subscribed are
	// the ones it follows, by asking or by naming them; Unsubscribed the ones
	// it said it won't, whatever its title names.
	Claims       []WorkClaim `json:"claims,omitempty"`
	Subscribed   []string    `json:"subscribed,omitempty"`
	Unsubscribed []string    `json:"unsubscribed,omitempty"`
}

// WorkRecord is one piece of work.
type WorkRecord struct {
	ID       string        `json:"id"`
	Title    string        `json:"title,omitempty"`
	Sessions []WorkSession `json:"sessions"`
	Status   string        `json:"status"`
	// Issue is the tracker id as written ("X-1..5"), IssueIDs every id it and
	// its sessions name, which is what later sessions join by.
	Issue    string   `json:"issue,omitempty"`
	IssueIDs []string `json:"issue_ids,omitempty"`
	Project  string   `json:"project,omitempty"`
	// NotUntil is when a parked record comes back; WaitingOn and CheckBack
	// say who or what a waiting one waits for, and when to look again. The
	// status does not change by itself when a date passes.
	NotUntil  time.Time `json:"not_until,omitzero"`
	WaitingOn string    `json:"waiting_on,omitempty"`
	CheckBack time.Time `json:"check_back,omitzero"`
	// LastSeen is a person's last prompt, answer or visit, and nothing else.
	LastSeen time.Time `json:"last_seen,omitzero"`
	// SuggestedWith names records proposed to join this one, not yet
	// accepted; RejectedWith those turned down, never proposed again.
	SuggestedWith []string `json:"suggested_with,omitempty"`
	RejectedWith  []string `json:"rejected_with,omitempty"`
	// Pinned is read from the box's pins, for whoever lists records.
	Pinned bool `json:"pinned,omitempty"`
	// Keys is read from its sessions: each thread key, claimed or mentioned.
	Keys []WorkKey `json:"keys,omitempty"`
	// Manual is set once anyone has changed it: rules stop retitling it.
	Manual  bool      `json:"manual,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// WorkPin is a repo, a project or a work record that stays pinned until
// removed; a pinned work record that closes loses its pin.
type WorkPin struct {
	Kind   string    `json:"kind"`
	Target string    `json:"target"`
	At     time.Time `json:"at"`
}

// WorkSettings are the box's rules for forming records.
type WorkSettings struct {
	// IDPattern is the regular expression that finds a tracker id in a
	// session's title and worktree; empty is DefaultIssuePattern. An id's
	// range or list (X-1..5, X-1/2) follows from its trailing number.
	IDPattern string `json:"id_pattern,omitempty"`
}

type workState struct {
	Records  []*WorkRecord `json:"records"`
	Pins     []WorkPin     `json:"pins,omitempty"`
	Settings WorkSettings  `json:"settings,omitzero"`
	// Deliveries are prompts sent on a thread's behalf (workthreads.go). An
	// older binary rewriting this file drops them and the claims: callers then
	// find no owner and start new, which is what they did before there were any.
	Deliveries []*WorkDelivery `json:"deliveries,omitempty"`
	// Cursor is the journal Seq read up to, for last_seen, and Touch what
	// the reader held then: saved together, so a restart goes on from there.
	Cursor int64       `json:"cursor,omitempty"`
	Touch  *touchState `json:"touch,omitempty"`
}

// workEvent is a journal event a change owes, published once the store's lock
// is released.
type workEvent struct {
	Type string
	Data map[string]any
}

// WorkStore keeps a box's work records in one private file.
type WorkStore struct {
	Path string
	// Box is the box's own name, for its sessions' lines.
	Box string
	// Now, in tests, is the clock.
	Now func() time.Time

	mu      sync.Mutex
	loaded  bool
	loadErr error
	st      workState
	// re finds ids; skipKnown says it's the default, so notIssuePrefixes apply.
	re        *regexp.Regexp
	skipKnown bool
}

func (s *WorkStore) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// lock holds the store, loaded, until the func it returns is called.
func (s *WorkStore) lock() func() {
	s.mu.Lock()
	s.load()
	return s.mu.Unlock
}

// load reads the file, once it has read it right. A file that's there and
// can't be read leaves the store refusing to write, and trying again next
// time: writing now would replace what a person has set with an empty store.
// One that's read and isn't a store is set aside first.
func (s *WorkStore) load() {
	if s.loaded {
		return
	}
	s.st, s.loadErr = workState{}, nil
	defer s.compile()
	b, err := os.ReadFile(s.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		s.loadErr = fmt.Errorf("work records can't be read: %w", err)
		return
	default:
		if err := json.Unmarshal(b, &s.st); err != nil {
			s.st = workState{}
			log.Printf("work: %s can't be read, keeping it as .bad: %v", s.Path, err)
			if err := os.Rename(s.Path, s.Path+".bad"); err != nil {
				s.loadErr = fmt.Errorf("work records can't be read or set aside: %w", err)
				return
			}
		}
	}
	s.loaded = true
}

// ready says the store's file could be read.
func (s *WorkStore) ready() error {
	defer s.lock()()
	return s.loadErr
}

func (s *WorkStore) compile() {
	s.re = nil
	if p := s.st.Settings.IDPattern; p != "" {
		if re, err := compileIssuePattern(p); err == nil {
			s.re = re
		} else {
			log.Printf("work: id pattern ignored: %v", err)
		}
	}
	s.skipKnown = s.re == nil
	if s.re == nil {
		s.re = regexp.MustCompile(DefaultIssuePattern)
	}
}

// ids is issueIDs by the box's own pattern.
func (s *WorkStore) ids(text string) ([]string, string) {
	return issueIDs(s.re, s.skipKnown, text)
}

func (s *WorkStore) save() error {
	if s.loadErr != nil {
		return s.loadErr
	}
	b, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	return statefile.Write(s.Path, append(b, '\n'))
}

func compileIssuePattern(p string) (*regexp.Regexp, error) {
	re, err := regexp.Compile(p)
	if err != nil {
		return nil, badRequest("the id pattern isn't a regular expression: %v", err)
	}
	if re.MatchString("") {
		return nil, badRequest("the id pattern must not match empty text")
	}
	return re, nil
}

var (
	issueMore     = regexp.MustCompile(`^(\.\.|/)(?:([A-Za-z][A-Za-z0-9]*-))?([0-9]+)`)
	issueTrailing = regexp.MustCompile(`^(.*?)([0-9]+)$`)
)

// issueIDs finds the tracker ids in text, with ranges and lists written out
// (X-1..3 is X-1, X-2, X-3; X-1/2 is X-1, X-2), and the first as it was
// written. skipKnown leaves out notIssuePrefixes.
func issueIDs(re *regexp.Regexp, skipKnown bool, text string) (ids []string, first string) {
	seen := map[string]bool{}
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, loc := range re.FindAllStringIndex(text, -1) {
		id := strings.ToUpper(text[loc[0]:loc[1]])
		end := loc[1]
		if m := issueTrailing.FindStringSubmatch(id); m != nil {
			if skipKnown && notIssuePrefixes[strings.TrimSuffix(m[1], "-")] {
				continue
			}
			add(id)
			end += issueTail(text[end:], m[1], m[2], add)
		} else {
			add(id)
		}
		if first == "" {
			first = strings.ToUpper(text[loc[0]:end])
		}
	}
	return ids, first
}

// issueTail adds the ids a range or list after one id names, and says how
// much of rest it read. X-1..100000 is a typo, and adds only its end.
func issueTail(rest, prefix, number string, add func(string)) int {
	last, err := strconv.Atoi(number)
	if err != nil {
		return 0
	}
	read := 0
	for {
		more := issueMore.FindStringSubmatch(rest[read:])
		if more == nil || (more[2] != "" && strings.ToUpper(more[2]) != prefix) {
			return read
		}
		n, err := strconv.Atoi(more[3])
		if err != nil {
			return read
		}
		if more[1] == ".." && n > last && n-last <= issueRangeMax {
			for i := last + 1; i < n; i++ {
				add(prefix + strconv.Itoa(i))
			}
		}
		add(prefix + strconv.Itoa(n))
		last = n
		read += len(more[0])
	}
}

func newWorkID() string { return "w_" + newArtifactID() }

func clipWork(s string) string { return oneLine(s, workTextMax) }

func (s *WorkStore) isOurs(m WorkSession) bool { return m.Box == s.Box }

func (s *WorkStore) find(id string) *WorkRecord {
	for _, r := range s.st.Records {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func (s *WorkStore) mustFind(id string) (*WorkRecord, error) {
	if r := s.find(id); r != nil {
		return r, nil
	}
	return nil, httpError{http.StatusNotFound, "no work record " + id}
}

func (s *WorkStore) pinned(id string) bool {
	return slices.ContainsFunc(s.st.Pins, func(p WorkPin) bool { return p.Kind == PinWork && p.Target == id })
}

func (s *WorkStore) view(r *WorkRecord) WorkRecord {
	out := *r
	out.Sessions = slices.Clone(r.Sessions)
	if out.Sessions == nil {
		out.Sessions = []WorkSession{}
	}
	for i := range out.Sessions {
		m := &out.Sessions[i]
		m.Claims, m.Subscribed, m.Unsubscribed = slices.Clone(m.Claims), slices.Clone(m.Subscribed), slices.Clone(m.Unsubscribed)
	}
	out.Keys = keysOf(r)
	out.IssueIDs = slices.Clone(r.IssueIDs)
	out.SuggestedWith = slices.Clone(r.SuggestedWith)
	out.RejectedWith = slices.Clone(r.RejectedWith)
	out.Pinned = s.pinned(r.ID)
	return out
}

// List is the records, oldest first; closed ones only with all or when status
// asks for them.
func (s *WorkStore) List(status string, all bool) []WorkRecord {
	defer s.lock()()
	out := []WorkRecord{}
	for _, r := range s.st.Records {
		if status != "" && r.Status != status {
			continue
		}
		if status == "" && !all && r.Status == WorkClosed {
			continue
		}
		out = append(out, s.view(r))
	}
	return out
}

// Get is one record.
func (s *WorkStore) Get(id string) (WorkRecord, error) {
	defer s.lock()()
	r, err := s.mustFind(id)
	if err != nil {
		return WorkRecord{}, err
	}
	return s.view(r), nil
}

// WorkSessionRef names a session in a request; no box is this one.
type WorkSessionRef struct {
	Box  string `json:"box,omitempty"`
	Name string `json:"name"`
}

// WorkInput makes a record by hand.
type WorkInput struct {
	Title    string           `json:"title,omitempty"`
	Sessions []WorkSessionRef `json:"sessions,omitempty"`
	Issue    string           `json:"issue,omitempty"`
	Project  string           `json:"project,omitempty"`
}

func (s *WorkStore) ref(r WorkSessionRef) WorkSession {
	box := r.Box
	if box == "" {
		box = s.Box
	}
	return WorkSession{Box: box, Name: r.Name}
}

// take removes a session from the open record that holds it, and returns its
// line: its history goes with it. A record it leaves empty goes too, unless
// someone has given that one state of its own.
func (s *WorkStore) take(box, name string) (WorkSession, bool) {
	for _, r := range s.st.Records {
		if r.Status == WorkClosed {
			continue
		}
		for i, m := range r.Sessions {
			if m.Name == name && m.Box == box {
				r.Sessions = slices.Delete(r.Sessions, i, i+1)
				if len(r.Sessions) == 0 && !r.Manual {
					s.remove(r.ID)
				}
				return m, true
			}
		}
	}
	return WorkSession{}, false
}

// adopt moves a session into r, with its history if another open record has it.
func (s *WorkStore) adopt(r *WorkRecord, m WorkSession) {
	if have, ok := s.take(m.Box, m.Name); ok {
		m = have
	}
	r.Sessions = append(r.Sessions, m)
	r.LastSeen = laterTime(r.LastSeen, m.LastSeen)
}

// remove forgets a record, and any proposal to group with it.
func (s *WorkStore) remove(id string) {
	s.st.Records = slices.DeleteFunc(s.st.Records, func(r *WorkRecord) bool { return r.ID == id })
	for _, o := range s.st.Records {
		o.SuggestedWith = slices.DeleteFunc(o.SuggestedWith, func(x string) bool { return x == id })
	}
}

func (s *WorkStore) setIssue(r *WorkRecord, issue string) {
	r.Issue = clipWork(issue)
	r.IssueIDs, _ = s.ids(r.Issue)
}

// Create makes a record by hand, moving the sessions it names into it.
func (s *WorkStore) Create(in WorkInput) (WorkRecord, []workEvent, error) {
	defer s.lock()()
	now := s.now()
	r := &WorkRecord{ID: newWorkID(), Title: clipWork(in.Title), Status: WorkOpen, Project: clipWork(in.Project), Manual: true, Created: now, Updated: now, Sessions: []WorkSession{}}
	s.setIssue(r, in.Issue)
	s.st.Records = append(s.st.Records, r)
	for _, ref := range in.Sessions {
		s.adopt(r, s.ref(ref))
	}
	if err := s.save(); err != nil {
		return WorkRecord{}, nil, err
	}
	return s.view(r), []workEvent{{"work.created", map[string]any{"id": r.ID, "status": r.Status, "sessions": sessionNames(r.Sessions)}}}, nil
}

func sessionNames(ms []WorkSession) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

// addMissing adds to list what it lacks, and says if there was anything.
func addMissing(list *[]string, more []string) bool {
	added := false
	for _, x := range more {
		if !slices.Contains(*list, x) {
			*list = append(*list, x)
			added = true
		}
	}
	return added
}

func laterTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// WorkPatch changes a record. A field left out stays as it is; an empty one
// is cleared.
type WorkPatch struct {
	Title         *string   `json:"title,omitempty"`
	Status        *string   `json:"status,omitempty"`
	Issue         *string   `json:"issue,omitempty"`
	Project       *string   `json:"project,omitempty"`
	NotUntil      *string   `json:"not_until,omitempty"`
	WaitingOn     *string   `json:"waiting_on,omitempty"`
	CheckBack     *string   `json:"check_back,omitempty"`
	SuggestedWith *[]string `json:"suggested_with,omitempty"`
}

// parseWorkTime takes an RFC 3339 time or a date; empty is no time.
func parseWorkTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, badRequest("%q isn't a date: use YYYY-MM-DD or an RFC 3339 time", s)
}

func validWorkStatus(s string) bool {
	return s == WorkOpen || s == WorkParked || s == WorkWaiting || s == WorkClosed
}

// Update changes a record. A Not now with no status parks it and a Waiting on
// with no status makes it waiting; going back to open clears both. A record
// that closes loses its pin.
func (s *WorkStore) Update(id string, p WorkPatch) (WorkRecord, []workEvent, error) {
	defer s.lock()()
	r, err := s.mustFind(id)
	if err != nil {
		return WorkRecord{}, nil, err
	}
	// Everything is checked first, so a refused request changes nothing.
	var notUntil, checkBack time.Time
	if p.Status != nil && !validWorkStatus(*p.Status) {
		return WorkRecord{}, nil, badRequest("status must be open, parked, waiting or closed")
	}
	if p.NotUntil != nil {
		if notUntil, err = parseWorkTime(*p.NotUntil); err != nil {
			return WorkRecord{}, nil, err
		}
	}
	if p.CheckBack != nil {
		if checkBack, err = parseWorkTime(*p.CheckBack); err != nil {
			return WorkRecord{}, nil, err
		}
	}
	was := r.Status
	fields := []string{}
	if p.Title != nil {
		r.Title = clipWork(*p.Title)
		fields = append(fields, "title")
	}
	if p.Issue != nil {
		s.setIssue(r, *p.Issue)
		fields = append(fields, "issue")
	}
	if p.Project != nil {
		r.Project = clipWork(*p.Project)
		fields = append(fields, "project")
	}
	if p.Status != nil {
		r.Status = *p.Status
		if r.Status == WorkOpen {
			r.NotUntil, r.WaitingOn, r.CheckBack = time.Time{}, "", time.Time{}
		}
		fields = append(fields, "status")
	}
	if p.NotUntil != nil {
		r.NotUntil = notUntil
		if p.Status == nil && !notUntil.IsZero() {
			r.Status = WorkParked
		}
		fields = append(fields, "not_until")
	}
	if p.WaitingOn != nil {
		r.WaitingOn = clipWork(*p.WaitingOn)
		if p.Status == nil && r.WaitingOn != "" {
			r.Status = WorkWaiting
		}
		fields = append(fields, "waiting_on")
	}
	if p.CheckBack != nil {
		r.CheckBack = checkBack
		fields = append(fields, "check_back")
	}
	evs := []workEvent{}
	if p.SuggestedWith != nil {
		r.SuggestedWith = nil
		s.suggest(r, *p.SuggestedWith)
		fields = append(fields, "suggested_with")
		if len(r.SuggestedWith) > 0 {
			evs = append(evs, workEvent{"work.suggested", map[string]any{"id": r.ID, "with": slices.Clone(r.SuggestedWith)}})
		}
	}
	r.Manual, r.Updated = true, s.now()
	evs = append(evs, workEvent{"work.updated", map[string]any{"id": r.ID, "status": r.Status, "fields": fields}})
	if r.Status == WorkClosed && was != WorkClosed {
		evs = append(evs, workEvent{"work.closed", map[string]any{"id": r.ID}})
		if s.pinned(r.ID) {
			s.dropPin(PinWork, r.ID)
			evs = append(evs, workEvent{"work.unpinned", map[string]any{"kind": PinWork, "target": r.ID, "reason": "closed"}})
		}
	}
	if err := s.save(); err != nil {
		return WorkRecord{}, nil, err
	}
	return s.view(r), evs, nil
}

// suggest adds the records a group is proposed with: open ones, never itself,
// and never a pair already proposed or turned down, from either side.
func (s *WorkStore) suggest(r *WorkRecord, ids []string) bool {
	changed := false
	for _, id := range ids {
		o := s.find(id)
		if o == nil || o == r || o.Status == WorkClosed || slices.Contains(r.RejectedWith, id) || slices.Contains(r.SuggestedWith, id) ||
			slices.Contains(o.RejectedWith, r.ID) || slices.Contains(o.SuggestedWith, r.ID) {
			continue
		}
		r.SuggestedWith = append(r.SuggestedWith, id)
		changed = true
	}
	return changed
}

// absorb moves other's sessions, ids and pin into r; other goes.
func (s *WorkStore) absorb(r, other *WorkRecord) {
	r.Sessions = append(r.Sessions, other.Sessions...)
	addMissing(&r.IssueIDs, other.IssueIDs)
	if r.Issue == "" {
		r.Issue = other.Issue
	}
	r.LastSeen = laterTime(r.LastSeen, other.LastSeen)
	if s.pinned(other.ID) {
		s.dropPin(PinWork, other.ID)
		if !s.pinned(r.ID) {
			s.st.Pins = append(s.st.Pins, WorkPin{Kind: PinWork, Target: r.ID, At: s.now()})
		}
	}
	s.remove(other.ID)
}

// Group puts other records and sessions into id. The records go, and id's own
// state stands.
func (s *WorkStore) Group(id string, with []string, sessions []WorkSessionRef) (WorkRecord, []workEvent, error) {
	defer s.lock()()
	return s.group(id, with, sessions, "work.grouped")
}

func (s *WorkStore) group(id string, with []string, sessions []WorkSessionRef, event string) (WorkRecord, []workEvent, error) {
	r, err := s.mustFind(id)
	if err != nil {
		return WorkRecord{}, nil, err
	}
	if r.Status == WorkClosed {
		return WorkRecord{}, nil, httpError{http.StatusConflict, id + " is closed: open it again before grouping into it"}
	}
	if len(with) == 0 && len(sessions) == 0 {
		return WorkRecord{}, nil, badRequest("say what to group into it: other records or sessions")
	}
	for _, other := range with {
		if other == id {
			return WorkRecord{}, nil, badRequest("a record can't be grouped with itself")
		}
		if _, err := s.mustFind(other); err != nil {
			return WorkRecord{}, nil, err
		}
	}
	for _, other := range with {
		// A name given twice is gone the second time.
		if o := s.find(other); o != nil {
			s.absorb(r, o)
		}
	}
	names := []string{}
	for _, ref := range sessions {
		m := s.ref(ref)
		if slices.ContainsFunc(r.Sessions, func(o WorkSession) bool { return o.Name == m.Name && o.Box == m.Box }) {
			continue
		}
		s.adopt(r, m)
		names = append(names, m.Name)
	}
	r.Manual, r.Updated = true, s.now()
	if err := s.save(); err != nil {
		return WorkRecord{}, nil, err
	}
	return s.view(r), []workEvent{{event, map[string]any{"id": r.ID, "with": with, "sessions": names}}}, nil
}

// Ungroup moves sessions out of id into a record each, kept apart from then
// on: rules only place a session that has no record.
func (s *WorkStore) Ungroup(id string, names []string) ([]WorkRecord, []workEvent, error) {
	defer s.lock()()
	r, err := s.mustFind(id)
	if err != nil {
		return nil, nil, err
	}
	if len(names) == 0 {
		return nil, nil, badRequest("say which sessions to take out")
	}
	for _, name := range names {
		if !slices.ContainsFunc(r.Sessions, func(m WorkSession) bool { return m.Name == name }) {
			return nil, nil, httpError{http.StatusNotFound, name + " isn't in " + id}
		}
	}
	if !slices.ContainsFunc(r.Sessions, func(m WorkSession) bool { return !slices.Contains(names, m.Name) }) {
		return nil, nil, badRequest("that would leave %s with no session: leave at least one", id)
	}
	now := s.now()
	out, made := []WorkRecord{}, []string{}
	for _, name := range names {
		i := slices.IndexFunc(r.Sessions, func(m WorkSession) bool { return m.Name == name })
		if i < 0 {
			continue // named twice: already out
		}
		m := r.Sessions[i]
		r.Sessions = slices.Delete(r.Sessions, i, i+1)
		n := &WorkRecord{ID: newWorkID(), Title: m.Title, Status: WorkOpen, Sessions: []WorkSession{m}, LastSeen: m.LastSeen, Manual: true, Created: now, Updated: now}
		s.st.Records = append(s.st.Records, n)
		out = append(out, s.view(n))
		made = append(made, n.ID)
	}
	r.Manual, r.Updated = true, now
	if err := s.save(); err != nil {
		return nil, nil, err
	}
	return out, []workEvent{{"work.ungrouped", map[string]any{"id": id, "sessions": names, "records": made}}}, nil
}

// Accept groups the records suggested with id into it.
func (s *WorkStore) Accept(id string) (WorkRecord, []workEvent, error) {
	defer s.lock()()
	r, err := s.mustFind(id)
	if err != nil {
		return WorkRecord{}, nil, err
	}
	if len(r.SuggestedWith) == 0 {
		return WorkRecord{}, nil, httpError{http.StatusConflict, id + " has no suggestion to accept"}
	}
	return s.group(id, slices.Clone(r.SuggestedWith), nil, "work.accepted")
}

// Reject turns down what is suggested with id, for good.
func (s *WorkStore) Reject(id string) (WorkRecord, []workEvent, error) {
	defer s.lock()()
	r, err := s.mustFind(id)
	if err != nil {
		return WorkRecord{}, nil, err
	}
	if len(r.SuggestedWith) == 0 {
		return WorkRecord{}, nil, httpError{http.StatusConflict, id + " has no suggestion to reject"}
	}
	with := r.SuggestedWith
	r.RejectedWith = append(r.RejectedWith, with...)
	r.SuggestedWith = nil
	r.Manual, r.Updated = true, s.now()
	if err := s.save(); err != nil {
		return WorkRecord{}, nil, err
	}
	return s.view(r), []workEvent{{"work.rejected", map[string]any{"id": r.ID, "with": with}}}, nil
}

// Pins are the box's pins, oldest first.
func (s *WorkStore) Pins() []WorkPin {
	defer s.lock()()
	return append([]WorkPin{}, s.st.Pins...)
}

func (s *WorkStore) dropPin(kind, target string) bool {
	n := len(s.st.Pins)
	s.st.Pins = slices.DeleteFunc(s.st.Pins, func(p WorkPin) bool { return p.Kind == kind && p.Target == target })
	return len(s.st.Pins) != n
}

// Pin keeps a repo, a project or an open work record pinned.
func (s *WorkStore) Pin(kind, target string) (WorkPin, []workEvent, error) {
	defer s.lock()()
	target = clipWork(target)
	if kind != PinRepo && kind != PinProject && kind != PinWork {
		return WorkPin{}, nil, badRequest("a pin is a repo, a project or a work record")
	}
	if target == "" {
		return WorkPin{}, nil, badRequest("say what to pin")
	}
	if kind == PinWork {
		r, err := s.mustFind(target)
		if err != nil {
			return WorkPin{}, nil, err
		}
		if r.Status == WorkClosed {
			return WorkPin{}, nil, httpError{http.StatusConflict, target + " is closed: a closed record can't be pinned"}
		}
		// Someone cares about it now: rules leave it where it is.
		r.Manual = true
	}
	for _, p := range s.st.Pins {
		if p.Kind == kind && p.Target == target {
			return p, nil, nil
		}
	}
	p := WorkPin{Kind: kind, Target: target, At: s.now()}
	s.st.Pins = append(s.st.Pins, p)
	if err := s.save(); err != nil {
		return WorkPin{}, nil, err
	}
	return p, []workEvent{{"work.pinned", map[string]any{"kind": kind, "target": target}}}, nil
}

// Unpin removes a pin; one that isn't there is fine.
func (s *WorkStore) Unpin(kind, target string) ([]workEvent, error) {
	defer s.lock()()
	if !s.dropPin(kind, target) {
		return nil, nil
	}
	if err := s.save(); err != nil {
		return nil, err
	}
	return []workEvent{{"work.unpinned", map[string]any{"kind": kind, "target": target}}}, nil
}

// Settings are the box's rules for forming records.
func (s *WorkStore) Settings() WorkSettings {
	defer s.lock()()
	return s.st.Settings
}

// SetSettings changes them. Records already formed stay as they are.
func (s *WorkStore) SetSettings(in WorkSettings) (WorkSettings, error) {
	defer s.lock()()
	if in.IDPattern != "" {
		if _, err := compileIssuePattern(in.IDPattern); err != nil {
			return WorkSettings{}, err
		}
	}
	s.st.Settings = in
	s.compile()
	return in, s.save()
}

// Seen is an explicit visit: the record and its running sessions were looked
// at, at a person's request.
func (s *WorkStore) Seen(id string) (WorkRecord, []workEvent, error) {
	defer s.lock()()
	r, err := s.mustFind(id)
	if err != nil {
		return WorkRecord{}, nil, err
	}
	at := s.now()
	r.LastSeen = laterTime(r.LastSeen, at)
	for i := range r.Sessions {
		if r.Sessions[i].Ended.IsZero() {
			r.Sessions[i].LastSeen = laterTime(r.Sessions[i].LastSeen, at)
		}
	}
	if err := s.save(); err != nil {
		return WorkRecord{}, nil, err
	}
	return s.view(r), []workEvent{{"work.seen", map[string]any{"id": r.ID, "source": "visit"}}}, nil
}

// touchSlack allows for a session's clock: tmux keeps its start in seconds,
// and a send can be noted just before the session exists.
const touchSlack = time.Minute

// touch notes a person's prompt or answer to a session. A touch from before
// the session's own start is for an earlier session of that name.
func (s *WorkStore) touch(name string, at time.Time) (string, bool) {
	var best *WorkRecord
	bestAt := -1
	for _, r := range s.st.Records {
		for i, m := range r.Sessions {
			if m.Name != name || !s.isOurs(m) || (!m.Created.IsZero() && at.Before(m.Created.Add(-touchSlack))) {
				continue
			}
			// The line still running, if one is: an ended one is its past.
			if best == nil || m.Ended.IsZero() {
				best, bestAt = r, i
			}
		}
	}
	if best == nil || !at.After(best.Sessions[bestAt].LastSeen) {
		return "", false
	}
	best.Sessions[bestAt].LastSeen = at
	best.LastSeen = laterTime(best.LastSeen, at)
	return best.ID, true
}

// Touch notes a person's prompt or answer and keeps it. It says which record
// moved; a repeat, or a session with no record, moves none.
func (s *WorkStore) Touch(name string, at time.Time) (string, error) {
	defer s.lock()()
	id, ok := s.touch(name, at)
	if !ok {
		return "", nil
	}
	return id, s.save()
}

// touchQuietly is Touch for a catch-up from the journal: Flush keeps them
// all at once, at the end.
func (s *WorkStore) touchQuietly(name string, at time.Time) {
	defer s.lock()()
	s.touch(name, at)
}

// holds says a session has a record, so it's worth finding out who typed.
func (s *WorkStore) holds(name string) bool {
	defer s.lock()()
	for _, r := range s.st.Records {
		for _, m := range r.Sessions {
			if m.Name == name && s.isOurs(m) {
				return true
			}
		}
	}
	return false
}

// resume is where the journal's reader left off.
func (s *WorkStore) resume() (int64, *touchState) {
	defer s.lock()()
	return s.st.Cursor, s.st.Touch
}

// Cursor is the journal Seq the box has read for last_seen.
func (s *WorkStore) Cursor() int64 {
	cursor, _ := s.resume()
	return cursor
}

// Flush keeps how far the reader is, what it holds, and whatever a catch-up
// noted, in one write.
func (s *WorkStore) Flush(cursor int64, touch *touchState) error {
	defer s.lock()()
	s.st.Cursor, s.st.Touch = max(s.st.Cursor, cursor), touch
	return s.save()
}

// sessionFacts is what the rules read of a session.
type sessionFacts struct {
	sess     Session
	worktree string
	title    string
	ids      []string
	issue    string
}

func (s *WorkStore) facts(sess Session) sessionFacts {
	f := sessionFacts{sess: sess, title: clipWork(sess.Title)}
	text, fallback := sess.Title, sess.Name
	// A worktree is location/name: a repository's main checkout, or a folder,
	// is shared by sessions that have nothing to do with each other.
	if _, wt, ok := strings.Cut(sess.Location, "/"); ok && wt != "" {
		f.worktree = sess.Location
		text += "\n" + wt
		fallback = wt
	}
	f.ids, f.issue = s.ids(text)
	if f.title == "" {
		f.title = fallback
	}
	return f
}

// refresh writes what it is now onto a session's line, and says if that moved.
func (f sessionFacts) refresh(m *WorkSession) bool {
	changed := m.Title != f.title || m.Worktree != f.worktree || !m.Created.Equal(f.sess.Created) || (m.StartedBy == "" && f.sess.StartedBy != "")
	// A new session under an old name didn't claim what that one did.
	if !m.sameRun(f.sess.Created) {
		m.Claims, m.Subscribed, m.Unsubscribed, m.Conversation = nil, nil, nil, ""
	}
	m.Title, m.Worktree, m.Created = f.title, f.worktree, f.sess.Created
	if m.StartedBy == "" {
		m.StartedBy = f.sess.StartedBy
	}
	if c := conversationOf(f.sess); c != "" && m.Conversation != c {
		m.Conversation, changed = c, true
	}
	// Naming a key follows it. It never claims it: a title can be a guess.
	for _, id := range f.ids {
		if !slices.Contains(m.Unsubscribed, id) && addMissing(&m.Subscribed, []string{id}) {
			changed = true
		}
	}
	return changed
}

// matching is the open records naming one of ids, oldest first.
func (s *WorkStore) matching(ids []string, except *WorkRecord) []*WorkRecord {
	var out []*WorkRecord
	for _, r := range s.st.Records {
		if r != except && r.Status != WorkClosed && slices.ContainsFunc(ids, r.names) {
			out = append(out, r)
		}
	}
	return out
}

func (s *WorkStore) holding(match func(WorkSession) bool) *WorkRecord {
	for _, r := range s.st.Records {
		if r.Status != WorkClosed && slices.ContainsFunc(r.Sessions, match) {
			return r
		}
	}
	return nil
}

// addIDs adds the ids a session names to its record. A session naming more
// than the record's issue says describes the work better, whichever came
// first (a title arrives just after its session starts), unless someone has
// said what this record is.
func (s *WorkStore) addIDs(r *WorkRecord, f sessionFacts) bool {
	changed := addMissing(&r.IssueIDs, f.ids)
	if r.Issue == "" && f.issue != "" {
		r.Issue, changed = f.issue, true
	}
	if !r.Manual && len(f.ids) > 1 && r.Issue != f.issue {
		if have, _ := s.ids(r.Issue); len(f.ids) > len(have) {
			r.Issue, r.Title, changed = f.issue, f.title, true
		}
	}
	return changed
}

// place puts a session with no record where the rules say: with the oldest
// open record naming its issue id, else one holding its worktree, else its
// parent's, else in a record of its own. A session whose ids span several
// records joins the oldest and proposes the rest: each has its own state, so
// only a person can say they are one.
func (s *WorkStore) place(f sessionFacts, now time.Time, evs *[]workEvent) {
	m := WorkSession{Box: s.Box, Name: f.sess.Name}
	f.refresh(&m)
	var r *WorkRecord
	how := ""
	if hits := s.matching(f.ids, nil); len(hits) > 0 {
		r, how = hits[0], "issue"
		s.proposeBridge(r, hits[1:], evs)
	}
	if r == nil && f.worktree != "" {
		if r = s.holding(func(o WorkSession) bool { return o.Worktree == f.worktree }); r != nil {
			how = "worktree"
		}
	}
	if r == nil && f.sess.StartedBy != "" {
		if r = s.holding(func(o WorkSession) bool { return o.Name == f.sess.StartedBy && s.isOurs(o) }); r != nil {
			how = "parent"
		}
	}
	if r != nil {
		s.join(r, m, f, how, now, evs)
		return
	}
	r = &WorkRecord{ID: newWorkID(), Title: f.title, Status: WorkOpen, Created: now, Updated: now, Sessions: []WorkSession{m}}
	s.st.Records = append(s.st.Records, r)
	s.addIDs(r, f)
	*evs = append(*evs, workEvent{"work.created", map[string]any{"id": r.ID, "status": r.Status, "sessions": []string{m.Name}, "rule": true}})
}

func (s *WorkStore) join(r *WorkRecord, m WorkSession, f sessionFacts, how string, now time.Time, evs *[]workEvent) {
	r.Sessions = append(r.Sessions, m)
	s.addIDs(r, f)
	r.LastSeen = laterTime(r.LastSeen, m.LastSeen)
	r.Updated = now
	*evs = append(*evs, workEvent{"work.joined", map[string]any{"id": r.ID, "session": m.Name, "by": how}})
}

func (s *WorkStore) proposeBridge(r *WorkRecord, others []*WorkRecord, evs *[]workEvent) {
	ids := make([]string, 0, len(others))
	for _, o := range others {
		ids = append(ids, o.ID)
	}
	if s.suggest(r, ids) {
		*evs = append(*evs, workEvent{"work.suggested", map[string]any{"id": r.ID, "with": slices.Clone(r.SuggestedWith)}})
	}
}

// holdsLive says an open record other than r has that session running.
func (s *WorkStore) holdsLive(r *WorkRecord, name string) bool {
	return slices.ContainsFunc(s.st.Records, func(o *WorkRecord) bool {
		return o != r && o.Status != WorkClosed && slices.ContainsFunc(o.Sessions, func(m WorkSession) bool {
			return m.Name == name && s.isOurs(m) && m.Ended.IsZero()
		})
	})
}

// Reconcile brings the records up to date with the box's sessions: a session
// that has gone is marked ended, an agent session with no record gets one by
// the rules, and a record no one has touched follows its one session's title.
// It only writes when something changed.
func (s *WorkStore) Reconcile(all []Session) ([]workEvent, error) {
	defer s.lock()()
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	now := s.now()
	live := make(map[string]Session, len(all))
	for _, sess := range all {
		live[sess.Name] = sess
	}
	changed := false
	var evs []workEvent
	placed := map[string]bool{}
	for _, r := range s.st.Records {
		for i := range r.Sessions {
			m := &r.Sessions[i]
			if !s.isOurs(*m) {
				continue
			}
			sess, ok := live[m.Name]
			switch {
			case !ok, r.Status == WorkClosed && !m.sameRun(sess.Created):
				// Gone; or, in a closed record, a new session under an old
				// name: that record's line has ended, and this is new work.
				if m.Ended.IsZero() {
					m.Ended, changed = now, true
				}
			case r.Status == WorkClosed:
				placed[m.Name] = true
			default:
				// Back under the same name (a resumed conversation) is still
				// this work, unless another record has it by now: this one
				// closed, its name started anew, and then this was reopened.
				if !m.Ended.IsZero() {
					if s.holdsLive(r, m.Name) {
						continue
					}
					m.Ended, changed = time.Time{}, true
				}
				placed[m.Name] = true
				if s.facts(sess).refresh(m) {
					changed = true
				}
			}
		}
	}
	// New sessions: the widest ids first, so a range forms the record its
	// singles then join, oldest next, parents before their children.
	var fresh []sessionFacts
	for _, sess := range all {
		if !placed[sess.Name] && agentFor(sess) != "" {
			fresh = append(fresh, s.facts(sess))
		}
	}
	slices.SortStableFunc(fresh, func(a, b sessionFacts) int {
		return cmp.Or(cmp.Compare(len(b.ids), len(a.ids)), a.sess.Created.Compare(b.sess.Created))
	})
	var put func(f sessionFacts)
	put = func(f sessionFacts) {
		if placed[f.sess.Name] {
			return
		}
		placed[f.sess.Name] = true
		if parent := f.sess.StartedBy; parent != "" {
			if i := slices.IndexFunc(fresh, func(o sessionFacts) bool { return o.sess.Name == parent }); i >= 0 {
				put(fresh[i])
			}
		}
		s.place(f, now, &evs)
	}
	for _, f := range fresh {
		put(f)
	}
	changed = s.follow(live, now, &evs) || changed || len(evs) > 0
	if !changed {
		return nil, nil
	}
	return evs, s.save()
}

// follow keeps records nobody has touched in step with their sessions, whose
// titles often come just after they start: a lone session's title is its
// record's, and an id it comes to name can join it to the record for that id.
func (s *WorkStore) follow(live map[string]Session, now time.Time, evs *[]workEvent) bool {
	changed := false
	for _, r := range slices.Clone(s.st.Records) {
		if r.Status == WorkClosed || r.Manual {
			continue
		}
		for _, m := range r.Sessions {
			sess, ok := live[m.Name]
			if !ok || !s.isOurs(m) {
				continue
			}
			f := s.facts(sess)
			lone := len(r.Sessions) == 1
			if lone && r.Title != f.title {
				r.Title, changed = f.title, true
			}
			hits := s.matching(f.ids, r)
			named := slices.ContainsFunc(f.ids, func(id string) bool { return !slices.Contains(r.IssueIDs, id) })
			if lone && named && len(hits) > 0 {
				s.remove(r.ID)
				s.proposeBridge(hits[0], hits[1:], evs)
				s.join(hits[0], m, f, "issue", now, evs)
				changed = true
				continue
			}
			if s.addIDs(r, f) {
				changed = true
			}
			s.proposeBridge(r, hits, evs)
		}
	}
	return changed
}
