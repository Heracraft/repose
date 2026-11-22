package vsockrpc

import (
	"context"
	"net"
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
)

func progressNote(line string) *guestdv1.Notify {
	return &guestdv1.Notify{N: &guestdv1.Notify_ApplyMenuProgress{ApplyMenuProgress: &guestdv1.ApplyMenuProgress{Line: line}}}
}

// An ApplyMenu's progress arrives as Notify frames under its own
// request_id (DECISIONS I-1460): CallStream hands them to the caller in
// order, none reaches the guest's notification stream, and one naming a
// request nobody is waiting for is dropped.
func TestCallStreamProgress(t *testing.T) {
	a, b := net.Pipe()
	var srv *ServerConn
	ready := make(chan struct{})
	srv = Serve(context.Background(), b, HandlerFunc(func(ctx context.Context, req *guestdv1.Request) *guestdv1.Response {
		<-ready
		id := RequestID(ctx)
		_ = srv.NotifyFor("someone-else", progressNote("stray"))
		for _, l := range []string{"one", "two", "three"} {
			_ = srv.NotifyFor(id, progressNote(l))
		}
		return &guestdv1.Response{Ok: true, Result: &guestdv1.Response_ApplyMenu{ApplyMenu: &guestdv1.ApplyMenuProgress{Done: true}}}
	}))
	close(ready)
	cl := NewClient(a, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got []string
	resp, err := cl.CallStream(ctx, &guestdv1.Request{Req: &guestdv1.Request_ApplyMenu{ApplyMenu: &guestdv1.ApplyMenu{}}}, func(n *guestdv1.Notify) {
		got = append(got, n.GetApplyMenuProgress().GetLine())
	})
	if err != nil || !resp.GetApplyMenu().GetDone() {
		t.Fatalf("resp %v err %v", resp, err)
	}
	if len(got) != 3 || got[0] != "one" || got[2] != "three" {
		t.Fatalf("progress %v", got)
	}
	select {
	case n := <-cl.Notifications():
		t.Fatalf("progress leaked into notifications: %v", n)
	case <-time.After(50 * time.Millisecond):
	}
	_ = srv.Close()
}
