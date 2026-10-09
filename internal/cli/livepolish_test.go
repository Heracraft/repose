package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/ca/testca"
	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// The conductor's live session of 2026-09-23 04:03-04:10Z (DECISIONS
// I-186..I-192): each test names the transcript line it pins.

// "rate_limited: too many requests" at the end of `repose restore`: a
// refused request waits out Retry-After and is sent again (I-187).
func TestRateLimitedRequestIsRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"too many requests"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"p1","slug":"izma","state":"running"}`))
	}))
	defer srv.Close()
	c := newClient(srv.URL, staticToken("tok"))
	start := time.Now()
	p, err := c.GetProject(context.Background(), "p1")
	if err != nil || p.Slug != "izma" {
		t.Fatalf("GetProject after two 429s: %+v %v", p, err)
	}
	if el := time.Since(start); el < 2*time.Second || calls.Load() != 3 {
		t.Fatalf("waited %s over %d calls; want Retry-After honoured twice", el, calls.Load())
	}
}

// A limit that outlasts the budget reaches the user as a sentence, never
// as "rate_limited: too many requests".
func TestRateLimitedNeverShowsTheRawCode(t *testing.T) {
	old := rateLimitBudget
	rateLimitBudget = 1500 * time.Millisecond
	defer func() { rateLimitBudget = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"too many requests"}}`))
	}))
	defer srv.Close()
	_, err := newClient(srv.URL, staticToken("tok")).GetProject(context.Background(), "p1")
	var out bytes.Buffer
	if code := exitCodeFor(err, &out); code != ExitGeneric || strings.Contains(out.String(), "rate_limited") || !strings.Contains(out.String(), "too many in the last minute") {
		t.Fatalf("exit %d, %q", code, out.String())
	}
	t.Logf("%s", out.String())
}

func TestPollDelayBacksOff(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		ago  time.Duration
		want time.Duration
	}{{0, 500 * time.Millisecond}, {9 * time.Second, 500 * time.Millisecond}, {25 * time.Second, 500 * time.Millisecond}, {40 * time.Second, time.Second}, {2 * time.Minute, 2 * time.Second}} {
		if got := pollDelay(now.Add(-c.ago)); got != c.want {
			t.Fatalf("after %s: %s, want %s", c.ago, got, c.want)
		}
	}
}

// "certificate not valid for this project" after `repose restore`: the
// restore made a new project id, and the certificate and config on disk
// only knew the old one. Restore now ends with both renewed (I-188).
func TestRestoreRenewsTheCertificate(t *testing.T) {
	home := withHome(t)
	ca, err := testca.New()
	if err != nil {
		t.Fatal(err)
	}
	fake := fakeapi.New(fakeapi.Options{CA: ca})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	e.TargetFor = hostTarget
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "e2e-a-private", Class: "small"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ensureCert(ctx, e.Client, certParams{Handle: fakeapi.CannedUser.Handle, Projects: []Project{*p}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := DestroyCmd(ctx, e, p.ID, true, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := RestoreCmd(ctx, e, "e2e-a-private", "", "", nil); err != nil {
		t.Fatal(err)
	}
	back, err := findByIDOrSlug(ctx, e.Client, "e2e-a-private")
	if err != nil || back == nil || back.ID == p.ID {
		t.Fatalf("restored project: %+v %v", back, err)
	}
	cert := parseCertFile(filepath.Join(home, ".ssh", "repose", "id_ed25519-cert.pub"))
	if cert == nil || !certUsableFor(cert, []string{back.ID}, time.Now(), 0) {
		t.Fatalf("the certificate does not cover the restored project %s: %+v", back.ID, cert)
	}
	cfg, _ := os.ReadFile(filepath.Join(home, ".ssh", "repose", sshHostsName))
	if !strings.Contains(string(cfg), "Host e2e-a-private.repose") {
		t.Fatalf("config has no Host block for the restored project:\n%s", cfg)
	}
}

// "has no snapshot left to restore" for a project still being destroyed:
// the CLI waits for the destroy's final snapshot and restores it (I-190).
func TestRestoreWaitsForADestroyInProgress(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	e.TargetFor = hostTarget
	var out, errOut strings.Builder
	e.Out, e.ErrOut = &out, &errOut
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "izma", Class: "small"})
	if err != nil {
		t.Fatal(err)
	}
	fake.SetState(p.ID, "destroying")
	go func() {
		time.Sleep(1500 * time.Millisecond)
		fake.SetState(p.ID, "running")
		_, _ = e.Client.DestroyProject(ctx, p.ID) // the fake destroys at once, final snapshot included
	}()
	if err := RestoreCmd(ctx, e, "izma", "", "", nil); err != nil {
		t.Fatalf("restore: %v (stderr %q)", err, errOut.String())
	}
	if !strings.Contains(errOut.String(), "Waiting for izma's destroy to take its final snapshot") || !strings.HasPrefix(out.String(), "Restored izma from its 1.0 GB snapshot of ") {
		t.Fatalf("stdout %q stderr %q", out.String(), errOut.String())
	}
}

// `repose ls --destroyed` listed izma three times with no way to
// tell which one `repose restore izma` takes (I-192).
func TestDestroyedListIsOneRowPerName(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 9, 23, h, 0, 0, 0, time.Local) }
	row := func(id, slug string, h int, free bool) DestroyedProject {
		until := at(h).AddDate(0, 0, 30)
		return DestroyedProject{ID: id, Slug: slug, Class: "large", DestroyedAt: at(h), NameFree: free, RestorableUntil: &until,
			Snapshot: Snapshot{ID: "s-" + id, CreatedAt: at(h), Bytes: 2 << 20}}
	}
	list := []DestroyedProject{row("e1", "e2e-a-private", 4, false), row("i3", "izma", 3, true), row("i2", "izma", 2, true), row("i1", "izma", 1, true)}
	var b strings.Builder
	writeDestroyedTable(&b, list)
	got := b.String()
	if strings.Count(got, "\nizma ") != 1 || !strings.Contains(got, "EARLIER") || strings.Contains(got, "--destroyed --all") ||
		!strings.Contains(got, "`repose restore e1 --as NEW-NAME`") {
		t.Fatalf("default table:\n%s", got)
	}
	lines := strings.Split(got, "\n")
	for _, l := range lines {
		if strings.HasPrefix(l, "izma ") && !strings.HasSuffix(strings.TrimSpace(l), " 2") {
			t.Fatalf("izma row does not count its 2 earlier copies: %q", l)
		}
	}
	b.Reset()
	writeDestroyedTableAll(&b, list)
	all := b.String()
	if !strings.Contains(all, "izma  ") || strings.Count(all, "(earlier)") != 2 || !strings.Contains(all, "i1") || strings.Index(all, "i3") > strings.Index(all, "i1") {
		t.Fatalf("--all table:\n%s", all)
	}
	t.Logf("\n%s\n%s", got, all)
}

// Non-TTY progress printed "Creating e2e-a-private..." twice: once from
// `run` and once from the project's creating state (I-191).
func TestProgressPrintsAPhaseOnce(t *testing.T) {
	var b strings.Builder
	pr := newProgress(&b, false)
	pr.Phase("Creating teksafari-org", "Created teksafari-org (large)")
	pr.Phase("Creating teksafari-org", "Created teksafari-org")
	pr.Phase("Booting teksafari-org", "Booted teksafari-org")
	pr.End()
	if got := b.String(); got != "Creating teksafari-org...\nBooting teksafari-org...\n" {
		t.Fatalf("%q", got)
	}
	var tty strings.Builder
	pr = newProgress(&tty, true)
	pr.Phase("Creating teksafari-org", "Created teksafari-org (large)")
	pr.Phase("Creating teksafari-org", "Created teksafari-org")
	pr.End()
	if strings.Count(tty.String(), "✓") != 1 || !strings.Contains(tty.String(), "✓ Created teksafari-org (large)") {
		t.Fatalf("%q", tty.String())
	}
}

// `repose status` showed the host's uuid and, for a running project, its
// first event ("guest_state_changed \"creating\"") as the last (I-192).
func TestStatusShowsHostNameAndNewestEvent(t *testing.T) {
	now := time.Now()
	started := now.Add(-time.Hour)
	p := &Project{Slug: "izma", Class: "large", State: "running", StartedAt: &started}
	route := &Route{HostID: "01a0bfd8-120c-7ec8-b0a0-ed83a33edd41", HostName: "host-01", GuestIP: "10.64.0.2"}
	events := []Event{ // the api's order: newest first
		{TS: now.Add(-2 * time.Minute), Kind: "guest_state_changed", Summary: "running"},
		{TS: now.Add(-3 * time.Minute), Kind: "guest_state_changed", Summary: "creating"},
	}
	var b strings.Builder
	writeStatusLines(&b, p, route, nil, events)
	got := b.String()
	if !strings.Contains(got, "\n  host       host-01, ip 10.64.0.2\n") || strings.Contains(got, route.HostID) || !strings.Contains(got, `"running"`) || strings.Contains(got, `"creating"`) {
		t.Fatalf("%s", got)
	}
	// Without -v status reads no route, and prints no host (I-616).
	b.Reset()
	writeStatusLines(&b, p, nil, nil, events)
	if strings.Contains(b.String(), "host") || strings.Contains(b.String(), "10.64") {
		t.Fatalf("host without -v:\n%s", b.String())
	}
}
