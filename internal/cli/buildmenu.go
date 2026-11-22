package cli

import (
	"fmt"
	"regexp"
	"strings"
)

// An erofs machine's configuration op carries guestd's ApplyMenu lines
// (docs/interfaces/vsock-guestd.md "ApplyMenu"): "installing postgresql",
// then "postgresql 16 ready" or "postgresql failed: ...", "removing
// redis", "redis removed". The CLI shows each one as guestd wrote it and
// adds only the step's ✓ and time (DECISIONS I-1488): no build steps,
// because nothing is built.

// menuStartRe is a line that opens an item's step. Nix never prints a
// line that starts this way, so a view that does not know the runtime
// (a create's) can tell an erofs log from its first such line.
var menuStartRe = regexp.MustCompile(`^(installing|removing) [a-z0-9]`)

// menuFailRe is a line that says an item did not install.
var menuFailRe = regexp.MustCompile(`^(could not |\d+ items failed: )| failed: `)

func isMenuStart(line string) bool { return menuStartRe.MatchString(line) }

// menuLine shows one ApplyMenu line: a start opens a step labelled with
// it, the next line closes it as its ✓ line, and a failure closes it
// without one and is printed as it is.
func (v *buildView) menuLine(line string) {
	if line == "switching the machine" {
		return // the api's apply marker; ApplyMenu's own lines say it
	}
	if v.pr == nil {
		if v.raw != nil {
			_, _ = fmt.Fprintln(v.raw, line) // --json: the lines off stdout, as written
		}
		return
	}
	if rest, ok := v.menuStepLine(line); ok {
		// A step of the running item (DECISIONS I-1536): printed as
		// guestd wrote it and shown as the item's phase, which stays
		// open for the item's own ready line.
		_, _ = fmt.Fprintln(v.pr, line)
		v.pr.Relabel(v.menuStep+" ("+rest+")", "")
		return
	}
	switch {
	case isMenuStart(line):
		v.menuStep = line
		v.pr.Phase(line, "")
	case menuFailRe.MatchString(line):
		v.menuStep = ""
		v.pr.Fail()
		_, _ = fmt.Fprintln(v.pr, line)
	case v.menuStep != "":
		v.pr.Relabel(v.menuStep, line)
		v.menuStep = ""
		v.pr.End()
	default:
		_, _ = fmt.Fprintln(v.pr, "✓ "+strings.TrimSpace(line))
	}
}

// menuStepLine reports whether line is a step of the item the open step
// names ("redis: unpacking" while "installing redis" runs), and the step.
func (v *buildView) menuStepLine(line string) (string, bool) {
	if v.menuStep == "" {
		return "", false
	}
	f := strings.Fields(v.menuStep)
	if len(f) < 2 {
		return "", false
	}
	rest, ok := strings.CutPrefix(line, f[1]+": ")
	return rest, ok && rest != ""
}
