// The fake api's trigger shapes against the real ones (DECISIONS
// I-1248). `internal/fakes/api` is a second, hand-written copy of what
// these two functions emit, and the dashboard's tests — including the
// screenshots a visual review is judged on — see only the fake. A key
// the real api sends and the fake does not is a tab that looks right in
// every test and is missing a column in front of a user.
package httpapi

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/triggers"
	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// tagKeys is the JSON keys a struct can send.
func tagKeys(v any) map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(v)
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		out[strings.Split(tag, ",")[0]] = true
	}
	return out
}

func missing(real map[string]any, fake map[string]bool, allowed ...string) []string {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	var out []string
	for k := range real {
		if !fake[k] && !ok[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func TestTheFakeApiSendsEveryRunKeyTheRealOneDoes(t *testing.T) {
	now := time.Now().UTC()
	id := uuid.Must(uuid.NewV7())
	run := triggers.Run{
		ID: id, ProjectID: id, TriggerID: &id, TriggerName: "apply",
		Source: triggers.SourceRoute, State: triggers.StateDone, Reason: "gated",
		Summary: "did it", LinkURL: "https://x", LinkLabel: "PR", Window: "t-apply",
		DedupeKey: "d1", PayloadBytes: 10, Skips: 4, StartedMachine: true,
		StartedAt: &now, FinishedAt: &now, CreatedAt: now, ProjectSlug: "todo-app",
	}
	real := runJSON(&run)
	fake := tagKeys(fakeapi.FakeTriggerRun{})
	// `project` is the cross-project listing's own key (GET
	// /trigger-runs), which the fake has no route for; `link` is a
	// nested object the fake does not model. Both are checked rather
	// than assumed, so this test is known to be comparing something and
	// the allowance is dropped when the fake grows the key.
	for _, a := range []string{"project", "link"} {
		if _, ok := real[a]; !ok {
			t.Errorf("%s is allowed for but the real api no longer sends it", a)
		}
		if fake[a] {
			t.Errorf("the fake sends %s now: take it out of the allowed list", a)
		}
	}
	if got := missing(real, fake, "project", "link"); len(got) > 0 {
		t.Errorf("the fake api cannot send %v, which the real api does", got)
	}
}

func TestTheFakeApiSendsEveryTriggerKeyTheRealOneDoes(t *testing.T) {
	now := time.Now().UTC()
	id := uuid.Must(uuid.NewV7())
	trig := triggers.Trigger{
		ID: id, Name: "apply", Kind: triggers.KindRoute, RouteName: "recruiting",
		Spec: "daily 03:00", Zone: "UTC", NextAt: &now, PromptPath: "p.md",
		Workdir: "checkout", Agent: "claude", TimeoutS: 3600, Concurrency: 1,
		Gate: "./v.sh", Dedupe: "header:X", Notify: "failed", Enabled: true,
		AllowStart: true, StopAfter: true, CatchUp: true, CreatedAt: now, UpdatedAt: now,
	}
	if got := missing(triggerJSON(&trig, "https://hooks/x/tok"), tagKeys(fakeapi.FakeTrigger{})); len(got) > 0 {
		t.Errorf("the fake api cannot send %v, which the real api does", got)
	}
}
