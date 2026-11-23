package box

import (
	"context"
	"maps"
	"net/http"
	"strings"

	"github.com/cosscom/shipyard/internal/wire"
)

// Which session started which. A new session's parent reaches tmux through
// the request's context, past the several ways a session starts, and lasts as
// long as the session does as a tmux option, across restarts of the daemon.

// labelStartedBy is the tmux option that keeps it.
const labelStartedBy = "@berth_started_by"

// varStartedBy is the run variable that carries it: a run's sessions start
// long after the request that asked for them.
const varStartedBy = "started_by"

type startedByKey struct{}

func withStartedBy(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, startedByKey{}, name)
}

// withStartedByLabel adds the context's parent to a new session's options.
func withStartedByLabel(ctx context.Context, labels map[string]string) map[string]string {
	by, _ := ctx.Value(startedByKey{}).(string)
	if by == "" {
		return labels
	}
	out := make(map[string]string, len(labels)+1)
	maps.Copy(out, labels)
	out[labelStartedBy] = by
	return out
}

// callerSession is the session asking for a new one: the name the request
// gives, or the CallerHeader. Only a caller on the box's own socket can name
// one, and only a session on this box counts: anyone else's word for it means
// nothing here.
func (b *Box) callerSession(r *http.Request, named string) string {
	if !wire.IsLocal(r.Context()) {
		return ""
	}
	name := strings.TrimSpace(firstNonEmpty(named, r.Header.Get(CallerHeader)))
	if !sessionName.MatchString(name) {
		return ""
	}
	if _, err := b.Sessions.Get(r.Context(), name); err != nil {
		return ""
	}
	return name
}
