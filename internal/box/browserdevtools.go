package box

import (
	"net/http"
)

// The app's Browser tab can show the agent's browser live; its Console and
// Network drawer then shows what that page said: the console lines and the
// failed requests the box keeps for `berthd browser console` and
// `berthd browser network`. Reading them here never marks them seen, so the
// agent still gets them on its next look.

// BrowserDevtools is what the agent's page said, oldest first.
type BrowserDevtools struct {
	Running  bool             `json:"running"`
	URL      string           `json:"url,omitempty"`
	Console  []DevtoolsLine   `json:"console"`
	Failures []DevtoolsFailed `json:"failures"`
}

// DevtoolsLine is a console line: its level is error, warning or log.
type DevtoolsLine struct {
	Seq   int    `json:"seq"`
	Level string `json:"level"`
	Text  string `json:"text"`
	Count int    `json:"count"`
}

// DevtoolsFailed is a request that failed: "404 GET http://…", or the
// network's error and the request.
type DevtoolsFailed struct {
	Seq  int    `json:"seq"`
	Text string `json:"text"`
}

func (br *browser) devtools() BrowserDevtools {
	br.mu.Lock()
	defer br.mu.Unlock()
	d := BrowserDevtools{Running: true, URL: br.url, Console: []DevtoolsLine{}, Failures: []DevtoolsFailed{}}
	for _, e := range br.console {
		d.Console = append(d.Console, DevtoolsLine{Seq: e.seq, Level: e.level, Text: e.text, Count: e.count})
	}
	for _, f := range br.failures {
		d.Failures = append(d.Failures, DevtoolsFailed{Seq: f.seq, Text: f.text})
	}
	return d
}

// browserDevtools answers GET …/browser/devtools. It neither starts a
// browser nor keeps one open: watching is not using it.
func (b *Box) browserDevtools(w http.ResponseWriter, r *http.Request) error {
	_, wt, err := b.browserTarget(r)
	if err != nil {
		return err
	}
	tab, err := browserTab(r)
	if err != nil {
		return err
	}
	br := b.Browsers.Lookup(wt.Path, tab)
	if br == nil {
		writeJSON(w, BrowserDevtools{Console: []DevtoolsLine{}, Failures: []DevtoolsFailed{}})
		return nil
	}
	writeJSON(w, br.devtools())
	return nil
}
