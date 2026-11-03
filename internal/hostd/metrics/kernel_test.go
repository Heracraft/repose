package metrics

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestKernelCollector is the wiring from hostinfo's decomposition to the
// scrape: the reasons the alert selects on have to appear with the values
// the read gave, and an unreadable verdict has to scrape as 1 rather than
// as nothing (DECISIONS I-1010).
func TestKernelCollector(t *testing.T) {
	m := New()
	c := newKernelCollector()
	c.readTainted = func() (map[string]bool, error) {
		return map[string]bool{"machine_check": true}, nil
	}
	c.readPanic = func() (bool, error) { return true, nil }
	replaceKernel(t, m, c)

	body := scrape(t, m)
	for _, want := range []string{
		`repose_host_kernel_tainted{reason="machine_check"} 1`,
		`repose_host_kernel_tainted{reason="die"} 0`,
		`repose_host_kernel_tainted{reason="bad_page"} 0`,
		`repose_host_kernel_tainted{reason="soft_lockup"} 0`,
		`repose_host_kernel_tainted{reason="warn"} 0`,
		"repose_host_panic_on_oops 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the scrape has no %s", want)
		}
	}

	// An unreadable taint word. hostinfo hands back every reason true with
	// the error, and the collector publishes that: five 1s, not five 0s and
	// not an absent series, because an absent series is green under
	// noDataState: OK.
	c.readTainted = func() (map[string]bool, error) {
		return map[string]bool{"die": true, "machine_check": true, "bad_page": true, "soft_lockup": true, "warn": true}, errors.New("no such file")
	}
	c.readPanic = func() (bool, error) { return false, errors.New("no such file") }
	body = scrape(t, m)
	for _, r := range []string{"die", "machine_check", "bad_page", "soft_lockup", "warn"} {
		if !strings.Contains(body, `repose_host_kernel_tainted{reason="`+r+`"} 1`) {
			t.Errorf("an unreadable taint word scraped %s as other than 1", r)
		}
	}
	if !strings.Contains(body, "repose_host_panic_on_oops 0") {
		t.Error("an unreadable sysctl did not scrape panic_on_oops as 0")
	}
}

// replaceKernel swaps the collector New registered for one with fixtures.
func replaceKernel(t *testing.T, m *M, c *kernelCollector) {
	t.Helper()
	if !m.obs.Unregister(newKernelCollector()) {
		t.Fatal("New did not register a kernel collector")
	}
	if err := m.obs.Register(c); err != nil {
		t.Fatalf("register the fixture collector: %v", err)
	}
}

func scrape(t *testing.T, m *M) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("scrape returned %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}
