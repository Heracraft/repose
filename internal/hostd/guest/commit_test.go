package guest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

const (
	gidC3 = "0192f0a3-3333-7000-8000-000000000003"
	gidC4 = "0192f0a4-4444-7000-8000-000000000004"
)

// On a Mac the machines' configured memory together stays within the
// commit (DECISIONS I-1666): three 12 GiB machines on a 24 GB Mac swapped
// 5.6-8.0 GB and their builds stalled (d2-lab, 21:59Z). A start gets its
// class's memory, or what the running machines leave, and below
// MinCommitMiB it is refused before anything is made; a stop frees its
// share.
func TestCommitSizesAndRefusesStarts(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.CommitMemMiB = 20480 })
	h.create(gid1)
	h.create(gid2)
	h.create(gidC3)
	for id, want := range map[string]uint64{gid1: 8192, gid2: 8192, gidC3: 4096} {
		if got := h.guest(id).MemMiB; got != want {
			t.Fatalf("%s started with %d MiB, want %d", id, got, want)
		}
	}
	args, _ := os.ReadFile(filepath.Join(h.cfg.GuestsDir, gidC3, "ch.args"))
	if !strings.Contains(string(args), "size=4096M") {
		t.Fatalf("the third VM's argv does not carry its size:\n%s", args)
	}
	req := createReq(gidC4)
	req.SystemClosure = h.closure
	res := h.mustFail(cmd(req), CodeInsufficientCapacity)
	if !strings.Contains(res.Error.Message, "stop one first") || strings.Contains(res.Error.Message, "not enough free memory") {
		t.Fatalf("message %q", res.Error.Message)
	}
	if g, _ := h.m.getGuest(gidC4); g != nil {
		t.Fatalf("a refused create left a guest in %s", g.State)
	}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.create(gidC4)
	if got := h.guest(gidC4).MemMiB; got != 8192 {
		t.Fatalf("after a stop the next start got %d MiB", got)
	}
}

// vCPUs are sized as memory is, but never refused: a start gets the
// class's vCPUs or what the running machines leave, at least two. Thirty
// guest vCPUs on a 10-core Mac made three builds take 775-811 s, twelve
// took 55-58 s (DECISIONS I-1666).
func TestCommitSizesVCPUs(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.CommitVCPUs = 5 })
	h.create(gid1)
	h.create(gid2)
	h.create(gidC3)
	for id, want := range map[string]uint32{gid1: 2, gid2: 2, gidC3: 2} {
		if got := h.guest(id).VCPUs; got != want {
			t.Fatalf("%s started with %d vCPUs, want %d", id, got, want)
		}
	}
	args, _ := os.ReadFile(filepath.Join(h.cfg.GuestsDir, gidC3, "ch.args"))
	if !strings.Contains(string(args), "boot=2") {
		t.Fatalf("argv:\n%s", args)
	}
}

// ENG-264 (DECISIONS I-1680): a start sized below what the machine gets
// with the host to itself says so. The third of three machines on the lab
// Mac ran at 2 vCPUs and 3907 MB instead of 5 and 8 GiB, and neither
// `repose start` nor `repose ls` showed it. hostd now answers the create
// and the start, and every sample, with the size the VM got and its full
// size; a host that does not size its starts sends neither.
func TestSizedDownStartReportsItsSize(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.CommitMemMiB = 20480; c.CommitVCPUs = 10 })
	for _, id := range []string{gid1, gid2} {
		if v := h.create(id).GetCreate().GetVm(); v == nil || v.MemMib != 8192 || v.FullMemMib != 8192 {
			t.Fatalf("%s: create vm %+v, want 8192 of 8192 MiB", id, v)
		}
	}
	v := h.create(gidC3).GetCreate().GetVm()
	if v == nil || v.MemMib != 4096 || v.FullMemMib != 8192 || v.Vcpus == 0 || v.FullVcpus == 0 {
		t.Fatalf("third create vm %+v, want 4096 of 8192 MiB", v)
	}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gidC3}))
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	if v := h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gidC3})).GetStart().GetVm(); v == nil || v.MemMib != 8192 || v.FullMemMib != 8192 {
		t.Fatalf("start with room: vm %+v, want 8192 of 8192 MiB", v)
	}
	seen := map[string]*hostdv1.VmSize{}
	for _, g := range h.m.CollectSamples(context.Background()).Guests {
		seen[g.GuestId] = g.Vm
	}
	if seen[gidC3] == nil || seen[gidC3].MemMib != 8192 || seen[gid1] != nil {
		t.Fatalf("samples vm %+v (running) and %+v (stopped)", seen[gidC3], seen[gid1])
	}
}

// A host that does not size its starts (the fleet, an x86 owned host)
// sends no size: a machine there is its class.
func TestUnsizedHostSendsNoVMSize(t *testing.T) {
	h := newHarness(t, nil)
	if v := h.create(gid1).GetCreate().GetVm(); v != nil {
		t.Fatalf("create vm %+v on a host that does not size starts", v)
	}
	for _, g := range h.m.CollectSamples(context.Background()).Guests {
		if g.Vm != nil {
			t.Fatalf("sample vm %+v", g.Vm)
		}
	}
}

// Three machines booted at once (a host's restart) each read the others
// as not started and took 8 GiB each, 24 GiB on a 24 GB Mac (lab Mac,
// 2026-11-27): sizing is one boot at a time, each holding its share
// until its record counts it (DECISIONS I-1712).
func TestConcurrentStartsStayWithinTheCommit(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.CommitMemMiB = 20480 })
	var cmds []*hostdv1.Command
	for _, id := range []string{gid1, gid2, gidC3} {
		req := createReq(id)
		req.SystemClosure = h.closure
		cmds = append(cmds, cmd(req)) // cmd numbers commands; not from goroutines
	}
	var wg sync.WaitGroup
	for _, c := range cmds {
		wg.Add(1)
		go func(c *hostdv1.Command) {
			defer wg.Done()
			h.run(c)
		}(c)
	}
	wg.Wait()
	var total uint64
	for _, id := range []string{gid1, gid2, gidC3} {
		total += h.guest(id).MemMiB
	}
	if total > 20480 {
		t.Fatalf("three machines started together hold %d MiB of a 20480 MiB commit", total)
	}
}
