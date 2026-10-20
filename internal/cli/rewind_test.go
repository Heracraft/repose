package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/machine"
)

// The two points every rewind test works with: one taken 14 minutes
// before clockOf's base, one two hours before.
const twoPointsJSON = `{"points":[
	{"name":"r-0199a1c2","project":"todo-app","reason":"run","taken_at":"2026-10-19T09:46:00Z"},
	{"name":"r-0199a0f1","project":"todo-app","reason":"manual","taken_at":"2026-10-19T08:00:00Z"}
]}`

// safetyPointsJSON is the list straight after a rewind: the newest point
// is the one that rewind took of the state it replaced.
const safetyPointsJSON = `{"points":[
	{"name":"r-0199a1ff","project":"todo-app","reason":"rewind","taken_at":"2026-10-19T09:58:00Z"},
	{"name":"r-0199a1c2","project":"todo-app","reason":"run","taken_at":"2026-10-19T09:46:00Z"}
]}`

// rewindEnv is an Env whose working directory is a project on homebox.
func rewindEnv(t *testing.T) (*Env, *discardWriter, *discardWriter) {
	t.Helper()
	e, reg, out, errOut := spineEnv(t)
	addMachine(t, reg, testMachine())
	recordPlace(t, e, "", e.Cwd, machine.Project{Machine: "homebox", Slug: "todo-app"})
	return e, out, errOut
}

// answeredWith records the question asked and answers it.
func answeredWith(answer bool, asked *string) func(string) (bool, error) {
	return func(prompt string) (bool, error) {
		*asked = prompt
		return answer, nil
	}
}

func TestRewindAsksThenSaysWhatItDid(t *testing.T) {
	e, out, _ := rewindEnv(t)
	sent := fakeAgent(t, map[string]string{
		"point list":   twoPointsJSON,
		"quiesce":      `{"stopped":["tmux:todo-app","claude"],"busy":[]}`,
		"point rewind": `{"rewound":true,"safety_point":"r-0199a1ff"}`,
	})
	var asked string
	opts := RewindOptions{
		Now:     clockOf(0, 0, 2100*time.Millisecond),
		Confirm: answeredWith(true, &asked),
	}
	if err := RewindCmd(context.Background(), e, opts); err != nil {
		t.Fatalf("rewind: %v", err)
	}
	wantAsk := "Rewind todo-app to the point taken 14 minutes ago? Everything written since is lost. [y/N] "
	if asked != wantAsk {
		t.Errorf("question:\n%q\nwant:\n%q", asked, wantAsk)
	}
	want := "Stopped 2 processes. Rewound todo-app (2.1s). " +
		"The state it replaced is the point r-0199a1ff.\n"
	if out.buf.String() != want {
		t.Errorf("stdout %q, want %q", out.buf.String(), want)
	}
	// The newest point, and the project is quiesced before the swap.
	if len(*sent) != 3 {
		t.Fatalf("sent %q", *sent)
	}
	if !strings.Contains((*sent)[1], "quiesce") || !strings.Contains((*sent)[2], "'r-0199a1c2'") {
		t.Errorf("sent %q", *sent)
	}
}

func TestRewindTakesNoForAnAnswer(t *testing.T) {
	e, out, _ := rewindEnv(t)
	fakeAgent(t, map[string]string{"point list": twoPointsJSON})
	var asked string
	opts := RewindOptions{Now: clockOf(0), Confirm: answeredWith(false, &asked)}
	err := RewindCmd(context.Background(), e, opts)
	if exitCodeOf(err) != ExitGeneric {
		t.Fatalf("exit %d, want %d (%v)", exitCodeOf(err), ExitGeneric, err)
	}
	if err.Error() != "Nothing rewound." {
		t.Errorf("refusal %q", err.Error())
	}
	if out.buf.String() != "" {
		t.Errorf("stdout %q", out.buf.String())
	}
}

func TestRewindToANamedPoint(t *testing.T) {
	e, out, _ := rewindEnv(t)
	sent := fakeAgent(t, map[string]string{
		"point list":   twoPointsJSON,
		"quiesce":      `{"stopped":[],"busy":[]}`,
		"point rewind": `{"rewound":true,"safety_point":"r-0199a1ff"}`,
	})
	// --yes asks nothing, so the clock's first reading is the start of
	// the rewind itself.
	opts := RewindOptions{To: "r-0199a0f1", Yes: true, Now: clockOf(0, 500*time.Millisecond)}
	if err := RewindCmd(context.Background(), e, opts); err != nil {
		t.Fatalf("rewind --to: %v", err)
	}
	if want := "Rewound todo-app (0.5s). The state it replaced is the point r-0199a1ff.\n"; out.buf.String() != want {
		t.Errorf("stdout %q, want %q", out.buf.String(), want)
	}
	if !strings.Contains((*sent)[2], "'r-0199a0f1'") {
		t.Errorf("sent %q", *sent)
	}
}

func TestRewindRefusalsAreOneSentence(t *testing.T) {
	for _, c := range []struct {
		name    string
		answers map[string]string
		opts    RewindOptions
		code    int
		says    string
	}{
		{
			name:    "no points",
			answers: map[string]string{"point list": `{"points":[]}`},
			code:    ExitGuestNotRunning,
			says:    "todo-app has no rewind points.",
		},
		{
			name:    "a point it does not have",
			answers: map[string]string{"point list": twoPointsJSON},
			opts:    RewindOptions{To: "r-nope", Yes: true},
			code:    ExitGuestNotRunning,
			says:    "todo-app has no rewind point called r-nope.",
		},
		{
			name: "something still writing",
			answers: map[string]string{
				"point list": twoPointsJSON,
				"quiesce":    `{"stopped":["tmux:todo-app"],"busy":["claude","node"]}`,
			},
			opts: RewindOptions{Yes: true},
			code: ExitGuestNotRunning,
			says: "Could not stop everything writing in todo-app: claude, node.",
		},
		{
			name: "an agent that refuses the swap",
			answers: map[string]string{
				"point list":   twoPointsJSON,
				"quiesce":      `{"stopped":[],"busy":[]}`,
				"point rewind": `{"error":{"code":"busy","message":"the project is not quiesced"}}`,
			},
			opts: RewindOptions{Yes: true},
			code: ExitGuestNotRunning,
			says: "Could not rewind todo-app: the project is not quiesced (busy).",
		},
		{
			name: "a swap the machine did not report",
			answers: map[string]string{
				"point list":   twoPointsJSON,
				"quiesce":      `{"stopped":[],"busy":[]}`,
				"point rewind": `{"rewound":false}`,
			},
			opts: RewindOptions{Yes: true},
			code: ExitGeneric,
			says: "Could not rewind todo-app: the machine did not report the swap.",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, out, _ := rewindEnv(t)
			fakeAgent(t, c.answers)
			opts := c.opts
			opts.Now = clockOf(0)
			err := RewindCmd(context.Background(), e, opts)
			if exitCodeOf(err) != c.code {
				t.Fatalf("exit %d, want %d (%v)", exitCodeOf(err), c.code, err)
			}
			if err.Error() != c.says {
				t.Errorf("refusal %q, want %q", err.Error(), c.says)
			}
			if out.buf.String() != "" {
				t.Errorf("stdout %q", out.buf.String())
			}
		})
	}
}

func TestRewindOnAFolderThatIsOnNoMachine(t *testing.T) {
	e, _, _, _ := spineEnv(t)
	err := RewindCmd(context.Background(), e, RewindOptions{Yes: true, Now: clockOf(0)})
	if exitCodeOf(err) != ExitGuestNotRunning {
		t.Fatalf("exit %d, want %d (%v)", exitCodeOf(err), ExitGuestNotRunning, err)
	}
	if err.Error() != "This folder's project is on no machine you added." {
		t.Errorf("refusal %q", err.Error())
	}
}

func TestRewindListTable(t *testing.T) {
	e, out, _ := rewindEnv(t)
	fakeAgent(t, map[string]string{"point list": twoPointsJSON})
	if err := RewindCmd(context.Background(), e, RewindOptions{List: true, Now: clockOf(0)}); err != nil {
		t.Fatal(err)
	}
	want := "POINT       TAKEN           REASON\n" +
		"r-0199a1c2  14 minutes ago  run\n" +
		"r-0199a0f1  2 hours ago     manual\n"
	if out.buf.String() != want {
		t.Errorf("table:\n%q\nwant:\n%q", out.buf.String(), want)
	}
}

func TestRewindFlagsThatContradict(t *testing.T) {
	for _, args := range [][]string{
		{"--list", "--to", "r-0199a1c2"},
		{"--list", "--rm", "r-0199a1c2"},
		{"--rm", "r-0199a1c2", "--to", "r-0199a0f1"},
	} {
		cmd := newRewindCmd(func() (*Env, error) { return nil, nil }, &globalFlags{})
		cmd.SetArgs(args)
		cmd.SetOut(&discardWriter{})
		cmd.SetErr(&discardWriter{})
		if err := cmd.Execute(); !isUsage(err) {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// A bare `repose rewind` never selects the point a rewind took of the
// state it replaced. Selecting it put the wreck the user had just
// escaped straight back.
func TestRewindSkipsASafetyPoint(t *testing.T) {
	e, out, _ := rewindEnv(t)
	sent := fakeAgent(t, map[string]string{
		"point list":   safetyPointsJSON,
		"quiesce":      `{"stopped":[],"busy":[]}`,
		"point rewind": `{"rewound":true,"safety_point":"r-0199a2aa"}`,
	})
	opts := RewindOptions{Yes: true, Now: clockOf(0, 0)}
	if err := RewindCmd(context.Background(), e, opts); err != nil {
		t.Fatalf("rewind: %v", err)
	}
	if !strings.Contains((*sent)[2], "'r-0199a1c2'") {
		t.Errorf("rewound to %q, want the run point and not the rewind's own", (*sent)[2])
	}
	if !strings.Contains(out.buf.String(), "r-0199a2aa") {
		t.Errorf("stdout %q does not name the point of the state it replaced", out.buf.String())
	}
}

// --to still reaches a safety point by name: undoing a rewind is what it
// is for.
func TestRewindToASafetyPointByName(t *testing.T) {
	e, _, _ := rewindEnv(t)
	sent := fakeAgent(t, map[string]string{
		"point list":   safetyPointsJSON,
		"quiesce":      `{"stopped":[],"busy":[]}`,
		"point rewind": `{"rewound":true,"safety_point":"r-0199a2aa"}`,
	})
	opts := RewindOptions{To: "r-0199a1ff", Yes: true, Now: clockOf(0, 0)}
	if err := RewindCmd(context.Background(), e, opts); err != nil {
		t.Fatalf("rewind --to: %v", err)
	}
	if !strings.Contains((*sent)[2], "'r-0199a1ff'") {
		t.Errorf("sent %q", (*sent)[2])
	}
}

// A project whose only points are its rewinds' own is exit 5, not a
// rewind to one of them.
func TestRewindWithOnlySafetyPoints(t *testing.T) {
	e, out, _ := rewindEnv(t)
	fakeAgent(t, map[string]string{
		"point list": `{"points":[{"name":"r-0199a1ff","project":"todo-app","reason":"rewind","taken_at":"2026-10-19T09:58:00Z"}]}`,
	})
	err := RewindCmd(context.Background(), e, RewindOptions{Yes: true, Now: clockOf(0)})
	if exitCodeOf(err) != ExitGuestNotRunning {
		t.Fatalf("exit %d, want %d (%v)", exitCodeOf(err), ExitGuestNotRunning, err)
	}
	if want := "todo-app has only the points its own rewinds took; --to names one."; err.Error() != want {
		t.Errorf("refusal %q, want %q", err.Error(), want)
	}
	if out.buf.String() != "" {
		t.Errorf("stdout %q", out.buf.String())
	}
}

// --rm frees space by hand: it asks, then deletes the one point.
func TestRewindRmDeletesOnePoint(t *testing.T) {
	e, out, _ := rewindEnv(t)
	sent := fakeAgent(t, map[string]string{
		"point list":   twoPointsJSON,
		"point delete": `{"deleted":true}`,
	})
	var asked string
	opts := RewindOptions{Rm: "r-0199a0f1", Now: clockOf(0), Confirm: answeredWith(true, &asked)}
	if err := RewindCmd(context.Background(), e, opts); err != nil {
		t.Fatalf("rewind --rm: %v", err)
	}
	wantAsk := "Delete todo-app's point taken 2 hours ago? It cannot be rewound to after this. [y/N] "
	if asked != wantAsk {
		t.Errorf("question %q, want %q", asked, wantAsk)
	}
	if want := "Deleted r-0199a0f1.\n"; out.buf.String() != want {
		t.Errorf("stdout %q, want %q", out.buf.String(), want)
	}
	// The list, then the delete, and no quiesce and no rewind.
	// agentRun quotes every word, so the recorded command reads
	// `'point' 'delete'`.
	if len(*sent) != 2 || !strings.Contains((*sent)[1], "'point' 'delete'") ||
		!strings.Contains((*sent)[1], "'r-0199a0f1'") {
		t.Fatalf("sent %q", *sent)
	}
}

func TestRewindRmTakesNoForAnAnswer(t *testing.T) {
	e, out, _ := rewindEnv(t)
	sent := fakeAgent(t, map[string]string{"point list": twoPointsJSON})
	var asked string
	opts := RewindOptions{Rm: "r-0199a0f1", Now: clockOf(0), Confirm: answeredWith(false, &asked)}
	err := RewindCmd(context.Background(), e, opts)
	if exitCodeOf(err) != ExitGeneric {
		t.Fatalf("exit %d, want %d (%v)", exitCodeOf(err), ExitGeneric, err)
	}
	if err.Error() != "Nothing deleted." {
		t.Errorf("refusal %q", err.Error())
	}
	if out.buf.String() != "" || len(*sent) != 1 {
		t.Errorf("stdout %q, sent %q", out.buf.String(), *sent)
	}
}

func TestRewindRmOfAPointThatIsNotThere(t *testing.T) {
	e, _, _ := rewindEnv(t)
	fakeAgent(t, map[string]string{"point list": twoPointsJSON})
	err := RewindCmd(context.Background(), e, RewindOptions{Rm: "r-nope", Yes: true, Now: clockOf(0)})
	if exitCodeOf(err) != ExitGuestNotRunning {
		t.Fatalf("exit %d, want %d (%v)", exitCodeOf(err), ExitGuestNotRunning, err)
	}
	if want := "todo-app has no rewind point called r-nope."; err.Error() != want {
		t.Errorf("refusal %q, want %q", err.Error(), want)
	}
}
