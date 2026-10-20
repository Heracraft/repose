package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/heracraft/repose/internal/machine"
)

// fakeBox replaces the ssh every spine noun runs its commands over:
// answer sees the machine it was aimed at and the command, so one test
// can give two machines two answers.
func fakeBox(t *testing.T, answer func(target sshTarget, remoteCmd string) ([]byte, error)) *[]string {
	t.Helper()
	var mu sync.Mutex
	var sent []string
	old := runOnMachine
	runOnMachine = func(_ context.Context, target sshTarget, remoteCmd string, _ io.Reader) ([]byte, error) {
		mu.Lock()
		sent = append(sent, remoteCmd)
		mu.Unlock()
		return answer(target, remoteCmd)
	}
	t.Cleanup(func() { runOnMachine = old })
	return &sent
}

// aimedAt is whether the ssh was aimed at the machine called name.
func aimedAt(target sshTarget, name string) bool {
	for _, a := range target.Args {
		if a == machine.Alias(name) {
			return true
		}
	}
	return false
}

// nounCmd is a command tree like the root's: `repose <path...>`, so
// rootNoun and CommandPath read what they read in the real tree.
func nounCmd(path ...string) *cobra.Command {
	cmd := &cobra.Command{Use: "repose"}
	for _, use := range path {
		child := &cobra.Command{Use: use}
		cmd.AddCommand(child)
		cmd = child
	}
	return cmd
}

// TestRefuseOnOwnedMachineIsOneSentencePerNoun: the nouns whose work is
// the api's say so, once, and exit 5; the nouns that answer for a
// machine the user owns are left alone, as is a command that names a
// project the project map has not got.
func TestRefuseOnOwnedMachineIsOneSentencePerNoun(t *testing.T) {
	for _, c := range []struct {
		name     string
		path     []string
		args     []string
		project  string
		flag     string // a flag set on the command before the check
		recorded bool   // whether this folder's project is on a machine
		wantCode int
		wantSaid string
	}{
		{
			name:     "stop",
			path:     []string{"stop [PROJECT...]"},
			recorded: true,
			wantCode: ExitGuestNotRunning,
			wantSaid: "`repose stop` is for a machine repose runs; todo-app is on homebox, a machine you own.",
		},
		{
			name:     "a group's subcommand names the whole path",
			path:     []string{"snapshots", "list [PROJECT]"},
			recorded: true,
			wantCode: ExitGuestNotRunning,
			wantSaid: "`repose snapshots list` is for a machine repose runs; todo-app is on homebox, a machine you own.",
		},
		{
			name:     "a noun with no PROJECT in its usage is this folder's",
			path:     []string{"secrets", "set NAME"},
			args:     []string{"STRIPE_KEY"},
			recorded: true,
			wantCode: ExitGuestNotRunning,
			wantSaid: "`repose secrets set` is for a machine repose runs",
		},
		{
			name:     "the project named is on the machine",
			path:     []string{"logs [PROJECT]"},
			args:     []string{"todo-app"},
			recorded: true,
			wantCode: ExitGuestNotRunning,
			wantSaid: "todo-app is on homebox",
		},
		{
			name:     "the project named is not one of the machine's",
			path:     []string{"stop [PROJECT...]"},
			args:     []string{"api"},
			recorded: true,
		},
		{
			name:     "--project names one that is not the machine's",
			path:     []string{"resize [PROJECT] [DISK]"},
			project:  "api",
			recorded: true,
		},
		{
			name:     "a noun that answers for the machine",
			path:     []string{"ps [PROJECT] [WINDOW]"},
			recorded: true,
		},
		{
			name:     "rewind",
			path:     []string{"rewind [PROJECT]"},
			recorded: true,
		},
		{
			name:     "--global acts on the account, not the project",
			path:     []string{"secrets", "set NAME"},
			args:     []string{"STRIPE_KEY"},
			flag:     "global",
			recorded: true,
		},
		{
			name:     "--unused acts on every machine",
			path:     []string{"stop [PROJECT...]"},
			flag:     "unused",
			recorded: true,
		},
		{
			name: "a checkout on no machine of theirs",
			path: []string{"stop [PROJECT...]"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, reg, _, _ := spineEnv(t)
			addMachine(t, reg, testMachine())
			if c.recorded {
				recordPlace(t, e, "", e.Cwd, machine.Project{Machine: "homebox", Slug: "todo-app"})
			}
			cmd := nounCmd(c.path...)
			if c.flag != "" {
				cmd.Flags().Bool(c.flag, false, "")
				if err := cmd.Flags().Set(c.flag, "true"); err != nil {
					t.Fatal(err)
				}
			}
			err := refuseOnOwnedMachine(func() (*Env, error) { return e, nil }, cmd, c.args, c.project)
			if c.wantCode == 0 {
				if err != nil {
					t.Fatalf("refused %s: %v", c.name, err)
				}
				return
			}
			if got := exitCodeOf(err); got != c.wantCode {
				t.Fatalf("exit %d, want %d (%v)", got, c.wantCode, err)
			}
			if !strings.Contains(err.Error(), c.wantSaid) {
				t.Errorf("said %q, want %q in it", err.Error(), c.wantSaid)
			}
			if strings.Contains(err.Error(), "\n") || !strings.HasSuffix(err.Error(), ".") {
				t.Errorf("refusal is not one sentence: %q", err.Error())
			}
		})
	}
}

// TestSpinePlaceOrWindowReadsALeftoverWordAsAWindow: ps and attach take
// one word that names no project of a machine as this project's window;
// every other noun does not.
func TestSpinePlaceOrWindowReadsALeftoverWordAsAWindow(t *testing.T) {
	for _, c := range []struct {
		name       string
		projectArg string
		asWindow   bool
		wantSlug   string
		wantWindow string
		wantOK     bool
	}{
		{name: "this folder's project", wantSlug: "todo-app", wantOK: true},
		{name: "the project named", projectArg: "todo-app", wantSlug: "todo-app", wantOK: true},
		{name: "a window of this folder's project", projectArg: "claude-2", asWindow: true, wantSlug: "todo-app", wantWindow: "claude-2", wantOK: true},
		{name: "a word that is no project, for a noun that takes none", projectArg: "claude-2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, reg, _, _ := spineEnv(t)
			addMachine(t, reg, testMachine())
			recordPlace(t, e, "", e.Cwd, machine.Project{Machine: "homebox", Slug: "todo-app"})
			m, place, word, ok, err := spinePlaceOrWindow(e, c.projectArg, resolveDeps{}, c.asWindow)
			if err != nil {
				t.Fatalf("spinePlaceOrWindow: %v", err)
			}
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if m.Name != "homebox" || place.Slug != c.wantSlug || word != c.wantWindow {
				t.Fatalf("resolved %s on %s, window %q", place.Slug, m.Name, word)
			}
		})
	}
}

// TestSpinePlaceIsNotTheHostedPathWithoutAMachine: a laptop that has
// added no machine never leaves the hosted path.
func TestSpinePlaceIsNotTheHostedPathWithoutAMachine(t *testing.T) {
	e, _, _, _ := spineEnv(t)
	if _, _, ok, err := spinePlace(e, "", resolveDeps{}); ok || err != nil {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
}

// TestSpineAttachForCarriesTheWindowAndRefusesTheBridge.
func TestSpineAttachForCarriesTheWindowAndRefusesTheBridge(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	recordPlace(t, e, "", e.Cwd, machine.Project{Machine: "homebox", Slug: "todo-app"})

	spine, err := spineAttachFor(e, "", "claude", false)
	if err != nil {
		t.Fatalf("spineAttachFor: %v", err)
	}
	if spine == nil || spine.Machine.Name != "homebox" || spine.Place.Slug != "todo-app" || spine.Window != "claude" {
		t.Fatalf("attach options %+v", spine)
	}
	// The one word that names no project is the window.
	spine, err = spineAttachFor(e, "claude-2", "", false)
	if err != nil {
		t.Fatalf("spineAttachFor: %v", err)
	}
	if spine == nil || spine.Window != "claude-2" || spine.Place.Slug != "todo-app" {
		t.Fatalf("attach options %+v", spine)
	}
	if _, err := spineAttachFor(e, "", "", true); exitCodeOf(err) != ExitUsage {
		t.Fatalf("--bridge: exit %d (%v)", exitCodeOf(err), err)
	}
}

// TestSpineAttachForLeavesAHostedProjectAlone.
func TestSpineAttachForLeavesAHostedProjectAlone(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	spine, err := spineAttachFor(e, "api", "", false)
	if spine != nil || err != nil {
		t.Fatalf("spine = %+v, err = %v", spine, err)
	}
}

// TestSpineAttachCmdRefusesASessionThatIsGone: nothing is started on a
// machine the user owns, so a session that is not there is exit 5.
func TestSpineAttachCmdRefusesASessionThatIsGone(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	fakeBox(t, func(sshTarget, string) ([]byte, error) {
		return nil, &sshError{ExitCode: psExitNoSession, Stderr: "can't find session: todo-app"}
	})
	err := spineAttachCmd(context.Background(), e, spineAttachOptions{
		Machine: testMachine(), Place: machine.Project{Machine: "homebox", Slug: "todo-app"},
	})
	if exitCodeOf(err) != ExitGuestNotRunning {
		t.Fatalf("exit %d (%v)", exitCodeOf(err), err)
	}
	if !strings.Contains(err.Error(), "no tmux session on homebox") {
		t.Errorf("said %q", err.Error())
	}
}

// TestSpineAttachCmdSaysWhenTheMachineDidNotAnswer: a box that is off is
// not a session that is gone.
func TestSpineAttachCmdSaysWhenTheMachineDidNotAnswer(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	fakeBox(t, func(sshTarget, string) ([]byte, error) {
		return nil, &sshError{ExitCode: 255, Stderr: "connection refused"}
	})
	err := spineAttachCmd(context.Background(), e, spineAttachOptions{
		Machine: testMachine(), Place: machine.Project{Machine: "homebox", Slug: "todo-app"},
	})
	if exitCodeOf(err) != ExitGeneric {
		t.Fatalf("exit %d (%v)", exitCodeOf(err), err)
	}
	if !strings.Contains(err.Error(), "Could not reach todo-app on homebox") {
		t.Errorf("said %q", err.Error())
	}
}

// TestSpineAttachCmdRefusesAWindowTheSessionHasNot: the leftover word
// that names no project is read as a window, so a hosted project named
// from an owned checkout must not open this project's session instead.
func TestSpineAttachCmdRefusesAWindowTheSessionHasNot(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	fakeBox(t, func(_ sshTarget, cmd string) ([]byte, error) {
		if strings.Contains(cmd, "window_name") {
			return []byte("claude\n"), nil
		}
		return nil, &sshError{ExitCode: 127, Stderr: "no such command"}
	})
	err := spineAttachCmd(context.Background(), e, spineAttachOptions{
		Machine: testMachine(),
		Place:   machine.Project{Machine: "homebox", Slug: "todo-app"},
		Window:  "api",
	})
	if exitCodeOf(err) != ExitGuestNotRunning {
		t.Fatalf("exit %d (%v)", exitCodeOf(err), err)
	}
	if !strings.Contains(err.Error(), "has no window called api") {
		t.Errorf("said %q", err.Error())
	}
}

// TestSpineSyncForTakesTheSpinePath: `repose sync` syncs the checkout
// and starts nothing, and the flags that act on a guest's tree or on the
// api's project are refused rather than ignored.
func TestSpineSyncForTakesTheSpinePath(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	recordPlace(t, e, "", e.Cwd, machine.Project{Machine: "homebox", Slug: "todo-app"})

	spine, err := spineSyncFor(e, RunOptions{NoAttach: true, Sync: true})
	if err != nil {
		t.Fatalf("spineSyncFor: %v", err)
	}
	if spine == nil || !spine.SyncOnly {
		t.Fatalf("sync options %+v", spine)
	}
	for _, c := range []struct {
		name string
		opts RunOptions
		json bool
	}{
		{name: "--size", opts: RunOptions{Size: "large"}},
		{name: "--temp", opts: RunOptions{Temp: time.Hour}},
		{name: "--json", json: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e.JSON = c.json
			defer func() { e.JSON = false }()
			_, err := spineSyncFor(e, c.opts)
			if exitCodeOf(err) != ExitUsage {
				t.Fatalf("exit %d (%v)", exitCodeOf(err), err)
			}
			if !strings.Contains(err.Error(), c.name) {
				t.Errorf("said %q, want %s named", err.Error(), c.name)
			}
		})
	}
}

// psListingOut is psScript's output for one window, as a box with no guestd
// answers it: the state option is empty.
func psListingOut(now time.Time) string {
	return strings.Join([]string{
		strconv.FormatInt(now.Unix(), 10),
		"#home /home/me",
		"#co /home/me/todo-app",
		// spinePsSep, not a tab: a distro tmux rewrites a tab in a format.
		strings.Join([]string{"1", "claude", "node", strconv.FormatInt(now.Unix(), 10), "1", "", "/home/me/todo-app"}, spinePsSep),
	}, "\n") + "\n"
}

// TestSpinePsListsTheMachinesWindows: the same table as a hosted
// project's, read over the machine's own ssh, with no agent state since
// there is no guestd on the box.
func TestSpinePsListsTheMachinesWindows(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	recordPlace(t, e, "", e.Cwd, machine.Project{Machine: "homebox", Slug: "todo-app"})
	var out bytes.Buffer
	e.Out = &out
	fakeBox(t, func(_ sshTarget, cmd string) ([]byte, error) {
		if strings.Contains(cmd, "pane_current_command") {
			return []byte(psListingOut(time.Now())), nil
		}
		return nil, &sshError{ExitCode: 127, Stderr: "no such command"}
	})
	handled, err := spinePs(context.Background(), e, PsOptions{})
	if !handled || err != nil {
		t.Fatalf("handled = %v, err = %v", handled, err)
	}
	if !strings.Contains(out.String(), "WINDOW") || !strings.Contains(out.String(), "1:claude*") {
		t.Fatalf("ps printed %q", out.String())
	}
}

// TestSpinePsRefusesASessionThatIsGone.
func TestSpinePsRefusesASessionThatIsGone(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	recordPlace(t, e, "", e.Cwd, machine.Project{Machine: "homebox", Slug: "todo-app"})
	fakeBox(t, func(sshTarget, string) ([]byte, error) {
		return nil, &sshError{ExitCode: psExitNoSession, Stderr: "can't find session: todo-app"}
	})
	handled, err := spinePs(context.Background(), e, PsOptions{})
	if !handled || exitCodeOf(err) != ExitGuestNotRunning {
		t.Fatalf("handled = %v, exit %d (%v)", handled, exitCodeOf(err), err)
	}
}

// TestSpinePsLeavesAHostedProjectToTheApi.
func TestSpinePsLeavesAHostedProjectToTheApi(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	handled, err := spinePs(context.Background(), e, PsOptions{})
	if handled || err != nil {
		t.Fatalf("handled = %v, err = %v", handled, err)
	}
}

// TestSpineStatusSaysWhereItIsAndHowFarBack.
func TestSpineStatusSaysWhereItIsAndHowFarBack(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	recordPlace(t, e, "", e.Cwd, machine.Project{Machine: "homebox", Slug: "todo-app"})
	taken := time.Now().Add(-14 * time.Minute).UTC().Format(time.RFC3339)
	answer := func(_ sshTarget, cmd string) ([]byte, error) {
		switch {
		case strings.Contains(cmd, "point") && strings.Contains(cmd, "list"):
			return []byte(`{"points":[{"name":"r-1","project":"todo-app","reason":"run","taken_at":"` + taken + `","bytes":104857600}]}`), nil
		case strings.Contains(cmd, "window_name"):
			return []byte("claude\nshell\n"), nil
		}
		return nil, &sshError{ExitCode: 127, Stderr: "no such command"}
	}

	var out bytes.Buffer
	e.Out = &out
	fakeBox(t, answer)
	handled, err := spineStatus(context.Background(), e, "")
	if !handled || err != nil {
		t.Fatalf("handled = %v, err = %v", handled, err)
	}
	for _, want := range []string{"todo-app on homebox", "claude, shell", "1 rewind point, newest 14 minutes ago"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status printed %q, want %q in it", out.String(), want)
		}
	}

	out.Reset()
	e.JSON = true
	defer func() { e.JSON = false }()
	if handled, err := spineStatus(context.Background(), e, ""); !handled || err != nil {
		t.Fatalf("--json: handled = %v, err = %v", handled, err)
	}
	var doc spineStatusJSON
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("--json printed %q: %v", out.String(), err)
	}
	if doc.Slug != "todo-app" || doc.Machine != "homebox" || doc.Points != 1 || len(doc.Windows) != 2 {
		t.Fatalf("--json doc %+v", doc)
	}
}

// TestSpineStatusWithNoSessionAndNoPointsSaysSo: a box that answered but
// holds nothing running is what the user needs to read, not an error.
func TestSpineStatusWithNoSessionAndNoPointsSaysSo(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	recordPlace(t, e, "", e.Cwd, machine.Project{Machine: "homebox", Slug: "todo-app"})
	var out bytes.Buffer
	e.Out = &out
	fakeBox(t, func(_ sshTarget, cmd string) ([]byte, error) {
		if strings.Contains(cmd, "point") {
			return []byte(`{"points":[]}`), nil
		}
		return nil, &sshError{ExitCode: psExitNoSession, Stderr: "can't find session: todo-app"}
	})
	if handled, err := spineStatus(context.Background(), e, ""); !handled || err != nil {
		t.Fatalf("handled = %v, err = %v", handled, err)
	}
	for _, want := range []string{"windows    none", "no rewind points"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status printed %q, want %q in it", out.String(), want)
		}
	}
}

// TestOwnedProjectsReadsEachMachineAndFallsBackToWhatIsRecorded: the
// box's own list is what it holds; a machine that did not answer still
// shows the projects the laptop put there, so none of them is invisible.
func TestOwnedProjectsReadsEachMachineAndFallsBackToWhatIsRecorded(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	addMachine(t, reg, machine.Machine{Name: "shed", Address: "10.0.0.2", Login: "me", UndoPath: defaultUndoPath})
	recordPlace(t, e, "", e.Cwd, machine.Project{Machine: "homebox", Slug: "todo-app"})
	recordPlace(t, e, "git@github.com:me/blog.git", "/home/me/code/blog", machine.Project{Machine: "shed", Slug: "blog"})
	fakeBox(t, func(target sshTarget, cmd string) ([]byte, error) {
		if aimedAt(target, "homebox") && strings.Contains(cmd, "status") {
			return []byte(`{"projects":[{"slug":"todo-app","points":3},{"slug":"api","points":1}]}`), nil
		}
		return nil, &sshError{ExitCode: 255, Stderr: "connection refused"}
	})
	rows := ownedProjects(context.Background(), e)
	var got []string
	for _, r := range rows {
		cell := r.Machine + "/" + r.Slug
		if r.Unreachable {
			cell += "/unreachable"
		}
		if r.here {
			cell += "/here"
		}
		got = append(got, cell)
	}
	want := "homebox/api, homebox/todo-app/here, shed/blog/unreachable"
	if strings.Join(got, ", ") != want {
		t.Fatalf("rows %q, want %q", strings.Join(got, ", "), want)
	}
	if rows[1].Points != 3 {
		t.Errorf("todo-app has %d points", rows[1].Points)
	}
}

// TestWriteOwnedProjectsMarksThisFolderAndTheMachineThatIsOff.
func TestWriteOwnedProjectsMarksThisFolderAndTheMachineThatIsOff(t *testing.T) {
	var out bytes.Buffer
	writeOwnedProjects(&out, []ownedRow{
		{Slug: "todo-app", Machine: "homebox", Points: 7, here: true},
		{Slug: "blog", Machine: "shed", Unreachable: true},
	}, false)
	want := "MACHINE  PROJECT     POINTS\n" +
		"homebox  todo-app *  7\n" +
		"shed     blog        -\n" +
		"shed did not answer.\n"
	if out.String() != want {
		t.Fatalf("printed\n%q\nwant\n%q", out.String(), want)
	}
}

// TestWriteOwnedProjectsPrintsNothingWithoutAMachine: a hosted-only
// laptop's `repose ls` is byte for byte what it was.
func TestWriteOwnedProjectsPrintsNothingWithoutAMachine(t *testing.T) {
	var out bytes.Buffer
	writeOwnedProjects(&out, nil, true)
	if out.String() != "" {
		t.Fatalf("printed %q", out.String())
	}
}

// A distro tmux rewrites a tab in a format string: tmux 3.3a, which
// Debian 12 ships, prints `_` for one, so a box the user owns is listed
// with a printable separator. The guest keeps the tab it has always used.
func TestSpinePsSeparatorIsNotATab(t *testing.T) {
	if strings.Contains(spinePsSep, "\t") {
		t.Fatalf("the separator is a tab again: %q", spinePsSep)
	}
	script := psScriptSep("todo-app", spinePsSep)
	if !strings.Contains(script, "#{window_index}"+spinePsSep+"#{window_name}") {
		t.Errorf("the script does not use the separator:\n%s", script)
	}
	if strings.Contains(script, "#{window_index}\t") {
		t.Errorf("the script still asks for tabs:\n%s", script)
	}
	// The hosted script is untouched: a guest's tmux passes a tab through.
	if !strings.Contains(psScript("todo-app"), "#{window_index}\t#{window_name}") {
		t.Error("the hosted script no longer asks for tabs")
	}
	// What a box answers, parsed back.
	out := "1792466449\n#home /home/me\n#co /home/me/todo-app\n" +
		"0" + spinePsSep + "claude" + spinePsSep + "node" + spinePsSep +
		"1792466244" + spinePsSep + "1" + spinePsSep + spinePsSep +
		"/var/lib/repose/undo/projects/todo-app\n"
	l, err := parsePsSep(out, spinePsSep)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(l.Windows) != 1 || l.Windows[0].Name != "claude" || l.Windows[0].Command != "node" {
		t.Fatalf("windows %+v", l.Windows)
	}
	if l.Windows[0].Path != "/var/lib/repose/undo/projects/todo-app" {
		t.Errorf("path %q", l.Windows[0].Path)
	}
}
