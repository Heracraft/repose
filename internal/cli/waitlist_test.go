package cli

import (
	"context"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// DECISIONS I-290, I-294: the waitlist gates checkout, not the first
// project. A user without a plan who is on the list gets 402
// `payment_required` `subscription_required` from POST /projects, with the
// api's sentence naming the place and the address the email goes to,
// printed as it is, exit 7. A `waitlisted` error (checkout, or an older api
// that still gated the first project) prints the same sentence and exits 8
// like capacity.
func TestRunWaitlistedPrintsPlaceAndExits8(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	fake.SetWaitlisted(3)
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	_, err := createProjectForRun(ctx, e, "", RunOptions{Name: "todo-app"}, nil)
	if err == nil {
		t.Fatal("create went through while waitlisted without a plan")
	}
	want := "repose is full right now. You're number 3 on the waitlist; we'll email " + fakeapi.CannedUser.Email + " when there's a seat.\n"
	var out strings.Builder
	if code := exitCodeFor(err, &out); code != ExitPaymentRequired {
		t.Fatalf("exit %d, want %d (%s)", code, ExitPaymentRequired, out.String())
	}
	if out.String() != want {
		t.Fatalf("printed %q, want %q", out.String(), want)
	}

	// The checkout's refusal, or an older api's first-project gate: the
	// same sentence, exit 8.
	out.Reset()
	wl := &APIError{Code: "waitlisted", Message: strings.TrimSuffix(want, "\n"), Detail: map[string]any{"position": float64(3), "email": fakeapi.CannedUser.Email}}
	if code := exitCodeFor(wl, &out); code != ExitCapacity {
		t.Fatalf("waitlisted exit %d, want %d (%s)", code, ExitCapacity, out.String())
	}
	if out.String() != want {
		t.Fatalf("waitlisted printed %q, want %q", out.String(), want)
	}

	// A seat: the account is exempt again and the create goes through.
	fake.SetWaitlisted(0)
	if _, err := createProjectForRun(ctx, e, "", RunOptions{Name: "todo-app"}, nil); err != nil {
		t.Fatalf("after admission: %v", err)
	}
}

func TestWaitlistedMessageFallsBack(t *testing.T) {
	// The api's own sentence wins, whatever the detail says.
	e := &APIError{Code: "waitlisted", Message: "repose is full right now.", Detail: map[string]any{"position": float64(9)}}
	if got := waitlistedMessage(e); got != "repose is full right now." {
		t.Fatalf("got %q", got)
	}
	// No message (a proxy that stripped it): built from the detail.
	e = &APIError{Code: "waitlisted", Detail: map[string]any{"position": float64(2)}}
	if got := waitlistedMessage(e); got != "repose is full right now. You're number 2 on the waitlist; https://repose.herakraft.co/billing shows your place." {
		t.Fatalf("got %q", got)
	}
	e = &APIError{Code: "waitlisted"}
	if got := waitlistedMessage(e); got != "repose is full right now. You're on the waitlist; we'll email you when there's a seat." {
		t.Fatalf("got %q", got)
	}
}
