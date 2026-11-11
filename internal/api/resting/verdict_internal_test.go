package resting

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/idle"
)

// TestTheAgentTermIsIdleUsedSQLsOwn: the verdict blames `agent_working`
// with its own copy of the agent term of idle.UsedSQL, because the rule
// reads that term only as part of "this minute counted as used" and the
// verdict has to name it on its own. The copy must stay the same text:
// two spellings of "this agent is at work" would let a verdict blame a
// term the rule did not read, and the whole point of the row is that it
// says what the rule saw.
func TestTheAgentTermIsIdleUsedSQLsOwn(t *testing.T) {
	if !strings.Contains(idle.UsedSQL, agentWorkingSQL) {
		t.Fatalf("the verdict's agent term is no longer a substring of idle.UsedSQL.\nverdict:\n%s\n\nrule:\n%s",
			agentWorkingSQL, idle.UsedSQL)
	}
}

// TestTheQuietTermIsTheRulesOwn: the verdict's sample term is quietSQL
// itself, which is what makes a verdict a second read of the rule rather
// than a second rule.
func TestTheQuietTermIsTheRulesOwn(t *testing.T) {
	if !strings.Contains(verdictSQL, quietSQL) {
		t.Fatal("verdictSQL no longer contains quietSQL; the verdict and the rule can now disagree about a quiet minute")
	}
	if !strings.Contains(candidatesSQL, quietSQL) {
		t.Fatal("candidatesSQL no longer contains quietSQL")
	}
}

// TestAHostsRefusalWordIsBounded: why_not is a lower_snake enum in the
// contract and a host is still a thing that sends us bytes. An
// unbounded string would land in a text column and become a group-by
// key of the launch query.
func TestAHostsRefusalWordIsBounded(t *testing.T) {
	s := &Service{refusals: map[uuid.UUID]refusal{}, months: map[string]bool{}}
	id := uuid.New()
	now := time.Now()
	for _, c := range []struct{ in, want string }{
		{"timer_due", "timer_due"},
		{strings.Repeat("x", maxWhyNot+1), "other"},
		{"Timer Due; drop table", "other"},
		{"timer-due", "other"},
	} {
		s.noteRefusal(id, c.in, now)
		if got := s.whyNot(id, now); got != c.want {
			t.Errorf("noteRefusal(%q) kept %q, want %q", c.in, got, c.want)
		}
	}
	// And it ages out, so a refusal from an hour ago does not explain a
	// machine that is awake now.
	s.noteRefusal(id, "timer_due", now)
	if got := s.whyNot(id, now.Add(WhyNotFor+time.Second)); got != "" {
		t.Errorf("a stale refusal still read %q", got)
	}
}
