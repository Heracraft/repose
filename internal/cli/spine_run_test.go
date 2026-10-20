package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/machine"
)

// spineRunEnv is spineEnv with one machine added, writers a test can
// read, and a fake ssh on PATH for the two steps of a run that are not
// repose-agent: Claude Code's login check and the agent window. body is
// that ssh's shell, so a test says which remote commands fail.
func spineRunEnv(t *testing.T, body string) (*Env, *strings.Builder, *strings.Builder) {
	t.Helper()
	e, reg, _, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	out, errOut := &strings.Builder{}, &strings.Builder{}
	e.Out, e.ErrOut = out, errOut
	fakeSSHScript(t, "", body)
	return e, out, errOut
}

// spineRunAnswers is repose-agent's side of a run that works, plus the
// window list the run reads back over the same seam.
func spineRunAnswers() map[string]string {
	return map[string]string{
		"project init":      `{"path":"/var/lib/repose/undo/projects/todo-app","created":true}`,
		"point take":        `{"point":{"name":"r-0199a1c2","project":"todo-app","reason":"run","taken_at":"2026-10-19T10:00:00Z","bytes":0}}`,
		"tmux list-windows": "claude\n",
	}
}

// The working directory is not a repository in these tests, so no sync
// runs and the run is the four steps around it: the project, the point,
// the project map and the window.
const spineNoRepo = "Not a git repository, so nothing was synced.\n"

// sshExitsOn is a fake ssh that fails every remote command holding
// needle and takes every other one.
func sshExitsOn(needle string) string {
	return "case \"$*\" in *" + needle + "*) exit 1 ;; esac\nexit 0"
}

func TestSpineRunStartsTheAgentAndRemembersTheMachine(t *testing.T) {
	e, out, errOut := spineRunEnv(t, "exit 0")
	sent := fakeAgent(t, spineRunAnswers())

	err := SpineRunCmd(context.Background(), e, SpineRunOptions{
		MachineName: "homebox", NoAttach: true, Now: clockOf(0),
	})
	if err != nil {
		t.Fatalf("SpineRunCmd: %v", err)
	}
	if got := out.String(); got != spineNoRepo+"claude is running in todo-app on homebox.\n" {
		t.Errorf("stdout:\n%s", got)
	}
	for _, want := range []string{"✓ Prepared todo-app on homebox", "✓ Took a rewind point"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut.String())
		}
	}
	// The point is taken with a name of the undo store's shape and the
	// reason every run gives.
	var took bool
	for _, c := range *sent {
		if strings.Contains(c, "'point' 'take'") {
			took = strings.Contains(c, "'--reason' 'run'")
		}
	}
	if !took {
		t.Errorf("no point was taken with --reason run: %q", *sent)
	}
	// Only now does the checkout resolve to the machine.
	projects, err := machine.LoadProjects(e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	pr, ok := projects.Resolve("", e.Cwd)
	if !ok || pr.Machine != "homebox" || pr.Slug != "todo-app" {
		t.Errorf("the project map holds %+v, %v", pr, ok)
	}
}

// With no Claude Code login on the machine and nobody to log in (I-607),
// the prompt is refused rather than left to type itself later.
func TestSpineRunRefusesAPromptWithoutAClaudeLogin(t *testing.T) {
	e, out, _ := spineRunEnv(t, sshExitsOn("credentials.json"))
	fakeAgent(t, spineRunAnswers())

	err := SpineRunCmd(context.Background(), e, SpineRunOptions{
		MachineName: "homebox", Prompt: "finish the feature", NoAttach: true, Now: clockOf(0),
	})
	if exitCodeOf(err) != ExitGeneric {
		t.Fatalf("exit %d (%v)", exitCodeOf(err), err)
	}
	want := "Claude Code is not logged in on todo-app, so your prompt was not typed. Log in with `repose attach todo-app -w claude`, then run your prompt again."
	if err.Error() != want {
		t.Errorf("refusal:\n%s", err.Error())
	}
	if strings.Contains(out.String(), "is running in") {
		t.Errorf("a refused run said the agent is running:\n%s", out.String())
	}
}

// --sync's shape: the project, the work and the point, and no agent.
func TestSpineRunSyncOnlyStartsNoAgent(t *testing.T) {
	e, out, errOut := spineRunEnv(t, "exit 0")
	sent := fakeAgent(t, spineRunAnswers())

	err := SpineRunCmd(context.Background(), e, SpineRunOptions{
		MachineName: "homebox", SyncOnly: true, Now: clockOf(0),
	})
	if err != nil {
		t.Fatalf("SpineRunCmd: %v", err)
	}
	if got := out.String(); got != spineNoRepo {
		t.Errorf("stdout:\n%s", got)
	}
	if !strings.Contains(errOut.String(), "✓ Took a rewind point") {
		t.Errorf("stderr lacks the point:\n%s", errOut.String())
	}
	for _, c := range *sent {
		if strings.Contains(c, "list-windows") {
			t.Errorf("a sync read the machine's windows: %q", c)
		}
	}
}

// A ~/<slug> the box owner made themselves is a remedy, not a raw
// refusal: repose will not replace their file.
func TestSpineRunNamesTheFolderItCannotPrepare(t *testing.T) {
	e, _, _ := spineRunEnv(t, "exit 0")
	answers := spineRunAnswers()
	answers["project init"] = `{"error":{"code":"exists","message":"~/todo-app exists and is not a link"}}`
	fakeAgent(t, answers)

	err := SpineRunCmd(context.Background(), e, SpineRunOptions{
		MachineName: "homebox", NoAttach: true, Now: clockOf(0),
	})
	if exitCodeOf(err) != ExitUsage {
		t.Fatalf("exit %d (%v)", exitCodeOf(err), err)
	}
	want := "~/todo-app on homebox is not repose's. `repose run OTHERNAME --machine homebox` prepares another folder."
	if err.Error() != want {
		t.Errorf("refusal:\n%s", err.Error())
	}
}

// An agent that is not on the machine reads as a window that opened and
// closed; the run says so instead of attaching to nothing.
func TestSpineRunNamesAnAgentThatDidNotStart(t *testing.T) {
	e, _, _ := spineRunEnv(t, "exit 0")
	answers := spineRunAnswers()
	answers["tmux list-windows"] = "bash\n"
	fakeAgent(t, answers)

	err := SpineRunCmd(context.Background(), e, SpineRunOptions{
		MachineName: "homebox", NoAttach: true, Now: clockOf(0),
	})
	if exitCodeOf(err) != ExitGeneric || !strings.Contains(err.Error(), "started and stopped at once on homebox") {
		t.Fatalf("exit %d (%v)", exitCodeOf(err), err)
	}
}

func TestSpineRunPlaceRefusesAnUnknownMachine(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	for _, c := range []struct {
		name     string
		opts     SpineRunOptions
		wantCode int
		wantSlug string
	}{
		{
			name:     "a machine that was never added",
			opts:     SpineRunOptions{MachineName: "shed"},
			wantCode: ExitUsage,
		},
		{
			name:     "the folder names the project",
			opts:     SpineRunOptions{MachineName: "homebox"},
			wantSlug: "todo-app",
		},
		{
			name:     "a project named outright",
			opts:     SpineRunOptions{MachineName: "homebox", ProjectArg: "blog"},
			wantSlug: "blog",
		},
		{
			name:     "no machine, and this checkout is on none",
			opts:     SpineRunOptions{},
			wantCode: ExitGuestNotRunning,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, place, err := spineRunPlace(e, c.opts)
			if c.wantCode != 0 {
				if exitCodeOf(err) != c.wantCode {
					t.Fatalf("exit %d, want %d (%v)", exitCodeOf(err), c.wantCode, err)
				}
				if !strings.HasSuffix(err.Error(), ".") || strings.Contains(err.Error(), "\n") {
					t.Errorf("refusal is not one sentence: %q", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("spineRunPlace: %v", err)
			}
			if m.Name != "homebox" || place.Machine != "homebox" || place.Slug != c.wantSlug {
				t.Fatalf("resolved %s on %s, want %s on homebox", place.Slug, place.Machine, c.wantSlug)
			}
		})
	}
}

func TestSpineSyncReport(t *testing.T) {
	for _, c := range []struct {
		name     string
		sum      *SyncSummary
		named    string
		wantTick string
		wantLine string
	}{
		{name: "no summary", wantTick: "Synced"},
		{name: "nothing to send", sum: &SyncSummary{}, wantTick: "Nothing to sync"},
		{name: "files", sum: &SyncSummary{Modified: 2, Untracked: 1}, wantTick: "Synced 3 files"},
		{name: "one commit", sum: &SyncSummary{Commits: 1}, wantTick: "Synced 1 commit"},
		{
			name:     "files and commits",
			sum:      &SyncSummary{Modified: 1, Commits: 2},
			wantTick: "Synced 1 file and 2 commits",
		},
		{
			name: "a checkout the run left alone",
			sum:  &SyncSummary{Skipped: true},
		},
		{
			name:     "the laptop has work that checkout never took",
			sum:      &SyncSummary{Skipped: true, LaptopAhead: true, Modified: 2, Commits: 1},
			wantLine: "Not synced: your laptop has work the machine doesn't (2 modified, 1 commit). `repose sync` sends it.",
		},
		{
			name:     "the project was named, so the command names it",
			sum:      &SyncSummary{Skipped: true, LaptopAhead: true, Untracked: 1},
			named:    "todo-app",
			wantLine: "Not synced: your laptop has work the machine doesn't (1 untracked). `repose sync todo-app` sends it.",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			tick, line := spineSyncReport(c.sum, c.named)
			if tick != c.wantTick || line != c.wantLine {
				t.Errorf("spineSyncReport = %q, %q, want %q, %q", tick, line, c.wantTick, c.wantLine)
			}
		})
	}
}

func TestSpineLoginSentences(t *testing.T) {
	refusal := spineLoginRefusal("todo-app", "claude-2")
	if exitCodeOf(refusal) != ExitGeneric {
		t.Errorf("the refusal is exit %d", exitCodeOf(refusal))
	}
	if !strings.Contains(refusal.Error(), "`repose attach todo-app -w claude-2`") {
		t.Errorf("the refusal does not name the window: %s", refusal.Error())
	}
	notice := spineLoginNotice("todo-app")
	if strings.Contains(notice, "repose ") {
		t.Errorf("the notice names a command the user is about to see anyway: %s", notice)
	}
	for _, s := range []string{refusal.Error(), notice} {
		if !strings.Contains(s, "Claude Code is not logged in on todo-app") {
			t.Errorf("not the hosted sentence: %s", s)
		}
	}
}

func TestSpineInitFailedIsARemedy(t *testing.T) {
	for _, c := range []struct {
		name     string
		err      error
		wantCode int
		want     string
	}{
		{
			name:     "the folder is the box owner's",
			err:      &agentError{Code: agentCodeExists, Message: "~/todo-app is a link to somewhere else"},
			wantCode: ExitUsage,
			want:     "~/todo-app on homebox is not repose's. `repose run OTHERNAME --machine homebox` prepares another folder.",
		},
		{
			name:     "any other refusal is the agent's own sentence",
			err:      &agentError{Code: agentCodeBusy, Message: "the project is not quiesced"},
			wantCode: ExitGeneric,
			want:     "Could not prepare todo-app: the project is not quiesced",
		},
		{
			name:     "an ssh that failed",
			err:      &sshError{ExitCode: 255},
			wantCode: ExitGeneric,
			want:     "Could not prepare todo-app: " + (&sshError{ExitCode: 255}).Error(),
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := spineInitFailed("todo-app", "homebox", c.err)
			if exitCodeOf(err) != c.wantCode || err.Error() != c.want {
				t.Errorf("spineInitFailed = exit %d, %q", exitCodeOf(err), err.Error())
			}
		})
	}
}

// A failure that already reads as its own sentence (the sync's
// stepFailed, a refusal with its own exit code) is not wrapped again.
func TestSpineRunFailedPassesSentencesThrough(t *testing.T) {
	own := exitf(ExitDirtyRemoteTree, "The machine has changes to 2 files.")
	if err := spineRunFailed("sync todo-app", own); err != own {
		t.Errorf("spineRunFailed rewrote %q as %q", own.Error(), err.Error())
	}
	plain := spineRunFailed("sync todo-app", errors.New("no commits yet"))
	if plain.Error() != "Could not sync todo-app: no commits yet" {
		t.Errorf("spineRunFailed = %q", plain.Error())
	}
}

func TestSpineRunForRefusesWhatTheOwnedPathCannotDo(t *testing.T) {
	e, _, _, _ := spineEnv(t)
	for _, c := range []struct {
		name string
		opts RunOptions
	}{
		{name: "a size", opts: RunOptions{Machine: "homebox", Size: "large"}},
		{name: "a temporary machine", opts: RunOptions{Machine: "homebox", Temp: 1}},
		{name: "a worktree", opts: RunOptions{Machine: "homebox", Worktree: true}},
		{name: "the browser bridge", opts: RunOptions{Machine: "homebox", Bridge: true}},
		{name: "another checkout", opts: RunOptions{Machine: "homebox", On: "todo-app"}},
		{name: "a multiplexer", opts: RunOptions{Machine: "homebox", Multiplexer: "herdr"}},
		{name: "the personal configuration", opts: RunOptions{Machine: "homebox", NoPersonal: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := spineRunFor(e, c.opts)
			if got != nil {
				t.Fatalf("not refused: %+v", got)
			}
			var ue cobraUsageError
			if !errors.As(err, &ue) {
				t.Fatalf("err is %T: %v", err, err)
			}
			if !strings.Contains(err.Error(), "is for a machine repose runs") {
				t.Errorf("refusal: %v", err)
			}
		})
	}
}

func TestSpineRunForCarriesTheRunsOptions(t *testing.T) {
	e, _, _, _ := spineEnv(t)
	got, err := spineRunFor(e, RunOptions{
		Machine: "homebox", ProjectArg: "todo-app", Prompt: "finish it", Agent: "codex",
		NoAttach: true, Sync: true, StashRemote: true,
	})
	if err != nil {
		t.Fatalf("spineRunFor: %v", err)
	}
	if got == nil {
		t.Fatal("--machine was not taken as an owned machine")
	}
	if got.MachineName != "homebox" || got.ProjectArg != "todo-app" || got.Prompt != "finish it" || got.Agent != "codex" {
		t.Errorf("options: %+v", *got)
	}
	// `repose sync` is the only command that sets Sync: the whole sync,
	// and no agent.
	if !got.SyncOnly || !got.StashRemote || !got.NoAttach {
		t.Errorf("sync options: %+v", *got)
	}
	// Without the real deps the carry has no remote to point the box's
	// git at and the project map records the directory alone.
	if got.Deps.RemoteFor == nil || got.Deps.RootFor == nil {
		t.Error("spineRunFor left the checkout's git out of the options")
	}
}

// A checkout on no machine of the user's is not this path's at all: the
// hosted run answers for it.
func TestSpineRunForLeavesHostedCheckoutsAlone(t *testing.T) {
	e, _, _, _ := spineEnv(t)
	got, err := spineRunFor(e, RunOptions{})
	if err != nil || got != nil {
		t.Fatalf("spineRunFor = %+v, %v", got, err)
	}
}
