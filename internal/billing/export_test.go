package billing

import "context"

// PatchSubscriptionForTest sends a raw subscription update; the sandbox
// test ends a trial with it.
func PatchSubscriptionForTest(ctx context.Context, p *Polar, id string, body map[string]any) error {
	_, err := p.patchSubscription(ctx, id, body)
	return err
}
