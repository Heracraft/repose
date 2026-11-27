package guest

import (
	"github.com/heracraft/repose/internal/hostd/state"
)

// Memory commit on a host whose VMs keep what they touch (a Mac, VZ:
// nothing comes back while a machine runs, DECISIONS I-1666). Three 12 GiB
// machines on a 24 GB Mac each filled their memory with a build's page
// cache, macOS swapped 5.6-8.0 GB and the builds stalled (d2-lab,
// 2026-11-26 21:59Z). So the machines' configured memory together stays
// within Config.CommitMemMiB: a start gets the class's memory when it
// fits, less when the running machines have taken some, and is refused
// below MinCommitMiB.

// MinCommitMiB is the least memory a machine starts with when the host is
// nearly committed.
const MinCommitMiB = 2048

// committedMiB is what the guests holding memory, other than except, were
// started with: running, starting, stopping or resting (a guest resting as
// a save holds none now, but takes it back at its wake).
func (m *Manager) committedMiB(except string) uint64 {
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return 0
	}
	var n uint64
	for _, g := range gs {
		if g.GuestID == except {
			continue
		}
		switch g.State {
		case StateRunning, StateStarting, StateStopping:
			n += m.guestMemMiB(g)
		}
	}
	return n
}

// guestMemMiB is the memory a guest was started with.
func (m *Manager) guestMemMiB(g *state.Guest) uint64 {
	if g.MemMiB > 0 {
		return g.MemMiB
	}
	return m.classOf(g.Class).MemMiB
}

// commitSize is the memory g starts with now.
func (m *Manager) commitSize(g *state.Guest) (uint64, *Error) {
	want := m.classOf(g.Class).MemMiB
	used := m.committedMiB(g.GuestID)
	left := uint64(0)
	if m.cfg.CommitMemMiB > used {
		left = m.cfg.CommitMemMiB - used
	}
	size := min(want, left)
	size -= size % 128
	if size < MinCommitMiB {
		return 0, errf(CodeInsufficientCapacity, "not enough free memory: the running machines hold %d of this host's %d MiB, and a machine starts with at least %d MiB; stop one first", used, m.cfg.CommitMemMiB, MinCommitMiB)
	}
	return size, nil
}

// commitRoom refuses a create or a start, before anything is done, that
// commitSize would refuse at the boot: the guest stays as it was.
func (m *Manager) commitRoom(guestID, class string) *Error {
	if m.cfg.CommitMemMiB == 0 {
		return nil
	}
	_, e := m.commitSize(&state.Guest{GuestID: guestID, Class: class})
	return e
}

// MinCommitVCPUs is the fewest vCPUs a machine starts with when the
// host's cores are taken: it still starts, a little over the budget,
// rather than being refused for CPU.
const MinCommitVCPUs = 2

// committedVCPUs is what the guests holding a VM, other than except, were
// started with.
func (m *Manager) committedVCPUs(except string) uint32 {
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return 0
	}
	var n uint32
	for _, g := range gs {
		if g.GuestID == except {
			continue
		}
		switch g.State {
		case StateRunning, StateStarting, StateStopping:
			if g.VCPUs > 0 {
				n += g.VCPUs
			} else {
				n += m.classOf(g.Class).VCPUs
			}
		}
	}
	return n
}

// commitVCPUs is the vCPUs g starts with now: want, or what the running
// machines leave of CommitVCPUs, never fewer than MinCommitVCPUs.
func (m *Manager) commitVCPUs(g *state.Guest, want uint32) uint32 {
	used := m.committedVCPUs(g.GuestID)
	left := uint32(0)
	if m.cfg.CommitVCPUs > used {
		left = m.cfg.CommitVCPUs - used
	}
	return max(min(want, left), MinCommitVCPUs)
}
