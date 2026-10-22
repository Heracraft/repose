package resting

import "context"

// SendPauseForTest sends one pause the way the tick does, so an external
// test can drive the result path without going through the rule.
func (s *Service) SendPauseForTest(ctx context.Context, c Candidate) error {
	return s.sendPause(ctx, c, s.Now())
}
