package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/multiplexer"
)

// Temporary machines (DECISIONS I-347..I-355): `repose run --temp` makes
// a project that the api destroys, with no snapshot, 24 hours (or the
// given lifetime) after it was made, and `repose keep` makes it a normal
// one. The CLI never caches one (no by_dir, no remote key), never points
// the checkout's `repose` git remote at one, and destroys one at once when
// its tmux session ends.

const (
	tempDefault = 24 * time.Hour
	tempMin     = 10 * time.Minute
	tempMax     = 24 * time.Hour
)

// tempBare is the --temp flag's value when it is given without one
// (cobra's NoOptDefVal): the command then looks at its first argument for
// a duration, so `--temp 3h` works as well as `--temp=3h`.
const tempBare = "bare"

// parseTempDuration reads --temp's value: a Go duration (90m, 3h,
// 1h30m) from 10 minutes to 24 hours.
func parseTempDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("--temp takes a duration such as 3h or 90m, got %q", s)
	}
	if d < tempMin || d > tempMax {
		return 0, fmt.Errorf("--temp must be from 10m to 24h, got %s", s)
	}
	return d, nil
}

// looksLikeDuration is whether an argument after a bare --temp is its
// value rather than the prompt (run) or the PROJECT (sync): a number
// followed by a unit, as time.ParseDuration reads it.
func looksLikeDuration(s string) bool {
	_, err := time.ParseDuration(s)
	return err == nil
}

// resolveTempFlag turns the --temp flag's raw value and the command's
// arguments into the lifetime, consuming a duration argument after a bare
// --temp. raw is "" when --temp was not given.
func resolveTempFlag(raw string, args []string) (time.Duration, []string, error) {
	switch raw {
	case "":
		return 0, args, nil
	case tempBare:
		if len(args) > 0 && looksLikeDuration(args[0]) {
			d, err := parseTempDuration(args[0])
			return d, args[1:], err
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

// KeepCmd implements `repose keep [PROJECT]`: a temporary project becomes
// a normal one (PATCH expires_at: null). It keeps no remote.
func KeepCmd(ctx context.Context, e *Env, projectArg string) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	if project.ExpiresAt == nil {
		_, _ = fmt.Fprintf(e.Out, "%s is not temporary.\n", project.Slug)
		return nil
	}
	if _, err := e.Client.KeepProject(ctx, project.ID); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == "conflict" {
			return exitf(ExitGeneric, "%s is already being destroyed; it cannot be kept.", project.Slug)
		}
		return err
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
func tempSessionEndedWith(ctx context.Context, e *Env, t sshTarget, p *Project, m muxer) {
	if p == nil || p.ExpiresAt == nil {
		return
	}
	if ended, err := m.SessionEnded(ctx, t, p.Slug); err != nil || !ended {
		return
	}
	_, _ = fmt.Fprintf(e.ErrOut, "%s is temporary and its session has ended; destroying it.\n", p.Slug)
	if _, err := e.Client.DestroyProject(ctx, p.ID); err != nil {
		_, _ = fmt.Fprintf(e.ErrOut, "Could not destroy %s (%s). It goes at its expiry anyway; `repose rm %s` destroys it now.\n", p.Slug, oneLine(err.Error()), p.Slug)
		return
	}
	closeMaster(ctx, e, p.Slug)
}
