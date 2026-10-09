package cli

import (
	"context"
	"errors"
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
	mux   string // "herdr", "tmux", or "" when the machine did not answer
	disk  guestDisk
	git   []gitRow // nil when the machine has no checkout or did not say
	// probeErr is why the machine did not say (I-634): "ssh_timeout"
	// when it did not answer in time, "ssh_failed" when ssh failed; ""
	// when it answered.
	probeErr string
	// probeDetail is ssh's last error line, for the row.
	probeDetail string
}

// guestStatusRead asks the guest, best effort, for its listening
// processes, its root filesystem, the multiplexer that runs now (I-509)
// and its checkout's git state (I-616). have is the laptop's commit ids
// (laptopCommits), sent on stdin, so the machine can count the commits
// the laptop lacks. It rides a multiplexed connection when one is open
// and never leaves a new one behind (ControlMaster=no), so a status does
// not hold a gateway session for ControlPersist's ten minutes.
func guestStatusRead(ctx context.Context, t sshTarget, slug string, have []string) guestStatus {
	ctx, cancel := context.WithTimeout(ctx, statusProcsTimeout)
	defer cancel()
	args := append([]string{"-o", "ControlMaster=no", "-o", "ConnectTimeout=3", "-o", "BatchMode=yes"}, t.Args...)
	var stdin io.Reader
	if len(have) > 0 {
		stdin = strings.NewReader(strings.Join(have, "\n") + "\n")
	}
	raw, err := runSSH(ctx, sshTarget{Args: args}, statusScript(slug), stdin)
	out := string(raw)
	if err != nil && !strings.Contains(out, "#mux") {
		var se *sshError
		if ctx.Err() != nil || (errors.As(err, &se) && strings.Contains(strings.ToLower(se.Stderr), "timed out")) {
			return guestStatus{probeErr: "ssh_timeout"}
		}
		g := guestStatus{probeErr: "ssh_failed"}
		if errors.As(err, &se) {
			g.probeDetail = sshStderrDetail(se.Stderr)
		}
		return g
	}
	mux := multiplexer.Tmux
	if _, m, ok := strings.Cut(out, "#mux"); ok {
		m, _, _ = strings.Cut(m, "#git")
		if strings.TrimSpace(m) == multiplexer.Herdr {
			mux = multiplexer.Herdr
		}
	}
	return guestStatus{procs: parseStatusProcs(out), mux: mux, disk: parseStatusDisk(out), git: parseStatusGit(out, len(have) > 0)}
}

// statusScript is statusProcsScript, which reads the laptop's commit
// ids from stdin first so no later command takes them, then the git
// section.
func statusScript(slug string) string {
	return "repose_have=$(cat)\n" + statusProcsScript + "\necho '#git'\n" + checkoutVar(slug, "") + statusGitScript
}

// statusGitScript prints, after "#git", one line per worktree of the
// machine's checkout, the checkout itself first, tab-separated: "wt",
// the worktree's folder, its branch ("" when detached), how many of its
// commits are not among the laptop's ("-" when the laptop sent none, or
// none of them is on the machine), how many files `git status` lists
// ("-" when it took over two seconds) and its last commit's time in Unix
// seconds ("" for none). Nothing for a machine with no checkout or a
// checkout that is not a repository. The laptop's ids are filtered to
// commits the machine has, so one it lacks never fails the count.
const statusGitScript = `[ "$repose_co" != "$HOME" ] && cd "$repose_co" 2>/dev/null && git rev-parse --git-dir >/dev/null 2>&1 && {
repose_not=
[ -n "$repose_have" ] && repose_not=$(printf '%s\n' "$repose_have" | grep -E '^[0-9a-f]{40,64}$' | git cat-file --batch-check='%(objectname) %(objecttype)' 2>/dev/null | awk '$2 == "commit" { print "^" $1 }')
git worktree list --porcelain 2>/dev/null | awk '
/^worktree / { p = substr($0, 10); b = ""; bare = 0 }
/^branch / { b = substr($0, 8); sub(/^refs\/heads\//, "", b) }
/^bare/ { bare = 1 }
/^$/ { if (p != "" && !bare) print p "\t" b; p = "" }
END { if (p != "" && !bare) print p "\t" b }' | while IFS='	' read -r p b; do
  [ -d "$p" ] || continue
  n=-
  [ -n "$repose_not" ] && n=$(printf '%s\n' "$repose_not" | git -C "$p" rev-list --count HEAD --stdin 2>/dev/null)
  if s=$(timeout 2 git -C "$p" status --porcelain 2>/dev/null); then d=$(printf '%s' "$s" | grep -c .); else d=-; fi
  t=$(git -C "$p" log -1 --format=%ct 2>/dev/null)
  printf 'wt\t%s\t%s\t%s\t%s\t%s\n' "${p##*/}" "$b" "${n:--}" "$d" "$t"
done
}
true
`

// gitRow is one worktree of the machine's checkout as status shows it
// and `status --json` carries it (I-616).
type gitRow struct {
	// Worktree is the folder under /home/dev: the checkout's, or a
	// worktree's beside it.
	Worktree string `json:"worktree"`
	// Branch is "" when the worktree is on a detached HEAD.
	Branch string `json:"branch"`
	// NotOnLaptop counts the branch's commits the laptop that ran status
	// does not have, which `git fetch repose` brings; absent when status
	// ran outside the project's checkout and cannot tell.
	NotOnLaptop *int `json:"commits_not_on_laptop,omitempty"`
	// Uncommitted counts the files `git status` lists; absent when it
	// did not answer in time.
	Uncommitted  *int       `json:"uncommitted_files,omitempty"`
	LastCommitAt *time.Time `json:"last_commit_at,omitempty"`
}

// parseStatusGit reads statusGitScript's lines. counted is whether the
// laptop sent its commits: without them no count is believed.
func parseStatusGit(out string, counted bool) []gitRow {
	_, sec, ok := strings.Cut(out, "#git")
	if !ok {
		return nil
	}
	var rows []gitRow
	for _, l := range strings.Split(sec, "\n") {
		f := strings.Split(l, "\t")
		if len(f) != 6 || f[0] != "wt" || f[1] == "" {
			continue
		}
		r := gitRow{Worktree: f[1], Branch: f[2]}
		if n, err := strconv.Atoi(f[3]); err == nil && n >= 0 && counted {
			r.NotOnLaptop = &n
		}
		if d, err := strconv.Atoi(f[4]); err == nil && d >= 0 {
			r.Uncommitted = &d
		}
		if ts, err := strconv.ParseInt(f[5], 10, 64); err == nil && ts > 0 {
			t := time.Unix(ts, 0).UTC()
			r.LastCommitAt = &t
		}
		rows = append(rows, r)
	}
	return rows
}

// laptopCommitsMax bounds the ids status sends: a laptop with thousands
// of branches still answers in one round trip.
const laptopCommitsMax = 4000

// laptopCommits is the commit at every branch tip of p's checkouts on
// this laptop, their fetched `repose/*` branches included: the checkout
// at the working directory when it is p's (its `repose` remote names p's
// machine, or its origin is p's remote), and every other folder whose
// `repose` remote names it (laptopFolders, I-638), so `status todo-app`
// from ~ counts too. Nil when there is none, and status then shows each
// branch's last commit instead of what the laptop lacks (I-616).
func laptopCommits(e *Env, p *Project) []string {
	var roots []string
	if root := gitRepoRoot(e.Cwd); root != "" {
		if reposeRemoteHost(remoteURLOf(root, reposeRemoteName)) == p.Slug || (p.RemoteURL != "" && gitRemoteOrigin(root) == p.RemoteURL) {
			roots = append(roots, root)
		}
	}
	for _, r := range e.laptopFolders(p) {
		if len(roots) == 0 || r != roots[0] {
			roots = append(roots, r)
		}
	}
	seen := map[string]bool{}
	var ids []string
	for _, root := range roots {
		out, err := gitCmd(root, "for-each-ref", "--format=%(objectname)", "refs/heads", "refs/remotes")
		if err != nil {
			continue
		}
		for _, id := range nonEmptyLines(out) {
			if !seen[id] && len(ids) < laptopCommitsMax {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if head, err := gitHeadCommit(root); err == nil && !seen[head] {
			seen[head] = true
			ids = append(ids, head)
		}
	}
	return ids
}

// writeGitRowsOr prints the checkout row, one line per worktree:
//
//	checkout   main: 3 commits not on this laptop, 2 files not committed
//	           worktree-1: nothing new
//
// or, when the probe failed, one row that says the checkout is unknown
// and why (I-634): no row would read as a machine with no checkout.
func writeGitRowsOr(w io.Writer, rows []gitRow, now time.Time, probeErr, detail string) {
	switch probeErr {
	case "ssh_timeout":
		statusRow(w, "checkout", fmt.Sprintf("unknown (no ssh answer in %d s)", int(statusProcsTimeout.Seconds())))
		return
	case "ssh_failed":
		if detail != "" {
			statusRow(w, "checkout", "unknown (ssh failed: "+detail+")")
		} else {
			statusRow(w, "checkout", "unknown (ssh failed)")
		}
		return
	}
	for i, r := range rows {
		line := gitRowText(r, now)
		if i == 0 {
			statusRow(w, "checkout", line)
		} else {
			_, _ = fmt.Fprintln(w, statusIndent+line)
		}
	}
}

func gitRowText(r gitRow, now time.Time) string {
	name := r.Branch
	if name == "" {
		name = r.Worktree + " (detached)"
	}
	var parts []string
	switch {
	case r.NotOnLaptop != nil && *r.NotOnLaptop > 0:
		parts = append(parts, count(*r.NotOnLaptop, "commit")+" not on this laptop")
	case r.NotOnLaptop == nil && r.LastCommitAt != nil:
		age := "just now"
		if d := now.Sub(*r.LastCommitAt); d >= time.Minute {
			age = compactAge(d) + " ago"
		}
		parts = append(parts, "last commit "+age)
	}
	if r.Uncommitted != nil && *r.Uncommitted > 0 {
		parts = append(parts, count(*r.Uncommitted, "file")+" not committed")
	}
	if len(parts) == 0 {
		if r.NotOnLaptop == nil && r.LastCommitAt == nil {
			parts = append(parts, "no commits")
		} else {
			parts = append(parts, "nothing new")
		}
	}
	return name + ": " + strings.Join(parts, ", ")
}
