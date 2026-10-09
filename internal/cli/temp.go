package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/multiplexer"
)

// Temporary machines (DECISIONS I-347..I-355): `repose run --temp` makes
// a project that the api destroys, with no snapshot, 24 hours (or the
// given lifetime) after it was made, and `repose keep` makes it a normal
// one. The CLI never caches one (no by_dir, no remote key), never points
// the checkout's `repose` git remote at one, and destroys one at once when
// its tmux session ends with no work on it the laptop lacks (I-612).

const (
	tempDefault = 24 * time.Hour
	tempMin     = 10 * time.Minute
	tempMax     = 24 * time.Hour
)

// tempBare is the --temp flag's value when it is given without one
// (cobra's NoOptDefVal): the command then looks at its first argument for
// a duration, so `--temp 3h` works as well as `--temp=3h`.
const tempBare = "bare"

// parseTempDuration reads --temp's value: a duration as --since reads
// one (90m, 3h, 1h30m, 1d) from 10 minutes to 24 hours.
func parseTempDuration(s string) (time.Duration, error) {
	d, ok := sinceDuration(strings.TrimSpace(s))
	if !ok {
		return 0, fmt.Errorf("--temp takes a duration from 10m to 24h, such as 3h or 90m, got %q", s)
	}
	if d < tempMin || d > tempMax {
		return 0, fmt.Errorf("--temp must be from 10m to 24h, got %s", s)
	}
	return d, nil
}

// durationShape is a number and a unit, repeated: 3h, 90m, 1h30m, 1d,
// and also 2x, which is then refused as a duration rather than taken
// for a project.
var durationShape = regexp.MustCompile(`^[0-9]+[a-z]+([0-9]+[a-z]+)*$`)

// looksLikeDuration is whether an argument after a bare --temp is its
// value rather than the PROJECT, and whether keep's one argument is
// DURATION (review C3).
func looksLikeDuration(s string) bool {
	return durationShape.MatchString(s)
}

// resolveTempFlag turns the --temp flag's raw value and the command's
// arguments into the lifetime, consuming a duration argument after a bare
// --temp. raw is "" when --temp was not given.
func resolveTempFlag(raw string, args []string) (time.Duration, []string, error) {
	switch raw {
	case "":
		return 0, args, nil
	case tempBare:
		// Flags and arguments interleave, so `run spike --temp 3h` has
		// the duration after the PROJECT.
		for i, a := range args {
			if looksLikeDuration(a) {
				d, err := parseTempDuration(a)
				return d, append(slices.Clone(args[:i]), args[i+1:]...), err
			}
		}
		return tempDefault, args, nil
	}
	d, err := parseTempDuration(raw)
	return d, args, err
}

// tempName is `tmp-` and four lowercase base32 characters.
func tempName() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on the platforms repose ships for;
		// the time is still unlikely to repeat within a user's projects.
		n := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(n >> (8 * i))
		}
	}
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = alphabet[int(c)%len(alphabet)]
	}
	return "tmp-" + string(out)
}

// timeLeft renders how long a temporary machine has: minutes under an
// hour, else whole hours, rounded up so "destroyed in 1h" is never early
// (the api's temp_expiring notification says it the same way).
func timeLeft(d time.Duration) string {
	if d < time.Hour {
		m := int((d + time.Minute - 1) / time.Minute)
		if m < 1 {
			m = 1
		}
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh", int((d+time.Hour-1)/time.Hour))
}

// tempWhen is the part after "temporary: ", or "" for a project that is
// not temporary.
func tempWhen(p *Project, now time.Time) string {
	if p == nil || p.ExpiresAt == nil {
		return ""
	}
	left := p.ExpiresAt.Sub(now)
	if left <= 0 {
		return "its time is up, and it is destroyed once nobody is attached"
	}
	return "destroyed in " + timeLeft(left)
}

// tempLeft is the LEFT cell of `repose ls`: "5h", "40m", or "up" once
// the time has run out and the machine waits for nobody to be attached;
// "" for a project that is not temporary.
func tempLeft(p *Project, now time.Time) string {
	if p == nil || p.ExpiresAt == nil {
		return ""
	}
	left := p.ExpiresAt.Sub(now)
	if left <= 0 {
		return "up"
	}
	return timeLeft(left)
}

// tempLine is what run and attach print before they attach: "tmp-k3f9 is
// temporary: destroyed in 5h."
func tempLine(p *Project, now time.Time) string {
	w := tempWhen(p, now)
	if w == "" {
		return ""
	}
	return fmt.Sprintf("%s is temporary: %s.", p.Slug, w)
}

// createdLabel is the create phase's done line. It names herdr, and
// says nothing for tmux (I-502).
func createdLabel(p *Project, class string) string {
	if multiplexer.Normalize(p.Multiplexer) == multiplexer.Herdr {
		class += ", herdr"
	}
	if p.ExpiresAt == nil {
		return fmt.Sprintf("Created %s (%s)", p.Slug, class)
	}
	return fmt.Sprintf("Created %s (%s, temporary: destroyed %s)", p.Slug, class, p.ExpiresAt.Local().Format("Jan 2 15:04"))
}

// parseKeepDuration reads `repose keep PROJECT DURATION`: a Go duration
// within --temp's bounds, counted from now (I-612).
func parseKeepDuration(s string) (time.Duration, error) {
	d, ok := sinceDuration(strings.TrimSpace(s))
	if !ok || d < tempMin || d > tempMax {
		return 0, fmt.Errorf("keep takes a duration from 10m to 24h, such as 3h, got %q", s)
	}
	return d, nil
}

// KeepCmd implements `repose keep [PROJECT] [DURATION]`: with no
// duration a temporary project becomes a normal one (PATCH expires_at:
// null); with one it stays temporary and goes that long from now (PATCH
// expires_in_s, I-612). It keeps no remote.
func KeepCmd(ctx context.Context, e *Env, projectArg string, d time.Duration) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	if project.ExpiresAt == nil {
		_, _ = fmt.Fprintf(e.Out, "%s is not temporary.\n", project.Slug)
		return nil
	}
	var p *Project
	if d > 0 {
		p, err = e.Client.ExtendProject(ctx, project.ID, d)
	} else {
		p, err = e.Client.KeepProject(ctx, project.ID)
	}
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == "conflict" {
			return exitf(ExitGeneric, "%s is already being destroyed; it cannot be kept.", project.Slug)
		}
		return err
	}
	if d > 0 {
		if p == nil || p.ExpiresAt == nil {
			// An api from before I-612 ignores expires_in_s.
			return exitf(ExitGeneric, "The api did not take the new time for %s; it goes as before. `repose keep %s` keeps it for good.", project.Slug, project.Slug)
		}
		_, _ = fmt.Fprintf(e.Out, "%s is temporary: destroyed %s.\n", project.Slug, p.ExpiresAt.Local().Format("Jan 2 15:04"))
		return nil
	}
	_, _ = fmt.Fprintf(e.Out, "%s is no longer temporary.\n", project.Slug)
	return nil
}

// tempSessionEnded is run and attach after an attach the CLI waited on
// returned (DECISIONS I-352): when the project is temporary and its
// session is gone (the last tmux window exited; a detach leaves it; herdr
// never says so, I-602), the machine is destroyed at once, as
// `docker run --rm` would. The check rides the ssh master the attach
// used. Only the multiplexer answering that the session is over counts;
// an ssh that could not connect says nothing, and the machine then waits
// for its expiry.
func tempSessionEnded(ctx context.Context, e *Env, t sshTarget, p *Project) {
	tempSessionEndedWith(ctx, e, t, p, tmuxMux{})
}

// tempSessionEndedWith is tempSessionEnded on the machine's multiplexer.
// Before the destroy, one more ssh over the same master asks the
// checkout for work the laptop does not have (I-612): changed files
// beyond what the last sync wrote, and commits on any branch or worktree
// HEAD that are neither what the last sync sent nor on a remote-tracking
// branch. Either keeps the machine until its expiry, as does a check
// that cannot answer: exiting the last shell is a habit, and a temporary
// machine keeps no snapshot.
func tempSessionEndedWith(ctx context.Context, e *Env, t sshTarget, p *Project, m muxer) {
	if p == nil || p.ExpiresAt == nil {
		return
	}
	if ended, err := m.SessionEnded(ctx, t, p.Slug); err != nil || !ended {
		return
	}
	w, err := tempWork(ctx, t, p.Slug)
	if err != nil {
		_, _ = fmt.Fprintf(e.ErrOut, "%s is temporary and its session has ended, but its checkout could not be checked for work (%s), so %s. `repose rm %s` destroys it now.\n", p.Slug, oneLine(err.Error()), tempStays(p, time.Now()), p.Slug)
		return
	}
	if w.Commits > 0 || w.Files > 0 {
		_, _ = fmt.Fprintf(e.ErrOut, "%s has %s that your laptop does not, so %s. `repose attach %s` goes back to it.\n", p.Slug, w, tempStays(p, time.Now()), p.Slug)
		return
	}
	_, _ = fmt.Fprintf(e.ErrOut, "%s is temporary and its session has ended; destroying it.\n", p.Slug)
	if _, err := e.Client.DestroyProject(ctx, p.ID); err != nil {
		_, _ = fmt.Fprintf(e.ErrOut, "Could not destroy %s (%s). It goes at its expiry anyway; `repose rm %s` destroys it now.\n", p.Slug, oneLine(err.Error()), p.Slug)
		return
	}
	closeMaster(ctx, e, p.Slug)
}

// tempStays is when a temporary machine the CLI did not destroy goes:
// "it stays until 14:02", or, past its expiry, once nobody is on it.
func tempStays(p *Project, now time.Time) string {
	if !p.ExpiresAt.After(now) {
		return "it goes once nobody is attached"
	}
	at := p.ExpiresAt.Local()
	if at.Format("2006-01-02") == now.Local().Format("2006-01-02") {
		return "it stays until " + at.Format("15:04")
	}
	return "it stays until " + at.Format("Jan 2 15:04")
}

// machineWork is what tempWorkScript found.
type machineWork struct{ Commits, Files int }

func (w machineWork) String() string {
	var parts []string
	if w.Commits > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", w.Commits, plural(w.Commits, "commit", "commits")))
	}
	if w.Files > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", w.Files, plural(w.Files, "changed file", "changed files")))
	}
	return strings.Join(parts, " and ")
}

// tempWork runs tempWorkScript on the machine.
func tempWork(ctx context.Context, t sshTarget, slug string) (machineWork, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := runSSH(ctx, t, tempWorkScript(slug), nil)
	if err != nil {
		return machineWork{}, err
	}
	return parseTempWork(string(out))
}

func parseTempWork(out string) (machineWork, error) {
	var w machineWork
	seen := 0
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) != 2 {
			continue
		}
		n, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		switch f[0] {
		case "#commits":
			w.Commits, seen = n, seen+1
		case "#files":
			w.Files, seen = n, seen+1
		}
	}
	if seen != 2 {
		return w, fmt.Errorf("no answer from the check")
	}
	return w, nil
}

// tempWorkScript prints "#files N" and "#commits N" for the machine's
// checkout. A machine with no repository there has nothing git can lose
// and prints zeros. The last sync's own changes match its fingerprint
// (I-210) and do not count; files in another worktree all do.
func tempWorkScript(slug string) string {
	return checkoutVar(slug, "") + `cd "$repose_co" 2>/dev/null && git rev-parse --git-dir >/dev/null 2>&1 || { echo '#files 0'; echo '#commits 0'; exit 0; }
` + syncedFP + `repose_f=0
st=$(repose_dirty)
if [ -n "$st" ] && ! { [ -s "$repose_synced" ] && [ "$(repose_fp)" = "$(cat "$repose_synced")" ]; }; then repose_f=$(printf '%s\n' "$st" | wc -l); fi
repose_w=$(git worktree list --porcelain 2>/dev/null | sed -n 's/^worktree //p' | tail -n +2 | while IFS= read -r d; do git -C "$d" status --porcelain 2>/dev/null; done | wc -l)
echo "#files $((repose_f + repose_w))"
repose_sent=$([ -f "$repose_synced-key" ] && tail -n +2 "$repose_synced-key" | while IFS=' ' read -r c p; do [ -z "$p" ] && git cat-file -e "$c^{commit}" 2>/dev/null && printf '%s\n' "$c"; done)
repose_heads=$(git worktree list --porcelain 2>/dev/null | sed -n 's/^HEAD //p' | grep -v '^0*$' || true)
echo "#commits $(git rev-list --count $repose_heads --branches --not --remotes $repose_sent 2>/dev/null || echo 0)"
`
}
