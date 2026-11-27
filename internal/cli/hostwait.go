package cli

import (
	"context"
	"errors"
	"strings"
)

// A create whose host of the account's own is still installing waits for
// it instead of being refused (ENG-131, DECISIONS I-1700): the api writes
// the project with no host and `waiting_for_host` naming the host, and
// the machine starts there once it registers.

// ProjectWaitingForHost is Project.waiting_for_host: the host the create
// waits for, by name, and `mac` when `repose host add` ran on that Mac.
type ProjectWaitingForHost struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

// waitingForHost is the host p's create waits for, as the sentence names
// it: "your Mac (mac-mini)" for a Mac, "your host box-1" otherwise; ""
// when it waits for none.
func waitingForHost(p *Project) string {
	if p == nil || p.WaitingForHost == nil || p.State != "creating" {
		return ""
	}
	if p.WaitingForHost.Provider == "mac" {
		return "your Mac (" + p.WaitingForHost.Name + ")"
	}
	return "your host " + p.WaitingForHost.Name
}

// waitingLine is the line `repose run` prints once when its create waits
// for the host, in the owner's words (I-1700); "" when it does not.
func waitingLine(p *Project) string {
	who := waitingForHost(p)
	if who == "" {
		return ""
	}
	return "Waiting for " + who + " to finish installing. This project starts there when it's ready."
}

// waitingStateLine is that wait as `repose ls` and `repose status` say a
// machine's state under the table.
func waitingStateLine(p *Project) string {
	who := waitingForHost(p)
	if who == "" {
		return ""
	}
	return "waiting for " + who + " to finish installing; it starts there when it's ready."
}

// notCreated is the error for a create that ended with the project gone,
// as the api answers a read of it (`PROJECT was not created: WHY`, reason
// create_refused): the host the create waited for was removed or never
// joined (I-1700), or the host refused it for room (I-1648). nil when the
// project is still there. The cache forgets it: no later command can
// reach it.
func notCreated(ctx context.Context, e *Env, project *Project) error {
	_, err := e.Client.GetProject(ctx, project.ID)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "not_found" {
		return nil
	}
	if reason, _ := apiErr.Detail["reason"].(string); reason != "create_refused" {
		return nil
	}
	forgetProject(&e.Cache, project.ID)
	_ = e.saveCache()
	return exitf(ExitCapacity, "%s.", strings.TrimSuffix(apiErr.Message, "."))
}
