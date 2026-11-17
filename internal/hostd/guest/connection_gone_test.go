package guest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/heracraft/repose/internal/hostd/vsockclient"
	"github.com/heracraft/repose/internal/hostd/vsockrpc"
)

// stubSession is a Session whose Done the test controls; no other method
// is called.
type stubSession struct {
	vsockclient.Session
	done chan struct{}
}

func (s stubSession) Done() <-chan struct{} { return s.done }

// The wake's "try the next session" decision reads the call's error as
// well as Done: on CI the clock step's Exec came back with EOF while Done
// was still open, and the wake failed where it should have retried
// (DECISIONS I-1264).
func TestConnectionGoneReadsTheErrorBeforeDone(t *testing.T) {
	open := stubSession{done: make(chan struct{})}
	closed := stubSession{done: make(chan struct{})}
	close(closed.done)
	for _, c := range []struct {
		name string
		s    vsockclient.Session
		err  error
		want bool
	}{
		{"EOF with Done open, the CI ordering", open, fmt.Errorf("vsockrpc: %w", io.EOF), true},
		{"closed connection with Done open", open, fmt.Errorf("vsockrpc: %w", vsockrpc.ErrClosed), true},
		{"Done closed, whatever the error", closed, context.DeadlineExceeded, true},
		{"a timeout on a live connection", open, context.DeadlineExceeded, false},
		{"a guestd error on a live connection", open, &vsockrpc.RemoteError{Code: "internal", Message: "x"}, false},
		{"no error", closed, nil, false},
	} {
		if got := connectionGone(c.s, c.err); got != c.want {
			t.Errorf("%s: connectionGone = %v, want %v", c.name, got, c.want)
		}
	}
	if !errors.Is(fmt.Errorf("vsockrpc: %w", io.EOF), io.EOF) {
		t.Fatal("the client's wrapping does not keep EOF")
	}
}
