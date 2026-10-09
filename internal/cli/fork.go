package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"
)

// `repose fork` (DECISIONS I-254): snapshot a project now (or take one of
// its snapshots) and restore it into N new projects, each its own machine,
// so N agents can try N approaches from the same state. The api creates
// all N in one transaction (POST /projects/:id/fork); the CLI takes the
// snapshot, waits for the forks to run, and optionally starts an agent
// with the same prompt in each.

// ForkOptions is `repose fork`'s flags.
type ForkOptions struct {
	ProjectArg string
	Count      int
	Name       string
	Size       string
	SnapshotID string
	Prompt     string
	Agent      string
	// NoStart creates the forks stopped (start: false), so they take
	// none of the plan's memory until `repose start` (I-610).
	NoStart bool
}

// ForkRequest is POST /projects/:id/fork's body.
type ForkRequest struct {
	SnapshotID string `json:"snapshot_id"`
	Count      int    `json:"count"`
	Name       string `json:"name,omitempty"`
	Class      string `json:"class,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	// Start false makes the forks stopped; omitted, they start.
	Start *bool `json:"start,omitempty"`
}

// ForkedProject is one project a fork made.
type ForkedProject struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Class     string `json:"class"`
	OpID      string `json:"op_id"`
	// State and Error are the CLI's own, filled once its restore ended
	// (for --json).
	State string `json:"state,omitempty"`
	Error string `json:"error,omitempty"`
}

// ForkResult is POST /projects/:id/fork's answer.
type ForkResult struct {
	SnapshotID        string          `json:"snapshot_id"`
	SnapshotCreatedAt time.Time       `json:"snapshot_created_at"`
	FromProjectID     string          `json:"from_project_id"`
	Projects          []ForkedProject `json:"projects"`
}

func (c *Client) Fork(ctx context.Context, projectID string, req ForkRequest) (*ForkResult, error) {
	var r ForkResult
	if err := c.post(ctx, "/projects/"+url.PathEscape(projectID)+"/fork", req, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// maxForks is the api's bound on one fork request.
const maxForks = 10

// forkStartAgent starts the agent with the prompt in one fork, without
// syncing the laptop's checkout into it (the fork's state is the
// snapshot's) and without attaching. Tests replace it.
var forkStartAgent = func(ctx context.Context, e *Env, slug, agent, prompt string) error {
	return runRun(ctx, e, RunOptions{ProjectArg: slug, Agent: agent, Prompt: prompt, NoSync: true, NoAttach: true}, false)
}

// newRequestID is a random UUID (version 4) naming one fork request, so a
// resend after a lost answer gets the same projects back.
func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ForkCmd implements `repose fork [PROJECT] [-n N] [--name BASE] [--size
// S] [--snapshot ID] [--prompt TEXT] [--agent A]`.
func ForkCmd(ctx context.Context, e *Env, opts ForkOptions) error {
	if opts.Count < 1 || opts.Count > maxForks {
		return exitf(ExitUsage, "--count must be 1 to %d, got %d.", maxForks, opts.Count)
	}
	if opts.NoStart && opts.Prompt != "" {
		return exitf(ExitUsage, "--no-start leaves the forks stopped, so --prompt has no agent to start in them; pass one of the two.")
	}
	src, err := requireProject(ctx, e, opts.ProjectArg)
	if err != nil {
		return err
	}
	// The cap is the api's to enforce, for all N at once; asking first
	// only spares a snapshot that nothing would use. It counts running
	// and stopped projects alike (I-569). Then the plan's memory for all
	// N, which the api's gate asks for one machine and only after the
	// snapshot, so each later fork failed to start (I-610). PROJECT keeps
	// running, so it counts.
	if me, err := e.Client.GetMe(ctx); err == nil {
		var projects []Project
		var listErr error
		listed := false
		list := func() ([]Project, error) {
			if !listed {
				projects, listErr = e.Client.ListProjects(ctx)
				listed = true
			}
			return projects, listErr
		}
		if me.Limits.Projects > 0 {
			if projects, err := list(); err == nil {
				have := 0
				for _, p := range projects {
					if p.State != "destroying" {
						have++
					}
				}
				if have+opts.Count > me.Limits.Projects {
					return exitf(ExitGeneric, "%s", projectLimitMessage(have, me.Limits.Projects, opts.Count))
				}
			}
		}
		if !opts.NoStart {
			class := opts.Size
			if class == "" {
				class = src.Class
			}
			fix := fmt.Sprintf("`repose fork %s --no-start` creates the fork stopped", src.Slug)
			if opts.Count > 1 {
				fix = fmt.Sprintf("`repose fork %s -n %d --no-start` creates them stopped", src.Slug, opts.Count)
			}
			if msg := planMemoryRefusalOf(me, list, planAsk{Class: class, Count: opts.Count, Fix: fix}); msg != "" {
				return exitf(ExitPaymentRequired, "%s", msg)
			}
		}
	}

	pr := e.newProgress()
	defer pr.Fail()
	snapID := opts.SnapshotID
	if snapID == "" {
		if src.State != "running" && src.State != "stopped" {
			return notRunningError(src)
		}
		opID, err := e.Client.CreateSnapshot(ctx, src.ID)
		if err != nil {
			return err
		}
		pr.Phase("Snapshotting "+src.Slug, "Snapshot of "+src.Slug+" taken")
		op, err := waitOpPhased(ctx, e, src, opID, pr, false)
		if err != nil {
			return err
		}
		if op.State == "error" {
			pr.Fail()
			return e.opFailed("snapshot", src.Slug, op.Error, "Nothing was forked.")
		}
		snapID, _ = op.Result["snapshot_id"].(string)
		if snapID == "" {
			// An api that does not return the op's result: the newest
			// manual snapshot is the one just taken.
			snaps, err := e.Client.ListSnapshots(ctx, src.ID)
			if err != nil {
				return err
			}
			if s := newestSnapshot(snaps); s != nil {
				snapID = s.ID
			}
		}
		if snapID == "" {
			pr.Fail()
			return exitf(ExitGeneric, "The snapshot of %s finished but the api did not say which it is. `repose snapshots list %s` shows it; `repose fork %s --snapshot ID` forks from it.", src.Slug, src.Slug, src.Slug)
		}
	}

	req := ForkRequest{SnapshotID: snapID, Count: opts.Count, Name: opts.Name, Class: opts.Size, RequestID: newRequestID()}
	want := "running"
	if opts.NoStart {
		no := false
		req.Start = &no
		want = "stopped"
	}
	pr.Phase(fmt.Sprintf("Forking %s into %d", src.Slug, opts.Count), "")
	res, err := forkWithRetry(ctx, e, src.ID, req)
	if err != nil {
		pr.Fail()
		var apiErr *APIError
		// The api's cap refusal (a create elsewhere since the check
		// above, or /me unreadable) in the words every command uses.
		if errors.As(err, &apiErr) {
			if have, limit, n, ok := projectLimitOf(apiErr); ok {
				return exitf(ExitGeneric, "%s", projectLimitMessage(have, limit, n))
			}
		}
		if apiErr != nil && (apiErr.Code == "invalid" || apiErr.Code == "not_found") {
			return exitf(ExitGeneric, "Could not fork %s: %s. Nothing was created.", src.Slug, strings.TrimSuffix(humaneMessage(apiErr.Message), "."))
		}
		return err
	}
	if len(res.Projects) == 0 {
		return exitf(ExitGeneric, "The api answered the fork of %s without any project. `repose ls` shows what exists.", src.Slug)
	}

	// Each fork's restore is its own op; they run side by side on the
	// api, so waiting on them in turn takes as long as the slowest.
	failed := 0
	for i := range res.Projects {
		f := &res.Projects[i]
		if opts.NoStart {
			pr.Phase("Restoring "+f.Slug, "")
		} else {
			pr.Phase("Starting "+f.Slug, "")
		}
		op, err := waitOp(ctx, e.Client, f.ProjectID, f.OpID, pr)
		if err != nil {
			return err
		}
		if p, err := e.Client.GetProject(ctx, f.ProjectID); err == nil {
			f.State = p.State
			if p.LastError != nil {
				f.Error = *p.LastError
			}
		}
		if op.State == "error" {
			f.State = "error"
			if f.Error == "" {
				f.Error = humaneMessage(op.Error.Message)
			}
		}
		if f.State != want {
			failed++
		}
	}
	pr.Fail()
	// A fork's name may be one a destroyed project had, whose ssh master
	// leads to the old guest; and the certificate must name the new ones.
	for _, f := range res.Projects[1:] {
		closeMaster(ctx, e, f.Slug)
	}
	refreshSSHAccess(ctx, e, res.Projects[0].Slug)

	if opts.Prompt != "" {
		for _, f := range res.Projects {
			if f.State != "running" {
				continue
			}
			if err := forkStartAgent(ctx, e, f.Slug, opts.Agent, opts.Prompt); err != nil {
				e.warn("Could not start the agent in %s: %s. `repose run %s --no-sync -p PROMPT` tries again.", f.Slug, oneLine(err.Error()), f.Slug)
			}
		}
	}

	if e.JSON {
		if err := writeJSONOut(e.Out, res); err != nil {
			return err
		}
	} else {
		writeForkSummary(e, src, res, pr.Total())
	}
	if failed > 0 {
		verb := "start"
		if opts.NoStart {
			verb = "restore"
		}
		return exitf(ExitGeneric, "%d of %d forks did not %s. Each failed fork is still a project: `repose rm NAME` removes it, and `repose fork %s --snapshot %s` makes another from the same snapshot.", failed, len(res.Projects), verb, src.Slug, res.SnapshotID)
	}
	return nil
}

// forkWithRetry posts the fork, and posts it again with the same
// request_id while the api is away (a redeploy's 502, a dropped
// connection), so a request the api did take is answered with its
// projects instead of making N more.
func forkWithRetry(ctx context.Context, e *Env, projectID string, req ForkRequest) (*ForkResult, error) {
	var since time.Time
	for {
		res, err := e.Client.Fork(ctx, projectID, req)
		if err == nil || !transientAPIError(err) || ctx.Err() != nil {
			return res, err
		}
		if since.IsZero() {
			since = time.Now()
		}
		if time.Since(since) > opTransientBudget {
			return nil, err
		}
		if err := sleepOrDone(ctx, opTransientPause); err != nil {
			return nil, err
		}
	}
}

func writeForkSummary(e *Env, src *Project, res *ForkResult, took time.Duration) {
	n := len(res.Projects)
	noun := "projects"
	if n == 1 {
		noun = "project"
	}
	_, _ = fmt.Fprintf(e.Out, "Forked %s into %d %s from its snapshot of %s in %s:\n", src.Slug, n, noun, res.SnapshotCreatedAt.Local().Format("2006-01-02 15:04"), fmtElapsed(took))
	tw := tabwriter.NewWriter(e.Out, 0, 0, 2, ' ', 0)
	for _, f := range res.Projects {
		state := stateWords(f.State) + " (" + f.Class + ")"
		if f.State == "error" && f.Error != "" {
			state = "error: " + f.Error
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s\n", f.Slug, state)
	}
	_ = tw.Flush()
}
