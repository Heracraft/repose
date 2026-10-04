package notify

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"syscall"
	"time"
)

// The ntfy URL is the user's own, so a POST to it is the api reaching out
// to an address a user chose, from inside the platform's network (DECISIONS
// I-444). PublicClient refuses every address that is not on the public
// internet, checked on the address actually dialled so a name that
// resolves to one later is refused too, and it follows no redirects.

// ErrRedirect is PublicClient's answer to a redirect: the target would
// be a second URL nobody checked when the user saved the first.
var ErrRedirect = errors.New("redirect refused")

// ErrNonPublicAddress is a dial to an address PublicClient refuses.
var ErrNonPublicAddress = errors.New("address is not on the public internet")

// blockedPrefixes are the ranges netip has no predicate for: CGNAT (and
// tailnets), "this network", benchmarking, and the Azure platform
// endpoint (wireserver), which is not in a private range.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("168.63.129.16/32"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// PublicAddr reports whether ip is one PublicClient may dial: not
// loopback, private (which includes the platform's 10.255.0.0/16 mesh and
// IPv6 ULA), link-local (which includes the instance metadata service),
// multicast, unspecified, CGNAT, or another reserved range.
func PublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

func publicOnly(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	if !PublicAddr(ip) {
		return ErrNonPublicAddress
	}
	return nil
}

// PublicClient is the http client for user-chosen URLs: public addresses
// only, no proxy from the environment (the check would see the proxy, not
// the target), and no redirects.
func PublicClient(timeout time.Duration) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, Control: publicOnly}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return d.DialContext(ctx, network, addr)
			},
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns:        16,
			IdleConnTimeout:     90 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrRedirect },
	}
}

// ntfyClient is the default Ntfy client, built once so connections are
// reused.
var ntfyClient = sync.OnceValue(func() *http.Client { return PublicClient(15 * time.Second) })
