package cli

import (
	"context"

	"github.com/spf13/cobra"
)

// --json on the commands that change a project (F1 of the 2026-10-08
// ergonomics review, DECISIONS I-609): start, stop, resize and sync print
// the Project object when they finish, and snapshots create the Snapshot,
// so a script reads the result instead of parsing a sentence. The
// sentence and any notes go to stderr instead of stdout.

// addProjectJSONFlag is the --json flag of a command that prints the
// Project when it finishes.
func addProjectJSONFlag(cmd *cobra.Command, g *globalFlags) {
	cmd.Flags().BoolVar(&g.json, "json", false, "print the Project object as JSON when done")
}

// withProjectJSON runs fn; under --json its text goes to stderr, and the
// project it acted on, read again once fn returns, is printed on stdout.
// acted names that project; nil reads projectArg again.
func withProjectJSON(ctx context.Context, e *Env, projectArg string, fn func() error, acted func() *Project) error {
	if !e.JSON {
		return fn()
	}
	out := e.Out
	e.Out = e.ErrOut
	err := fn()
	e.Out = out
	if err != nil {
		return err
	}
	var p *Project
	if acted != nil {
		if a := acted(); a != nil {
			if p, err = e.Client.GetProject(ctx, a.ID); err != nil {
				return err
			}
		}
	}
	if p == nil {
		if p, err = requireProject(ctx, e, projectArg); err != nil {
			return err
		}
	}
	return writeJSONOut(out, p)
}

// stopWithJSON is `repose stop` with its --json (I-609): the stop lines
// go to stderr and the projects it acted on, read again, to stdout: one
// Project for one PROJECT, an array for several or for --idle.
func stopWithJSON(ctx context.Context, e *Env, o StopOptions) error {
	if !e.JSON {
		return StopProjectsCmd(ctx, e, o)
	}
	var acted []*Project
	o.Acted = &acted
	out := e.Out
	e.Out = e.ErrOut
	err := StopProjectsCmd(ctx, e, o)
	e.Out = out
	if err != nil {
		return err
	}
	fresh := make([]*Project, 0, len(acted))
	for _, p := range acted {
		q, err := e.Client.GetProject(ctx, p.ID)
		if err != nil {
			return err
		}
		fresh = append(fresh, q)
	}
	if !o.Idle && len(o.Projects) <= 1 && len(fresh) == 1 {
		return writeJSONOut(out, fresh[0])
	}
	return writeJSONOut(out, fresh)
}
