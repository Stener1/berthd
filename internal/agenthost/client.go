package agenthost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"
)

// ErrNoHost is a socket no host answers on: the agent has ended (its log
// is still there to read), or never started.
var ErrNoHost = errors.New("agent-host: no host is running")

// Dial connects to the host on sock: ErrNoHost when none answers.
func Dial(ctx context.Context, sock string) (net.Conn, error) { return dial(ctx, sock) }

func dial(ctx context.Context, sock string) (net.Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, ErrNoHost
	}
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	}
	return c, nil
}

func call(ctx context.Context, sock string, req request) (reply, error) {
	c, err := dial(ctx, sock)
	if err != nil {
		return reply{}, err
	}
	defer c.Close()
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return reply{}, err
	}
	var rep reply
	if err := json.NewDecoder(bufio.NewReader(c)).Decode(&rep); err != nil {
		return reply{}, err
	}
	if rep.Error != "" {
		return rep, errors.New(rep.Error)
	}
	return rep, nil
}

// Write gives the agent one JSON line on its stdin, and returns the log
// entry it became.
func Write(ctx context.Context, sock string, data json.RawMessage) (int64, error) {
	rep, err := call(ctx, sock, request{Op: "write", Data: data})
	return rep.Seq, err
}

// Stop ends the agent and waits for it to be gone.
func Stop(ctx context.Context, sock string) error {
	_, err := call(ctx, sock, request{Op: "stop"})
	return err
}

// Attach calls fn with each log entry after from, then each new one as the
// agent works, until the stream ends (nil), ctx ends or fn fails. The
// stream ends after the agent's "exited" entry; one that ends before it is
// a host that let a reader fall behind go, or went away: attach again from
// the last Seq seen. With no host running it is ErrNoHost: read the log
// file instead.
func Attach(ctx context.Context, sock string, from int64, fn func(Entry) error) error {
	c, err := dial(ctx, sock)
	if err != nil {
		return err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.SetReadDeadline(time.Now()) })
	defer stop()
	if err := json.NewEncoder(c).Encode(request{Op: "attach", From: from}); err != nil {
		return err
	}
	dec := json.NewDecoder(bufio.NewReaderSize(c, 64<<10))
	for {
		var e Entry
		if err := dec.Decode(&e); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
		if e.Seq == 0 {
			return errors.New("agent-host: the host refused to attach")
		}
		if err := fn(e); err != nil {
			return err
		}
	}
}
