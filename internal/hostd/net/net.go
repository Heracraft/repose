// Package net wires a guest into the host network per
// docs/interfaces/host-conventions.md "Network": a tap on br-guests attached
// `isolated on learning off flood off` with a static FDB entry, membership
// in the `bridge repose` table's `guests` set (mac . ip . tap, which is what
// admits the guest's ARP and IPv4 frames to the host at all), a per-guest
// egress counter and rule in the hostd-owned `inet repose` chain
// `guest_dyn`, a counter and rule per blocked kind (smtp, stratum, flows)
// in the hostd-owned chains the host's static drops jump to (DECISIONS
// I-238..I-240), and a policer on the tap's ingress limiting what the guest
// sends (DECISIONS I-217). DECISIONS I-18 explains why
// the admission lives in the bridge family: frames between two taps never
// traverse the inet forward hook, and `learning off` plus the static FDB
// entry is what stops a guest claiming another guest's MAC.
package net

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// Net is what the guest state machine needs from the network layer.
type Net interface {
	TapExists(ctx context.Context, tap string) (bool, error)
	AddTap(ctx context.Context, tap string) error
	DelTap(ctx context.Context, tap string) error
	// AddGuestRules registers the guest's (mac, ip, tap) tuple with the
	// bridge (static FDB entry, `guests` set element) and creates its egress
	// counter and rule.
	AddGuestRules(ctx context.Context, guestID, ip, mac, tap string) error
	DelGuestRules(ctx context.Context, guestID, ip, mac, tap string) error
	// Shape limits what the guest sends to upMbit and what the host sends
	// it, from anywhere but the host itself, to downMbit.
	Shape(ctx context.Context, tap string, upMbit, downMbit int) error
	Unshape(ctx context.Context, tap string) error
	// CounterBytes reads the guest's egress counter.
	CounterBytes(ctx context.Context, guestID string) (uint64, error)
	// BlockedPackets reads every guest's blocked-attempt counters in one
	// call: guest id -> kind (BlockedKinds) -> packets dropped.
	BlockedPackets(ctx context.Context) (map[string]map[string]uint64, error)
	// DelCounter removes the guest's counters after the egress counter's
	// final value was read.
	DelCounter(ctx context.Context, guestID string) error
	// TapStats returns bytes from the guest's point of view: rx is what the
	// guest received (the tap's tx), tx what it sent (the tap's rx).
	TapStats(tap string) (rx, tx uint64, err error)
	// ListTaps returns the repose taps present on the host.
	ListTaps(ctx context.Context) ([]string, error)
}

// CounterName is the nft counter for a guest.
func CounterName(guestID string) string { return "egress-" + guestID }

// The kinds of blocked outbound attempt the host counts per guest: tcp 25
// (DECISIONS I-238), a mining pool's stratum port (I-239), a new flow over
// the per-guest rate (I-240). Each is the metric's `reason` label, the
// prefix of the guest's counter and the suffix of the hostd-owned chain the
// static drop jumps to (nix/hosts/nftables.nix).
const (
	BlockedSMTP    = "smtp"
	BlockedStratum = "stratum"
	BlockedFlows   = "flows"
)

// BlockedKinds lists them in rule order.
var BlockedKinds = []string{BlockedSMTP, BlockedStratum, BlockedFlows}

// BlockedCounterName is the nft counter of one kind for a guest.
func BlockedCounterName(kind, guestID string) string { return kind + "-" + guestID }

// BlockedChain is the hostd-owned chain counting one kind.
func BlockedChain(kind string) string { return "guest_" + kind }

// Real drives ip, bridge, nft and tc through a shell.Runner.
type Real struct {
	Bridge       string // br-guests
	TapUser      string // hostd: the tap's owner (host-conventions.md)
	Family       string // inet: the table holding guest_dyn and the counters
	Table        string // repose
	Chain        string // guest_dyn
	BridgeFamily string // bridge: the table holding the guests set
	BridgeTable  string // repose
	Set          string // guests
	SysFS        string // /sys/class/net
	R            shell.Runner
}

// NewReal returns the documented defaults.
func NewReal(r shell.Runner) *Real {
	return &Real{Bridge: "br-guests", TapUser: "hostd", Family: "inet", Table: "repose", Chain: "guest_dyn", BridgeFamily: "bridge", BridgeTable: "repose", Set: "guests", SysFS: "/sys/class/net", R: r}
}

// TapExists implements Net.
func (n *Real) TapExists(ctx context.Context, tap string) (bool, error) {
	_, err := n.R.Run(ctx, "ip", "link", "show", "dev", tap)
	var ee *shell.ExitError
	if errors.As(err, &ee) {
		return false, nil
	}
	return err == nil, err
}

// AddTap implements Net with exactly the attach sequence the host's
// bridge table depends on: `isolated on` stops frames between taps at the
// bridge, `learning off` (with the static FDB entry AddGuestRules adds)
// stops a guest from claiming another guest's MAC, `flood off` keeps
// broadcast off every other tap. vnet_hdr is what the runner contract in
// guest-conventions.md expects of the tap.
func (n *Real) AddTap(ctx context.Context, tap string) error {
	ok, err := n.TapExists(ctx, tap)
	if err != nil {
		return err
	}
	if !ok {
		if _, err := n.R.Run(ctx, "ip", "tuntap", "add", "dev", tap, "mode", "tap", "user", n.TapUser, "vnet_hdr"); err != nil {
			return err
		}
	}
	if _, err := n.R.Run(ctx, "ip", "link", "set", "dev", tap, "master", n.Bridge, "up"); err != nil {
		return err
	}
	_, err = n.R.Run(ctx, "bridge", "link", "set", "dev", tap, "isolated", "on", "learning", "off", "flood", "off")
	return err
}

// DelTap implements Net.
func (n *Real) DelTap(ctx context.Context, tap string) error {
	ok, err := n.TapExists(ctx, tap)
	if err != nil || !ok {
		return err
	}
	_, err = n.R.Run(ctx, "ip", "link", "del", "dev", tap)
	return err
}

func (n *Real) element(ip, mac, tap string) string {
	return "{ " + mac + " . " + ip + " . " + tap + " }"
}

// guestCounters is every (chain, counter) pair a guest has: the egress
// counter in guest_dyn and one per blocked kind.
func (n *Real) guestCounters(guestID string) [][2]string {
	out := [][2]string{{n.Chain, CounterName(guestID)}}
	for _, k := range BlockedKinds {
		out = append(out, [2]string{BlockedChain(k), BlockedCounterName(k, guestID)})
	}
	return out
}

// AddGuestRules implements Net. `bridge fdb replace` and `nft add element`
// are idempotent; a counter is added when `nft list counters` lacks it and
// a rule when its chain lacks one naming the counter, so a re-run after a
// crash converges, and a guest started again after a stop (its rules gone,
// its counters kept until destroy) gets its rules back. Before I-238 the
// rule was skipped whenever the counter existed, so a restarted guest's
// egress went uncounted.
func (n *Real) AddGuestRules(ctx context.Context, guestID, ip, mac, tap string) error {
	if _, err := n.R.Run(ctx, "bridge", "fdb", "replace", mac, "dev", tap, "master", "static"); err != nil {
		return err
	}
	if _, err := n.R.Run(ctx, "nft", "add", "element", n.BridgeFamily, n.BridgeTable, n.Set, n.element(ip, mac, tap)); err != nil {
		return err
	}
	res, err := n.R.Run(ctx, "nft", "list", "counters", "table", n.Family, n.Table)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, m := range counterRe.FindAllSubmatch(res.Stdout, -1) {
		have[string(m[1])] = true
	}
	for _, cc := range n.guestCounters(guestID) {
		chain, counter := cc[0], cc[1]
		if !have[counter] {
			if _, err := n.R.Run(ctx, "nft", "add", "counter", n.Family, n.Table, counter); err != nil {
				return err
			}
		}
		handles, err := n.ruleHandles(ctx, chain, counter)
		if err != nil {
			return err
		}
		if len(handles) > 0 {
			continue
		}
		if _, err := n.R.Run(ctx, "nft", "add", "rule", n.Family, n.Table, chain, "ip", "saddr", ip, "counter", "name", `"`+counter+`"`); err != nil {
			return err
		}
	}
	return nil
}

// ruleHandles lists the handles of the rules in chain that name counter.
func (n *Real) ruleHandles(ctx context.Context, chain, counter string) ([]string, error) {
	res, err := n.R.Run(ctx, "nft", "-a", "list", "chain", n.Family, n.Table, chain)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		m := handleRe.FindStringSubmatch(line)
		if m != nil && m[1] == counter {
			out = append(out, m[2])
		}
	}
	return out, nil
}

var (
	handleRe  = regexp.MustCompile(`counter name "?([^"\s]+)"?.*# handle (\d+)`)
	counterRe = regexp.MustCompile(`counter (\S+) \{\s*packets (\d+) bytes (\d+)`)
)

// DelGuestRules implements Net: set element, FDB entry, the rules (found
// by handle) and nothing else; the counters stay until DelCounter reads the
// egress counter's final value. An element or entry that is already gone
// is not an error.
func (n *Real) DelGuestRules(ctx context.Context, guestID, ip, mac, tap string) error {
	_, err := n.R.Run(ctx, "nft", "delete", "element", n.BridgeFamily, n.BridgeTable, n.Set, n.element(ip, mac, tap))
	var ee *shell.ExitError
	if err != nil && !errors.As(err, &ee) {
		return err
	}
	_, err = n.R.Run(ctx, "bridge", "fdb", "del", mac, "dev", tap, "master")
	if err != nil && !errors.As(err, &ee) {
		return err
	}
	for _, cc := range n.guestCounters(guestID) {
		handles, err := n.ruleHandles(ctx, cc[0], cc[1])
		if err != nil {
			return err
		}
		for _, h := range handles {
			if _, err := n.R.Run(ctx, "nft", "delete", "rule", n.Family, n.Table, cc[0], "handle", h); err != nil {
				return err
			}
		}
	}
	return nil
}

// ShapeExempt are the destinations a guest's traffic is never policed
// toward: the host's guest ranges (the gateway, so DNS and whatever else
// the host answers on it; guest-to-guest is dropped by nftables anyway)
// and the host services address the caches listen on (nix/hosts/caches.nix,
// DECISIONS I-202). Both are fixed on every host (host-conventions.md
// "Network"), and traffic to them never leaves the host.
var ShapeExempt = []string{"10.64.0.0/12", "10.63.255.254/32"}

var (
	ingressRe  = regexp.MustCompile(`(?m)^qdisc ingress ffff: `)
	downRootRe = regexp.MustCompile(`(?m)^qdisc htb 2: root `)
)

// Shape implements Net in two directions.
//
// What the guest sends is the tap's ingress (DECISIONS I-217): on the
// ingress qdisc, one `pass` filter per ShapeExempt prefix, then a flower
// with no match policing every other IPv4 packet to upMbit with a 500 ms
// burst.
//
// What the guest receives is the tap's egress (DECISIONS I-451): a root
// HTB, handle 2:, whose default class 2:20 holds everything to downMbit
// with 10 ms of burst (at least 128 KiB) and fq_codel under it, and whose
// class 2:10, at 10 Gbit/s, takes what comes from a ShapeExempt source
// (the caches, the gateway), so the caches stay as fast as before. The
// guests and the WireGuard tunnel then share the host NIC's receive
// bandwidth with a bound on each guest.
//
// Idempotent and reconciling: each qdisc is added only when absent and
// every class and filter is a `replace` with a fixed handle, so a re-run
// or a new rate swaps them in place, with no window and no duplicate. A
// tap still carrying the root HTB 1: of the shape before I-217 has it
// replaced by 2: in one `tc qdisc replace`; connections survive.
func (n *Real) Shape(ctx context.Context, tap string, upMbit, downMbit int) error {
	res, err := n.R.Run(ctx, "tc", "qdisc", "show", "dev", tap)
	if err != nil {
		return err
	}
	qd := string(res.Stdout)
	if !ingressRe.MatchString(qd) {
		if _, err := n.R.Run(ctx, "tc", "qdisc", "add", "dev", tap, "handle", "ffff:", "ingress"); err != nil {
			return err
		}
	}
	prio := 1
	for _, dst := range ShapeExempt {
		if _, err := n.R.Run(ctx, "tc", "filter", "replace", "dev", tap, "parent", "ffff:", "protocol", "ip",
			"prio", strconv.Itoa(prio), "handle", "1", "flower", "dst_ip", dst, "action", "pass"); err != nil {
			return err
		}
		prio++
	}
	rate := strconv.Itoa(upMbit) + "mbit"
	burst := strconv.Itoa(upMbit * 1000 * 1000 / 8 / 2) // 500 ms at the rate, in bytes
	// mtu 64kb: a vnet_hdr tap hands the host GSO packets of up to 64 KB,
	// which the policer's small default would count as exceeding.
	if _, err := n.R.Run(ctx, "tc", "filter", "replace", "dev", tap, "parent", "ffff:", "protocol", "ip",
		"prio", strconv.Itoa(prio), "handle", "1", "flower",
		"action", "police", "rate", rate, "burst", burst, "mtu", "64kb", "conform-exceed", "drop/ok"); err != nil {
		return err
	}

	if !downRootRe.MatchString(qd) {
		if _, err := n.R.Run(ctx, "tc", "qdisc", "replace", "dev", tap, "root", "handle", "2:", "htb", "default", "20"); err != nil {
			return err
		}
	}
	// The host sends GSO packets of up to 64 KB too: a quantum and a
	// burst below that would starve the class.
	if _, err := n.R.Run(ctx, "tc", "class", "replace", "dev", tap, "parent", "2:", "classid", "2:10",
		"htb", "rate", "10gbit", "burst", "1mb", "cburst", "1mb", "quantum", "65536"); err != nil {
		return err
	}
	down := strconv.Itoa(downMbit) + "mbit"
	dburst := strconv.Itoa(max(downMbit*1000*1000/8/100, 2*65536)) // 10 ms at the rate, and at least two GSO packets
	if _, err := n.R.Run(ctx, "tc", "class", "replace", "dev", tap, "parent", "2:", "classid", "2:20",
		"htb", "rate", down, "ceil", down, "burst", dburst, "cburst", dburst, "quantum", "65536"); err != nil {
		return err
	}
	if _, err := n.R.Run(ctx, "tc", "qdisc", "replace", "dev", tap, "parent", "2:20", "handle", "20:", "fq_codel"); err != nil {
		return err
	}
	for i, src := range ShapeExempt {
		if _, err := n.R.Run(ctx, "tc", "filter", "replace", "dev", tap, "parent", "2:", "protocol", "ip",
			"prio", strconv.Itoa(i+1), "handle", "1", "flower", "src_ip", src, "classid", "2:10"); err != nil {
			return err
		}
	}
	return nil
}

// Unshape implements Net: the ingress qdisc with its filters and the root
// HTB with its classes. An exit status means no such qdisc or
// no device, both nothing to remove.
func (n *Real) Unshape(ctx context.Context, tap string) error {
	for _, dir := range []string{"ingress", "root"} {
		_, err := n.R.Run(ctx, "tc", "qdisc", "del", "dev", tap, dir)
		var ee *shell.ExitError
		if err != nil && !errors.As(err, &ee) {
			return err
		}
	}
	return nil
}

var bytesRe = regexp.MustCompile(`packets\s+\d+\s+bytes\s+(\d+)`)

// CounterBytes implements Net.
func (n *Real) CounterBytes(ctx context.Context, guestID string) (uint64, error) {
	res, err := n.R.Run(ctx, "nft", "list", "counter", n.Family, n.Table, CounterName(guestID))
	if err != nil {
		return 0, err
	}
	m := bytesRe.FindSubmatch(res.Stdout)
	if m == nil {
		return 0, fmt.Errorf("nft: no counter value in output for %s", guestID)
	}
	return strconv.ParseUint(string(m[1]), 10, 64)
}

// BlockedPackets implements Net with one `nft list counters`: every
// counter named <kind>-<guest id> for a kind in BlockedKinds.
func (n *Real) BlockedPackets(ctx context.Context) (map[string]map[string]uint64, error) {
	res, err := n.R.Run(ctx, "nft", "list", "counters", "table", n.Family, n.Table)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]uint64{}
	for _, m := range counterRe.FindAllSubmatch(res.Stdout, -1) {
		name := string(m[1])
		for _, k := range BlockedKinds {
			gid, ok := strings.CutPrefix(name, k+"-")
			if !ok || gid == "" {
				continue
			}
			v, err := strconv.ParseUint(string(m[2]), 10, 64)
			if err != nil {
				return nil, err
			}
			if out[gid] == nil {
				out[gid] = map[string]uint64{}
			}
			out[gid][k] = v
		}
	}
	return out, nil
}

// DelCounter implements Net: the egress counter and the blocked-attempt
// counters. One already gone is not an error.
func (n *Real) DelCounter(ctx context.Context, guestID string) error {
	for _, cc := range n.guestCounters(guestID) {
		_, err := n.R.Run(ctx, "nft", "delete", "counter", n.Family, n.Table, cc[1])
		var ee *shell.ExitError
		if err != nil && !errors.As(err, &ee) {
			return err
		}
	}
	return nil
}

func readUint(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}

// TapStats implements Net.
func (n *Real) TapStats(tap string) (uint64, uint64, error) {
	d := filepath.Join(n.SysFS, tap, "statistics")
	tapTX, err := readUint(filepath.Join(d, "tx_bytes"))
	if err != nil {
		return 0, 0, err
	}
	tapRX, err := readUint(filepath.Join(d, "rx_bytes"))
	if err != nil {
		return 0, 0, err
	}
	return tapTX, tapRX, nil
}

// ListTaps implements Net.
func (n *Real) ListTaps(ctx context.Context) ([]string, error) {
	res, err := n.R.Run(ctx, "ip", "-o", "link", "show")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		name := strings.TrimSuffix(f[1], ":")
		if i := strings.Index(name, "@"); i >= 0 {
			name = name[:i]
		}
		if strings.HasPrefix(name, "tap-") {
			out = append(out, name)
		}
	}
	return out, nil
}

// Fake is the in-memory model for the state machine tests.
type Fake struct {
	mu       sync.Mutex
	Taps     map[string]bool
	Shaped   map[string]int    // tap -> upMbit
	Down     map[string]int    // tap -> downMbit
	Elements map[string]string // mac . ip . tap by guest
	Counters map[string]uint64
	Blocked  map[string]map[string]uint64 // guest -> kind -> packets
	Stats    map[string][2]uint64         // tap -> rx, tx (guest view)
	FailOn   map[string]error             // "tap", "rules", "shape"
	Ops      []string
}

// NewFake returns an empty fake network.
func NewFake() *Fake {
	return &Fake{Taps: map[string]bool{}, Shaped: map[string]int{}, Down: map[string]int{}, Elements: map[string]string{}, Counters: map[string]uint64{}, Blocked: map[string]map[string]uint64{}, Stats: map[string][2]uint64{}, FailOn: map[string]error{}}
}

func (f *Fake) TapExists(_ context.Context, tap string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Taps[tap], nil
}

func (f *Fake) AddTap(_ context.Context, tap string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Ops = append(f.Ops, "tap+"+tap)
	if err := f.FailOn["tap"]; err != nil {
		return err
	}
	if !f.Taps[tap] {
		f.Stats[tap] = [2]uint64{} // a new tap's counters start at zero
	}
	f.Taps[tap] = true
	return nil
}

func (f *Fake) DelTap(_ context.Context, tap string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Ops = append(f.Ops, "tap-"+tap)
	delete(f.Taps, tap)
	delete(f.Shaped, tap)
	delete(f.Down, tap)
	delete(f.Stats, tap)
	return nil
}

func (f *Fake) AddGuestRules(_ context.Context, guestID, ip, mac, tap string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Ops = append(f.Ops, "rules+"+guestID)
	if err := f.FailOn["rules"]; err != nil {
		return err
	}
	f.Elements[guestID] = mac + " . " + ip + " . " + tap
	if _, ok := f.Counters[guestID]; !ok {
		f.Counters[guestID] = 0
	}
	return nil
}

func (f *Fake) DelGuestRules(_ context.Context, guestID, _, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Ops = append(f.Ops, "rules-"+guestID)
	delete(f.Elements, guestID)
	return nil
}

func (f *Fake) Shape(_ context.Context, tap string, upMbit, downMbit int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Ops = append(f.Ops, "shape+"+tap)
	if err := f.FailOn["shape"]; err != nil {
		return err
	}
	f.Shaped[tap] = upMbit
	f.Down[tap] = downMbit
	return nil
}

func (f *Fake) Unshape(_ context.Context, tap string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Ops = append(f.Ops, "shape-"+tap)
	delete(f.Shaped, tap)
	delete(f.Down, tap)
	return nil
}

func (f *Fake) CounterBytes(_ context.Context, guestID string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.Counters[guestID]
	if !ok {
		return 0, fmt.Errorf("nft: counter %s missing", CounterName(guestID))
	}
	return v, nil
}

func (f *Fake) DelCounter(_ context.Context, guestID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Ops = append(f.Ops, "counter-"+guestID)
	delete(f.Counters, guestID)
	delete(f.Blocked, guestID)
	return nil
}

func (f *Fake) BlockedPackets(context.Context) (map[string]map[string]uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]map[string]uint64{}
	for g, kinds := range f.Blocked {
		out[g] = map[string]uint64{}
		for k, v := range kinds {
			out[g][k] = v
		}
	}
	return out, nil
}

// Block adds packets to a guest's blocked counter of kind, as the kernel
// does when the guest's attempts hit a drop.
func (f *Fake) Block(guestID, kind string, packets uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Blocked[guestID] == nil {
		f.Blocked[guestID] = map[string]uint64{}
	}
	f.Blocked[guestID][kind] += packets
}

// Send adds bytes to a tap's counters, as traffic does: rx what the guest
// received, tx what it sent.
func (f *Fake) Send(tap string, rx, tx uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.Stats[tap]
	f.Stats[tap] = [2]uint64{s[0] + rx, s[1] + tx}
}

func (f *Fake) TapStats(tap string) (uint64, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.Stats[tap]
	if !ok {
		return 0, 0, fmt.Errorf("no such tap %s", tap)
	}
	return s[0], s[1], nil
}

func (f *Fake) ListTaps(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for t := range f.Taps {
		out = append(out, t)
	}
	return out, nil
}

// Leftovers reports anything still wired for a guest, for the rollback tests.
func (f *Fake) Leftovers(guestID, tap string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	if f.Taps[tap] {
		out = append(out, "tap")
	}
	if _, ok := f.Shaped[tap]; ok {
		out = append(out, "tc")
	}
	if _, ok := f.Elements[guestID]; ok {
		out = append(out, "nft element")
	}
	return out
}
