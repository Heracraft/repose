package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// Questions an agent asked with repose-ask in a guest (DECISIONS I-244,
// I-245): `repose questions` lists the waiting ones and `repose reply`
// answers one.

// Question is docs/interfaces/api.md "Questions".
type Question struct {
	ID          string     `json:"id"`
	ProjectID   string     `json:"project_id"`
	Project     string     `json:"project"`
	Agent       string     `json:"agent"`
	Window      string     `json:"window,omitempty"`
	Text        string     `json:"text"`
	Options     []string   `json:"options"`
	State       string     `json:"state"`
	Answer      *string    `json:"answer"`
	AnsweredVia *string    `json:"answered_via"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	AnsweredAt  *time.Time `json:"answered_at"`
}

type questionList struct {
	Questions []Question `json:"questions"`
}

// ListQuestions is GET /questions (pending only).
func (c *Client) ListQuestions(ctx context.Context) ([]Question, error) {
	var r questionList
	if err := c.get(ctx, "/questions", &r); err != nil {
		return nil, err
	}
	return r.Questions, nil
}

// ListProjectQuestions is GET /projects/:id/questions?state=pending: one
// project's waiting questions, which the all-projects list (50 at most)
// can crowd out.
func (c *Client) ListProjectQuestions(ctx context.Context, projectID string) ([]Question, error) {
	var r questionList
	if err := c.get(ctx, "/projects/"+url.PathEscape(projectID)+"/questions?state=pending", &r); err != nil {
		return nil, err
	}
	return r.Questions, nil
}

// AnswerQuestion is POST /projects/:id/questions/:qid/answer.
func (c *Client) AnswerQuestion(ctx context.Context, projectID, questionID, answer string) (*Question, error) {
	var q Question
	path := "/projects/" + url.PathEscape(projectID) + "/questions/" + url.PathEscape(questionID) + "/answer"
	if err := c.post(ctx, path, map[string]string{"answer": answer, "via": "cli"}, &q); err != nil {
		return nil, err
	}
	return &q, nil
}

// matchesProject reports whether arg names q's project by slug or id.
func matchesProject(q Question, arg string) bool {
	arg = strings.ToLower(strings.TrimSpace(arg))
	return arg != "" && (strings.ToLower(q.Project) == arg || q.ProjectID == arg)
}

// questionAsker is who asked: the window (claude-2), the handle `attach
// --window` takes (I-606), else the agent.
func questionAsker(q Question) string {
	if q.Window != "" {
		return q.Window
	}
	return q.Agent
}

func printQuestion(w io.Writer, q Question, now time.Time) {
	_, _ = fmt.Fprintf(w, "%s  %s asked %s, expires in %s  (id %s)\n", q.Project, questionAsker(q), ageOf(now.Sub(q.CreatedAt)), humanDuration(q.ExpiresAt.Sub(now)), shortID(q.ID))
	for _, line := range strings.Split(q.Text, "\n") {
		_, _ = fmt.Fprintf(w, "  %s\n", line)
	}
	if len(q.Options) > 0 {
		_, _ = fmt.Fprintf(w, "  options: %s\n", strings.Join(q.Options, "|"))
	}
}

func ageOf(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	return humanDuration(d) + " ago"
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

// QuestionsCmd implements `repose questions [PROJECT]`: every question
// still waiting for an answer, or only PROJECT's. With no PROJECT it
// covers every project, wherever it is run, and says so; agents waiting
// at a prompt in their terminal (needs_input) are named after, since
// they are not questions and `repose reply` cannot answer them.
func QuestionsCmd(ctx context.Context, e *Env, projectArg string) error {
	var qs []Question
	var projects []Project
	var err error
	if projectArg != "" {
		p, perr := requireProject(ctx, e, projectArg)
		if perr != nil {
			return perr
		}
		projects = []Project{*p}
		qs, err = e.Client.ListProjectQuestions(ctx, p.ID)
	} else {
		qs, err = e.Client.ListQuestions(ctx)
	}
	if err != nil {
		return err
	}
	if e.Quiet {
		for _, q := range qs {
			_, _ = fmt.Fprintln(e.Out, q.ID)
		}
		return nil
	}
	if e.JSON {
		return writeJSONOut(e.Out, questionsJSON(qs))
	}
	now := time.Now()
	for i, q := range qs {
		if i > 0 {
			_, _ = fmt.Fprintln(e.Out)
		}
		printQuestion(e.Out, q, now)
	}
	if projectArg == "" {
		// For the terminal waits; a failure leaves only the questions.
		projects, _ = e.Client.ListProjects(ctx)
	}
	var waiting []string
	for _, p := range projects {
		if p.Signals == nil || p.State != "running" {
			continue
		}
		for _, a := range p.Signals.Agents {
			if a.State == "needs_input" {
				name := a.Window
				if name == "" {
					name = a.Agent
				}
				waiting = append(waiting, fmt.Sprintf("  %s on %s", name, p.Slug))
			}
		}
	}
	switch {
	case len(qs) > 0:
	case projectArg != "":
		_, _ = fmt.Fprintf(e.Out, "No questions are waiting on %s.\n", projects[0].Slug)
	default:
		_, _ = fmt.Fprintln(e.Out, "No questions are waiting in any of your projects.")
	}
	if len(waiting) > 0 {
		if len(qs) > 0 {
			_, _ = fmt.Fprintln(e.Out)
		}
		_, _ = fmt.Fprintln(e.Out, "Waiting at a prompt in their terminal, which `repose reply` can't answer:")
		for _, l := range waiting {
			_, _ = fmt.Fprintln(e.Out, l)
		}
	}
	return nil
}

// questionsJSON is `repose questions --json`: the questions, each a
// Question, as before I-609. Agents waiting at a terminal prompt have no
// id to reply to, so they are not in it; `repose ls --json` has them in
// each project's signals (I-631).
func questionsJSON(qs []Question) []Question {
	if qs == nil {
		return []Question{}
	}
	return qs
}

// ReplyCmd implements `repose reply [PROJECT] [ANSWER...]`. The first word
// is the project when it names one of the account's projects (B2, I-608:
// before, only one with a waiting question, so a project with none sent
// its own name as the answer to another project's agent); otherwise every
// word is the answer. With one waiting question (after the project and
// --question narrow the list) it is answered; with several they are listed
// and nothing is sent. With no answer on a terminal it asks for one.
func ReplyCmd(ctx context.Context, e *Env, args []string, projectFlag, questionFlag string, in io.Reader, interactive bool) error {
	return replyCmd(ctx, e, args, projectFlag, questionFlag, in, interactive, true)
}

// replyCmd is ReplyCmd; mayNameProject is false after `--` (`repose
// reply -- todo-app is fine`), where every word is the answer.
func replyCmd(ctx context.Context, e *Env, args []string, projectFlag, questionFlag string, in io.Reader, interactive, mayNameProject bool) error {
	qs, err := e.Client.ListQuestions(ctx)
	if err != nil {
		return err
	}
	project := projectFlag
	if project == "" && mayNameProject && len(args) > 0 {
		for _, q := range qs {
			if matchesProject(q, args[0]) {
				project, args = args[0], args[1:]
				break
			}
		}
		if project == "" {
			// A project with no waiting question is still the project.
			// A failed list leaves the word to the answer, as before.
			if ps, err := e.Client.ListProjects(ctx); err == nil {
				for _, p := range ps {
					if matchesProject(Question{Project: p.Slug, ProjectID: p.ID}, args[0]) {
						project, args = p.Slug, args[1:]
						break
					}
				}
			}
		}
	}
	var cands []Question
	for _, q := range qs {
		if project != "" && !matchesProject(q, project) {
			continue
		}
		if questionFlag != "" && q.ID != questionFlag && !strings.HasSuffix(q.ID, questionFlag) {
			continue
		}
		cands = append(cands, q)
	}
	switch {
	case len(cands) == 0 && project != "":
		return exitf(ExitGeneric, "No question is waiting in %s. `repose questions` lists the waiting ones.", project)
	case len(cands) == 0:
		return exitf(ExitGeneric, "No questions are waiting.")
	case len(cands) > 1:
		now := time.Now()
		_, _ = fmt.Fprintf(e.ErrOut, "%d questions are waiting; say which with the project or --question ID:\n\n", len(cands))
		for i, q := range cands {
			if i > 0 {
				_, _ = fmt.Fprintln(e.ErrOut)
			}
			printQuestion(e.ErrOut, q, now)
		}
		return silent(ExitUsage)
	}
	q := cands[0]
	answer := strings.TrimSpace(strings.Join(args, " "))
	if answer == "" {
		if !interactive {
			return exitf(ExitUsage, "No answer given. Run `repose reply %s ANSWER`.", q.Project)
		}
		printQuestion(e.ErrOut, q, time.Now())
		prompt := "Answer: "
		if len(q.Options) > 0 {
			prompt = "Answer (" + strings.Join(q.Options, "/") + "): "
		}
		_, _ = fmt.Fprint(e.ErrOut, prompt)
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if answer = strings.TrimSpace(line); answer == "" {
			return exitf(ExitUsage, "No answer given; nothing was sent.")
		}
	}
	got, err := e.Client.AnswerQuestion(ctx, q.ProjectID, q.ID, answer)
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "invalid":
			if len(q.Options) > 0 {
				return exitf(ExitUsage, "The answer is one of: %s.", strings.Join(q.Options, ", "))
			}
			return exitf(ExitUsage, "%s", apiErr.Message)
		case "conflict":
			return exitf(ExitGeneric, "Not sent: %s.", apiErr.Message)
		}
	}
	if err != nil {
		return err
	}
	if e.JSON {
		return writeJSONOut(e.Out, got)
	}
	ans := answer
	if got.Answer != nil {
		ans = *got.Answer
	}
	_, _ = fmt.Fprintf(e.Out, "Answered %s in %s: %s\n", questionAsker(q), q.Project, ans)
	return nil
}
