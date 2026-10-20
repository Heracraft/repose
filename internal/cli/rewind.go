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

	"github.com/heracraft/repose/internal/undo"
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
	Rm      string // the point to delete instead of rewinding to one
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
			"it are stopped first. --list prints the points, --rm deletes one.",
		Example: "  repose rewind\n  repose rewind todo-app --to r-0199a1c2\n  repose rewind --rm r-0199a0f1",
		Args:    projectArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.List && (opts.To != "" || opts.Rm != "") {
				return cobraUsageError{errors.New("--list prints the points and takes no --to or --rm")}
			}
			if opts.Rm != "" && opts.To != "" {
				return cobraUsageError{errors.New("--rm deletes a point and --to rewinds to one; pass one")}
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
	cmd.Flags().StringVar(&opts.Rm, "rm", "", "delete the point called `POINT` and free the space it holds")
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
	if opts.Rm != "" {
		return rmPoint(ctx, e, opts, target, slug, points.Points, now)
	}
	var point *agentPoint
	switch {
	case opts.To != "":
		point = pointNamed(points.Points, opts.To)
		if point == nil {
			return exitf(ExitGuestNotRunning, "%s has no rewind point called %s.", slug, opts.To)
		}
	default:
		point = newestRewindable(points.Points)
		if point == nil {
			// Every point this project has is one a rewind took of the
			// state it replaced. Going back to one of those would restore
			// the wreck the last rewind escaped, so it takes --to to ask
			// for it by name.
			return exitf(ExitGuestNotRunning, "%s has only the points its own rewinds took; --to names one.", slug)
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
	_, _ = fmt.Fprintf(&b, "Rewound %s (%s).", slug, fmtElapsed(now().Sub(started)))
	if done.SafetyPoint != "" {
		// The one line of help this command keeps. Everything written
		// since the point is gone from the project, and this name is the
		// only thing on the box that can bring it back: the loss the
		// user cannot otherwise undo, which is the exception
		// "Trust the reader" makes (CLAUDE.md, DECISIONS I-485).
		_, _ = fmt.Fprintf(&b, " The state it replaced is the point %s.", done.SafetyPoint)
	}
	b.WriteString("\n")
	_, _ = fmt.Fprint(e.Out, b.String())
	return nil
}

// rmPoint is `repose rewind --rm POINT`: the one way a user frees undo
// space by hand. The agent's gc works to floors that keep a runaway from
// eating the history, so a store that is full of points a user no longer
// wants is theirs to empty.
func rmPoint(ctx context.Context, e *Env, opts RewindOptions, target sshTarget, slug string, points []agentPoint, now func() time.Time) error {
	point := pointNamed(points, opts.Rm)
	if point == nil {
		return exitf(ExitGuestNotRunning, "%s has no rewind point called %s.", slug, opts.Rm)
	}
	if !opts.Yes {
		confirm := opts.Confirm
		if confirm == nil {
			confirm = func(prompt string) (bool, error) {
				return askYesNo(ctx, prompt, false, "the delete")
			}
		}
		prompt := fmt.Sprintf("Delete %s's point taken %s? It cannot be rewound to after this. [y/N] ",
			slug, pointAge(now().Sub(point.TakenAt)))
		if err := confirmOr(confirm, prompt, "Nothing deleted."); err != nil {
			return err
		}
	}
	if err := agentRun(ctx, target, nil, "point", "delete", "--project", slug, "--name", point.Name); err != nil {
		return rewindFailed("delete "+slug+"'s point "+point.Name, err)
	}
	_, _ = fmt.Fprintf(e.Out, "Deleted %s.\n", point.Name)
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

// newestPoint is the project's newest point, safety points included:
// what `repose status` reports as the last point taken (spine_nouns.go).
// It picks by time, not by position: a list that came back in another
// order would otherwise name the wrong point (I-192).
func newestPoint(points []agentPoint) *agentPoint {
	var best *agentPoint
	for i := range points {
		if best == nil || points[i].TakenAt.After(best.TakenAt) {
			best = &points[i]
		}
	}
	return best
}

// newestRewindable is where a bare `repose rewind` goes: the newest
// point that is not one a rewind took of the state it replaced.
//
// It skips the safety points, because the newest point straight after a rewind is
// the wreck the user has just escaped — selecting it made a second
// `repose rewind` restore exactly what the first one undid.
func newestRewindable(points []agentPoint) *agentPoint {
	var best *agentPoint
	for i := range points {
		if points[i].Reason == undo.ReasonRewind {
			continue
		}
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
//
// There is no SIZE column. btrfs only reports what a snapshot holds
// through qgroups, which means quotas enabled, and quotas on a store of
// thousands of snapshots cost every write; so the figure was 0 B on
// every real box, which is worse than no column at all (I-814).
func writePointsTable(w io.Writer, points []agentPoint, now time.Time) error {
	// Newest first, whatever order they came back in.
	sorted := append([]agentPoint(nil), points...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].TakenAt.After(sorted[j].TakenAt) })
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "POINT\tTAKEN\tREASON")
	for _, p := range sorted {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, pointAge(now.Sub(p.TakenAt)), p.Reason)
	}
	return tw.Flush()
}
