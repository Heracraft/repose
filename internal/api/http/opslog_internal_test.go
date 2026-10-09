package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/cli"
)

// DECISIONS I-609: the CLI decoded an ops line as {ts, kind, line} while
// this handler sends the op's state, duration and error, so every row in
// production read "<ts> start " with nothing after it. This holds the
// CLI's LogLine to the shape the handler writes.
func TestOpsLogLineDecodes(t *testing.T) {
	created := time.Date(2026, 10, 8, 21, 40, 0, 0, time.UTC)
	finished := created.Add(3100 * time.Millisecond)
	op := store.Op{ID: uuid.New(), Kind: "start", State: "error", CreatedAt: created, FinishedAt: &finished,
		Error: map[string]any{"code": "boot_timeout", "message": "the machine did not boot"}}
	b, err := json.Marshal(opsLogLine(op))
	if err != nil {
		t.Fatal(err)
	}
	var l cli.LogLine
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatal(err)
	}
	if !l.TS.Equal(created) || l.Kind != "start" || l.State != "error" || l.OpID != op.ID.String() {
		t.Fatalf("decoded %+v from %s", l, b)
	}
	if l.DurationMS == nil || *l.DurationMS != 3100 {
		t.Fatalf("duration_ms: %+v from %s", l.DurationMS, b)
	}
	if l.Error == nil || l.Error.Code != "boot_timeout" || l.Error.Message != "the machine did not boot" {
		t.Fatalf("error: %+v from %s", l.Error, b)
	}
	// A running op has no duration and no error.
	op.State, op.FinishedAt, op.Error = "running", nil, nil
	b, _ = json.Marshal(opsLogLine(op))
	l = cli.LogLine{}
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatal(err)
	}
	if l.DurationMS != nil || l.Error != nil || l.State != "running" {
		t.Fatalf("running op decoded %+v from %s", l, b)
	}
}
