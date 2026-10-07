package cli

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/multiplexer"
)

// The listening processes `repose status PROJECT` shows for a running
// guest (DECISIONS I-200, I-207): which dev servers are still up, for how
// long and at what memory, so a stale one can be found and stopped by
// hand. Nothing is ever stopped for the user.
//
// They are read from the guest over the user's own SSH at the moment
// status runs (ss and ps, stock tools), never sampled or stored by the
// platform: the privacy policy promises the platform records process names,
// CPU, memory and network bytes, and nothing else. Only names, ports, ages
// and memory come back, never a command line. The api's lines print first
// and stand alone; a guest that does not answer within a few seconds (no
// certificate yet, a network problem) just shows no list.

// statusProcsTimeout bounds the SSH status makes.
const statusProcsTimeout = 4 * time.Second

// listeningProc is one line of the list.
type listeningProc struct {
	Comm   string
	Port   int
	Age    time.Duration
	RSS    int64 // bytes
	HasPID bool
}

// statusProcsScript also says how full the root filesystem is, after
// "#df" (statfs's blocks, blocks available to dev and block size, I-567),
// and what runs the machine's terminals now (I-509), after "#mux":
// "herdr", or nothing for tmux.
var statusProcsScript = `ss -Hltnp 2>/dev/null; echo '#ps'; ps -o pid=,etimes=,rss=,comm= -u "$(id -u)"; echo '#df'; stat -f -c '%b %a %S' /; echo '#mux'; ` + muxProbeScript + ` && echo herdr`

// guestDisk is the guest's root filesystem as statfs sees it: what the
// guest's writes run out of. The api's disk_used_bytes is the host
// volume's allocated blocks, which only the guest's fstrim gives back
// (daily, and at every stop since I-585), so it can read fuller than a
// disk with room (kanali, 2026-10-07: 39.5 of 40 GB
// allocated, 33 GB used). The api's root_used_bytes and root_size_bytes
// are the same figure from the newest sample (apiDisk). Zero Size means
// the guest did not say.
type guestDisk struct {
	Used, Size int64
}

// Percent is used over size as guestd's disk_high counts it (blocks less
// those available to dev, so root's reserve counts as used).
func (d guestDisk) Percent() int {
	if d.Size <= 0 {
		return 0
	}
	return int(d.Used * 100 / d.Size)
}

// parseStatusDisk reads the "#df" section: "BLOCKS AVAIL BSIZE".
func parseStatusDisk(out string) guestDisk {
	_, rest, ok := strings.Cut(out, "#df")
	if !ok {
		return guestDisk{}
	}
	rest, _, _ = strings.Cut(rest, "#mux")
	f := strings.Fields(rest)
	if len(f) < 3 {
		return guestDisk{}
	}
	blocks, err1 := strconv.ParseInt(f[0], 10, 64)
	avail, err2 := strconv.ParseInt(f[1], 10, 64)
	bsize, err3 := strconv.ParseInt(f[2], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || blocks <= 0 || bsize <= 0 || avail < 0 || avail > blocks {
		return guestDisk{}
	}
	return guestDisk{Used: (blocks - avail) * bsize, Size: blocks * bsize}
}

var ssUsers = regexp.MustCompile(`users:\(\("((?:[^"\\]|\\.)*)",pid=(\d+)`)

// parseStatusProcs joins ss's listeners (the forwardable ones, as
// auto-forward sees them) with ps's age and memory by pid.
func parseStatusProcs(out string) []listeningProc {
	out, _, _ = strings.Cut(out, "#mux")
	out, _, _ = strings.Cut(out, "#df")
	ssPart, psPart, _ := strings.Cut(out, "#ps")
	type psRow struct {
		age time.Duration
		rss int64
	}
	ps := map[int]psRow{}
	for _, l := range strings.Split(psPart, "\n") {
		f := strings.Fields(l)
		if len(f) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		et, err2 := strconv.ParseInt(f[1], 10, 64)
		rss, err3 := strconv.ParseInt(f[2], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		ps[pid] = psRow{time.Duration(et) * time.Second, rss * 1024}
	}
	listeners := parseListeners(ssPart)
	var procs []listeningProc
	for _, line := range strings.Split(ssPart, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		i := strings.LastIndex(f[3], ":")
		if i < 0 {
			continue
		}
		port, err := strconv.Atoi(f[3][i+1:])
		if err != nil {
			continue
		}
		if _, ok := listeners[port]; !ok {
			continue
		}
		delete(listeners, port) // one line per port (a server's v4 and v6 sockets)
		p := listeningProc{Port: port}
		if m := ssUsers.FindStringSubmatch(line); m != nil {
			p.Comm = m[1]
			if pid, err := strconv.Atoi(m[2]); err == nil {
				if row, ok := ps[pid]; ok {
					p.Age, p.RSS, p.HasPID = row.age, row.rss, true
				}
			}
		}
		procs = append(procs, p)
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].Port < procs[j].Port })
	return procs
}

// compactAge is "12m", "5h" or "3d".
func compactAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

// writeListening prints the list under the status lines:
//
//	listening  node :5173 up 3d 410.0 MB
//	           :5432
func writeListening(w io.Writer, procs []listeningProc) {
	for i, p := range procs {
		label := "  listening  "
		if i > 0 {
			label = "             "
		}
		s := fmt.Sprintf(":%d", p.Port)
		if p.Comm != "" {
			s = p.Comm + " " + s
		}
		if p.HasPID {
			s += fmt.Sprintf(" up %s %s", compactAge(p.Age), humanBytes(p.RSS))
		}
		_, _ = fmt.Fprintln(w, label+s)
	}
}

// guestStatus is what status reads from the guest itself.
type guestStatus struct {
	procs []listeningProc
	mux   string // "herdr", "tmux", or "" when the guest did not answer
	disk  guestDisk
}

// guestStatusRead asks the guest, best effort, for its listening
// processes, its root filesystem and the multiplexer that runs now
// (I-509). It rides a multiplexed connection when one is open and never
// leaves a new one behind (ControlMaster=no), so a status does not hold a
// gateway session for ControlPersist's ten minutes.
func guestStatusRead(ctx context.Context, t sshTarget) guestStatus {
	ctx, cancel := context.WithTimeout(ctx, statusProcsTimeout)
	defer cancel()
	args := append([]string{"-o", "ControlMaster=no", "-o", "ConnectTimeout=3", "-o", "BatchMode=yes"}, t.Args...)
	out, err := runSSH(ctx, sshTarget{Args: args}, statusProcsScript, nil)
	if err != nil && !strings.Contains(string(out), "#mux") {
		return guestStatus{}
	}
	mux := multiplexer.Tmux
	if _, m, ok := strings.Cut(string(out), "#mux"); ok && strings.TrimSpace(m) == multiplexer.Herdr {
		mux = multiplexer.Herdr
	}
	return guestStatus{procs: parseStatusProcs(string(out)), mux: mux, disk: parseStatusDisk(string(out))}
}
