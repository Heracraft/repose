package hostmgr_test

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/hostmgr"
	"github.com/heracraft/repose/internal/api/store"
	fakehostd "github.com/heracraft/repose/internal/fakes/hostd"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// DECISIONS I-644: hosts.pool_unavailable follows Hello and every
// Heartbeat, both ways; a hostd older than the field sends nothing, which
// reads as false.
func TestHeartbeatStoresPoolUnavailable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	token, err := hostmgr.MintJoinToken(ctx, h.pool, "host-01", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	client := hostdv1.NewHostServiceClient(h.dial(t, nil))
	resp, err := client.Register(ctx, &hostdv1.RegisterRequest{JoinToken: token, Info: &hostdv1.HostInfo{Hostname: "host-01", MemBytes: 64 << 30, Vcpus: 16, PoolBytes: 500 << 30}})
	if err != nil {
		t.Fatal(err)
	}
	hostID := uuid.MustParse(resp.HostId)
	cert, err := tls.X509KeyPair(resp.ClientCert, resp.ClientKey)
	if err != nil {
		t.Fatal(err)
	}
	fake := fakehostd.New(fakehostd.Options{HostID: resp.HostId, Heartbeat: 50 * time.Millisecond, SampleInterval: time.Hour})
	fake.SetPoolUnavailable(true)
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = fake.Run(sctx, h.dial(t, &cert)) }()
	poolDown := func(want bool) func() bool {
		return func() bool {
			hr, err := store.GetHost(ctx, h.pool, hostID)
			return err == nil && hr.PoolUnavailable == want && hr.LastHeartbeatAt != nil
		}
	}
	waitFor(t, "pool_unavailable from hello", poolDown(true))
	// Zero bytes from a host whose pool is down keep the registered size.
	if hr, _ := store.GetHost(ctx, h.pool, hostID); hr.PoolBytes != 500<<30 {
		t.Fatalf("pool_bytes %d, want the registered size kept", hr.PoolBytes)
	}
	fake.SetPoolUnavailable(false)
	waitFor(t, "pool back from a heartbeat", poolDown(false))
	fake.SetPoolUnavailable(true)
	waitFor(t, "pool down from a heartbeat", poolDown(true))
}
