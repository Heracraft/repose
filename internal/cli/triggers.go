package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/triggerrun"
	"github.com/heracraft/repose/internal/triggerspec"
)

// `repose triggers` binds a webhook delivery or a time to an agent
// session on the project's machine (DECISIONS I-1248..I-1262).
//
// Two things never go to the api: the prompt's text and a payload's
// bytes. Both are files in the machine, written here over SSH, and the
// api holds a path (I-1248). That is also why `add -p` and
// `fire --payload` need a machine that is running, and say so when it is
// not.

var triggerNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// The limits the api holds, checked here so a typo exits 2 with no round
// trip (I-346).
const (
	triggerPromptCap  = 64 << 10
	triggerMaxTimeout = 24 * time.Hour
	triggerMaxConc    = 3
	triggerRunsKept   = 20
)

func checkTriggerName(name string) error {
	if !triggerNameRe.MatchString(name) {
		return exitf(ExitUsage, "NAME must match [a-z0-9][a-z0-9-]{0,31}, got %q", name)
	}
	return nil
}

// TriggerAddOptions is `repose triggers add`'s flags. A nil pointer is a
// flag the user did not pass, which leaves that field as it is on a
// trigger that already exists.
type TriggerAddOptions struct {
	Route       string
	Every       string
	Prompt      string // -p TEXT; written into the machine
	PromptFile  string
	Workdir     *string
	Agent       *string
	Timeout     *time.Duration
	Concurrency *int
	Gate        *string
	Dedupe      *string
	NoNotify    *bool
	Start       *bool
	StopAfter   *bool
	CatchUp     *bool
}

// TriggersAddCmd implements `repose triggers add NAME`. For a route it
// prints the URL and nothing else, so it pipes; for a schedule it prints
// the spec, the zone it stored and the next fire, because a user has to
// see that 07:00 is their 07:00.
func TriggersAddCmd(ctx context.Context, e *Env, projectArg, name string, o TriggerAddOptions) error {
	if err := checkTriggerName(name); err != nil {
		return err
	}
	switch {
	case o.Route != "" && o.Every != "":
		return cobraUsageError{errors.New("--route and --every are the two doors; pass one")}
	case o.Route == "" && o.Every == "" && !triggerFlagsOnly(o):
		return cobraUsageError{errors.New("a new trigger fires on a route (--route NAME) or on a schedule (--every SPEC)")}
	}
	if o.Route != "" && !triggerNameRe.MatchString(o.Route) {
		return exitf(ExitUsage, "--route takes a route name matching [a-z0-9][a-z0-9-]{0,31}, got %q", o.Route)
	}
	if o.Prompt != "" && o.PromptFile != "" {
		return cobraUsageError{errors.New("-p gives the prompt as text and --prompt-file as a path; pass one")}
	}
	if len(o.Prompt) > triggerPromptCap {
		return exitf(ExitUsage, "the prompt is %d bytes; at most %d", len(o.Prompt), triggerPromptCap)
	}
	if o.Dedupe != nil && *o.Dedupe != "" && !validTriggerDedupe(*o.Dedupe) {
		return exitf(ExitUsage, "--dedupe takes header:NAME or json:FIELD, got %q", *o.Dedupe)
	}
	if o.Every != "" && o.Dedupe != nil && *o.Dedupe != "" {
		return exitf(ExitUsage, "--dedupe reads a delivery's header or body, so a schedule has nothing to read")
	}
	if o.Timeout != nil && (*o.Timeout < 0 || *o.Timeout > triggerMaxTimeout) {
		return exitf(ExitUsage, "--timeout takes 0 or up to 24h, got %s", *o.Timeout)
	}
	if o.Concurrency != nil && (*o.Concurrency < 1 || *o.Concurrency > triggerMaxConc) {
		return exitf(ExitUsage, "--concurrency takes 1 to %d, got %d", triggerMaxConc, *o.Concurrency)
	}
	if o.Workdir != nil && !validTriggerWorkdir(*o.Workdir) {
		return exitf(ExitUsage, "--workdir takes checkout, worktree-N, ~/PATH or /PATH, got %q", *o.Workdir)
	}
	if o.Agent != nil && *o.Agent != "" && !slices.Contains(agentNames, *o.Agent) {
		return exitf(ExitUsage, "--agent takes one of claude, codex, opencode, gemini, pi; got %q", *o.Agent)
	}
	// pi reads a prompt that begins with @ as a file to include, so the
	// one prompt form it refuses is caught here rather than at 03:00
	// (I-780).
	if o.Agent != nil && *o.Agent == "pi" && strings.HasPrefix(strings.TrimSpace(o.Prompt), "@") {
		return exitf(ExitUsage, "pi reads a prompt that begins with @ as a file to include, so it cannot be a trigger's prompt")
	}
	// The spec is parsed here so a typo costs no round trip, and with the
	// laptop's own zone, which is what the api would fall back to.
	var spec triggerspec.Spec
	if o.Every != "" {
		s, err := triggerspec.Parse(o.Every, localTZ())
		if err != nil {
			return exitf(ExitUsage, "%v", err)
		}
		spec = s
	}

	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}

	promptPath := o.PromptFile
	if o.Prompt != "" {
		promptPath = triggerDefaultPrompt(name)
	}
	// The prompt is a file in the machine, so writing one and checking
	// one both need the machine (I-1248).
	if o.Prompt != "" || o.PromptFile != "" {
		if project.State != "running" {
			if o.Prompt != "" {
				return exitf(ExitGeneric, "%s is %s, and -p writes the prompt into the machine. Start it, or pass --prompt-file for a file that is already there.", project.Slug, project.State)
			}
			_, _ = fmt.Fprintf(e.ErrOut, "%s is %s, so the prompt file was not checked.\n", project.Slug, project.State)
		} else {
			wd := "checkout"
			if o.Workdir != nil {
				wd = *o.Workdir
			}
			if err := triggerPrompt(ctx, e, project, wd, promptPath, o.Prompt); err != nil {
				return err
			}
		}
	}

	in := TriggerInput{Name: name, Workdir: o.Workdir, Agent: o.Agent,
		Concurrency: o.Concurrency, Gate: o.Gate, Dedupe: o.Dedupe,
		AllowStart: o.Start, StopAfter: o.StopAfter, CatchUp: o.CatchUp}
	switch {
	case o.Route != "":
		in.Kind, in.Route = "route", o.Route
	case o.Every != "":
		in.Kind, in.Spec, in.Zone = "schedule", spec.Text, spec.Zone
	}
	if promptPath != "" {
		in.PromptPath = &promptPath
	}
	if o.Timeout != nil {
		secs := int(o.Timeout.Seconds())
		in.TimeoutS = &secs
	}
	if o.NoNotify != nil {
		v := "failed"
		if *o.NoNotify {
			v = "never"
		}
		in.Notify = &v
	}
	t, err := e.Client.PutTrigger(ctx, project.ID, in)
	if err != nil {
		return triggerCmdErr(err, project.Slug, name)
	}
	if e.JSON {
		return writeJSONOut(e.Out, t)
	}
	switch {
	case t.Route != nil:
		// The URL and nothing else, so `| pbcopy` works (I-1110's rule).
		_, _ = fmt.Fprintln(e.Out, t.Route.URL)
	case t.Spec != "":
		_, _ = fmt.Fprintf(e.Out, "%s %s, next %s\n", t.Spec, t.Zone, triggerNext(t.NextAt))
	default:
		_, _ = fmt.Fprintf(e.Out, "%s\n", t.Name)
	}
	// Two things the user cannot see and has to know, each said once.
	if t.Route != nil && t.Gate == "" {
		_, _ = fmt.Fprintf(e.ErrOut, "No gate: anyone with that URL can start an agent run. `--gate CMD` checks the provider's signature.\n")
	}
	if o.Start != nil && *o.Start && !t.StopAfter {
		_, _ = fmt.Fprintf(e.ErrOut, "A fire will start %s, and it stays running until you stop it. `--stop-after` stops it when the run ends.\n", project.Slug)
	}
	if o.Agent != nil && *o.Agent != "" && *o.Agent != "claude" {
		_, _ = fmt.Fprintf(e.ErrOut, "An unattended run is proven for Claude Code; %s may stop at its first permission prompt.\n", *o.Agent)
	}
	return nil
}

// triggerFlagsOnly reports whether `add` names only flags, which changes
// a trigger that exists and is how `on`, `off` and an edit reach it.
func triggerFlagsOnly(o TriggerAddOptions) bool {
	return o.Prompt != "" || o.PromptFile != "" || o.Workdir != nil || o.Agent != nil ||
		o.Timeout != nil || o.Concurrency != nil || o.Gate != nil || o.Dedupe != nil ||
		o.NoNotify != nil || o.Start != nil || o.StopAfter != nil || o.CatchUp != nil
}

func triggerDefaultPrompt(name string) string { return ".repose/triggers/" + name + ".md" }

func validTriggerDedupe(s string) bool {
	kind, rest, ok := strings.Cut(s, ":")
	if !ok || rest == "" {
		return false
	}
	return kind == "header" || kind == "json"
}

func validTriggerWorkdir(s string) bool {
	switch {
	case s == "checkout":
		return true
	case strings.HasPrefix(s, "worktree-"):
		return len(s) > len("worktree-")
	case strings.HasPrefix(s, "~/"), strings.HasPrefix(s, "/"):
		return len(s) > 1 && !strings.ContainsAny(s, "\x00")
	}
	return false
}

// triggerGuestDir is the workdir as the guest's shell spells it: the
// checkout is ~/<slug> and a worktree ~/<slug>-worktree-N (I-253).
func triggerGuestDir(slug, workdir string) string {
	switch {
	case workdir == "" || workdir == "checkout":
		return "~/" + slug
	case strings.HasPrefix(workdir, "worktree-"):
		return "~/" + slug + "-" + workdir
	default:
		return workdir
	}
}

// triggerPrompt writes the prompt into the machine, or checks that the
// file the user named is there. A missing file is exit 1 here rather than
// a trigger that fails every morning (I-1254 §7.1).
func triggerPrompt(ctx context.Context, e *Env, project *Project, workdir, promptPath, text string) error {
	dir := triggerGuestDir(project.Slug, workdir)
	full := dir + "/" + promptPath
	if strings.HasPrefix(promptPath, "/") || strings.HasPrefix(promptPath, "~/") {
		full = promptPath
	}
	t := e.target(project.Slug)
	if text == "" {
		if err := runSSHOK(ctx, t, "test -f "+shQuote(trimTilde(full))); err != nil {
			return exitf(ExitGeneric, "%s has no file %s. A trigger whose prompt is missing fails every time it fires.", project.Slug, promptPath)
		}
		return nil
	}
	script := "set -e\nf=" + shQuote(trimTilde(full)) + "\nmkdir -p \"$(dirname \"$f\")\"\ncat > \"$f.part\"\nmv -f \"$f.part\" \"$f\"\n"
	if _, err := runSSH(ctx, t, script, strings.NewReader(text)); err != nil {
		return fmt.Errorf("writing the prompt into %s: %w", project.Slug, err)
	}
	return nil
}

// trimTilde turns `~/x` into `$HOME/x`'s shell form, which is what a
// quoted path needs: shQuote would otherwise hand the shell a literal ~.
func trimTilde(p string) string {
	if strings.HasPrefix(p, "~/") {
		return "./" + strings.TrimPrefix(p, "~/")
	}
	return p
}

// TriggersListCmd implements `repose triggers list`.
func TriggersListCmd(ctx context.Context, e *Env, projectArg string, quiet bool) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	ts, err := e.Client.ListTriggers(ctx, project.ID)
	if err != nil {
		return err
	}
	if e.JSON {
		return writeJSONOut(e.Out, ts)
	}
	if quiet {
		for _, t := range ts {
			_, _ = fmt.Fprintln(e.Out, t.Name)
		}
		return nil
	}
	if len(ts) == 0 {
		_, _ = fmt.Fprintf(e.Out, "%s has no triggers.\n", project.Slug)
		return nil
	}
	tw := tabwriter.NewWriter(e.Out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tON\tPROMPT\tON/OFF\tLAST RUN\tNEXT")
	for _, t := range ts {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			t.Name, triggerDoor(t), t.PromptPath, onOff(t.Enabled), triggerLastRun(t), triggerNextCol(t))
	}
	return tw.Flush()
}

func triggerDoor(t Trigger) string {
	if t.Route != nil {
		return t.Route.Name + " (route)"
	}
	return t.Spec
}

func triggerLastRun(t Trigger) string {
	if t.Last == nil {
		return "never"
	}
	return t.Last.State + " " + humanAge(t.Last.CreatedAt)
}

func triggerNextCol(t Trigger) string {
	if t.Kind != "schedule" || !t.Enabled {
		return ""
	}
	return triggerNext(t.NextAt)
}

// triggerNext is the next fire in the laptop's own zone, since that is
// the clock the user is reading it on.
func triggerNext(at *time.Time) string {
	if at == nil {
		return ""
	}
	local := at.Local()
	now := time.Now()
	switch {
	case local.YearDay() == now.YearDay() && local.Year() == now.Year():
		return local.Format("15:04") + " today"
	case local.Sub(now) < 48*time.Hour && local.YearDay() == now.Add(24*time.Hour).YearDay():
		return local.Format("15:04") + " tomorrow"
	default:
		return local.Format("Mon 2 Jan 15:04")
	}
}

// TriggersRunsCmd implements `repose triggers runs [NAME]`.
func TriggersRunsCmd(ctx context.Context, e *Env, projectArg, name string, limit int, quiet bool) error {
	if name != "" {
		if err := checkTriggerName(name); err != nil {
			return err
		}
	}
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	if limit == 0 {
		limit = triggerRunsKept
	}
	runs, err := e.Client.TriggerRuns(ctx, project.ID, name, limit)
	if err != nil {
		return err
	}
	if e.JSON {
		return writeJSONOut(e.Out, runs)
	}
	if quiet {
		for _, r := range runs {
			_, _ = fmt.Fprintln(e.Out, r.RunID)
		}
		return nil
	}
	if len(runs) == 0 {
		if name != "" {
			_, _ = fmt.Fprintf(e.Out, "%s has not run.\n", name)
		} else {
			_, _ = fmt.Fprintf(e.Out, "%s has no trigger runs.\n", project.Slug)
		}
		return nil
	}
	tw := tabwriter.NewWriter(e.Out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "RUN\tTRIGGER\tSOURCE\tSTARTED\tTOOK\tSTATE\tWHY")
	for _, r := range runs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			shortID(r.RunID), r.Trigger, r.Source, humanAge(r.CreatedAt), triggerTook(r), r.State, triggerWhy(r))
	}
	return tw.Flush()
}

func triggerTook(r TriggerRun) string {
	if r.StartedAt == nil || r.FinishedAt == nil {
		return ""
	}
	d := r.FinishedAt.Sub(*r.StartedAt).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// triggerWhy is the agent's own summary where there is one, else the
// reason in the words of features/triggers.md §4.1, with the count where
// the row stands for several refusals.
func triggerWhy(r TriggerRun) string {
	why := r.Summary
	if why == "" {
		why = triggerReasonSentence(r.Reason)
	}
	if r.Skips > 1 {
		why += fmt.Sprintf(" (%d events)", r.Skips)
	}
	return why
}

// triggerReasonSentence is internal/triggerrun's one table, which the
// api and hostd read too: the copy that used to live here was already
// missing `clock_late` by the time it was written, so the most common
// skip on a machine that was off printed a raw token.
func triggerReasonSentence(reason string) string { return triggerrun.Sentence(reason) }

// TriggerFireOptions is `repose triggers fire`'s flags.
type TriggerFireOptions struct {
	Payload string // a local file, or "-" for stdin
	Run     string
	Wait    bool
	WaitFor time.Duration
	DryRun  bool
}

// TriggersFireCmd implements `repose triggers fire NAME`.
func TriggersFireCmd(ctx context.Context, e *Env, projectArg, name string, o TriggerFireOptions) error {
	if err := checkTriggerName(name); err != nil {
		return err
	}
	if o.Payload != "" && o.Run != "" {
		return cobraUsageError{errors.New("--payload takes a file and --run an earlier run's payload; pass one")}
	}
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	opts := FireTriggerOptions{DryRun: o.DryRun}
	if o.Run != "" {
		id, err := resolveRunID(ctx, e, project.ID, o.Run)
		if err != nil {
			return err
		}
		opts.PayloadRun = id
	}
	if o.Payload != "" {
		if project.State != "running" {
			return exitf(ExitGeneric, "%s is %s, and --payload writes the file into the machine.", project.Slug, project.State)
		}
		p, err := triggerWritePayload(ctx, e, project, name, o.Payload)
		if err != nil {
			return err
		}
		opts.PayloadPath = p
	}
	run, err := e.Client.FireTrigger(ctx, project.ID, name, opts)
	if err != nil {
		return triggerCmdErr(err, project.Slug, name)
	}
	if o.Wait {
		run, err = triggerWaitForRun(ctx, e, project.ID, run, o.WaitFor)
		if err != nil {
			return err
		}
	}
	if e.JSON {
		return writeJSONOut(e.Out, run)
	}
	switch {
	case run.State == "queued" || run.State == "working":
		if run.Window != "" {
			_, _ = fmt.Fprintf(e.Out, "%s, run %s\n", run.Window, shortID(run.RunID))
			return nil
		}
		_, _ = fmt.Fprintf(e.Out, "run %s %s\n", shortID(run.RunID), run.State)
	default:
		why := triggerWhy(*run)
		if why == "" {
			_, _ = fmt.Fprintf(e.Out, "%s\n", run.State)
			return nil
		}
		_, _ = fmt.Fprintf(e.Out, "%s: %s\n", run.State, why)
	}
	return nil
}

// triggerWaitForRun holds until the run ends, in the api's own long-poll
// steps. A run may take an hour, so the wait has its own ceiling and says
// where to look when it runs out.
func triggerWaitForRun(ctx context.Context, e *Env, projectID string, run *TriggerRun, waitFor time.Duration) (*TriggerRun, error) {
	if waitFor <= 0 {
		waitFor = 5 * time.Minute
	}
	deadline := time.Now().Add(waitFor)
	for {
		if triggerRunEnded(run.State) {
			return run, nil
		}
		if !time.Now().Before(deadline) {
			_, _ = fmt.Fprintf(e.ErrOut, "still %s after %s.\n", run.State, waitFor)
			return run, nil
		}
		next, err := e.Client.GetTriggerRun(ctx, projectID, run.RunID, 20*time.Second)
		if err != nil {
			return run, err
		}
		run = next
	}
}

func triggerRunEnded(state string) bool {
	switch state {
	case "done", "failed", "timeout", "skipped":
		return true
	}
	return false
}

// resolveRunID turns what the CLI printed back into the id the api
// takes: the last or first characters of a run's uuid, 4 or more, when
// one of this project's runs has them — the rule `repose snapshots
// restore` already uses for a snapshot id.
func resolveRunID(ctx context.Context, e *Env, projectID, want string) (string, error) {
	if _, err := uuid.Parse(want); err == nil {
		return want, nil
	}
	if len(want) < 4 {
		return "", exitf(ExitUsage, "--run takes a run id, or 4 or more characters of one; got %q", want)
	}
	runs, err := e.Client.TriggerRuns(ctx, projectID, "", 200)
	if err != nil {
		return "", err
	}
	var hits []string
	for _, r := range runs {
		if strings.HasSuffix(r.RunID, want) || strings.HasPrefix(r.RunID, want) {
			hits = append(hits, r.RunID)
		}
	}
	switch len(hits) {
	case 0:
		return "", exitf(ExitGeneric, "no run of this project has the id %s.", want)
	case 1:
		return hits[0], nil
	default:
		return "", exitf(ExitUsage, "%d runs have the id %s; give more of it", len(hits), want)
	}
}

// triggerWritePayload puts a local file at the one path the api accepts
// as a payload, under the trigger's own folder in the machine.
func triggerWritePayload(ctx context.Context, e *Env, project *Project, name, local string) (string, error) {
	var r io.Reader
	if local == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(local)
		if err != nil {
			return "", exitf(ExitGeneric, "%v", err)
		}
		defer f.Close()
		st, err := f.Stat()
		if err == nil && st.Size() > 1<<20 {
			return "", exitf(ExitUsage, "%s is %d bytes; a payload is at most 1 MiB, which is the webhook body cap", local, st.Size())
		}
		r = f
	}
	guest := path.Join(".repose/triggers", name, "fire-payload")
	script := "set -e\nf=" + shQuote(guest) + "\nmkdir -p \"$(dirname \"$f\")\"\nhead -c " +
		fmt.Sprint(1<<20) + " > \"$f.part\"\nmv -f \"$f.part\" \"$f\"\n"
	if _, err := runSSH(ctx, e.target(project.Slug), script, r); err != nil {
		return "", fmt.Errorf("writing the payload into %s: %w", project.Slug, err)
	}
	return "~/.repose/triggers/" + name + "/fire-payload", nil
}

// TriggersOnOffCmd implements `repose triggers on|off NAME`.
func TriggersOnOffCmd(ctx context.Context, e *Env, projectArg, name string, on bool) error {
	if err := checkTriggerName(name); err != nil {
		return err
	}
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	t, err := e.Client.PutTrigger(ctx, project.ID, TriggerInput{Name: name, Enabled: &on})
	if err != nil {
		return triggerCmdErr(err, project.Slug, name)
	}
	if e.JSON {
		return writeJSONOut(e.Out, t)
	}
	_, _ = fmt.Fprintf(e.Out, "%s is %s\n", t.Name, onOffWord(t.Enabled))
	if t.Enabled && t.Kind == "schedule" {
		_, _ = fmt.Fprintf(e.Out, "next %s\n", triggerNext(t.NextAt))
	}
	return nil
}

func onOffWord(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// TriggersRmCmd implements `repose triggers rm NAME...`.
func TriggersRmCmd(ctx context.Context, e *Env, projectArg string, names []string) error {
	for _, n := range names {
		if err := checkTriggerName(n); err != nil {
			return err
		}
	}
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := e.Client.DeleteTrigger(ctx, project.ID, n); err != nil {
			return triggerCmdErr(err, project.Slug, n)
		}
	}
	return nil
}

// triggerCmdErr turns the api's refusals into the lines and exit codes
// every other noun uses: a name the project does not have is exit 1, a
// field the api refused is exit 2.
func triggerCmdErr(err error, slug, name string) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	if apiErr.Code == "not_found" || apiErr.Status == 404 {
		return exitf(ExitGeneric, "%s has no trigger %s.", slug, name)
	}
	if apiErr.Code == "invalid" {
		return exitf(ExitUsage, "%s", apiErr.Message)
	}
	return err
}

// triggerStatusLine is `repose status`'s triggers row: each trigger's
// last run, and the next fire for a schedule. A project with no trigger
// has no such row, which is the webhooks row's own rule.
func triggerStatusLine(ts []Trigger, now time.Time) string {
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		switch {
		case !t.Enabled:
			parts = append(parts, t.Name+" off")
		case t.Last != nil:
			parts = append(parts, t.Name+" "+t.Last.State+" "+humanAge(t.Last.CreatedAt))
		case t.Kind == "schedule" && t.NextAt != nil:
			parts = append(parts, t.Name+" next "+triggerNext(t.NextAt))
		default:
			parts = append(parts, t.Name+" never run")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	_ = now
	return strings.Join(parts, ", ")
}
