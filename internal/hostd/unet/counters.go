package unet

import (
	"encoding/json"
	"os"
	"time"
)

// counterState is what CounterFile holds: the counters nft and the taps
// keep in the kernel on Linux, which outlive a hostd restart there.
type counterState struct {
	Egress  map[string]uint64            `json:"egress"`
	Blocked map[string]map[string]uint64 `json:"blocked"`
	Taps    map[string][2]uint64         `json:"taps"` // tap -> rx, tx (the guest's view)
}

// counterSave is how often the counters are written; a hostd killed
// between two writes loses at most this much, which the guest manager's
// first reading after the restart takes as its baseline anyway (I-448).
const counterSave = 2 * time.Second

func (n *Net) loadCounters() {
	if n.cfg.CounterFile == "" {
		return
	}
	b, err := os.ReadFile(n.cfg.CounterFile)
	if err != nil {
		return // none yet
	}
	var st counterState
	if json.Unmarshal(b, &st) != nil {
		n.log.Warn("unet counters unreadable; starting from zero", "event", "counters")
		return
	}
	for g, v := range st.Egress {
		n.egress[g] = v
	}
	for g, m := range st.Blocked {
		n.blocked[g] = m
	}
	n.savedTaps = st.Taps
}

func (n *Net) snapshotLocked() counterState {
	st := counterState{Egress: map[string]uint64{}, Blocked: map[string]map[string]uint64{}, Taps: map[string][2]uint64{}}
	for g, v := range n.egress {
		st.Egress[g] = v
	}
	for g, m := range n.blocked {
		c := map[string]uint64{}
		for k, v := range m {
			c[k] = v
		}
		st.Blocked[g] = c
	}
	for t, p := range n.ports {
		st.Taps[t] = [2]uint64{p.rx.Load(), p.tx.Load()}
	}
	return st
}

// saveCounters writes the file atomically (temp and rename).
func (n *Net) saveCounters() {
	if n.cfg.CounterFile == "" {
		return
	}
	n.mu.Lock()
	st := n.snapshotLocked()
	n.mu.Unlock()
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := n.cfg.CounterFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		n.log.Warn("unet counters not saved", "event", "counters", "err", err.Error())
		return
	}
	if err := os.Rename(tmp, n.cfg.CounterFile); err != nil {
		n.log.Warn("unet counters not saved", "event", "counters", "err", err.Error())
	}
}

func (n *Net) saveLoop() {
	if n.cfg.CounterFile == "" {
		return
	}
	t := time.NewTicker(counterSave)
	defer t.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-t.C:
			n.saveCounters()
		}
	}
}
