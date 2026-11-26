package sample

import (
	"context"
	"os"
	"testing"

	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// desktop is Claude Desktop's process tree on shule-id (ENG-255): its
// server, daemonised under pid 1, and its version-named CLI.
var desktop = []fakeProc{
	{pid: 500, ppid: 1, comm: "server", start: 4000},
	{pid: 501, ppid: 500, comm: "2.1.330-3cc8a86", ticks: 10, start: 4100},
}

const desktopID = "501:4100"

func agentsOf(t *testing.T, w *Watcher) map[string]string {
	t.Helper()
	sig, _ := w.Signals()
	out := map[string]string{}
	for _, a := range sig.GetAgents() {
		key := a.GetAgentId()
		if a.GetTmuxWindow() != "" {
			key = a.GetTerminal()
		}
		out[key] = a.GetState()
	}
	return out
}

// TestWindowlessSessionPromptsAndCompletes (I-1676, ENG-255): a Claude
// with no terminal is listed from its SessionStart, idle; its prompt
// makes it working at once, and announced so after the debounce; its
// Stop makes it idle; its SessionEnd removes it. Before the registry,
// none of these hooks listed any agent.
func TestWindowlessSessionPromptsAndCompletes(t *testing.T) {
	w, _, rec, clk, _ := newWatcherFixture(t, desktop)
	ctx := context.Background()
	settle := func() {
		clk.advance(Interval)
		w.Refresh(ctx)
		clk.advance(StateDebounce)
		w.Refresh(ctx)
	}
	hook := func(kind string) {
		w.RecordAgentHook(AgentHook{Agent: "claude", Kind: kind, Proc: desktopID, SessionID: "08b87fc9-c37a-4bc4-b0af-a55bc5dfc98a"}, clk.now())
	}
	hook(KindSessionStart)
	if got := agentsOf(t, w); got[desktopID] != StateIdle || len(got) != 1 {
		t.Fatalf("after SessionStart, agents = %v, want %s idle", got, desktopID)
	}
	hook(KindPrompt)
	if got := agentsOf(t, w)[desktopID]; got != StateWorking {
		t.Fatalf("after a prompt = %q, want working at once", got)
	}
	settle()
	hook(KindCompleted)
	if got := agentsOf(t, w)[desktopID]; got != StateIdle {
		t.Fatalf("after its Stop = %q, want idle", got)
	}
	settle()
	states, _, _ := rec.snapshot()
	if !containsState(states, "claude/="+StateWorking) || !containsState(states, "claude/="+StateIdle) {
		t.Fatalf("states = %v, want working then idle announced", states)
	}
	if got := w.AgentSession(desktopID); got != "08b87fc9-c37a-4bc4-b0af-a55bc5dfc98a" {
		t.Fatalf("session id = %q", got)
	}
	sig, _ := w.Signals()
	if a := sig.GetAgents()[0]; a.GetTmuxWindow() != "" || a.GetTerminal() != "" || a.GetAgent() != "claude" {
		t.Fatalf("agent = %+v, want claude with no window and no terminal", a)
	}
	hook(KindSessionEnd)
	if got := agentsOf(t, w); len(got) != 0 {
		t.Fatalf("after SessionEnd, agents = %v, want none", got)
	}
}

// TestWindowlessAgentGoesWithItsProcess: killing the process removes the
// record at the next refresh, and so does a pid the kernel reused for
// another process (another start time).
func TestWindowlessAgentGoesWithItsProcess(t *testing.T) {
	w, _, rec, clk, p := newWatcherFixture(t, desktop)
	ctx := context.Background()
	w.RecordAgentHook(AgentHook{Agent: "claude", Kind: KindPrompt, Proc: desktopID}, clk.now())
	clk.advance(StateDebounce)
	w.Refresh(ctx)
	if got := agentsOf(t, w)[desktopID]; got != StateWorking {
		t.Fatalf("state = %q", got)
	}
	if err := os.RemoveAll(p.ProcPID("501")); err != nil {
		t.Fatal(err)
	}
	clk.advance(Interval)
	w.Refresh(ctx)
	if got := agentsOf(t, w); len(got) != 0 {
		t.Fatalf("after its process died, agents = %v, want none", got)
	}
	states, _, _ := rec.snapshot()
	if !containsState(states, "claude/="+StateUnknown) {
		t.Fatalf("states = %v, want the agent's end announced as unknown", states)
	}

	// The pid comes back as something else.
	writeProc(t, p, []fakeProc{{pid: 501, ppid: 500, comm: "2.1.330-3cc8a86", start: 9999}})
	w.RecordAgentHook(AgentHook{Agent: "claude", Kind: KindPrompt, Proc: desktopID}, clk.now())
	w.Refresh(ctx)
	if got := agentsOf(t, w); len(got) != 0 {
		t.Fatalf("a reused pid kept the record: %v", got)
	}
}

// TestGuestdRestartRediscoversAWindowlessAgent: a guestd that restarts
// mid-turn reads the registry back from the run directory, checks it
// against /proc, and still lists the agent as working.
func TestGuestdRestartRediscoversAWindowlessAgent(t *testing.T) {
	w, run, rec, clk, p := newWatcherFixture(t, desktop)
	ctx := context.Background()
	w.RecordAgentHook(AgentHook{Agent: "claude", Kind: KindSessionStart, Proc: desktopID}, clk.now())
	w.RecordAgentHook(AgentHook{Agent: "claude", Kind: KindPrompt, Proc: desktopID}, clk.now())

	again := NewWatcher(p, run, &sysdep.FakeDocker{Up: true}, fixedSlug("todo-app"), rec, quietLog(), clk.now)
	clk.advance(Interval)
	again.Refresh(ctx)
	if got := agentsOf(t, again); got[desktopID] != StateWorking || len(got) != 1 {
		t.Fatalf("after a restart, agents = %v, want %s working", got, desktopID)
	}
	again.RecordAgentHook(AgentHook{Agent: "claude", Kind: KindCompleted, Proc: desktopID}, clk.now())
	if got := agentsOf(t, again)[desktopID]; got != StateIdle {
		t.Fatalf("its Stop after the restart = %q", got)
	}

	// A restart after the process ended while guestd was down drops it.
	if err := os.RemoveAll(p.ProcPID("501")); err != nil {
		t.Fatal(err)
	}
	third := NewWatcher(p, run, &sysdep.FakeDocker{Up: true}, fixedSlug("todo-app"), rec, quietLog(), clk.now)
	third.Refresh(ctx)
	if got := agentsOf(t, third); len(got) != 0 {
		t.Fatalf("a dead process survived the restart: %v", got)
	}
}

// TestTmuxAgentIsUnchangedAndNotListedTwice: a tmux window's agent is
// listed as before, now with its process id and terminal; a windowless
// hook whose process turns out to be in that pane does not list it a
// second time.
func TestTmuxAgentIsUnchangedAndNotListedTwice(t *testing.T) {
	procs := []fakeProc{
		{pid: 100, ppid: 1, comm: "bash", start: 10},
		{pid: 101, ppid: 100, comm: "claude", ticks: 50, start: 11},
	}
	w, run, _, clk, _ := newWatcherFixture(t, procs)
	run.Match["list-windows"] = tmuxOutput([4]string{"claude", "101", "claude", "0"})
	ctx := context.Background()
	w.Refresh(ctx)
	w.RecordHook("claude", KindPrompt, clk.now())
	w.RecordAgentHook(AgentHook{Agent: "claude", Kind: KindPrompt, Proc: "101:11"}, clk.now())
	clk.advance(Interval)
	w.Refresh(ctx)
	sig, _ := w.Signals()
	if len(sig.GetAgents()) != 1 {
		t.Fatalf("agents = %v, want the window alone", sig.GetAgents())
	}
	a := sig.GetAgents()[0]
	if a.GetTmuxWindow() != "claude" || a.GetState() != StateWorking || a.GetTerminal() != "tmux:claude" || a.GetAgentId() != "101:11" {
		t.Fatalf("agent = %+v", a)
	}
}

// TestRegistryPIDsCoverTheAgent (I-200, ENG-255): Desktop's CLI is
// not named claude, so only the registry knows to protect it.
func TestRegistryPIDsCoverTheAgent(t *testing.T) {
	w, _, _, clk, _ := newWatcherFixture(t, desktop)
	w.RecordAgentHook(AgentHook{Agent: "claude", Kind: KindSessionStart, Proc: desktopID}, clk.now())
	if got := w.registryPIDs(); len(got) != 1 || got[0] != 501 {
		t.Fatalf("registry pids = %v, want [501]", got)
	}
}

// TestWindowlessWorkingGoesQuiet: a working agent with no hook and no CPU
// for WindowlessQuietAfter is idle (an interrupted turn sends no Stop).
func TestWindowlessWorkingGoesQuiet(t *testing.T) {
	w, _, _, clk, _ := newWatcherFixture(t, desktop)
	ctx := context.Background()
	w.RecordAgentHook(AgentHook{Agent: "claude", Kind: KindPrompt, Proc: desktopID}, clk.now())
	w.Refresh(ctx)
	clk.advance(WindowlessQuietAfter - Interval)
	w.Refresh(ctx)
	if got := agentsOf(t, w)[desktopID]; got != StateWorking {
		t.Fatalf("before the quiet time = %q", got)
	}
	clk.advance(2 * Interval)
	w.Refresh(ctx)
	if got := agentsOf(t, w)[desktopID]; got != StateIdle {
		t.Fatalf("after the quiet time = %q, want idle", got)
	}
}

func TestRegistryRefusesABadKeyOrAgent(t *testing.T) {
	w, _, _, clk, _ := newWatcherFixture(t, desktop)
	for _, h := range []AgentHook{
		{Agent: "claude", Kind: KindPrompt, Proc: "501"},
		{Agent: "claude", Kind: KindPrompt, Proc: "0:1"},
		{Agent: "aider", Kind: KindPrompt, Proc: desktopID},
	} {
		w.RecordAgentHook(h, clk.now())
	}
	if got := agentsOf(t, w); len(got) != 0 {
		t.Fatalf("agents = %v, want none", got)
	}
}

// TestSayNamesAnAgentWithNoTerminal: with only a windowless agent, say
// refuses and says why, rather than "no agent is running".
func TestSayNamesAnAgentWithNoTerminal(t *testing.T) {
	w, _, _, clk, _ := newWatcherFixture(t, desktop)
	w.RecordAgentHook(AgentHook{Agent: "claude", Kind: KindSessionStart, Proc: desktopID}, clk.now())
	_, err := w.Say(context.Background(), SayRequest{Text: "hello", FromOwner: true})
	if err == nil || err.Error() != "claude runs outside a terminal repose can type into" {
		t.Fatalf("say = %v", err)
	}
}

// TestWindowlessWorkCountsForTheBrowserStamp: a working Desktop session
// is agent work to the desktop's idle check, as a window's is (I-1122).
func TestWindowlessWorkCountsForTheBrowserStamp(t *testing.T) {
	w, _, _, clk, _ := newWatcherFixture(t, desktop)
	w.RecordAgentHook(AgentHook{Agent: "claude", Kind: KindPrompt, Proc: desktopID}, clk.now())
	if !w.agentWorking() {
		t.Fatal("a working windowless agent is not agent work")
	}
	w.RecordAgentHook(AgentHook{Agent: "claude", Kind: KindCompleted, Proc: desktopID}, clk.now())
	if w.agentWorking() {
		t.Fatal("an idle one still is")
	}
}
