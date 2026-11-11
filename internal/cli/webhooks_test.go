package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// `repose webhooks add` prints the URL and nothing else, so it pipes;
// the listing is the table of docs/features/webhooks.md.
func TestWebhooksRoundTrip(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out = out

	if err := WebhooksAddCmd(ctx, e, "", "stripe", "3000/api/webhooks/stripe", nil); err != nil {
		t.Fatal(err)
	}
	added := strings.TrimSpace(out.buf.String())
	if strings.Count(out.buf.String(), "\n") != 1 {
		t.Fatalf("add printed more than the URL: %q", out.buf.String())
	}
	if !strings.HasPrefix(added, "https://"+fakeapi.FakeWebhookHost+"/"+p.ID+"/stripe/") {
		t.Fatalf("add printed %q", added)
	}
	token := added[strings.LastIndexByte(added, '/')+1:]
	if len(token) != 22 {
		t.Fatalf("token %q is %d characters, want 22", token, len(token))
	}

	// url prints the same URL; add again keeps it and changes the target.
	out.buf.Reset()
	if err := WebhooksURLCmd(ctx, e, "", "stripe"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.buf.String()); got != added {
		t.Fatalf("url printed %q, want %q", got, added)
	}
	out.buf.Reset()
	yes := true
	if err := WebhooksAddCmd(ctx, e, "", "stripe", "4000/sms", &yes); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.buf.String()); got != added {
		t.Fatalf("adding the name again changed the URL: %q", got)
	}

	// rotate prints a new URL for the same name.
	out.buf.Reset()
	if err := WebhooksRotateCmd(ctx, e, "", "stripe"); err != nil {
		t.Fatal(err)
	}
	rotated := strings.TrimSpace(out.buf.String())
	if rotated == added || !strings.Contains(rotated, "/stripe/") {
		t.Fatalf("rotate printed %q", rotated)
	}

	// The listing: one header and one row per route, with the delivery.
	fake.AddDelivery(p.ID, "stripe", fakeapi.WebhookDelivery{At: time.Now().Add(-2 * time.Minute), Status: 200, Outcome: "delivered", DurationMs: 12, BytesIn: 900, BytesOut: 20})
	if err := WebhooksAddCmd(ctx, e, "", "github", "3000/api/webhooks/github", nil); err != nil {
		t.Fatal(err)
	}
	out.buf.Reset()
	if err := WebhooksListCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.buf.String(), "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "NAME") {
		t.Fatalf("list:\n%s", out.buf.String())
	}
	if !strings.Contains(lines[1], "stripe") || !strings.Contains(lines[1], ":4000/sms") ||
		!strings.Contains(lines[1], "on") || !strings.Contains(lines[1], "200") {
		t.Fatalf("the stripe row is %q", lines[1])
	}
	if !strings.Contains(lines[2], "github") || !strings.Contains(lines[2], "never") {
		t.Fatalf("the github row is %q", lines[2])
	}
	// A listing teaches no command (I-484): nothing under the table.
	if strings.Contains(out.buf.String(), "repose ") {
		t.Fatalf("the listing names a command:\n%s", out.buf.String())
	}

	// -q is the names alone; a removed route is gone from both.
	e.Quiet = true
	out.buf.Reset()
	if err := WebhooksListCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); got != "stripe\ngithub\n" {
		t.Fatalf("-q printed %q", got)
	}
	e.Quiet = false
	out.buf.Reset()
	if err := WebhooksRmCmd(ctx, e, "", []string{"stripe", "github"}); err != nil {
		t.Fatal(err)
	}
	if out.buf.String() != "" {
		t.Fatalf("rm printed %q, want nothing", out.buf.String())
	}
	out.buf.Reset()
	if err := WebhooksListCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); got != "todo-app has no webhook routes.\n" {
		t.Fatalf("the empty listing is %q", got)
	}
}

// A name or target the api would refuse is refused on the laptop, with
// exit 2 and what it received (I-346), before any round trip.
func TestWebhooksRefusesBadArgumentsLocally(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	ctx := context.Background()
	e.Out = &discardWriter{}
	for _, tc := range []struct{ name, target, want string }{
		{"Stripe", "3000", `got "Stripe"`},
		{"has space", "3000", `got "has space"`},
		{"ok", "0", `got "0"`},
		{"ok", "70000", `got "70000"`},
		{"ok", "nope", `got "nope"`},
		{"ok", "3000/a b", `got "3000/a b"`},
	} {
		err := WebhooksAddCmd(ctx, e, "", tc.name, tc.target, nil)
		if err == nil {
			t.Fatalf("add %q %q was accepted", tc.name, tc.target)
		}
		if code := exitCodeOf(err); code != ExitUsage {
			t.Fatalf("add %q %q: exit %d, want %d (%v)", tc.name, tc.target, code, ExitUsage, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("add %q %q: %v, want it to name what it got", tc.name, tc.target, err)
		}
	}
}

// A route the project does not have exits 1 and names the project and
// the route, as `secrets rm` does.
func TestWebhooksUnknownRouteExitsOne(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	e.Out = &discardWriter{}
	err := WebhooksURLCmd(context.Background(), e, "", "nosuch")
	if err == nil || exitCodeOf(err) != ExitGeneric {
		t.Fatalf("url of an unknown route: %v", err)
	}
	if !strings.Contains(err.Error(), "todo-app has no webhook route nosuch") {
		t.Fatalf("url of an unknown route: %v", err)
	}
}

// `repose status` grows one webhooks row, and only for routes a provider
// has called.
func TestStatusWebhooksRow(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out = out
	if err := WebhooksAddCmd(ctx, e, "", "stripe", "3000", nil); err != nil {
		t.Fatal(err)
	}
	out.buf.Reset()
	if err := StatusCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.buf.String(), "webhooks") {
		t.Fatalf("a route nobody has called added a row:\n%s", out.buf.String())
	}
	fake.AddDelivery(p.ID, "stripe", fakeapi.WebhookDelivery{At: time.Now().Add(-2 * time.Minute), Status: 200, Outcome: "delivered"})
	out.buf.Reset()
	if err := StatusCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.buf.String(), "webhooks   stripe 200 2m ago") {
		t.Fatalf("status has no webhooks row:\n%s", out.buf.String())
	}
}
