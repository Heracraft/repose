package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

func questionEnv(t *testing.T) (*Env, *Project, *fakeapi.Fake, *discardWriter, *discardWriter) {
	t.Helper()
	fake := fakeapi.New(fakeapi.Options{})
	t.Cleanup(fake.Close)
	e, p := newRoundtripEnv(t, fake)
	out, errOut := &discardWriter{}, &discardWriter{}
	e.Out, e.ErrOut = out, errOut
	return e, p, fake, out, errOut
}

func exitCodeOf(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	if err == nil {
		return ExitOK
	}
	// A usage mistake is exit 2, which is what Run does with one
	// (cli.go:81). Without this a test asserting ExitUsage reads -1.
	var ue cobraUsageError
	if errors.As(err, &ue) {
		return ExitUsage
	}
	return -1
}

// TestReplyAnswersTheOneWaitingQuestion: `repose reply todo-app yes`
// answers the project's question, the answer is held to the options, and
// a second reply is refused.
func TestReplyAnswersTheOneWaitingQuestion(t *testing.T) {
	e, p, fake, out, errOut := questionEnv(t)
	ctx := context.Background()
	if err := ReplyCmd(ctx, e, []string{"yes"}, "", "", strings.NewReader(""), false); exitCodeOf(err) != ExitGeneric {
		t.Fatalf("with nothing waiting: %v", err)
	}
	q, err := fake.AddQuestion(p.ID, "claude", "Drop the legacy sessions table?", []string{"yes", "no"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := QuestionsCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if s := out.buf.String(); !strings.Contains(s, "todo-app  claude asked") || !strings.Contains(s, "Drop the legacy sessions table?") || !strings.Contains(s, "  options: yes|no\n") {
		t.Fatalf("questions:\n%s", s)
	}
	if err := ReplyCmd(ctx, e, []string{"todo-app", "maybe"}, "", "", strings.NewReader(""), false); exitCodeOf(err) != ExitUsage || !strings.Contains(err.Error(), "yes, no") {
		t.Fatalf("not an option: %v", err)
	}
	out.buf.Reset()
	if err := ReplyCmd(ctx, e, []string{"todo-app", "YES"}, "", "", strings.NewReader(""), false); err != nil {
		t.Fatalf("reply: %v (stderr %s)", err, errOut.buf.String())
	}
	if s := out.buf.String(); s != "Answered claude in todo-app: yes\n" {
		t.Fatalf("reply said %q", s)
	}
	got := fake.Questions()[0]
	if got.ID != q.ID || got.State != "answered" || *got.Answer != "yes" || *got.AnsweredVia != "cli" {
		t.Fatalf("question after reply: %+v", got)
	}
	if err := ReplyCmd(ctx, e, []string{"todo-app", "no"}, "", "", strings.NewReader(""), false); exitCodeOf(err) != ExitGeneric {
		t.Fatalf("reply with nothing waiting: %v", err)
	}
}

// TestReplyListsWhenAmbiguousAndPrompts: two waiting questions are listed
// and nothing is sent; --question picks one; no answer on a terminal
// prompts for it, and off a terminal is a usage error.
func TestReplyListsWhenAmbiguousAndPrompts(t *testing.T) {
	e, p, fake, _, errOut := questionEnv(t)
	ctx := context.Background()
	q1, _ := fake.AddQuestion(p.ID, "claude", "First?", nil, time.Hour)
	q2, _ := fake.AddQuestion(p.ID, "codex", "Second?", []string{"a", "b"}, time.Hour)

	err := ReplyCmd(ctx, e, []string{"sure"}, "", "", strings.NewReader(""), false)
	if exitCodeOf(err) != ExitUsage || !strings.Contains(errOut.buf.String(), "2 questions are waiting") || !strings.Contains(errOut.buf.String(), "First?") {
		t.Fatalf("ambiguous: %v\n%s", err, errOut.buf.String())
	}
	for _, q := range fake.Questions() {
		if q.State != "pending" {
			t.Fatalf("an ambiguous reply answered %s", q.ID)
		}
	}
	if err := ReplyCmd(ctx, e, nil, "", shortID(q1.ID), strings.NewReader(""), false); exitCodeOf(err) != ExitUsage {
		t.Fatalf("no answer off a terminal: %v", err)
	}
	if err := ReplyCmd(ctx, e, nil, "", shortID(q1.ID), strings.NewReader("go ahead\n"), true); err != nil {
		t.Fatalf("prompted reply: %v", err)
	}
	if !strings.Contains(errOut.buf.String(), "Answer: ") {
		t.Fatalf("no prompt:\n%s", errOut.buf.String())
	}
	if err := ReplyCmd(ctx, e, []string{"b"}, "", "", strings.NewReader(""), false); err != nil {
		t.Fatalf("the one left: %v", err)
	}
	qs := fake.Questions()
	if *qs[0].Answer != "go ahead" || qs[1].ID != q2.ID || *qs[1].Answer != "b" {
		t.Fatalf("answers %+v %+v", qs[0], qs[1])
	}
	e.JSON = true
	out := &discardWriter{}
	e.Out = out
	if err := QuestionsCmd(ctx, e, "todo-app"); err != nil || strings.TrimSpace(out.buf.String()) != "[]" {
		t.Fatalf("questions --json with none waiting: %v %q", err, out.buf.String())
	}
}

// TestQuestionsSaysWhereItLooked: with no PROJECT the empty answer says it
// covered every project (it does not use the checkout's), with one it
// names it, and an agent at a terminal prompt is listed apart from the
// questions, with how to reach it.
func TestQuestionsSaysWhereItLooked(t *testing.T) {
	e, p, fake, out, _ := questionEnv(t)
	ctx := context.Background()
	if err := QuestionsCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if s := out.buf.String(); s != "No questions are waiting in any of your projects.\n" {
		t.Fatalf("all projects: %q", s)
	}
	out.buf.Reset()
	if err := QuestionsCmd(ctx, e, "todo-app"); err != nil {
		t.Fatal(err)
	}
	if s := out.buf.String(); s != "No questions are waiting on todo-app.\n" {
		t.Fatalf("one project: %q", s)
	}
	fake.SetState(p.ID, "running")
	fake.SetAgents(p.ID, []fakeapi.AgentSignal{{Agent: "claude", Window: "claude", State: "needs_input"}, {Agent: "codex", Window: "codex", State: "idle"}})
	out.buf.Reset()
	if err := QuestionsCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	want := "No questions are waiting in any of your projects.\n" +
		"Waiting at a prompt in their terminal; `repose run PROJECT -w WINDOW -p TEXT` types the answer:\n" +
		"  claude on todo-app\n"
	if s := out.buf.String(); s != want {
		t.Fatalf("terminal wait:\n%s\nwant\n%s", s, want)
	}
}

// The project's own list is asked for, so a project's questions are not
// lost behind 50 newer ones from other projects.
func TestQuestionsForOneProjectPastTheCap(t *testing.T) {
	e, p, fake, out, _ := questionEnv(t)
	ctx := context.Background()
	other, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "noisy", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fake.AddQuestion(p.ID, "claude", "Keep me?", nil, time.Hour); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 55; i++ {
		if _, err := fake.AddQuestion(other.ID, "claude", "noise", nil, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if err := QuestionsCmd(ctx, e, "todo-app"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.buf.String(), "Keep me?") {
		t.Fatalf("todo-app's question was lost:\n%s", out.buf.String())
	}
}

// B2, I-608: a first word that names a project with no waiting question
// is still the project, so `reply izma main` never sends "izma main" to
// another project's agent; `--` keeps every word in the answer.
func TestReplyNeverSendsAProjectName(t *testing.T) {
	e, p, fake, out, errOut := questionEnv(t)
	ctx := context.Background()
	other, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "izma", Class: "small"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fake.AddQuestion(p.ID, "claude", "Which branch?", nil, time.Hour); err != nil {
		t.Fatal(err)
	}
	err = ReplyCmd(ctx, e, []string{"izma", "main"}, "", "", strings.NewReader(""), false)
	if exitCodeOf(err) != ExitGeneric || !strings.Contains(err.Error(), "No question is waiting in izma.") {
		t.Fatalf("reply izma main: %v", err)
	}
	if q := fake.Questions()[0]; q.State != "pending" {
		t.Fatalf("an answer went out: %+v", q)
	}
	if err := ReplyCmd(ctx, e, []string{other.ID, "main"}, "", "", strings.NewReader(""), false); exitCodeOf(err) != ExitGeneric {
		t.Fatalf("reply by id: %v", err)
	}
	if err := replyCmd(ctx, e, []string{"izma", "main"}, "", "", strings.NewReader(""), false, false); err != nil {
		t.Fatalf("reply -- izma main: %v (stderr %s)", err, errOut.buf.String())
	}
	if s := out.buf.String(); s != "Answered claude in todo-app: izma main\n" {
		t.Fatalf("reply said %q", s)
	}
}

// I-606: questions and the terminal waits name the window, the handle
// `attach -w` takes.
func TestQuestionsNameTheWindow(t *testing.T) {
	e, p, fake, out, _ := questionEnv(t)
	ctx := context.Background()
	fake.SetState(p.ID, "running")
	fake.SetAgents(p.ID, []fakeapi.AgentSignal{{Agent: "claude", Window: "claude-2", State: "needs_input"}, {Agent: "codex", Window: "codex", State: "working"}})
	if err := QuestionsCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if s := out.buf.String(); !strings.Contains(s, "\n  claude-2 on todo-app\n") || strings.Contains(s, "codex") {
		t.Fatalf("questions:\n%s", s)
	}
	if got := questionAsker(Question{Agent: "claude", Window: "claude-3"}); got != "claude-3" {
		t.Fatal(got)
	}
	if got := questionAsker(Question{Agent: "claude"}); got != "claude" {
		t.Fatal(got)
	}
}

// TestReplyDismiss (DECISIONS I-690): `repose reply --dismiss [PROJECT]`
// cancels the one waiting question through the api's cancel route, lists
// instead of guessing when several wait, refuses an answer, and says how
// a question that closed meanwhile ended.
func TestReplyDismiss(t *testing.T) {
	e, p, fake, out, errOut := questionEnv(t)
	ctx := context.Background()
	if err := DismissCmd(ctx, e, nil, "", ""); exitCodeOf(err) != ExitGeneric {
		t.Fatalf("with nothing waiting: %v", err)
	}
	q1, _ := fake.AddQuestion(p.ID, "claude", "First?", nil, time.Hour)
	q2, _ := fake.AddQuestion(p.ID, "codex", "Second?", []string{"a", "b"}, time.Hour)
	if err := DismissCmd(ctx, e, []string{"todo-app"}, "", ""); exitCodeOf(err) != ExitUsage || !strings.Contains(errOut.buf.String(), "2 questions are waiting") {
		t.Fatalf("ambiguous: %v\n%s", err, errOut.buf.String())
	}
	if err := DismissCmd(ctx, e, []string{"todo-app", "yes"}, "", ""); exitCodeOf(err) != ExitUsage {
		t.Fatalf("an answer with --dismiss: %v", err)
	}
	for _, q := range fake.Questions() {
		if q.State != "pending" {
			t.Fatalf("%s closed by a refused dismiss", q.ID)
		}
	}
	if err := DismissCmd(ctx, e, []string{"todo-app"}, "", shortID(q1.ID)); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if s := out.buf.String(); s != "Dismissed the question claude asked in todo-app.\n" {
		t.Fatalf("dismiss said %q", s)
	}
	if got := fake.Questions()[0]; got.State != "cancelled" || *got.AnsweredVia != "cli" {
		t.Fatalf("after dismiss: %+v", got)
	}
	// The one left closes in the terminal before the dismiss lands.
	if _, err := fake.CloseQuestion(q2.ID, "cancelled", "terminal"); err != nil {
		t.Fatal(err)
	}
	if err := DismissCmd(ctx, e, nil, "", ""); exitCodeOf(err) != ExitGeneric {
		t.Fatalf("nothing left: %v", err)
	}
	if got := fake.Questions()[1]; got.State != "answered" || got.Answer != nil || *got.AnsweredVia != "terminal" {
		t.Fatalf("closed in the terminal: %+v", got)
	}
}

// closeBeforeCancel is a transport that closes the question in the fake
// just before the cancel POST reaches it, as an answer typed in the
// agent's terminal would between `repose reply --dismiss` listing the
// question and cancelling it.
type closeBeforeCancel struct {
	next  http.RoundTripper
	close func()
}

func (c closeBeforeCancel) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel") {
		c.close()
	}
	return c.next.RoundTrip(r)
}

// TestReplyDismissLosesTheRace: a question that closes in the terminal
// after the list and before the cancel prints how it ended and exits 1
// (I-690).
func TestReplyDismissLosesTheRace(t *testing.T) {
	e, p, fake, out, _ := questionEnv(t)
	ctx := context.Background()
	q, err := fake.AddQuestion(p.ID, "claude", "First?", nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	next := e.Client.HTTP.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	hc := *e.Client.HTTP
	hc.Transport = closeBeforeCancel{next: next, close: func() {
		if _, err := fake.CloseQuestion(q.ID, "cancelled", "terminal"); err != nil {
			t.Error(err)
		}
	}}
	e.Client.HTTP = &hc
	err = DismissCmd(ctx, e, []string{"todo-app"}, "", "")
	if exitCodeOf(err) != ExitGeneric || err.Error() != "Not dismissed: answered in the terminal." {
		t.Fatalf("lost race: %v", err)
	}
	if out.buf.String() != "" {
		t.Fatalf("printed %q", out.buf.String())
	}
}
