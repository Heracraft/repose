package admin

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/billing"
)

// DefaultWebhookURL is the api's public webhook route (docs/ops/coolify.md).
const DefaultWebhookURL = "https://api.repose.herakraft.co/v1/billing/webhook"

// PolarBaseURL overrides the Polar API origin (tests point it at the fake).
var PolarBaseURL string

// polar builds the Polar client from the environment, or returns
// ErrDisabled so a command can say billing is not configured.
func (e *Env) polar() (*billing.Polar, billing.Config, error) {
	cfg, on := billing.ConfigFromEnv()
	if !on {
		return nil, cfg, billing.ErrDisabled
	}
	cfg.BaseURL = PolarBaseURL
	return billing.NewPolar(cfg, e.logger()), cfg, nil
}

// billingPolarBootstrap creates or finds every Polar object the api
// needs and prints the POLAR_* block for its environment (DECISIONS
// I-289, I-604). It needs no database: the token is read from
// POLAR_ACCESS_TOKEN, never from the command line, where it would show in
// `ps` and shell history. Progress goes to stderr and the block alone to
// stdout, so `... > polar.env` captures exactly what is pasted.
func (e *Env) billingPolarBootstrap(ctx context.Context, args []string) error {
	fs, err := flagsFor("polar-bootstrap", args, func(fs *flag.FlagSet) {
		fs.String("webhook-url", "", "the api's public webhook route (default "+DefaultWebhookURL+", or $API_PUBLIC_URL/v1/billing/webhook)")
		fs.Bool("no-webhook", false, "do not create the webhook endpoint")
		fs.Bool("production", false, "allow Polar's production environment")
	})
	if err != nil {
		return err
	}
	usage := fmt.Errorf("%w: POLAR_ACCESS_TOKEN=polar_oat_... POLAR_ENVIRONMENT=sandbox|production repose-admin billing polar-bootstrap [--webhook-url URL] [--no-webhook] [--production]", ErrUsage)
	token := strings.TrimSpace(os.Getenv("POLAR_ACCESS_TOKEN"))
	environment := strings.TrimSpace(os.Getenv("POLAR_ENVIRONMENT"))
	if token == "" || (environment != billing.EnvSandbox && environment != billing.EnvProduction) {
		return usage
	}
	get := func(name string) string { return fs.Lookup(name).Value.String() }
	opts := billing.BootstrapOptions{WebhookURL: get("webhook-url"), Production: get("production") == "true", Progress: e.Stderr}
	if opts.WebhookURL == "" {
		opts.WebhookURL = DefaultWebhookURL
		if u := strings.TrimRight(os.Getenv("API_PUBLIC_URL"), "/"); u != "" {
			opts.WebhookURL = u + "/v1/billing/webhook"
		}
	}
	if get("no-webhook") == "true" {
		opts.WebhookURL = ""
	}
	p := billing.NewPolar(billing.Config{AccessToken: token, Env: environment, BaseURL: PolarBaseURL}, e.logger())
	res, err := billing.Bootstrap(ctx, p, opts)
	if errors.Is(err, billing.ErrProduction) {
		return err
	}
	if err != nil {
		return fmt.Errorf("polar-bootstrap stopped; rerunning is safe, every object is found before it is made: %w", err)
	}
	_, _ = fmt.Fprintf(e.Stderr, "%s: created %d object(s), found %d\n\n", res.Environment, len(res.Created), len(res.Found))
	_, _ = fmt.Fprint(e.Stdout, res.EnvBlock())
	return nil
}

// billingShow prints one account's billing state: subscription, plan,
// period, usage and the overage arithmetic (docs/ops/M4-GATE.md).
func (e *Env) billingShow(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%w: billing show HANDLE", ErrUsage)
	}
	u, err := e.findUser(ctx, args[0])
	if err != nil {
		return err
	}
	a, err := billing.LoadAccount(ctx, e.pool, u.ID, time.Now())
	if err != nil {
		return err
	}
	_, err = a.WriteTo(e.Stdout)
	return err
}

// billingOverageNow sends this period's egress overage line for one
// account now rather than within three hours of the period's end: the gate
// proof of docs/ops/M4-GATE.md §4. A period already sent is not sent twice.
func (e *Env) billingOverageNow(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%w: billing overage-now HANDLE", ErrUsage)
	}
	u, err := e.findUser(ctx, args[0])
	if err != nil {
		return err
	}
	p, cfg, err := e.polar()
	if err != nil {
		return err
	}
	sub, err := billing.LiveSubscription(ctx, e.pool, u.ID)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("%s has no live subscription", u.Handle)
	}
	o := billing.NewOverage(e.pool, p, cfg, nil, metrics.NewNop(), e.logger())
	c, err := o.ChargePeriod(ctx, sub)
	if err != nil {
		return err
	}
	switch {
	case c == nil:
		_, _ = fmt.Fprintf(e.Stdout, "%s: the subscription has no billing period yet\n", u.Handle)
	case c.Cents == 0:
		_, _ = fmt.Fprintf(e.Stdout, "%s: %s to now: egress within the allowance, nothing to charge; period marked\n", u.Handle, c.PeriodStart.Format("2006-01-02"))
	case c.Sent:
		_, _ = fmt.Fprintf(e.Stdout, "%s: sent %d GB over = %d cents to Polar for the period from %s (event %s)\n", u.Handle, c.EgressGB, c.Cents, c.PeriodStart.Format("2006-01-02"), c.Ref)
	default:
		_, _ = fmt.Fprintf(e.Stdout, "%s: the period from %s already has its line (%d cents, event %s); nothing sent\n", u.Handle, c.PeriodStart.Format("2006-01-02"), c.Cents, orNone(c.Ref))
	}
	detail := map[string]any{"sent": false}
	if c != nil {
		detail = map[string]any{"cents": c.Cents, "sent": c.Sent}
	}
	_, err = e.audited(ctx, "billing_overage_now", u.Handle, detail)
	return err
}

func orNone(s string) string {
	if s == "" {
		return "none yet"
	}
	return s
}
