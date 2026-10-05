package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// buildView turns a configuration op's log and phase into run's ✓ steps
// (DECISIONS I-320): "Evaluating your config", "Fetching 17/42 paths
// (123.4 MiB)", "Building 3/12 derivations", "Switching the machine".
// The steps come from the step lines of docs/interfaces/api.md "Build log
// lines" and from Nix's own "will be built" / "will be fetched" and
// per-path lines; I-57 kept those out of metrics, not out of a display.
// Nix's lines themselves go to raw when it is set (-v, or no terminal),
// and are hidden otherwise: a failure prints its own tail.
type buildView struct {
	pr  *progress
	raw io.Writer

	fetchN, fetchDone int
	fetchSize         string
	drvN, drvDone     int
	stage             int // 0 before nix build's lists, 1 fetching, 2 building, 3 built
}

const (
	stageNone = iota
	stageFetch
	stageBuild
	stageBuilt
)

var (
	nixDrvsRe  = regexp.MustCompile(`(?i)^(?:these (\d+)|this) derivations? will be built:`)
	nixPathsRe = regexp.MustCompile(`(?i)^(?:these (\d+)|this) paths? will be fetched \(([\d.]+ [KMGT]?i?B) download`)
)

func countOf(s string) int {
	if s == "" {
		return 1 // "this path", "this derivation"
	}
	n, _ := strconv.Atoi(s)
	return n
}

// Line takes one build log line.
func (v *buildView) Line(line string) {
	if v.raw != nil {
		_, _ = fmt.Fprintf(v.raw, "nix › %s\n", line) // a gone terminal is noticed by ctx
	}
	switch {
	case line == "waiting for a build slot":
		v.pr.Phase("Waiting for a build slot", "Got a build slot")
	case line == "fetching the base":
		v.pr.Phase("Fetching the base", "Fetched the base")
	case line == "evaluating configuration":
		v.pr.Phase("Evaluating your config", "Evaluated your config")
	case line == "building" || strings.HasPrefix(line, "building ") && !strings.HasPrefix(line, "building '"):
		// hostd's own line: nix build has started and is working out what
		// it needs; its lists follow in a moment.
		v.stage = stageNone
		v.pr.Phase("Working out what to fetch", "")
	case line == "built" || strings.HasPrefix(line, "built /"):
		v.stage = stageBuilt
		v.pr.End()
	case line == "switching the machine":
		v.switching()
	default:
		if m := nixDrvsRe.FindStringSubmatch(line); m != nil {
			v.drvN = countOf(m[1])
			return
		}
		if m := nixPathsRe.FindStringSubmatch(line); m != nil {
			v.fetchN, v.fetchSize = countOf(m[1]), m[2]
			if v.stage == stageNone {
				v.stage = stageFetch
				v.pr.Phase(v.fetchLabel())
			}
			return
		}
		if strings.HasPrefix(line, "copying path '") {
			v.fetchDone++
			if v.stage == stageFetch {
				if v.fetchDone >= v.fetchN && v.drvDone > 0 {
					v.startBuild()
					return
				}
				v.pr.Relabel(v.fetchLabel())
			}
			return
		}
		if strings.HasPrefix(line, "building '/nix/store/") {
			v.drvDone++
			switch v.stage {
			case stageNone:
				v.startBuild()
			case stageFetch:
				// A derivation whose inputs are here starts while other
				// paths still download; the step moves on once they are in.
				if v.fetchDone >= v.fetchN {
					v.startBuild()
				}
			case stageBuild:
				v.pr.Relabel(v.buildLabel())
			}
		}
	}
}

// Phase takes the op's phase from an op read (an apply op has no log).
func (v *buildView) Phase(phase string) {
	if phase == "apply_config" {
		v.switching()
	}
}

func (v *buildView) switching() {
	v.stage = stageBuilt
	v.pr.Phase("Switching the machine", "Switched the machine")
}

func (v *buildView) startBuild() {
	v.stage = stageBuild
	if v.drvN < v.drvDone {
		v.drvN = v.drvDone
	}
	v.pr.Phase(v.buildLabel())
}

func (v *buildView) fetchLabel() (string, string) {
	done := min(v.fetchDone, v.fetchN)
	return fmt.Sprintf("Fetching %d/%d %s (%s)", done, v.fetchN, plural(v.fetchN, "path", "paths"), v.fetchSize),
		fmt.Sprintf("Fetched %d %s (%s)", v.fetchN, plural(v.fetchN, "path", "paths"), v.fetchSize)
}

func (v *buildView) buildLabel() (string, string) {
	n := max(v.drvN, v.drvDone)
	return fmt.Sprintf("Building %d/%d %s", v.drvDone, n, plural(n, "derivation", "derivations")),
		fmt.Sprintf("Built %d %s", n, plural(n, "derivation", "derivations"))
}

// waitConfigOp waits on a configuration op (a build, or an apply of a
// built revision) showing its steps. On Ctrl-C it says the build goes on
// without the CLI and exits 130.
func waitConfigOp(ctx context.Context, e *Env, project *Project, opID string) (*Op, *progress, error) {
	pr := e.newProgress()
	v := &buildView{pr: pr}
	switch {
	case pr == nil:
		v.raw = e.ErrOut // --json: no steps, the log as before, off stdout
	case e.Verbose || !e.TTY:
		v.raw = pr
	}
	w := &opWatch{line: v.Line, phase: v.Phase}
	op, err := waitOpWith(ctx, e.Client, project.ID, opID, io.Discard, w)
	if err != nil {
		pr.Fail()
		if ctx.Err() != nil && errors.Is(err, context.Canceled) {
			_, _ = fmt.Fprintln(e.ErrOut, "Interrupted. The build keeps going on the machine.")
			return nil, pr, silent(ExitInterrupted)
		}
		return nil, pr, err
	}
	if op.State == "done" && !op.RebootRequired {
		pr.End()
	} else {
		pr.Fail()
	}
	return op, pr, nil
}

// printApplied is the line after a configuration op that finished: the
// revision is live, or (a kernel change on a running machine) built and
// waiting for a restart, in which case the api did not switch to it.
func printApplied(e *Env, project *Project, op *Op, revisionID string, pr *progress) {
	if op.RebootRequired {
		_, _ = fmt.Fprintf(e.Out, "Built revision %s. It changes the kernel, so it applies when %s restarts: `repose stop %s && repose start %s` when the agents are idle.\n",
			shortRev(revisionID), project.Slug, project.Slug, project.Slug)
		return
	}
	if pr == nil {
		_, _ = fmt.Fprintf(e.Out, "Applied revision %s.\n", shortRev(revisionID))
		return
	}
	_, _ = fmt.Fprintf(e.Out, "Applied revision %s in %s.\n", shortRev(revisionID), fmtElapsed(pr.Total()))
}
