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

// planSpecs is each plan's memory for running machines, smallest first,
// as docs/PRICING.md lists it; TestClassSpecsMatchBillingAndHost pins it
// to internal/billing's plans.
var planSpecs = []struct {
	ID, Name string
	MemGB    int
}{{"solo", "Solo", 8}, {"plus", "Plus", 16}, {"pro", "Pro", 32}}

// billingURL is where a plan is changed; the api's gate names the same
// page in its refusals.
const billingURL = "https://repose.herakraft.co/billing"

// memoryStates are the project states that hold plan memory, as the
// api's gate counts them (internal/billing/usage.go memoryStates).
var memoryStates = map[string]bool{"running": true, "starting": true, "restoring": true, "creating": true, "building": true}

// planName is "Solo" for the plan id "solo"; "" for one the CLI does not
// know.
func planName(id string) string {
	for _, p := range planSpecs {
		if p.ID == id {
			return p.Name
		}
	}
	return ""
}

// smallestPlanFor is the first plan whose memory holds gb.
func smallestPlanFor(gb int) string {
	for _, p := range planSpecs {
		if p.MemGB >= gb {
			return p.Name
		}
	}
	return planSpecs[len(planSpecs)-1].Name
}

// planAsk is a start the CLI is about to cause: count machines of class,
// beside what runs now except the project Except (one being resized, which
// stops first). Fix is the command that avoids the start, named in the
// refusal when what runs now is in the way ("" names `repose stop`).
type planAsk struct {
	Class  string
	Count  int
	Except string
	Fix    string
}

// planMemoryRefusal asks the api's memory question from /me and the
// project list before the CLI stops or snapshots anything (DECISIONS
// I-610): the gate would refuse the start only after the stop had ended
// every agent, or after the snapshot. It returns the refusal sentence, or
// "" when the machines fit or the CLI cannot tell (no plan, an exempt
// account, /me or the list unreadable): the api's gate stays the
// authority, and answers again when the start is asked for.
func planMemoryRefusal(ctx context.Context, e *Env, ask planAsk) string {
	me, err := e.Client.GetMe(ctx)
	if err != nil {
		return ""
	}
	return planMemoryRefusalOf(me, func() ([]Project, error) { return e.Client.ListProjects(ctx) }, ask)
}

// planMemoryRefusalOf is planMemoryRefusal on a /me already read; list
// reads the projects only when the answer needs them.
func planMemoryRefusalOf(me *Me, list func() ([]Project, error), ask planAsk) string {
	if ask.Count < 1 {
		ask.Count = 1
	}
	if me == nil || me.Billing.Plan == nil || me.Billing.Status == "exempt" || me.Limits.MemoryGB <= 0 {
		return ""
	}
	plan := planName(*me.Billing.Plan)
	if plan == "" {
		return ""
	}
	limit := me.Limits.MemoryGB
	one := classSpecs[ask.Class].MemGB
	if one == 0 {
		one = classSpecs["large"].MemGB // the gate counts an unknown class as large
	}
	if one > limit {
		return fmt.Sprintf("Your %s plan runs %d GB at once and an %s machine needs %d GB. Upgrade to %s at %s.", plan, limit, ask.Class, one, smallestPlanFor(one), billingURL)
	}
	projects, err := list()
	if err != nil {
		return ""
	}
	used := 0
	var slugs []string
	for _, p := range projects {
		if p.ID == ask.Except || !memoryStates[p.State] {
			continue
		}
		used += classSpecs[p.Class].MemGB
		slugs = append(slugs, p.Slug)
	}
	need := one * ask.Count
	if used+need <= limit {
		return ""
	}
	if ask.Count > 1 {
		head := fmt.Sprintf("Your %s plan runs %d GB at once and %d %s machines need %d GB", plan, limit, ask.Count, ask.Class, need)
		if used > 0 {
			head += fmt.Sprintf(" beside the %d GB %s %s using", used, joinNames(slugs), isAre(len(slugs)))
		}
		return fmt.Sprintf("%s. %s, or upgrade at %s.", head, fixOr(ask.Fix, slugs), billingURL)
	}
	if len(slugs) == 0 {
		return fmt.Sprintf("Your %s plan runs %d GB at once. Upgrade at %s.", plan, limit, billingURL)
	}
	return fmt.Sprintf("Your %s plan runs %d GB at once and %s %s using it. %s, or upgrade at %s.", plan, limit, joinNames(slugs), isAre(len(slugs)), fixOr(ask.Fix, slugs), billingURL)
}

// fixOr is the way out a memory refusal names: the caller's own (fork's
// --no-start), else `repose stop` for the one machine in the way.
func fixOr(fix string, slugs []string) string {
	if fix != "" {
		return fix
	}
	if len(slugs) == 1 {
		return fmt.Sprintf("`repose stop %s` frees it", slugs[0])
	}
	return "Stop one"
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
	return fmt.Sprintf("%s is running. Changing it from %s to %s stops it, which ends every process on it, agents included, then starts it again. Go ahead? [y/N] ", slug, from, to)
}

// ResizeClassCmd implements `repose resize --size small|large|xl` (DECISIONS
// I-260). The api changes a project's class only while it is stopped, so
// a running project is stopped, changed and started again, after a
// confirmation (confirm nil means --yes). The class reaches the machine at
// its next start: StartGuest carries it. The stop takes no snapshot: the
// disk stays on the host and boots again at once, untouched by the change,
// and the snapshot was most of the restart's time (kanali, 37 GB used:
// 56 s of an 80 s change; DECISIONS I-595).
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
	// The plan is asked before the prompt: a refusal after the stop would
	// have ended every agent for nothing (I-610). A smaller size always
	// fits.
	if classSpecs[class].MemGB > classSpecs[from].MemGB {
		if msg := planMemoryRefusal(ctx, e, planAsk{Class: class, Except: project.ID}); msg != "" {
			return exitf(ExitPaymentRequired, "%s", msg)
		}
	}
	if confirm != nil {
		if err := confirmOr(confirm, classChangePrompt(s, from, class), fmt.Sprintf("Not changed. %s is still %s.", s, from)); err != nil {
			return err
		}
	}
	pr := e.newProgress()
	defer pr.Fail()
	var opID string
	err = retryOnOpConflict(ctx, func() error {
		var err error
		opID, err = e.Client.StopProject(ctx, project.ID, false)
		return err
	})
	if err != nil {
		return err
	}
	closeMaster(ctx, e, s)
	pr.Phase("Stopping "+s, "")
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
