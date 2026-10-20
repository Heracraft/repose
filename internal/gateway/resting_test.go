package gateway

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDialWakesAFrozenMachine is the gateway half of the idle pause
// (DECISIONS I-818): a route that says `resting` is woken before the TCP
// dial, inside the relay and not inside the handshake, so every client
// that reaches a guest over SSH -- plain ssh, scp, VS Code Remote-SSH,
// every repose command and the fast attach -- is covered by one wake.
func TestDialWakesAFrozenMachine(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	since := time.Now().Add(-42 * time.Minute)
	if err := h.api.SetResting(h.project.ID, &since); err != nil {
		t.Fatal(err)
	}
	c, banner, err := h.dial(h.login, cert)
	if err != nil {
		t.Fatalf("dial of a frozen machine: %v (banner %q)", err, banner)
	}
	// The relay works: the wake happened before the dial and the dial got
	// its own full budget after it.
	out, _, status := run(t, c, "echo awake")
	if out != "awake\n" || status != 0 {
		t.Fatalf("echo: %q %d", out, status)
	}
	_ = c.Close()
	if n := h.api.Wakes(); n != 1 {
		t.Fatalf("%d wakes, want 1", n)
	}
	if !strings.Contains(h.logs.String(), `"event":"guest_wake"`) || !strings.Contains(h.logs.String(), `"was_resting":true`) {
		t.Fatalf("no guest_wake log line:\n%s", h.logs.String())
	}
	if !strings.Contains(h.logs.String(), `"event":"session_open"`) || !strings.Contains(h.logs.String(), `"resting":true`) {
		t.Fatalf("session_open does not say the machine was frozen:\n%s", h.logs.String())
	}

	// A machine that is awake is not woken: the successful wake dropped
	// the cached route, so the next connection reads the real answer
	// rather than the five-second-old flag.
	c2, _, err := h.dial(h.login, cert)
	if err != nil {
		t.Fatal(err)
	}
	if out, _, _ := run(t, c2, "echo again"); out != "again\n" {
		t.Fatalf("second relay: %q", out)
	}
	_ = c2.Close()
	if n := h.api.Wakes(); n != 1 {
		t.Fatalf("%d wakes after a second connection to an awake machine, want 1", n)
	}
}

// TestDialOfAMachineThatDoesNotWake: a failed wake is a failure and says
// so. Not "not accepting connections yet", which would be false of a
// machine whose processes are all still there (DECISIONS I-818).
func TestDialOfAMachineThatDoesNotWake(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	since := time.Now().Add(-10 * time.Minute)
	if err := h.api.SetResting(h.project.ID, &since); err != nil {
		t.Fatal(err)
	}
	h.api.SetWakeFails(true)
	// Authentication still succeeds: nothing refuses on the flag.
	c, banner, err := h.dial(h.login, cert)
	if err != nil {
		t.Fatalf("auth must succeed before the dial: %v (banner %q)", err, banner)
	}
	out, errb, status := run(t, c, "true")
	if !strings.Contains(errb, MsgWakeFailed) || status != 255 {
		t.Fatalf("stderr %q status %d, want %q and 255", errb, status, MsgWakeFailed)
	}
	if out != "" {
		t.Fatalf("the guest answered %q on a failed wake", out)
	}
	_ = c.Close()
	if strings.Contains(errb, MsgNotReady) {
		t.Fatal("a frozen machine that did not wake was called not ready")
	}
	if !strings.Contains(h.logs.String(), `"reason":"wake_failed"`) {
		t.Fatalf("no dial_fail wake_failed log line:\n%s", h.logs.String())
	}
	if v := counterValue(t, h.metrics.DialFailTotal); v != 1 {
		t.Fatalf("dial_fail_total %v, want 1", v)
	}
}

// TestWakeGroupAsksOnce: authenticate runs once per key a client offers
// and an editor opens several connections at once, so one window must be
// one wake (DECISIONS I-818).
func TestWakeGroupAsksOnce(t *testing.T) {
	wg := newWakeGroup()
	release := make(chan struct{})
	var calls int
	var mu sync.Mutex
	var started sync.WaitGroup
	var done sync.WaitGroup
	for i := 0; i < 5; i++ {
		started.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			started.Done()
			_ = wg.Do(context.Background(), "p1", func() error {
				mu.Lock()
				calls++
				mu.Unlock()
				<-release
				return nil
			})
		}()
	}
	started.Wait()
	// Every caller is either in the one call or waiting on it.
	time.Sleep(50 * time.Millisecond)
	close(release)
	done.Wait()
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 1 {
		t.Fatalf("%d wakes for five connections to one project, want 1", n)
	}
	// The call is finished, so the next one asks again.
	if err := wg.Do(context.Background(), "p1", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}
