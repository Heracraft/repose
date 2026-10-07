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

// The guest's root filesystem (DECISIONS I-567) rides the sample beside the
// volume's allocated figure, and a size past the volume is dropped.
func TestSamplesCarryRootFilesystem(t *testing.T) {
	h := newHarness(t, nil)
	h.gopts.Sample = &guestdv1.SampleResult{
		Signals:       &hostdv1.GuestSignals{GuestdOk: true},
		RootUsedBytes: 20 << 30,
		RootSizeBytes: 30 << 30,
	}
	h.create(gid1)
	gs := h.m.CollectSamples(context.Background()).Guests[0]
	if gs.RootUsedBytes != 20<<30 || gs.RootSizeBytes != 30<<30 {
		t.Fatalf("root %d of %d, want 20 GiB of 30 GiB", gs.RootUsedBytes, gs.RootSizeBytes)
	}
	if gs.DiskAllocBytes < gs.RootSizeBytes {
		t.Fatalf("volume %d smaller than the root filesystem", gs.DiskAllocBytes)
	}
}

func TestBoundRootFS(t *testing.T) {
	for _, c := range []struct{ used, size, volume, wantUsed, wantSize uint64 }{
		{20, 30, 40, 20, 30},
		{0, 0, 40, 0, 0},   // an old guest
		{20, 50, 40, 0, 0}, // larger than the volume
		{31, 30, 40, 0, 0}, // used past size
		{20, 30, 0, 0, 0},  // no volume figure
	} {
		u, s := boundRootFS(c.used, c.size, c.volume)
		if u != c.wantUsed || s != c.wantSize {
			t.Errorf("boundRootFS(%d, %d, %d) = %d, %d; want %d, %d", c.used, c.size, c.volume, u, s, c.wantUsed, c.wantSize)
		}
	}
}
