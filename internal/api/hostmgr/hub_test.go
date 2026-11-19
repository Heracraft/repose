package hostmgr_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/hostmgr"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// A host the user owns has no route to the api's VNet address, so its
// place on the hub is minted at `host add` and Register travels over the
// tunnel the edge builds from it (DECISIONS I-1380): the row carries a
// public key, an address and a guest range before it registers; Register
// keeps the addresses and mints a fresh key, so the key that passed
// through the install step is dead afterwards; and a deployment with no
// edge recorded mints nothing and the bundle is the token alone.
func TestHubBootstrapPeersTheHostBeforeItRegisters(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for k, v := range map[string]string{
		hostmgr.SettingEdgeWGPubkey:   "dyP2l2byrPpr9ehOwYga0Ya9/qkUChpSCR0vJVRa0f8=",
		hostmgr.SettingEdgeWGEndpoint: "edge.example:51820",
	} {
		if err := store.SetSetting(ctx, h.pool, k, v); err != nil {
			t.Fatal(err)
		}
	}
	var uid uuid.UUID
	if err := h.pool.QueryRow(ctx, "insert into users (id, logto_sub, email, handle) values ($1, 'sub-hub', 'hub@example.test', 'hub') returning id", store.NewID()).Scan(&uid); err != nil {
		t.Fatal(err)
	}

	var token string
	var hostID uuid.UUID
	var hub *hostmgr.HubBootstrap
	err := db.InTx(ctx, h.pool, func(tx db.Tx) error {
		var err error
		token, hostID, err = hostmgr.MintJoinTokenIn(ctx, tx, hostmgr.JoinTokenRequest{Name: "mac-mini", Provider: "metal", Owner: &uid})
		if err != nil {
			return err
		}
		hub, err = hostmgr.PrepareHub(ctx, tx, hostID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if hub == nil || hub.PrivateKey == "" || hub.EdgePubkey != "dyP2l2byrPpr9ehOwYga0Ya9/qkUChpSCR0vJVRa0f8=" || hub.EdgeEndpoint != "edge.example:51820" {
		t.Fatalf("hub bootstrap %+v", hub)
	}
	before, err := store.GetHost(ctx, h.pool, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if before.RegisteredAt != nil || before.WGPubkey == nil || before.WGIP == nil || before.GuestCIDR == nil {
		t.Fatalf("before registration the row is %+v; want a peer with no registered_at", before)
	}
	if hub.Address != before.WGIP.String()+"/16" {
		t.Fatalf("bundle address %q, row wg_ip %s", hub.Address, before.WGIP)
	}
	bootstrapPub := *before.WGPubkey

	// The bundle the install step carries decodes to the same material.
	arg, err := hostmgr.EncodeJoinBundle(hostmgr.JoinBundle{Token: token, WG: hub})
	if err != nil {
		t.Fatal(err)
	}
	b, err := hostmgr.DecodeJoinBundle(arg)
	if err != nil || b.Token != token || b.WG == nil || b.WG.PrivateKey != hub.PrivateKey || b.WG.Address != hub.Address {
		t.Fatalf("decoded bundle %+v (%v), want the token and the hub material back", b, err)
	}

	// Register over the (notional) tunnel: the addresses stay, the key
	// does not, and the private key in the answer is a new one.
	client := hostdv1.NewHostServiceClient(h.dial(t, nil))
	resp, err := client.Register(ctx, &hostdv1.RegisterRequest{JoinToken: token, Info: &hostdv1.HostInfo{Hostname: "repose-mac", MemBytes: 18 << 30, Vcpus: 8, Cores: 8}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.GetHost(ctx, h.pool, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if after.RegisteredAt == nil || after.WGIP.String() != before.WGIP.String() || after.GuestCIDR.String() != before.GuestCIDR.String() {
		t.Fatalf("after registration the addresses moved: %s %s -> %s %s", before.WGIP, before.GuestCIDR, after.WGIP, after.GuestCIDR)
	}
	if *after.WGPubkey == bootstrapPub {
		t.Fatal("Register kept the bootstrap public key; the key that passed through the install step must die at registration")
	}
	if resp.Edge == nil || resp.Edge.Address != hub.Address || resp.WgPrivateKey == "" || resp.WgPrivateKey == hub.PrivateKey {
		t.Fatalf("RegisterResponse edge %+v key-changed=%v; want the same address and a new private key", resp.Edge, resp.WgPrivateKey != hub.PrivateKey)
	}
	if resp.GuestCidr != before.GuestCIDR.String() {
		t.Fatalf("RegisterResponse guest_cidr %s, want the pre-allocated %s", resp.GuestCidr, before.GuestCIDR)
	}

	// A reissued token (the host removed and added again) keeps the
	// addresses too, with yet another bootstrap key.
	err = db.InTx(ctx, h.pool, func(tx db.Tx) error {
		if _, _, err := hostmgr.MintJoinTokenIn(ctx, tx, hostmgr.JoinTokenRequest{Name: "mac-mini", Provider: "metal", Owner: &uid, Reissue: true}); err != nil {
			return err
		}
		hub, err = hostmgr.PrepareHub(ctx, tx, hostID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	re, err := store.GetHost(ctx, h.pool, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if re.WGIP.String() != before.WGIP.String() || *re.WGPubkey == *after.WGPubkey || hub.Address != before.WGIP.String()+"/16" {
		t.Fatalf("reissue: wg_ip %s pubkey-changed=%v address %s", re.WGIP, *re.WGPubkey != *after.WGPubkey, hub.Address)
	}

	// A second host does not take the first one's addresses.
	var otherID uuid.UUID
	err = db.InTx(ctx, h.pool, func(tx db.Tx) error {
		var err error
		if _, otherID, err = hostmgr.MintJoinTokenIn(ctx, tx, hostmgr.JoinTokenRequest{Name: "spare", Provider: "metal", Owner: &uid}); err != nil {
			return err
		}
		_, err = hostmgr.PrepareHub(ctx, tx, otherID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.GetHost(ctx, h.pool, otherID)
	if err != nil {
		t.Fatal(err)
	}
	if other.WGIP.String() == before.WGIP.String() || other.GuestCIDR.String() == before.GuestCIDR.String() {
		t.Fatalf("two hosts share an address: %s %s", other.WGIP, other.GuestCIDR)
	}

	// No edge recorded: nothing to bootstrap, and the bundle is the token.
	for _, k := range []string{hostmgr.SettingEdgeWGPubkey, hostmgr.SettingEdgeWGEndpoint} {
		if err := store.SetSetting(ctx, h.pool, k, ""); err != nil {
			t.Fatal(err)
		}
	}
	var noneID uuid.UUID
	var none *hostmgr.HubBootstrap
	err = db.InTx(ctx, h.pool, func(tx db.Tx) error {
		var err error
		if _, noneID, err = hostmgr.MintJoinTokenIn(ctx, tx, hostmgr.JoinTokenRequest{Name: "no-edge", Provider: "metal", Owner: &uid}); err != nil {
			return err
		}
		none, err = hostmgr.PrepareHub(ctx, tx, noneID)
		return err
	})
	if err != nil || none != nil {
		t.Fatalf("with no edge recorded: hub %+v err %v, want nil nil", none, err)
	}
	arg, err = hostmgr.EncodeJoinBundle(hostmgr.JoinBundle{Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if b, err := hostmgr.DecodeJoinBundle(arg); err != nil || b.Token != "t" || b.WG != nil {
		t.Fatalf("token-only bundle decoded to %+v (%v)", b, err)
	}
	if _, err := hostmgr.DecodeJoinBundle("not a bundle"); err == nil {
		t.Fatal("a string that is not a bundle decoded")
	}
}
