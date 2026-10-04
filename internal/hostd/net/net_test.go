package net

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// nftModel answers the nft commands AddGuestRules, DelGuestRules,
// BlockedPackets and DelCounter run, the way the kernel would for the
// `inet repose` table: counters by name, rules per chain with handles.
type nftModel struct {
	counters map[string]uint64 // name -> packets
	chains   map[string][]nftRule
	next     int
}

type nftRule struct {
	handle  int
	counter string
	saddr   string
}

func newNFTModel() *nftModel {
	return &nftModel{counters: map[string]uint64{}, chains: map[string][]nftRule{}, next: 17}
}

func (m *nftModel) handle(argv []string) (shell.Result, error) {
	fail := func() (shell.Result, error) {
		r := shell.Result{ExitCode: 1}
		return r, &shell.ExitError{Argv: argv, Result: r}
	}
	cmd := strings.Join(argv, " ")
	switch {
	case strings.HasPrefix(cmd, "nft list counters table inet repose"):
		out := "table inet repose {\n"
		for name, p := range m.counters {
			out += fmt.Sprintf("\tcounter %s {\n\t\tpackets %d bytes %d\n\t}\n", name, p, p*60)
		}
		return shell.Result{Stdout: []byte(out + "}\n")}, nil
	case strings.HasPrefix(cmd, "nft add counter inet repose "):
		if _, ok := m.counters[argv[5]]; !ok {
			m.counters[argv[5]] = 0
		}
	case strings.HasPrefix(cmd, "nft -a list chain inet repose "):
		out := "table inet repose {\n\tchain " + argv[6] + " {\n"
		for _, r := range m.chains[argv[6]] {
			out += fmt.Sprintf("\t\tip saddr %s counter name \"%s\" # handle %d\n", r.saddr, r.counter, r.handle)
		}
		return shell.Result{Stdout: []byte(out + "\t}\n}\n")}, nil
	case strings.HasPrefix(cmd, "nft add rule inet repose "):
		// nft add rule inet repose <chain> ip saddr <ip> counter name "<c>"
		m.chains[argv[5]] = append(m.chains[argv[5]], nftRule{handle: m.next, saddr: argv[8], counter: strings.Trim(argv[11], `"`)})
		m.next++
	case strings.HasPrefix(cmd, "nft delete rule inet repose "):
		rules := m.chains[argv[5]]
		for i, r := range rules {
			if strconv.Itoa(r.handle) == argv[7] {
				m.chains[argv[5]] = append(rules[:i:i], rules[i+1:]...)
				return shell.Result{}, nil
			}
		}
		return fail()
	case strings.HasPrefix(cmd, "nft delete counter inet repose "):
		if _, ok := m.counters[argv[5]]; !ok {
			return fail()
		}
		delete(m.counters, argv[5])
	}
	return shell.Result{}, nil
}

func (m *nftModel) rules(chain string) []string {
	var out []string
	for _, r := range m.chains[chain] {
		out = append(out, r.saddr+" "+r.counter)
	}
	return out
}

func TestRealRendersDocumentedCommands(t *testing.T) {
	nft := newNFTModel()
	// another guest's rule, which nothing here may touch
	nft.counters["egress-g2"] = 5
	nft.chains["guest_dyn"] = []nftRule{{handle: 9, saddr: "10.64.4.3", counter: "egress-g2"}}
	r := &shell.Fake{Scripts: []shell.Script{
		{Prefix: []string{"ip", "link", "show"}, Result: shell.Result{ExitCode: 1}},
		{Prefix: []string{"nft"}, Handle: nft.handle},
	}}
	n := NewReal(r)
	ctx := context.Background()
	if err := n.AddTap(ctx, "tap-0192abcd"); err != nil {
		t.Fatal(err)
	}
	if err := n.AddGuestRules(ctx, "g1", "10.64.4.2", "52:54:01:92:ab:cd", "tap-0192abcd"); err != nil {
		t.Fatal(err)
	}
	if err := n.Shape(ctx, "tap-0192abcd", 200, 1000); err != nil {
		t.Fatal(err)
	}
	if err := n.DelGuestRules(ctx, "g1", "10.64.4.2", "52:54:01:92:ab:cd", "tap-0192abcd"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range r.Calls {
		got = append(got, strings.Join(c, " "))
	}
	golden, err := os.ReadFile("testdata/create.golden")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "\n")+"\n" != string(golden) {
		t.Fatalf("commands differ from testdata/create.golden:\n%s", strings.Join(got, "\n"))
	}
	if got := nft.rules("guest_dyn"); len(got) != 1 || got[0] != "10.64.4.3 egress-g2" {
		t.Fatalf("guest_dyn after the stop: %v; want only the other guest's rule", got)
	}
}

// A guest's rules come back when it starts again after a stop (its
// counters kept), a re-run adds nothing twice, and the blocked counters
// are read per guest and kind (DECISIONS I-238..I-240).
func TestGuestRulesReconcileAcrossRestart(t *testing.T) {
	nft := newNFTModel()
	r := &shell.Fake{Scripts: []shell.Script{{Prefix: []string{"nft"}, Handle: nft.handle}}}
	n := NewReal(r)
	ctx := context.Background()
	start := func() {
		t.Helper()
		if err := n.AddGuestRules(ctx, "g1", "10.64.4.2", "52:54:01:92:ab:cd", "tap-1"); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{"guest_dyn": "egress-g1", "guest_smtp": "smtp-g1", "guest_stratum": "stratum-g1", "guest_flows": "flows-g1"}
	check := func(when string) {
		t.Helper()
		for chain, counter := range want {
			if got := nft.rules(chain); len(got) != 1 || got[0] != "10.64.4.2 "+counter {
				t.Fatalf("%s: %s has %v; want one rule for %s", when, chain, got, counter)
			}
		}
	}
	start()
	start() // hostd restarted mid-start
	check("after a repeated start")
	nft.counters["smtp-g1"] = 12
	nft.counters["flows-g1"] = 3
	got, err := n.BlockedPackets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got["g1"]["smtp"] != 12 || got["g1"]["flows"] != 3 || got["g1"]["stratum"] != 0 || len(got) != 1 {
		t.Fatalf("BlockedPackets = %v", got)
	}
	if err := n.DelGuestRules(ctx, "g1", "10.64.4.2", "52:54:01:92:ab:cd", "tap-1"); err != nil {
		t.Fatal(err)
	}
	for chain := range want {
		if got := nft.rules(chain); len(got) != 0 {
			t.Fatalf("after stop %s still has %v", chain, got)
		}
	}
	if nft.counters["smtp-g1"] != 12 {
		t.Fatal("a stop must keep the counters")
	}
	start()
	check("after a start following a stop")
	if err := n.DelCounter(ctx, "g1"); err != nil {
		t.Fatal(err)
	}
	if err := n.DelCounter(ctx, "g1"); err != nil {
		t.Fatalf("a second DelCounter must be a no-op: %v", err)
	}
	if len(nft.counters) != 0 {
		t.Fatalf("counters left after destroy: %v", nft.counters)
	}
}

func TestTapStatsAreGuestView(t *testing.T) {
	dir := t.TempDir()
	st := filepath.Join(dir, "tap-x", "statistics")
	_ = os.MkdirAll(st, 0o755)
	_ = os.WriteFile(filepath.Join(st, "tx_bytes"), []byte("100\n"), 0o644)
	_ = os.WriteFile(filepath.Join(st, "rx_bytes"), []byte("7\n"), 0o644)
	n := &Real{SysFS: dir}
	rx, tx, err := n.TapStats("tap-x")
	if err != nil || rx != 100 || tx != 7 {
		t.Fatalf("rx=%d tx=%d err=%v; guest rx must be the tap's tx", rx, tx, err)
	}
}

// qdiscModel answers `tc qdisc` the way the kernel would for one tap, so
// Shape's migration and idempotence are tested against state, not a script.
type qdiscModel struct{ htb, htb2, ingress bool }

func (q *qdiscModel) handle(argv []string) (shell.Result, error) {
	fail := func() (shell.Result, error) {
		r := shell.Result{ExitCode: 2}
		return r, &shell.ExitError{Argv: argv, Result: r}
	}
	verb, last := argv[2], argv[len(argv)-1]
	switch verb + " " + last {
	case "show " + last:
		out := ""
		switch {
		case q.htb:
			out += "qdisc htb 1: root refcnt 2 r2q 10 default 0x10 direct_packets_stat 0 direct_qlen 1000\n"
		case q.htb2:
			out += "qdisc htb 2: root refcnt 2 r2q 10 default 0x20 direct_packets_stat 0 direct_qlen 1000\n"
		default:
			out += "qdisc fq_codel 0: root refcnt 2 limit 10240p flows 1024 quantum 1514\n"
		}
		if q.ingress {
			out += "qdisc ingress ffff: parent ffff:fff1 ----------------\n"
		}
		return shell.Result{Stdout: []byte(out)}, nil
	case "add ingress":
		if q.ingress {
			return fail()
		}
		q.ingress = true
	case "del ingress":
		if !q.ingress {
			return fail()
		}
		q.ingress = false
	case "del root":
		if !q.htb && !q.htb2 {
			return fail() // "Cannot delete qdisc with handle of zero."
		}
		q.htb, q.htb2 = false, false
	case "replace 20":
		if len(argv) > 5 && argv[5] == "root" {
			if q.htb2 {
				return fail() // replacing a qdisc with itself would drop its classes; Shape must not
			}
			q.htb, q.htb2 = false, true
		}
	}
	return shell.Result{}, nil
}

func TestShapeMigratesLegacyRootAndIsIdempotent(t *testing.T) {
	q := &qdiscModel{htb: true} // a tap shaped before I-217
	r := &shell.Fake{Scripts: []shell.Script{{Prefix: []string{"tc", "qdisc"}, Handle: q.handle}}}
	n := NewReal(r)
	ctx := context.Background()
	if err := n.Shape(ctx, "tap-0192abcd", 200, 1000); err != nil {
		t.Fatal(err)
	}
	if q.htb || !q.htb2 || !q.ingress {
		t.Fatalf("after the first Shape htb=%v htb2=%v ingress=%v; want the legacy root replaced by 2: and the ingress policer in place", q.htb, q.htb2, q.ingress)
	}
	// a hostd restart re-applies, here with a new rate: nothing added twice
	if err := n.Shape(ctx, "tap-0192abcd", 100, 500); err != nil {
		t.Fatal(err)
	}
	if err := n.Unshape(ctx, "tap-0192abcd"); err != nil {
		t.Fatal(err)
	}
	if q.htb || q.htb2 || q.ingress {
		t.Fatalf("after Unshape htb=%v htb2=%v ingress=%v", q.htb, q.htb2, q.ingress)
	}
	if err := n.Unshape(ctx, "tap-0192abcd"); err != nil {
		t.Fatalf("a second Unshape must be a no-op: %v", err)
	}
	var got []string
	for _, c := range r.Calls[:len(r.Calls)-2] {
		got = append(got, strings.Join(c, " "))
	}
	golden, err := os.ReadFile("testdata/reshape.golden")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "\n")+"\n" != string(golden) {
		t.Fatalf("commands differ from testdata/reshape.golden:\n%s", strings.Join(got, "\n"))
	}
}
