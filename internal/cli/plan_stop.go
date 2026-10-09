package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// A start or create the plan's memory refuses (payment_required,
// plan_limit) asks on a terminal to stop the machines in the way, then
// starts, in one command (DECISIONS I-637). On Solo every second large
// machine hits it, and the refusal named `repose stop api`, after which
// the run was typed again. The question is stop's own, with stop's
// busy-agent clause, and a yes runs stop's path: the fetch first, the
// stop lines. Without a terminal, or with --json, the refusal exits 7 as
// before.

// planStopAsk asks prompt and says whether it could ask at all; tests
// replace it.
var planStopAsk = func(ctx context.Context, e *Env, prompt string) (asked, yes bool, err error) {
	if !planWaitable(e) {
		return false, false, nil
	}
	yes, err = askYesNo(ctx, prompt, false, "stopping")
	return true, yes, err
}

// planLimitSlugs is the gate's plan_limit refusal's machines using the
// memory; nil for any other error, or a refusal no stop lifts (a class
// bigger than the plan).
func planLimitSlugs(err error) []string {
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != "payment_required" {
		return nil
	}
	if r, _ := ae.Detail["reason"].(string); r != "plan_limit" {
		return nil
	}
	ps, _ := ae.Detail["projects"].([]any)
	var slugs []string
	for _, p := range ps {
		if s, _ := p.(string); s != "" {
			slugs = append(slugs, s)
		}
	}
	return slugs
}

// machinesToStop picks, among the running projects, the fewest whose
// memory makes room for one machine of class, preferring those with no
// agent working or waiting, then unused ones, then the biggest. nil when
// stopping all of them would not make room, or the plan is unknown.
func machinesToStop(me *Me, projects []Project, except, class string) []*Project {
	if me == nil || me.Limits.MemoryGB <= 0 {
		return nil
	}
	need := classSpecs[class].MemGB
	if need == 0 {
		need = classSpecs["large"].MemGB
	}
	used := 0
	var cands []*Project
	for i := range projects {
		p := &projects[i]
		if p.ID == except || p.Slug == except || !memoryStates[p.State] {
			continue
		}
		used += classSpecs[p.Class].MemGB
		cands = append(cands, p)
	}
	sort.SliceStable(cands, func(i, j int) bool {
		bi, bj := len(busyAgentList(cands[i])), len(busyAgentList(cands[j]))
		if (bi == 0) != (bj == 0) {
			return bi == 0
		}
		ui, uj := cands[i].Idle != nil, cands[j].Idle != nil
		if ui != uj {
			return ui
		}
		return classSpecs[cands[i].Class].MemGB > classSpecs[cands[j].Class].MemGB
	})
	var out []*Project
	for _, p := range cands {
		if used+need <= me.Limits.MemoryGB {
			break
		}
		used -= classSpecs[p.Class].MemGB
		out = append(out, p)
	}
	if used+need > me.Limits.MemoryGB {
		return nil
	}
	return out
}

// stopToFit answers a plan_limit refusal of target (the slug being
// started, or the name being created) on a terminal: it asks to stop the
// machines in the way and, on a yes, stops them. It returns true when it
// stopped them and the caller should ask again; false with no error
// when it could not ask, so the caller prints the refusal. A no prints
// the refusal and exits 7.
func stopToFit(ctx context.Context, e *Env, err error, target, class string, pr *progress) (bool, error) {
	slugs := planLimitSlugs(err)
	if len(slugs) == 0 || !planWaitable(e) {
		return false, nil
	}
	var stop []*Project
	me, merr := e.Client.GetMe(ctx)
	all, lerr := e.Client.ListProjects(ctx)
	if merr == nil && lerr == nil {
		stop = machinesToStop(me, all, target, class)
	}
	if len(stop) == 0 && lerr == nil {
		// The CLI's count disagrees with the gate's: stop what the gate
		// named.
		for _, s := range slugs {
			for i := range all {
				if all[i].Slug == s {
					stop = append(stop, &all[i])
				}
			}
		}
	}
	if len(stop) == 0 {
		return false, nil
	}
	var b strings.Builder
	agents := 0
	for _, p := range stop {
		if names := busyAgentList(p); len(names) > 0 {
			agents += len(names)
			fmt.Fprintf(&b, "%s has %s. ", p.Slug, joinNames(names))
		}
	}
	if agents == 1 {
		b.WriteString("Stopping ends it. ")
	} else if agents > 1 {
		b.WriteString("Stopping ends them. ")
	}
	fmt.Fprintf(&b, "Stop %s to start %s? [y/N] ", joinNames(slugsOf(stop)), target)
	if pr != nil {
		pr.Fail()
	}
	asked, yes, aerr := planStopAsk(ctx, e, b.String())
	switch {
	case !asked:
		return false, nil
	case aerr != nil:
		return false, aerr
	case !yes:
		var ae *APIError
		errors.As(err, &ae)
		return false, exitf(ExitPaymentRequired, "%s", paymentRequiredMessage(ae))
	}
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: slugsOf(stop), Snapshot: true, Yes: true, asked: true}); err != nil {
		return false, err
	}
	return true, nil
}
