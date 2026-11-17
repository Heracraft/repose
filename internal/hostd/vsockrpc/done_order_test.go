package vsockrpc

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
)

// When the connection ends under a call, Done is already closed by the
// time that call returns its error: a caller that reads the error and
// then asks Done must not see a live connection (DECISIONS I-1264).
func TestDoneClosedBeforeTheCallReturns(t *testing.T) {
	for i := 0; i < 200; i++ {
		a, b := net.Pipe()
		cl := NewClient(a, 8)
		errc := make(chan error, 1)
		go func() {
			_, err := cl.Call(context.Background(), &guestdv1.Request{Req: &guestdv1.Request_Ping{Ping: &guestdv1.Ping{}}})
			errc <- err
		}()
		// Read the request so the Write completes, then end the connection.
		buf := make([]byte, 4096)
		if _, err := b.Read(buf); err != nil {
			t.Fatal(err)
		}
		_ = b.Close()
		err := <-errc
		if !errors.Is(err, io.EOF) {
			t.Fatalf("call error %v, want EOF", err)
		}
		select {
		case <-cl.Done():
		default:
			t.Fatalf("round %d: the call returned %v with Done still open", i, err)
		}
	}
}
