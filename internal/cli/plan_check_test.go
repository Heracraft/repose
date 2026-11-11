package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// soloEnv is a lifecycle env on an account with billing on and an active
// Solo plan (8 GB of memory for running machines).
func soloEnv(t *testing.T, plan string) (*fakeapi.Fake, *Env, *bytes.Buffer) {
	t.Helper()
	fake := fakeapi.New(fakeapi.Options{Billing: true})
	t.Cleanup(fake.Close)
	if plan != "" {
		if err := fake.SetBillingState(fakeapi.BillingState{Plan: &plan}); err != nil {
			t.Fatal(err)
		}
	}
	e := newLifecycleEnv(t, fake)
	out := &bytes.Buffer{}
	e.Out = out
	return fake, e, out
}

// `repose resize --size` asks the plan before the prompt and before the
// stop, so a size the plan cannot run ends no agent (I-610, review 5.1).
func TestResizeClassChecksThePlanFirst(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, plan, from, to string
		others               []string // small machines running beside it
		want                 string   // "" when the change goes ahead
	}{
		{name: "xl on Solo", from: "large", to: "xl",
			want: "Your Solo plan runs 8 GB at once and an xl machine needs 16 GB. Upgrade to Plus at https://repose.run/billing."},
		{name: "large beside one small on Solo", from: "small", to: "large", others: []string{"web"},
			want: "Your Solo plan runs 8 GB at once and web is using it. `repose stop web` frees it, or upgrade at https://repose.run/billing."},
		{name: "xl on Plus with nothing else running", plan: "plus", from: "large", to: "xl"},
		{name: "smaller is never refused", from: "large", to: "small"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, e, _ := soloEnv(t, c.plan)
			for _, o := range c.others {
				if _, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: o, Class: "small"}); err != nil {
					t.Fatal(err)
				}
			}
			p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "todo-app", Class: c.from})
			if err != nil {
				t.Fatal(err)
			}
			asked := false
			err = ResizeClassCmd(ctx, e, p.ID, c.to, func(string) (bool, error) { asked = true; return false, nil })
			got, _ := e.Client.GetProject(ctx, p.ID)
			if c.want == "" {
				// The question was asked; the test's no exits 1 (I-614).
				if !asked || (err != nil && exitCodeOf(err) != ExitGeneric) {
					t.Fatalf("err %v, asked %v", err, asked)
				}
				return
			}
			if exitCodeOf(err) != ExitPaymentRequired || err.Error() != c.want {
				t.Fatalf("err %v (exit %d), want %q", err, exitCodeOf(err), c.want)
			}
			if asked || got.State != "running" || got.Class != c.from {
				t.Fatalf("asked %v, after: %s %s", asked, got.State, got.Class)
			}
		})
	}
}

// An exempt account, or one whose /me says no plan, is the api's to judge.
func TestPlanMemoryRefusalLeavesUnknownsToTheAPI(t *testing.T) {
	ctx := context.Background()
	fake := fakeapi.New(fakeapi.Options{}) // billing off: an exempt account
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	if msg := planMemoryRefusal(ctx, e, planAsk{Class: "xl", Count: 10}); msg != "" {
		t.Fatalf("exempt account refused: %q", msg)
	}
}

// `repose run --size` on a project that exists: a stopped one starts at
// that size, a running one is refused with the command that changes it
// (I-611, review B6).
func TestRunSizeOnAnExistingProject(t *testing.T) {
	ctx := context.Background()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	e, _, _ := freshEnv(f.env, f.local)
	if err := runRun(ctx, e, RunOptions{Name: testSlug, Size: "small", NoSync: true, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	p := bySlug(listed(t, e), testSlug)
	if p == nil || p.Class != "small" {
		t.Fatalf("created %+v", p)
	}
	err := runRun(ctx, e, RunOptions{Name: testSlug, Size: "large", NoSync: true, NoAttach: true}, false)
	want := testSlug + " is small and running; --size sizes a machine this command creates or starts. `repose resize " + testSlug + " --size large` changes it, which restarts it."
	if exitCodeOf(err) != ExitUsage || err.Error() != want {
		t.Fatalf("err %v, want %q", err, want)
	}
	if got := bySlug(listed(t, e), testSlug); got.Class != "small" || got.State != "running" {
		t.Fatalf("after the refusal: %s %s", got.Class, got.State)
	}
	if _, err := e.Client.StopProject(ctx, p.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := runRun(ctx, e, RunOptions{Name: testSlug, Size: "large", NoSync: true, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if got := bySlug(listed(t, e), testSlug); got.Class != "large" || got.State != "running" {
		t.Fatalf("a stopped project with --size large: %s %s", got.Class, got.State)
	}
	if err := runRun(ctx, e, RunOptions{Name: testSlug, Size: "huge", NoSync: true, NoAttach: true}, false); exitCodeOf(err) != ExitUsage {
		t.Fatalf("--size huge: %v", err)
	}
}

// A disk size needs a unit, and the CLI compares it with the disk before
// it asks the api (I-613, review D6).
func TestParseSizeNeedsAUnit(t *testing.T) {
	for in, want := range map[string]int64{"80G": 80 << 30, "80g": 80 << 30, "80GB": 80 << 30, "80GiB": 80 << 30, "1T": 1 << 40, "512M": 512 << 20} {
		if got, err := parseSize(in); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v", in, got, err)
		}
	}
	for in, want := range map[string]string{
		"80":        `"80" needs a unit, like 80G`,
		"big":       `"big" is not a size like 80G`,
		"0G":        `"0G" is not a size like 80G`,
		"-5G":       `"-5G" is not a size like 80G`,
		"99999999T": `"99999999T" is not a size like 80G`,
	} {
		if _, err := parseSize(in); err == nil || err.Error() != want {
			t.Errorf("parseSize(%q): %v, want %q", in, err, want)
		}
	}
	// One bare number is DISK without its unit, not a project called 100.
	if _, _, err := parseResizeArgs([]string{"100"}, &globalFlags{}); !isUsage(err) || !strings.Contains(err.Error(), `"100" needs a unit, like 100G`) {
		t.Fatalf("resize 100: %v", err)
	}
	if got := diskSize(80 << 30); got != "80G" {
		t.Fatalf("diskSize = %q", got)
	}
}

func TestResizeDiskComparesFirst(t *testing.T) {
	ctx := context.Background()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	out := &bytes.Buffer{}
	e.Out = out
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "izma", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	cur := diskSize(p.VolumeBytes)
	if err := ResizeCmd(ctx, e, "izma", p.VolumeBytes); err != nil || out.String() != "izma's disk is already "+cur+".\n" {
		t.Fatalf("same size: %v %q", err, out.String())
	}
	err = ResizeCmd(ctx, e, "izma", p.VolumeBytes-1<<30)
	if exitCodeOf(err) != ExitUsage || err.Error() != "izma's disk is "+cur+" and can only grow." {
		t.Fatalf("smaller: %v", err)
	}
	out.Reset()
	if err := ResizeCmd(ctx, e, "izma", p.VolumeBytes+10<<30); err != nil || out.String() != "Resized izma's disk to "+diskSize(p.VolumeBytes+10<<30)+".\n" {
		t.Fatalf("grow: %v %q", err, out.String())
	}
}

// `repose keep PROJECT 3h` moves a temporary machine's expiry; a bare
// `keep` still makes it normal (I-612, review A2).
func TestKeepForADuration(t *testing.T) {
	ctx := context.Background()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	out := &bytes.Buffer{}
	e.Out = out
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "tmp-abcd", Class: "large", ExpiresIn: 600})
	if err != nil {
		t.Fatal(err)
	}
	if err := KeepCmd(ctx, e, "tmp-abcd", 3*time.Hour); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Client.GetProject(ctx, p.ID)
	if got.ExpiresAt == nil || !got.ExpiresAt.After(p.ExpiresAt.Add(2*time.Hour)) {
		t.Fatalf("expiry %v, was %v", got.ExpiresAt, p.ExpiresAt)
	}
	if want := "tmp-abcd is temporary until " + got.ExpiresAt.Local().Format("Jan 2 15:04") + ".\n"; out.String() != want {
		t.Fatalf("stdout %q, want %q", out.String(), want)
	}
	for _, s := range []string{"5m", "25h", "soon"} {
		if _, err := parseKeepDuration(s); err == nil {
			t.Errorf("keep %s accepted", s)
		}
	}
	// An api before I-612 answers with the expiry it had (I-631).
	old := *p.ExpiresAt
	now := time.Now()
	if extensionTaken(&old, &Project{ExpiresAt: &old}, now.Add(3*time.Hour)) {
		t.Error("an unchanged expiry counted as taken")
	}
	if !extensionTaken(&old, &Project{ExpiresAt: &old}, old.Add(20*time.Second)) {
		t.Error("asking for the expiry it has did not count as taken")
	}
	later := old.Add(time.Hour)
	if !extensionTaken(&old, &Project{ExpiresAt: &later}, now.Add(3*time.Hour)) || extensionTaken(&old, nil, now) {
		t.Error("extensionTaken")
	}
	normal, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "normal", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := e.Client.patch(ctx, "/projects/"+normal.ID, map[string]any{"expires_in_s": 3600}, &raw); err == nil {
		t.Fatal("expires_in_s on a normal project accepted")
	}
}

func TestParseTempWork(t *testing.T) {
	for _, c := range []struct {
		out  string
		want machineWork
		err  bool
	}{
		{out: "#files 0\n#commits 0\n"},
		{out: "noise\n#files 2\n#commits 3\n", want: machineWork{Commits: 3, Files: 2}},
		{out: "#files 1\n", err: true},
		{out: "", err: true},
	} {
		got, err := parseTempWork(c.out)
		if (err != nil) != c.err || (!c.err && got != c.want) {
			t.Errorf("%q: %+v %v", c.out, got, err)
		}
	}
	if s := (machineWork{Commits: 1, Files: 2}).String(); s != "1 commit and 2 changed files" {
		t.Fatalf("%q", s)
	}
}

func TestTempStays(t *testing.T) {
	now := time.Date(2026, 10, 8, 10, 0, 0, 0, time.Local)
	at := func(d time.Duration) *Project { x := now.Add(d); return &Project{ExpiresAt: &x} }
	for _, c := range []struct {
		p    *Project
		want string
	}{
		{at(2 * time.Hour), "it stays until 12:00"},
		{at(20 * time.Hour), "it stays until Oct 9 06:00"},
		{at(-time.Minute), "it is destroyed once nobody is attached"},
	} {
		if got := tempStays(c.p, now); got != c.want {
			t.Errorf("%v: %q, want %q", c.p.ExpiresAt, got, c.want)
		}
	}
}
