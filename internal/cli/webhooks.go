package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// `repose webhooks` gives a project named routes on one public hostname,
// each with its own unguessable token, forwarded to a port and path
// inside the machine (DECISIONS I-1110). The noun is `webhooks` and not
// `hooks`, which agent hooks own.

var webhookNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// checkWebhookName refuses a name before any round trip, and says what it
// got (I-346).
func checkWebhookName(name string) error {
	if !webhookNameRe.MatchString(name) {
		return exitf(ExitUsage, "NAME must match [a-z0-9][a-z0-9-]{0,31}, got %q", name)
	}
	return nil
}

// checkWebhookTarget refuses a target the api would refuse. The forms are
// PORT and PORT/PATH, with an optional leading colon so `:3000` reads as
// it does in `repose open`.
func checkWebhookTarget(target string) error {
	s := strings.TrimPrefix(strings.TrimSpace(target), ":")
	portPart, rest, hasPath := strings.Cut(s, "/")
	port, err := strconv.Atoi(portPart)
	if err != nil || port < 1 || port > 65535 {
		return exitf(ExitUsage, "TARGET is PORT or PORT/PATH with a port from 1 to 65535, got %q", target)
	}
	if hasPath && strings.ContainsAny("/"+rest, " \t?#") {
		return exitf(ExitUsage, "TARGET's path takes no space, query or fragment, got %q", target)
	}
	return nil
}

// WebhooksAddCmd implements `repose webhooks add NAME TARGET`. It prints
// the URL and nothing else, so `| pbcopy` works.
func WebhooksAddCmd(ctx context.Context, e *Env, projectArg, name, target string, start *bool) error {
	if err := checkWebhookName(name); err != nil {
		return err
	}
	if err := checkWebhookTarget(target); err != nil {
		return err
	}
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	route, err := e.Client.PutWebhook(ctx, project.ID, name, target, start)
	if err != nil {
		return err
	}
	if e.JSON {
		return writeJSONOut(e.Out, route)
	}
	_, _ = fmt.Fprintln(e.Out, route.URL)
	return nil
}

// WebhooksURLCmd implements `repose webhooks url NAME`.
func WebhooksURLCmd(ctx context.Context, e *Env, projectArg, name string) error {
	if err := checkWebhookName(name); err != nil {
		return err
	}
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	route, err := e.Client.GetWebhook(ctx, project.ID, name)
	if err != nil {
		return webhookErr(err, project.Slug, name)
	}
	if e.JSON {
		return writeJSONOut(e.Out, route)
	}
	_, _ = fmt.Fprintln(e.Out, route.URL)
	return nil
}

// WebhooksRotateCmd implements `repose webhooks rotate NAME`.
func WebhooksRotateCmd(ctx context.Context, e *Env, projectArg, name string) error {
	if err := checkWebhookName(name); err != nil {
		return err
	}
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	route, err := e.Client.RotateWebhook(ctx, project.ID, name)
	if err != nil {
		return webhookErr(err, project.Slug, name)
	}
	if e.JSON {
		return writeJSONOut(e.Out, route)
	}
	_, _ = fmt.Fprintln(e.Out, route.URL)
	return nil
}

// WebhooksRmCmd implements `repose webhooks rm NAME...`.
func WebhooksRmCmd(ctx context.Context, e *Env, projectArg string, names []string) error {
	for _, name := range names {
		if err := checkWebhookName(name); err != nil {
			return err
		}
	}
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := e.Client.DeleteWebhook(ctx, project.ID, name); err != nil {
			return webhookErr(err, project.Slug, name)
		}
	}
	return nil
}

// WebhooksListCmd implements `repose webhooks` and `repose webhooks list`.
func WebhooksListCmd(ctx context.Context, e *Env, projectArg string) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	routes, err := e.Client.ListWebhooks(ctx, project.ID)
	if err != nil {
		return err
	}
	if e.JSON {
		return writeJSONOut(e.Out, routes)
	}
	if e.Quiet {
		for _, r := range routes {
			_, _ = fmt.Fprintln(e.Out, r.Name)
		}
		return nil
	}
	if len(routes) == 0 {
		_, _ = fmt.Fprintf(e.Out, "%s has no webhook routes.\n", project.Slug)
		return nil
	}
	tw := tabwriter.NewWriter(e.Out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tTARGET\tSTART\tLAST DELIVERY\tSTATUS")
	for _, r := range routes {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Name, webhookTarget(r), onOff(r.AllowStart), webhookWhen(r), webhookStatus(r))
	}
	return tw.Flush()
}

// webhookTarget is the port and path as the user typed them.
func webhookTarget(r WebhookRoute) string {
	if r.Path == "" || r.Path == "/" {
		return ":" + strconv.Itoa(r.Port) + "/"
	}
	return ":" + strconv.Itoa(r.Port) + r.Path
}

func webhookWhen(r WebhookRoute) string {
	if r.Last == nil {
		return "never"
	}
	return humanAge(r.Last.At)
}

// webhookStatus is the status with the outcome beside it where the
// outcome says something the number does not: a 503 is `stopped` or
// `wake_failed`, and those are different problems.
func webhookStatus(r WebhookRoute) string {
	if r.Last == nil {
		return ""
	}
	d := r.Last
	if d.Status == 0 {
		return d.Outcome
	}
	if d.Outcome == "delivered" || d.Outcome == "woke" {
		return strconv.Itoa(d.Status)
	}
	return strconv.Itoa(d.Status) + " " + d.Outcome
}

// webhookErr turns the api's not_found into a line that names the project
// and the route, as `secrets rm` does (I-633).
func webhookErr(err error, slug, name string) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) && (apiErr.Code == "not_found" || apiErr.Status == 404) {
		return exitf(ExitGeneric, "%s has no webhook route %s.", slug, name)
	}
	return err
}

// webhookNamesForCompletion lists a project's route names for the shell.
func webhookNamesForCompletion(env func() (*Env, error), projectArg string) []string {
	e, err := env()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return nil
	}
	routes, err := e.Client.ListWebhooks(ctx, project.ID)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		out = append(out, r.Name)
	}
	return out
}

// webhookStatusLine is the one line `repose status` adds for a project
// with routes: the newest delivery per route, newest first. Empty when
// the project has none.
func webhookStatusLine(routes []WebhookRoute) string {
	parts := make([]string, 0, len(routes))
	for _, r := range routes {
		if r.Last == nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s %s", r.Name, webhookStatus(r), humanAge(r.Last.At)))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}
