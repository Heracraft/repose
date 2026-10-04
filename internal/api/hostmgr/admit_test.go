package hostmgr_test

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/heracraft/repose/internal/api/hostmgr"
	"github.com/heracraft/repose/internal/api/store"
	fakehostd "github.com/heracraft/repose/internal/fakes/hostd"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

func (h *harness) register(t *testing.T, name string) (uuid.UUID, tls.Certificate) {
	t.Helper()
	ctx := context.Background()
	token, err := hostmgr.MintJoinToken(ctx, h.pool, name, "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hostdv1.NewHostServiceClient(h.dial(t, nil)).Register(ctx, &hostdv1.RegisterRequest{JoinToken: token, Info: &hostdv1.HostInfo{Hostname: name}})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(resp.ClientCert, resp.ClientKey)
	if err != nil {
		t.Fatal(err)
	}
	return uuid.MustParse(resp.HostId), cert
}

func (h *harness) rotate(t *testing.T, cert tls.Certificate) (tls.Certificate, error) {
	t.Helper()
	resp, err := hostdv1.NewHostServiceClient(h.dial(t, &cert)).Rotate(context.Background(), &hostdv1.RegisterRequest{})
	if err != nil {
		return tls.Certificate{}, err
	}
	next, err := tls.X509KeyPair(resp.ClientCert, resp.ClientKey)
	if err != nil {
		t.Fatal(err)
	}
	return next, nil
}

// sessionCode opens a stream with cert, says hello, and returns the code
// the api ends it with; codes.OK means it was still open after a second.
func (h *harness) sessionCode(t *testing.T, cert tls.Certificate) codes.Code {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s, err := hostdv1.NewHostServiceClient(h.dial(t, &cert)).Session(ctx)
	if err != nil {
		return status.Code(err)
	}
	_ = s.Send(&hostdv1.HostMessage{Msg: &hostdv1.HostMessage_Hello{Hello: &hostdv1.Hello{}}})
	_, err = s.Recv()
	if status.Code(err) == codes.DeadlineExceeded {
		return codes.OK
	}
	return status.Code(err)
}

// The api admits a host's current certificate and the one its last
// rotate replaced, until the host connects with the new one; anything
// older is refused (I-432).
func TestHostCertificateSerialIsEnforced(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	hostID, a := h.register(t, "host-01")
	b, err := h.rotate(t, a)
	if err != nil {
		t.Fatal(err)
	}
	row, _ := store.GetHost(ctx, h.pool, hostID)
	if row.CertSerial == nil || row.PrevCertSerial == nil || *row.CertSerial == *row.PrevCertSerial {
		t.Fatalf("after rotate: cert %v prev %v", row.CertSerial, row.PrevCertSerial)
	}
	if c := h.sessionCode(t, a); c != codes.OK {
		t.Fatalf("the replaced certificate before the new one connected: %v", c)
	}
	if c := h.sessionCode(t, b); c != codes.OK {
		t.Fatalf("the new certificate: %v", c)
	}
	row, _ = store.GetHost(ctx, h.pool, hostID)
	if row.PrevCertSerial != nil {
		t.Fatal("the replaced serial outlived the new certificate's first connection")
	}
	if c := h.sessionCode(t, a); c != codes.PermissionDenied {
		t.Fatalf("a superseded certificate opened a session: %v", c)
	}
	if _, err := h.rotate(t, a); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a superseded certificate rotated: %v", err)
	}
	if _, err := h.rotate(t, b); err != nil {
		t.Fatalf("current certificate: %v", err)
	}
}

// A host marked lost or retired keeps no identity: Rotate and Session are
// refused for its last certificate, and an open stream ends at the next
// heartbeat (I-432).
func TestLostAndRetiredHostsAreRefused(t *testing.T) {
	for _, state := range []string{"lost", "retired"} {
		t.Run(state, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			hostID, cert := h.register(t, "host-01")
			fake := fakehostd.New(fakehostd.Options{HostID: hostID.String(), Heartbeat: 50 * time.Millisecond, SampleInterval: time.Hour})
			done := make(chan error, 1)
			sctx, cancel := context.WithCancel(ctx)
			defer cancel()
			go func() { done <- fake.Run(sctx, h.dial(t, &cert)) }()
			waitFor(t, "connected", func() bool { return h.srv.Connected(hostID) })

			// Only the state, as an operator could leave it: the state
			// alone refuses, whatever the serial columns say.
			if _, err := h.pool.Exec(ctx, "update hosts set state = $2, draining = true where id = $1", hostID, state); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if status.Code(err) != codes.PermissionDenied {
					t.Fatalf("open stream ended with %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("open stream survived the host being marked " + state)
			}
			if _, err := h.rotate(t, cert); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("rotate: %v", err)
			}
			if c := h.sessionCode(t, cert); c != codes.PermissionDenied {
				t.Fatalf("session: %v", c)
			}
		})
	}
}
