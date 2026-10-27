package postgres

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cosscom/shipyard/internal/database"
)

const (
	// idleFor is how long a connection nobody uses is kept. An editor
	// that is closed holds no connection to the database a minute later.
	idleFor = time.Minute
	// maxMessage is the largest message taken from the server: one row.
	// A larger one ends the statement instead of the helper's memory.
	maxMessage = 64 << 20
)

// pool is a few connections to one database, made as they are needed.
type pool struct {
	cfg *pgconn.Config
	// sem holds a place for each connection in use.
	sem chan struct{}

	mu     sync.Mutex
	idle   []idleConn
	busy   map[*pgconn.PgConn]struct{}
	closed bool
}

type idleConn struct {
	pc    *pgconn.PgConn
	since time.Time
}

var errClosed = errors.New("the connection was closed")

func newPool(cfg *pgconn.Config, size int) *pool {
	return &pool{cfg: cfg, sem: make(chan struct{}, size), busy: map[*pgconn.PgConn]struct{}{}}
}

// acquire gives a connection that answers: one that was kept, or a new one.
func (p *pool) acquire(ctx context.Context) (*pgconn.PgConn, error) {
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			<-p.sem
			return nil, errClosed
		}
		var kept idleConn
		if n := len(p.idle); n > 0 {
			kept = p.idle[n-1]
			p.idle = p.idle[:n-1]
			p.busy[kept.pc] = struct{}{}
		}
		p.mu.Unlock()
		if kept.pc == nil {
			break
		}
		// One that has sat for a while may have lost its server.
		if time.Since(kept.since) < 2*time.Second || kept.pc.Ping(ctx) == nil {
			return kept.pc, nil
		}
		p.forget(kept.pc)
		closeNow(kept.pc)
	}
	pc, err := pgconn.ConnectConfig(ctx, p.cfg)
	if err != nil {
		<-p.sem
		return nil, database.Unavailable(whyNot(err))
	}
	pc.Frontend().SetMaxBodyLen(maxMessage)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		closeNow(pc)
		<-p.sem
		return nil, errClosed
	}
	p.busy[pc] = struct{}{}
	p.mu.Unlock()
	return pc, nil
}

func (p *pool) forget(pc *pgconn.PgConn) {
	p.mu.Lock()
	delete(p.busy, pc)
	p.mu.Unlock()
}

// release takes a connection back. It is kept only when the caller says it
// is as it was found: idle, in no transaction, with no session state of a
// stranger's statement.
func (p *pool) release(pc *pgconn.PgConn, reuse bool) {
	p.mu.Lock()
	delete(p.busy, pc)
	keep := reuse && !p.closed && !pc.IsClosed() && pc.TxStatus() == 'I'
	if keep {
		p.idle = append(p.idle, idleConn{pc, time.Now()})
	}
	p.mu.Unlock()
	if keep {
		time.AfterFunc(idleFor+time.Second, p.reap)
	} else {
		closeNow(pc)
	}
	<-p.sem
}

// reap closes the connections nobody has used for idleFor.
func (p *pool) reap() {
	var old []*pgconn.PgConn
	p.mu.Lock()
	kept := p.idle[:0]
	for _, c := range p.idle {
		if time.Since(c.since) >= idleFor {
			old = append(old, c.pc)
		} else {
			kept = append(kept, c)
		}
	}
	p.idle = kept
	p.mu.Unlock()
	for _, pc := range old {
		closeNow(pc)
	}
}

// close ends every connection. One in the middle of a statement loses its
// socket, which ends the statement for whoever is waiting on it.
func (p *pool) close() {
	p.mu.Lock()
	p.closed = true
	idle := p.idle
	p.idle = nil
	for pc := range p.busy {
		pc.Conn().Close()
	}
	p.mu.Unlock()
	for _, c := range idle {
		closeNow(c.pc)
	}
}

// open is how many connections there are, for tests.
func (p *pool) open() (idle, busy int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle), len(p.busy)
}

func closeNow(pc *pgconn.PgConn) {
	ctx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	pc.Close(ctx)
}

// whyNot says why a connection could not be made, in words that never hold
// the password: pgconn's own error names the settings it used.
func whyNot(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Message + "."
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "it did not answer in time."
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return op.Err.Error() + "."
	}
	return "the connection failed."
}
