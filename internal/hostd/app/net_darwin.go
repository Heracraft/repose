//go:build darwin

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	hnet "github.com/heracraft/repose/internal/hostd/net"
	"github.com/heracraft/repose/internal/hostd/register"
	"github.com/heracraft/repose/internal/hostd/unet"
)

// The Mac's userspace network (DECISIONS I-1668) and its edge tunnel
// (I-1670): one of each per hostd. prepareMacNet brings the stack and
// the tunnel up before registration, so Register and the Session reach
// the api at the hub address through it; NewMacNet hands the stack to the
// guest manager once the host's range is known.
var macNet struct {
	mu   sync.Mutex
	n    *unet.Net
	e    *unet.Edge
	last unet.EdgeConfig
}

func init() { NewMacNet = newMacNet }

func edgeFiles(o Options) (hostJSON, hubJSON string) {
	return filepath.Join(o.StateDir, register.HostFile), filepath.Join(o.StateDir, "hub.json")
}

// prepareMacNet starts the stack and, when host.json or hub.json carries
// WireGuard material, the edge, and points the api dial at it. With no
// --mac-net-dir it does nothing: the runtime's lab mode on VZ NAT.
func prepareMacNet(ctx context.Context, o Options, log *slog.Logger) (Options, error) {
	m := macDefaults(o)
	if m.NetDir == "" {
		return o, nil
	}
	if err := os.MkdirAll(m.NetDir, 0o700); err != nil {
		return o, fmt.Errorf("hostd: network dir: %w", err)
	}
	macNet.mu.Lock()
	defer macNet.mu.Unlock()
	if macNet.n == nil {
		guests := o.GuestsDir
		n, err := unet.New(unet.Config{
			MTU: uint32(m.NetMTU), SocketDir: m.NetDir, Log: log,
			Reconnect: unet.LauncherReconnect(func(id string) string { return filepath.Join(guests, id, "ch.sock") }),
			// A connection for a machine resting as a save file wakes it
			// (I-1666); the edge waits for its NIC.
			OnWake: func(tap string) { go MacWake(tap) },
		})
		if err != nil {
			return o, err
		}
		macNet.n = n
	}
	if err := refreshEdgeLocked(o, log); err != nil {
		return o, err
	}
	o.APIDial = func(ctx context.Context, addr string) (net.Conn, error) {
		macNet.mu.Lock()
		e := macNet.e
		macNet.mu.Unlock()
		if e == nil {
			return nil, errors.New("hostd: no WireGuard material yet (host.json or hub.json)")
		}
		return e.DialContext(ctx, "tcp", addr)
	}
	return o, nil
}

// refreshEdgeLocked starts the edge, or reconfigures it when the material
// changed (registration replaces the bundle's key with host.json's).
func refreshEdgeLocked(o Options, log *slog.Logger) error {
	host, hub := edgeFiles(o)
	cfg, ok, err := unet.LoadEdgeConfig(host, hub)
	if err != nil {
		return err
	}
	if !ok {
		log.Warn("no WireGuard material: the api is unreachable until host.json or hub.json exists", "event", "start", "part", "edge")
		return nil
	}
	if macNet.e != nil && reflect.DeepEqual(cfg, macNet.last) {
		return nil
	}
	if macNet.e == nil {
		e, err := macNet.n.StartEdge(cfg)
		if err != nil {
			return err
		}
		macNet.e = e
	} else if err := macNet.e.Reconfigure(cfg); err != nil {
		return err
	}
	macNet.last = cfg
	log.Info("edge tunnel configured", "event", "start", "part", "edge", "addr", macNet.e.Addr().String())
	return nil
}

// newMacNet is NewMacNet: after registration, so host.json names the
// guest range (its .1 is the guests' gateway) and the registration's key.
func newMacNet(ctx context.Context, o Options, log *slog.Logger) (hnet.Net, error) {
	if _, err := prepareMacNet(ctx, o, log); err != nil {
		return nil, err
	}
	id, err := register.Load(o.StateDir)
	if err != nil {
		return nil, fmt.Errorf("hostd: network: %w", err)
	}
	p, err := netip.ParsePrefix(id.Host.GuestCIDR)
	if err != nil {
		return nil, fmt.Errorf("hostd: network: guest range %q", id.Host.GuestCIDR)
	}
	macNet.mu.Lock()
	defer macNet.mu.Unlock()
	if err := macNet.n.SetGateway(p.Masked().Addr().Next()); err != nil {
		return nil, err
	}
	return macNet.n, nil
}
