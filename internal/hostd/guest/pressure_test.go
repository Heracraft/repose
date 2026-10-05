package guest

import (
	"context"
	"testing"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// The two CPU pressure counters of DECISIONS I-493: the guest's own
// (guest-written, bounded) and the hypervisor unit's on the host.
func TestSamplesCarryCPUPressureAndHostWait(t *testing.T) {
	h := newHarness(t, nil)
	h.gopts.Sample = &guestdv1.SampleResult{
		Signals:            &hostdv1.GuestSignals{GuestdOk: true},
		CpuPressureUsTotal: 5_000_000,
		MemUsedBytes:       3 << 30,
	}
	h.cgWait.Store(1_000_000)
	h.create(gid1)
	ctx := context.Background()

	// First reading after a start is the baseline for both.
	gs := h.m.CollectSamples(ctx).Guests[0]
	if gs.GuestMemUsedBytes != 3<<30 {
		t.Fatalf("guest memory used %d, want 3 GiB", gs.GuestMemUsedBytes)
	}
	if gs.CpuPressureUsDelta != 0 || gs.HostCpuWaitUsDelta != 0 {
		t.Fatalf("first sample: pressure %d wait %d, want baselines", gs.CpuPressureUsDelta, gs.HostCpuWaitUsDelta)
	}
	h.cgWait.Store(4_000_000)
	gs = h.m.CollectSamples(ctx).Guests[0]
	if gs.HostCpuWaitUsDelta != 3_000_000 {
		t.Fatalf("host wait delta %d, want 3000000", gs.HostCpuWaitUsDelta)
	}
	if gs.CpuPressureUsDelta != 0 {
		t.Fatalf("an unchanged pressure total moved: %d", gs.CpuPressureUsDelta)
	}
}

func TestAdvancePressure(t *testing.T) {
	h := newHarness(t, nil)
	m := h.m
	if d := m.advancePressure("g", 0); d != 0 {
		t.Fatalf("no PSI gave %d", d)
	}
	if d := m.advancePressure("g", 10_000_000); d != 0 {
		t.Fatalf("baseline gave %d", d)
	}
	if d := m.advancePressure("g", 40_000_000); d != 30_000_000 {
		t.Fatalf("delta %d, want 30000000", d)
	}
	// A guest that writes a huge total moves the chart by at most the bound.
	if d := m.advancePressure("g", 1<<62); d != maxPressureDelta {
		t.Fatalf("huge total gave %d, want %d", d, maxPressureDelta)
	}
	// A reboot restarts the counter: the new total counts in full.
	if d := m.advancePressure("g", 2_000_000); d != 2_000_000 {
		t.Fatalf("after reboot %d, want 2000000", d)
	}
	// The pressure cursor is independent of CPU and tap counters.
	if d := m.advanceCPU("g", 7); d != 0 {
		t.Fatalf("cpu cursor shared with pressure: %d", d)
	}
}
