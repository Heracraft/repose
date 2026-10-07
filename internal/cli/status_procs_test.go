package cli

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

func TestStatusListsListeningProcesses(t *testing.T) {
	out := `LISTEN 0 511 0.0.0.0:5173 0.0.0.0:* users:(("node",pid=4242,fd=20))
LISTEN 0 511 [::]:5173 [::]:* users:(("node",pid=4242,fd=21))
LISTEN 0 4096 127.0.0.1:5432 0.0.0.0:*
LISTEN 0 128 0.0.0.0:22 0.0.0.0:*
LISTEN 0 5 127.0.0.1:6080 0.0.0.0:* users:(("systemd",pid=1,fd=40))
LISTEN 0 511 127.0.0.1:3000 0.0.0.0:* users:(("next-server (v1",pid=77,fd=3))
#ps
   4242  259200 419840 node
     77    1500  98304 next-server (v1
`
	var b bytes.Buffer
	writeListening(&b, parseStatusProcs(out))
	want := `  listening  next-server (v1 :3000 up 25m 96.0 MB
             node :5173 up 3d 410.0 MB
             :5432
`
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

// End to end against the fake api and the local sshd harness (whose
// "guest" is this machine): a listener this test opens shows in `repose
// status`, with this process's name, age and memory.
func TestStatusShowsTheGuestsListeners(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port
	out := &discardWriter{}
	f.env.Out = out
	if err := StatusCmd(ctx, f.env, testSlug); err != nil {
		t.Fatal(err)
	}
	var line string
	for _, l := range strings.Split(out.buf.String(), "\n") {
		if strings.Contains(l, fmt.Sprintf(":%d up ", port)) {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("port %d not in status:\n%s", port, out.buf.String())
	}
	t.Logf("%s", line)
}

// The disk figure is the guest's root filesystem, and a nearly full one
// gets a line with the resize that grows it (I-567). The api's
// disk_used_bytes, the volume's allocated blocks, is not shown: kanali's
// read 39.5 of 40 GB with 33 GB in use.
func TestStatusDiskIsTheGuestsFilesystem(t *testing.T) {
	out := "LISTEN 0 511 0.0.0.0:5173 0.0.0.0:* users:((\"node\",pid=4242,fd=20))\n#ps\n   4242  259200 419840 node\n#df\n10243384 512169 4096\n#mux\n"
	gd := parseStatusDisk(out)
	if gd.Size != 10243384*4096 || gd.Used != (10243384-512169)*4096 || gd.Percent() != 95 {
		t.Fatalf("parseStatusDisk = %+v (%d percent)", gd, gd.Percent())
	}
	if procs := parseStatusProcs(out); len(procs) != 1 || procs[0].Comm != "node" || !procs[0].HasPID {
		t.Fatalf("the #df section broke the ps join: %+v", procs)
	}
	for _, bad := range []string{"", "#df\n#mux\n", "#df\nstat: cannot read\n#mux\n", "#df\n10 20 4096\n#mux\n", "#df\n0 0 4096\n"} {
		if gd := parseStatusDisk(bad); gd.Size != 0 {
			t.Errorf("parseStatusDisk(%q) = %+v, want unknown", bad, gd)
		}
	}

	p := &Project{Slug: "kanali", Class: "large", State: "running", VolumeBytes: 40 << 30, DiskUsedBytes: 39_500 << 20}
	route := &Route{HostName: "host-01", GuestIP: "10.64.0.8"}
	var b bytes.Buffer
	writeStatusLinesMux(&b, p, route, nil, nil, "tmux", gd)
	if !strings.Contains(b.String(), "disk 37.1 GB/39.1 GB") {
		t.Errorf("disk is not the guest's filesystem:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "\n  disk 95 percent full; `repose resize kanali 80G` grows it\n") {
		t.Errorf("no disk-full line:\n%s", b.String())
	}

	b.Reset()
	writeStatusLinesMux(&b, p, route, nil, nil, "tmux", guestDisk{Used: 30 << 30, Size: 39 << 30})
	if strings.Contains(b.String(), "percent full") {
		t.Errorf("77 percent called full:\n%s", b.String())
	}
	b.Reset()
	writeStatusLinesMux(&b, p, route, nil, nil, "tmux", guestDisk{})
	if !strings.Contains(b.String(), "disk 40.0 GB   ") || strings.Contains(b.String(), "38.6") {
		t.Errorf("a guest that did not answer shows the volume's size alone:\n%s", b.String())
	}

	big := &Project{Slug: "big", VolumeBytes: 320 << 30}
	if l := diskFullLine(big, guestDisk{Used: 99, Size: 100}); l != "disk 99 percent full" {
		t.Errorf("at the largest size: %q", l)
	}
	mid := &Project{Slug: "mid", VolumeBytes: 200 << 30}
	if l := diskFullLine(mid, guestDisk{Used: 91, Size: 100}); !strings.Contains(l, "`repose resize mid 320G`") {
		t.Errorf("past half the largest size: %q", l)
	}
}

// AGENTS names one agent, and counts several by state (I-567): kanali
// with five agents read `claude: working`.
func TestAgentStateCountsEveryAgent(t *testing.T) {
	p := &Project{State: "running", Signals: &Signals{Agents: []AgentSignal{{Agent: "claude", State: "working"}}}}
	if got := agentState(p); got != "claude: working" {
		t.Errorf("one agent: %q", got)
	}
	p.Signals.Agents = []AgentSignal{
		{Agent: "claude", State: "working"}, {Agent: "claude", State: "idle"}, {Agent: "codex", State: "needs_input"},
		{Agent: "claude", State: "working"}, {Agent: "pi", State: "unknown"},
	}
	if got := agentState(p); got != "5 agents: 1 needs_input, 2 working, 1 idle, 1 unknown" {
		t.Errorf("five agents: %q", got)
	}
	p.State = "stopped"
	if got := agentState(p); got != "" {
		t.Errorf("stopped: %q", got)
	}
}
