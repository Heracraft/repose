package unet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// wgFile is the WireGuard material as host.json's `wg` and the join
// bundle's hub.json carry it (register.WG, hostmgr.HubBootstrap).
type wgFile struct {
	PrivateKey   string   `json:"private_key"`
	Address      string   `json:"address"`
	EdgePubkey   string   `json:"edge_pubkey"`
	EdgeEndpoint string   `json:"edge_endpoint"`
	AllowedIPs   []string `json:"allowed_ips,omitempty"`
}

func (w wgFile) complete() bool {
	return w.PrivateKey != "" && w.Address != "" && w.EdgePubkey != "" && w.EdgeEndpoint != ""
}

// LoadEdgeConfig is repose-host-net's choice on the Linux appliance, for
// the darwin hostd: host.json's `wg` once registered, else hub.json's
// bootstrap material (I-1380), else none (ok false). The route on the
// mesh stays 10.255.0.0/16 whatever host.json's allowed_ips say, as
// repose-host-net renders it.
func LoadEdgeConfig(hostJSON, hubJSON string) (cfg EdgeConfig, ok bool, err error) {
	if b, rerr := os.ReadFile(hostJSON); rerr == nil {
		var h struct {
			WG wgFile `json:"wg"`
		}
		if jerr := json.Unmarshal(b, &h); jerr != nil {
			return cfg, false, fmt.Errorf("unet: host.json: %w", jerr)
		}
		if h.WG.complete() {
			return EdgeConfig{PrivateKey: h.WG.PrivateKey, Address: h.WG.Address, EdgePubkey: h.WG.EdgePubkey, EdgeEndpoint: h.WG.EdgeEndpoint}, true, nil
		}
	} else if !errors.Is(rerr, os.ErrNotExist) {
		return cfg, false, rerr
	}
	b, rerr := os.ReadFile(hubJSON)
	if errors.Is(rerr, os.ErrNotExist) {
		return cfg, false, nil
	}
	if rerr != nil {
		return cfg, false, rerr
	}
	var w wgFile
	if jerr := json.Unmarshal(b, &w); jerr != nil {
		return cfg, false, fmt.Errorf("unet: hub.json: %w", jerr)
	}
	if !w.complete() {
		return cfg, false, nil
	}
	return EdgeConfig{PrivateKey: w.PrivateKey, Address: w.Address, EdgePubkey: w.EdgePubkey, EdgeEndpoint: w.EdgeEndpoint}, true, nil
}

// LauncherReconnect is a Reconnect that asks a guest's launcher
// (repose-vm, d2-runtime) to connect its NIC again: `PUT
// /api/v1/vm.net-reconnect` on the CH API socket apiSocket(guestID)
// returns, answered 204. The launcher also re-connects by itself when the
// socket's inode changes, so this only shortens the gap after a hostd
// restart.
func LauncherReconnect(apiSocket func(guestID string) string) Reconnect {
	return func(ctx context.Context, guestID, _ string) error {
		sock := apiSocket(guestID)
		c := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://localhost/api/v1/vm.net-reconnect", nil)
		if err != nil {
			return err
		}
		resp, err := c.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close() // no body
		if resp.StatusCode != http.StatusNoContent {
			return fmt.Errorf("unet: net-reconnect: %s", resp.Status)
		}
		return nil
	}
}
