package cli

import (
	"context"
	"fmt"
)

// classSpec is what a size class gives and which plan runs it, as
// docs/PRICING.md and apps/web/src/content/docs/billing.md list it. The
// CLI keeps its own copy rather than importing internal/billing (which
// pulls in the api's database code); TestClassSpecsMatchBillingAndHost
// pins it to both sources.
type classSpec struct {
	VCPUs int
	MemGB int
	// Plan is the smallest plan the class fits: a plan buys memory that
	// may run at once (DECISIONS I-289).
	Plan string
}

var classSpecs = map[string]classSpec{
	"small": {VCPUs: 2, MemGB: 4, Plan: "Solo"},
	"large": {VCPUs: 4, MemGB: 8, Plan: "Solo"},
	"xl":    {VCPUs: 8, MemGB: 16, Plan: "Plus"},
}

// classSummary is "4 vCPU, 8 GB memory; fits the Solo plan" or "8 vCPU,
// 16 GB memory; needs the Plus plan".
func classSummary(class string) string {
	c, ok := classSpecs[class]
	if !ok {
		return class
	}
	verb := "fits the"
	if c.Plan != "Solo" {
		verb = "needs the"
	}
	return fmt.Sprintf("%d vCPU, %d GB memory; %s %s plan", c.VCPUs, c.MemGB, verb, c.Plan)
}

// classChangePrompt is asked before a running project is stopped to change
// its size: the stop ends every process on the machine, agents included,
// and the new size takes a different share of the plan's memory.
func classChangePrompt(slug, from, to string) string {
	return fmt.Sprintf("%s is running. Changing it from %s to %s stops it (taking a snapshot first), which ends every process on it, agents included, then starts it again. Go ahead? [y/N] ", slug, from, to)
}

// ResizeClassCmd implements `repose resize --size small|large|xl` (DECISIONS
// I-260). The api changes a project's class only while it is stopped, so
// a running project is stopped, changed and started again, after a
// confirmation (confirm nil means --yes). The class reaches the machine at
// its next start: StartGuest carries it.
func ResizeClassCmd(ctx context.Context, e *Env, projectArg, class string, confirm func(prompt string) (bool, error)) error {
	if _, ok := classSpecs[class]; !ok {
		return exitf(ExitUsage, "--size must be small, large or xl, got %q.", class)
	}
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	s := project.Slug
	if project.Class == class {
		_, _ = fmt.Fprintf(e.Out, "%s is already %s (%s).\n", s, class, classSummary(class))
		return nil
	}
	from := project.Class
	switch project.State {
	case "stopped":
		if _, err := e.Client.PatchProject(ctx, project.ID, PatchProjectRequest{Class: &class}); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Out, "Changed %s from %s to %s: %s. It starts at the new size.\n", s, from, class, classSummary(class))
		return nil
	case "running":
	default:
		return exitf(ExitGuestNotRunning, "%s is %s; its size can be changed once it is running or stopped. `repose status %s` shows its state.", s, project.State, s)
	}
	if confirm != nil {
		ok, err := confirm(classChangePrompt(s, from, class))
		if err != nil {
			return err
		}
		if !ok {
			_, _ = fmt.Fprintf(e.Out, "Not changed. %s is still %s.\n", s, from)
			return nil
		}
	}
	pr := e.newProgress()
	defer pr.Fail()
	var opID string
	err = retryOnOpConflict(ctx, func() error {
		var err error
		opID, err = e.Client.StopProject(ctx, project.ID, true)
		return err
	})
	if err != nil {
		return err
	}
	closeMaster(ctx, e, s)
	pr.Phase("Snapshotting and stopping "+s, "")
	op, err := waitOpPhased(ctx, e, project, opID, pr, false)
	if err != nil {
		return err
	}
	if op.State == "error" {
		pr.Fail()
		return e.opFailed("stop", s, op.Error, fmt.Sprintf("Its size was not changed. `repose status %s` shows its state.", s))
	}
	if _, perr := e.Client.PatchProject(ctx, project.ID, PatchProjectRequest{Class: &class}); perr != nil {
		// Refused (the xl limit, say): put the machine back as it was
		// before reporting why, rather than leave it stopped.
		pr.Phase("Starting "+s+" again at "+from, "")
		if err := ensureRunningFrom(ctx, e, project, pr, false); err != nil {
			pr.Fail()
			return fmt.Errorf("%w (and starting it again failed: %v; `repose start %s`)", perr, err, s)
		}
		pr.Fail()
		return perr
	}
	if err := ensureRunningFrom(ctx, e, project, pr, false); err != nil {
		return err
	}
	pr.Fail()
	_, _ = fmt.Fprintf(e.Out, "Changed %s from %s to %s: %s. Running again in %s.\n", s, from, class, classSummary(class), fmtElapsed(pr.Total()))
	return nil
}
