package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// `repose rewind` is undo on a machine the user owns (docs/workstreams/SPINE.md
// Contract 4): the project's files go back to a rewind point, a
// read-only btrfs snapshot repose-agent took. A rewind is only safe once
// nothing is writing in the live area, so the CLI quiesces the project
// first and the agent refuses while anything is left
// (../research/wrapper-undo.md, "a rewind must stop the project first").

// RewindOptions is what `repose rewind` was asked for.
type RewindOptions struct {
	Project string
	To      string // the point to rewind to, "" for the newest
	List    bool
	Yes     bool
	Deps    resolveDeps                       // how a checkout resolves; zero is the directory alone
	Now     func() time.Time                  // the clock the result line times the rewind by
	Confirm func(prompt string) (bool, error) // the question; nil asks on the terminal
}

func newRewindCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var opts RewindOptions
	cmd := &cobra.Command{
		Use:   "rewind [PROJECT]",
		Short: "Rewind a project's files to a rewind point",
		Long: "Rewind the project's files to the point it was at: the newest point, or the\n" +
			"one --to names. Everything written since is lost, and the agents writing in\n" +
			"it are stopped first. --list prints the points.",
		Example: "  repose rewind\n  repose rewind todo-app --to r-0199a1c2",
		Args:    projectArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.List && opts.To != "" {
				return cobraUsageError{errors.New("--list prints the points and takes no --to")}
			}
			project, err := projectFrom(args, g)
			if err != nil {
				return err
			}
			e, err := env()
			if err != nil {
				return err
			}
			opts.Project = project
			opts.Deps = defaultResolveDeps()
			return RewindCmd(cmd.Context(), e, opts)
		},
	}
	cmd.Flags().StringVar(&opts.To, "to", "", "rewind to the point called `POINT`, not the newest")
	cmd.Flags().BoolVar(&opts.List, "list", false, "print the project's rewind points")
	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "rewind without asking")
	return cmd
}

// RewindCmd implements `repose rewind [PROJECT]`.
func RewindCmd(ctx context.Context, e *Env, opts RewindOptions) error {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	m, place, err := placeFor(e, opts.Project, opts.Deps)
	if err != nil {
		return err
	}
	configPath, err := machinesConfigPath()
	if err != nil {
		return err
	}
	target := machineTarget(configPath, m)
	slug := place.Slug
	var points agentPoints
	if err := agentRun(ctx, target, &points, "point", "list", "--project", slug); err != nil {
		return rewindFailed("read "+slug+"'s rewind points", err)
	}
	if len(points.Points) == 0 {
		return exitf(ExitGuestNotRunning, "%s has no rewind points.", slug)
	}
	if opts.List {
		return writePointsTable(e.Out, points.Points, now())
	}
	point := newestPoint(points.Points)
	if opts.To != "" {
		point = pointNamed(points.Points, opts.To)
		if point == nil {
			return exitf(ExitGuestNotRunning, "%s has no rewind point called %s.", slug, opts.To)
		}
	}
	if !opts.Yes {
		confirm := opts.Confirm
		if confirm == nil {
			// On a terminal; off one askYesNo is exit 2 naming --yes.
			confirm = func(prompt string) (bool, error) {
				return askYesNo(ctx, prompt, false, "the rewind")
			}
		}
		prompt := fmt.Sprintf("Rewind %s to the point taken %s? Everything written since is lost. [y/N] ",
			slug, pointAge(now().Sub(point.TakenAt)))
		if err := confirmOr(confirm, prompt, "Nothing rewound."); err != nil {
			return err
		}
	}
	started := now()
	var quiesced agentQuiesce
	if err := agentRun(ctx, target, &quiesced, "quiesce", "--project", slug); err != nil {
		return rewindFailed("stop what is writing in "+slug, err)
	}
	if len(quiesced.Busy) > 0 {
		return exitf(ExitGuestNotRunning, "Could not stop everything writing in %s: %s.", slug, strings.Join(quiesced.Busy, ", "))
	}
	var done agentRewind
	if err := agentRun(ctx, target, &done, "point", "rewind", "--project", slug, "--name", point.Name); err != nil {
		return rewindFailed("rewind "+slug, err)
	}
	if !done.Rewound {
		return exitf(ExitGeneric, "Could not rewind %s: the machine did not report the swap.", slug)
	}
	var b strings.Builder
	switch n := len(quiesced.Stopped); {
	case n == 1:
		b.WriteString("Stopped 1 process. ")
	case n > 1:
		_, _ = fmt.Fprintf(&b, "Stopped %d processes. ", n)
	}
	_, _ = fmt.Fprintf(&b, "Rewound %s (%s).\n", slug, fmtElapsed(now().Sub(started)))
	_, _ = fmt.Fprint(e.Out, b.String())
	return nil
}

// rewindFailed renders a refusal from repose-agent: a point that is gone
// and a project that is still being written to are both the wrong state
// (exit 5), as a project that is not running is for every other command.
func rewindFailed(step string, err error) error {
	var ae *agentError
	if errors.As(err, &ae) && (ae.Code == agentCodeBusy || ae.Code == agentCodeNotFound) {
		return exitCausef(ExitGuestNotRunning, err, "Could not %s: %s.", step, ae.Error())
	}
	return stepFailed(step, err, "")
}

// newestPoint picks by time, not by position: a list that came back in
// another order would otherwise rewind to the wrong point (I-192).
func newestPoint(points []agentPoint) *agentPoint {
	var best *agentPoint
	for i := range points {
		if best == nil || points[i].TakenAt.After(best.TakenAt) {
			best = &points[i]
		}
	}
	return best
}

func pointNamed(points []agentPoint, name string) *agentPoint {
	for i := range points {
		if points[i].Name == name {
			return &points[i]
		}
	}
	return nil
}

// writePointsTable is `repose rewind --list`.
func writePointsTable(w io.Writer, points []agentPoint, now time.Time) error {
	// Newest first, whatever order they came back in (newestPoint's rule).
	sorted := append([]agentPoint(nil), points...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].TakenAt.After(sorted[j].TakenAt) })
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "POINT\tTAKEN\tREASON\tSIZE")
	for _, p := range sorted {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.Name, pointAge(now.Sub(p.TakenAt)), p.Reason, sizeWords(p.Bytes))
	}
	return tw.Flush()
}
