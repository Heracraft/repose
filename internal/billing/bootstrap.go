package billing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
)

// Bootstrap creates, or finds, every Polar object the api needs
// (DECISIONS I-289, I-604): the egress overage meter, one monthly product
// per plan with its fixed price, its metered overage price and the
// seven-day trial, the introductory discount (DECISIONS I-497), and the
// webhook endpoint with the events list; and it sets the organization's
// subscription and customer portal settings. It is idempotent: objects
// are found by metadata.repose before anything is created, and a second
// run creates nothing. `repose-admin billing polar-bootstrap` runs it and
// prints the POLAR_* block.

// ErrProduction is returned for a production token without --production.
var ErrProduction = errors.New("this is Polar's production environment; pass --production to bootstrap it on purpose")

// BootstrapOptions are the command's flags.
type BootstrapOptions struct {
	// WebhookURL is the endpoint; empty creates none.
	WebhookURL string
	// Production allows the production environment.
	Production bool
	// Progress receives one line per object, created or found.
	Progress io.Writer
}

// BootstrapResult is what the run found or made.
type BootstrapResult struct {
	Environment   string
	Organization  string
	MeterOverage  string
	ProductSolo   string
	ProductPlus   string
	ProductPro    string
	DiscountIntro string
	WebhookID     string
	// WebhookSecret is the endpoint's signing secret, read back from the
	// endpoint on a rerun.
	WebhookSecret string
	Created       []string
	Found         []string
}

// overageMeterKey is the metadata.repose value of the overage meter.
const overageMeterKey = "overage-meter"

// planProductKey is the metadata.repose value of a plan's product. It
// names the price, so a changed price makes a new product (Polar locks a
// subscription to the price it started on) rather than reusing the old.
func planProductKey(plan Plan) string {
	return fmt.Sprintf("plan-%s-%d", plan.ID, plan.PriceCents)
}

// productFits reports whether a found product sells the plan as repose
// sells it: the fixed monthly price, the overage price on the meter, the
// trial.
func productFits(pr Product, plan Plan, meterID string) bool {
	fixed, metered := false, false
	for _, price := range pr.Prices {
		if price.IsArchived {
			continue
		}
		switch price.AmountType {
		case "fixed":
			fixed = price.PriceAmount == plan.PriceCents && strings.EqualFold(price.Currency, plan.Currency)
		case "metered_unit":
			metered = price.MeterID == meterID && strings.TrimRight(strings.TrimRight(price.UnitAmount, "0"), ".") == fmt.Sprint(OveragePerGBCents)
		}
	}
	return fixed && metered && pr.RecurringInterval == "month" && pr.TrialInterval == "day" && pr.TrialIntervalCount == plan.TrialDays
}

// Bootstrap runs against the client's environment.
func Bootstrap(ctx context.Context, p *Polar, o BootstrapOptions) (*BootstrapResult, error) {
	if p.Environment() != EnvSandbox && !o.Production {
		return nil, ErrProduction
	}
	say := func(format string, a ...any) {
		if o.Progress != nil {
			_, _ = fmt.Fprintf(o.Progress, format+"\n", a...)
		}
	}
	res := &BootstrapResult{Environment: p.Environment()}
	found := func(what, id string) {
		res.Found = append(res.Found, what+" "+id)
		say("found    %-16s %s", what, id)
	}
	created := func(what, id, note string) {
		res.Created = append(res.Created, what+" "+id)
		say("created  %-16s %s%s", what, id, note)
	}

	org, err := p.GetOrganization(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the organization: %w", err)
	}
	res.Organization = org.Slug
	wantSub, wantPortal, wantEmails := OrganizationSettings()
	if settingsMatch(org.SubscriptionSettings, wantSub) && settingsMatch(org.PortalSettings, wantPortal) && settingsMatch(org.EmailSettings, wantEmails) && org.DefaultTaxBehavior == TaxBehavior {
		found("organization", org.Slug)
	} else {
		if _, err := p.UpdateOrganization(ctx, org, wantSub, wantPortal, wantEmails); err != nil {
			return nil, fmt.Errorf("set the organization's settings: %w", err)
		}
		say("updated  %-16s %s (one subscription per customer, trial abuse prevention, no plan changes in the portal, prices exclude tax, no emails repose sends itself)", "organization", org.Slug)
	}

	meters, err := p.ListMeters(ctx)
	if err != nil {
		return nil, fmt.Errorf("list meters: %w", err)
	}
	for _, m := range meters {
		if k, _ := m.Metadata["repose"].(string); k == overageMeterKey {
			res.MeterOverage = m.ID
			found("meter overage", m.ID)
			break
		}
	}
	if res.MeterOverage == "" {
		m, err := p.CreateOverageMeter(ctx)
		if err != nil {
			return nil, fmt.Errorf("create the overage meter: %w", err)
		}
		res.MeterOverage = m.ID
		created("meter overage", m.ID, "")
	}

	products, err := p.ListProducts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	for _, plan := range Plans {
		key := planProductKey(plan)
		id := ""
		for _, pr := range products {
			if k, _ := pr.Metadata["repose"].(string); k == key && productFits(pr, plan, res.MeterOverage) {
				id = pr.ID
				found("product "+plan.ID, id)
				break
			}
		}
		if id == "" {
			pr, err := p.CreatePlanProduct(ctx, plan, res.MeterOverage, key)
			if err != nil {
				return nil, fmt.Errorf("create the %s product: %w", plan.ID, err)
			}
			id = pr.ID
			created("product "+plan.ID, id, fmt.Sprintf(" (%d %s a month, %d-day trial, egress overage %d cents a GB)", plan.PriceCents, plan.Currency, plan.TrialDays, OveragePerGBCents))
		}
		switch plan.ID {
		case Solo.ID:
			res.ProductSolo = id
		case Plus.ID:
			res.ProductPlus = id
		case Pro.ID:
			res.ProductPro = id
		}
	}

	if plan, ok := IntroPlan(); ok {
		productID := Config{ProductSolo: res.ProductSolo, ProductPlus: res.ProductPlus, ProductPro: res.ProductPro}.PlanProduct(plan.ID)
		discounts, err := p.ListDiscounts(ctx)
		if err != nil {
			return nil, fmt.Errorf("list discounts: %w", err)
		}
		for _, d := range discounts {
			ids := make([]string, 0, len(d.Products))
			for _, pr := range d.Products {
				ids = append(ids, pr.ID)
			}
			if k, _ := d.Metadata["repose"].(string); k == introDiscountKey(plan) && slices.Contains(ids, productID) {
				res.DiscountIntro = d.ID
				found("discount intro", d.ID)
				break
			}
		}
		if res.DiscountIntro == "" {
			d, err := p.CreateIntroDiscount(ctx, plan, productID)
			if err != nil {
				return nil, fmt.Errorf("create the introductory discount: %w", err)
			}
			res.DiscountIntro = d.ID
			created("discount intro", d.ID, fmt.Sprintf(" ($%d off the first %d %s charges)", plan.IntroDiscountCents()/100, plan.IntroMonths, plan.ID))
		}
	}

	if o.WebhookURL != "" {
		endpoints, err := p.ListWebhookEndpoints(ctx)
		if err != nil {
			return nil, fmt.Errorf("list webhook endpoints: %w", err)
		}
		for _, e := range endpoints {
			if e.URL != o.WebhookURL {
				continue
			}
			res.WebhookID, res.WebhookSecret = e.ID, e.Secret
			found("webhook", e.ID+" -> "+o.WebhookURL)
			if len(missingEvents(e.Events)) > 0 || e.APIVersion != APIVersion || !e.Enabled || e.Format != "raw" {
				if _, err := p.UpdateWebhookEndpoint(ctx, e.ID, WebhookEvents); err != nil {
					return nil, fmt.Errorf("update the webhook endpoint: %w", err)
				}
				say("updated  %-16s %s (events, api_version %s, enabled)", "webhook", e.ID, APIVersion)
			}
			if e.Format != "raw" {
				say("warning: the endpoint's format is %s; set it to raw in Polar's dashboard", e.Format)
			}
			break
		}
		if res.WebhookID == "" {
			e, err := p.CreateWebhookEndpoint(ctx, o.WebhookURL, WebhookEvents)
			if err != nil {
				return nil, fmt.Errorf("create the webhook endpoint: %w", err)
			}
			res.WebhookID, res.WebhookSecret = e.ID, e.Secret
			created("webhook", e.ID, " -> "+o.WebhookURL)
		}
	}
	return res, nil
}

// settingsMatch reports whether every key of want has that value in have.
func settingsMatch(have, want map[string]any) bool {
	for k, w := range want {
		h, ok := have[k]
		if !ok {
			return false
		}
		if wm, isMap := w.(map[string]any); isMap {
			hm, _ := h.(map[string]any)
			if !settingsMatch(hm, wm) {
				return false
			}
			continue
		}
		if fmt.Sprint(h) != fmt.Sprint(w) && !reflect.DeepEqual(h, w) {
			return false
		}
	}
	return true
}

func missingEvents(have []string) []string {
	var out []string
	for _, e := range WebhookEvents {
		if !slices.Contains(have, e) {
			out = append(out, e)
		}
	}
	return out
}

// EnvBlock is the POLAR_* block to paste into the api's environment. The
// token is never printed; the webhook secret is, because it is what the
// operator came for.
func (r *BootstrapResult) EnvBlock() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Polar %s, organization %s, from repose-admin billing polar-bootstrap\n", r.Environment, r.Organization)
	fmt.Fprintf(&b, "POLAR_ENVIRONMENT=%s\n", r.Environment)
	fmt.Fprintf(&b, "POLAR_PRODUCT_SOLO=%s\n", r.ProductSolo)
	fmt.Fprintf(&b, "POLAR_PRODUCT_PLUS=%s\n", r.ProductPlus)
	fmt.Fprintf(&b, "POLAR_PRODUCT_PRO=%s\n", r.ProductPro)
	if r.DiscountIntro != "" {
		fmt.Fprintf(&b, "POLAR_DISCOUNT_INTRO=%s\n", r.DiscountIntro)
	}
	switch {
	case r.WebhookSecret != "":
		fmt.Fprintf(&b, "POLAR_WEBHOOK_SECRET=%s\n", r.WebhookSecret)
	case r.WebhookID != "":
		b.WriteString("# POLAR_WEBHOOK_SECRET: Polar shows it on the webhook endpoint's page\n")
	default:
		b.WriteString("# POLAR_WEBHOOK_SECRET: no endpoint created (--webhook-url)\n")
	}
	b.WriteString("# POLAR_ACCESS_TOKEN: the organization access token this ran with\n")
	return b.String()
}
