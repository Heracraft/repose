package cli

import (
	"fmt"
	"strings"
)

// paymentRequiredMessage is what every command prints for the api's
// payment_required (exit 7): the api's message verbatim, because since
// DECISIONS I-289 it is the whole sentence for every reason
// (subscription_required, plan_limit, disk_limit, egress_limit, past_due,
// suspended), naming the machines using the memory or the date the period
// ends. An older api sends a fragment and the card-era reasons; those get
// the plan sentence.
func paymentRequiredMessage(e *APIError) string {
	reason, _ := e.Detail["reason"].(string)
	switch reason {
	case "plan_limit":
		if e.Message != "" {
			return namePlanFix(e)
		}
	case "subscription_required", "disk_limit", "egress_limit", "past_due", "suspended":
		if e.Message != "" {
			return e.Message
		}
	}
	return "Choose a plan at https://repose.herakraft.co/billing first."
}

// projectLimitMessage is what every command prints when a create,
// restore or fork would pass the account's project cap, running or
// stopped (DECISIONS I-569): fork's own check before it snapshots and the
// api's 400 for run, restore and the rest say it in these words.
func projectLimitMessage(have, limit, requested int) string {
	if requested <= 1 {
		return fmt.Sprintf("You have %d of the %d projects an account can have, running or stopped. Destroy one first with `repose rm PROJECT`.", have, limit)
	}
	return fmt.Sprintf("You have %d of the %d projects an account can have, running or stopped, and %d more would make %d. Destroy some first with `repose rm PROJECT`.", have, limit, requested, have+requested)
}

// projectLimitOf reads the api's project cap refusal: 400 invalid with
// detail {limit, projects, requested?}, and since I-569 detail.reason
// project_limit. An api from before I-569 sends no reason; the two
// numbers are enough to know it.
func projectLimitOf(e *APIError) (have, limit, requested int, ok bool) {
	if e == nil || e.Code != "invalid" || e.Detail == nil {
		return 0, 0, 0, false
	}
	if r, has := e.Detail["reason"]; has && r != "project_limit" {
		return 0, 0, 0, false
	}
	l, okL := e.Detail["limit"].(float64)
	p, okP := e.Detail["projects"].(float64)
	if !okL || !okP {
		return 0, 0, 0, false
	}
	requested = 1
	if r, ok := e.Detail["requested"].(float64); ok {
		requested = int(r)
	}
	return int(p), int(l), requested, true
}

// namePlanFix puts the command into the gate's plan_limit sentence when
// one machine is using the memory: "todo-app is using it. `repose stop
// todo-app` frees it, or upgrade at ..." in place of "Stop it, or
// upgrade" (I-610). Any other wording is printed as the api sent it.
func namePlanFix(e *APIError) string {
	ps, _ := e.Detail["projects"].([]any)
	if len(ps) != 1 {
		return e.Message
	}
	slug, _ := ps[0].(string)
	if slug == "" {
		return e.Message
	}
	return strings.Replace(e.Message, slug+" is using it. Stop it, or upgrade", slug+" is using it. `repose stop "+slug+"` frees it, or upgrade", 1)
}
