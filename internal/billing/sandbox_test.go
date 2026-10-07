package billing_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/billing"
)

// TestPaddleSandbox runs against Paddle's real sandbox when
// REPOSE_PADDLE_SANDBOX_KEY is set (docs/ops/M4-GATE.md §1): the bootstrap
// finds or creates the products, prices and (optionally, with
// REPOSE_PADDLE_SANDBOX_WEBHOOK_URL) the notification destination, then a
// customer and a checkout transaction are created for real. It skips
// without the key, so CI without a sandbox account is green; it refuses
// a live key.
func TestPaddleSandbox(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("REPOSE_PADDLE_SANDBOX_KEY"))
	if key == "" {
		t.Skip("REPOSE_PADDLE_SANDBOX_KEY not set")
	}
	if billing.Environment(key) != billing.EnvSandbox {
		t.Fatal("REPOSE_PADDLE_SANDBOX_KEY is not a sandbox key (pdl_sdbx_...); the test never runs against live")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cfg := billing.Config{APIKey: key, DashboardURL: "https://repose.herakraft.co"}
	p := billing.NewPaddle(cfg, quiet())
	res, err := billing.Bootstrap(ctx, p, billing.BootstrapOptions{WebhookURL: strings.TrimSpace(os.Getenv("REPOSE_PADDLE_SANDBOX_WEBHOOK_URL")), Progress: testWriter{t}})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	t.Logf("sandbox: price solo %s, price pro %s, overage product %s (created %d, found %d)", res.PriceSolo, res.PricePro, res.ProductOverage, len(res.Created), len(res.Found))
	// A rerun creates nothing.
	again, err := billing.Bootstrap(ctx, p, billing.BootstrapOptions{})
	if err != nil || len(again.Created) != 0 {
		t.Fatalf("rerun: created %v, %v", again.Created, err)
	}
	uid := uuid.Must(uuid.NewV7())
	email := "sandbox-" + uid.String()[:8] + "@repose.example"
	customer, err := p.CreateCustomer(ctx, email, uid)
	if err != nil {
		t.Fatalf("customer: %v", err)
	}
	found, err := p.FindCustomerByEmail(ctx, email)
	if err != nil || found != customer {
		t.Fatalf("find by email: %q %v", found, err)
	}
	// A first Solo checkout carries the introductory discount (I-497):
	// Paddle accepts it on a trialing price.
	txn, err := p.CreateCheckoutTransaction(ctx, customer, res.PriceSolo, res.DiscountIntro, uid)
	if err != nil || !strings.HasPrefix(txn, "txn_") {
		t.Fatalf("transaction: %q %v", txn, err)
	}
	t.Logf("sandbox: customer %s, checkout transaction %s with discount %s (open it with Paddle.js and the test card to finish the gate by hand; the subscription's discount should end three charges after the trial)", customer, txn, res.DiscountIntro)
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
