package billing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Bootstrap creates, or finds, every Paddle object the api needs
// (DECISIONS I-289): one product per plan with its monthly price and
// seven-day trial, the introductory discount (DECISIONS I-497), the
// overage product, and the webhook destination with
// the events list. It is idempotent: objects are found by
// custom_data.repose before anything is created, and a second run creates
// nothing. `repose-admin billing paddle-bootstrap` and
// ops/paddle/bootstrap.sh run it and print the PADDLE_* block.

// ErrLiveKey is returned for a live key without --live.
var ErrLiveKey = errors.New("this is a live Paddle key; pass --live to bootstrap the live environment on purpose")

// BootstrapOptions are the command's flags.
type BootstrapOptions struct {
	// WebhookURL is the notification destination; empty creates none.
	WebhookURL string
	// Live allows a live key.
	Live bool
	// Progress receives one line per object, created or found.
	Progress io.Writer
}

// BootstrapResult is what the run found or made.
type BootstrapResult struct {
	Environment    string
	ProductSolo    string
	ProductPlus    string
	ProductPro     string
	ProductOverage string
	PriceSolo      string
	PricePlus      string
	PricePro       string
	DiscountIntro  string
	WebhookID      string
	// WebhookSecret is the endpoint secret Paddle gives once; on a rerun
	// it is read back from the existing setting.
	WebhookSecret string
	Created       []string
	Found         []string
}

// overageProductKey is the custom_data.repose value of the overage product.
const overageProductKey = "overage"

// Bootstrap runs against the client's environment.
func Bootstrap(ctx context.Context, p *Paddle, o BootstrapOptions) (*BootstrapResult, error) {
	if p.Environment() == EnvLive && !o.Live {
		return nil, ErrLiveKey
	}
	say := func(format string, a ...any) {
		if o.Progress != nil {
			_, _ = fmt.Fprintf(o.Progress, format+"\n", a...)
		}
	}
	res := &BootstrapResult{Environment: p.Environment()}
	products, err := p.ListProducts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	byKey := map[string]Product{}
	for _, pr := range products {
		if k, _ := pr.CustomData["repose"].(string); k != "" {
			byKey[k] = pr
		}
	}
	product := func(key, name, desc string) (string, error) {
		if pr, ok := byKey[key]; ok {
			res.Found = append(res.Found, "product "+key+" "+pr.ID)
			say("found    product %-8s %s", key, pr.ID)
			return pr.ID, nil
		}
		pr, err := p.CreateProduct(ctx, name, desc, map[string]any{"repose": key})
		if err != nil {
			return "", fmt.Errorf("create the %s product: %w", key, err)
		}
		byKey[key] = pr
		res.Created = append(res.Created, "product "+key+" "+pr.ID)
		say("created  product %-8s %s", key, pr.ID)
		return pr.ID, nil
	}
	for _, plan := range Plans {
		id, err := product(plan.ID, "repose "+plan.Name, fmt.Sprintf("%d GB of memory for running machines, %d GB disk, %d GB egress a month", plan.MemoryGB, plan.DiskGB, plan.EgressGB))
		if err != nil {
			return nil, err
		}
		prices, err := p.ListPrices(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("list the %s prices: %w", plan.ID, err)
		}
		priceID := ""
		for _, pr := range prices {
			if k, _ := pr.CustomData["repose"].(string); k == plan.ID && pr.UnitPrice.Amount == fmt.Sprint(plan.PriceCents) && pr.UnitPrice.CurrencyCode == plan.Currency {
				priceID = pr.ID
				break
			}
		}
		if priceID != "" {
			res.Found = append(res.Found, "price "+plan.ID+" "+priceID)
			say("found    price   %-8s %s", plan.ID, priceID)
		} else {
			pr, err := p.CreatePlanPrice(ctx, id, plan)
			if err != nil {
				return nil, fmt.Errorf("create the %s price: %w", plan.ID, err)
			}
			priceID = pr.ID
			res.Created = append(res.Created, "price "+plan.ID+" "+priceID)
			say("created  price   %-8s %s (%d %s a month, %d-day trial)", plan.ID, priceID, plan.PriceCents, plan.Currency, plan.TrialDays)
		}
		switch plan.ID {
		case Solo.ID:
			res.ProductSolo, res.PriceSolo = id, priceID
		case Plus.ID:
			res.ProductPlus, res.PricePlus = id, priceID
		case Pro.ID:
			res.ProductPro, res.PricePro = id, priceID
		}
	}
	if plan, ok := IntroPlan(); ok {
		priceID := res.PriceSolo
		switch plan.ID {
		case Plus.ID:
			priceID = res.PricePlus
		case Pro.ID:
			priceID = res.PricePro
		}
		discounts, err := p.ListDiscounts(ctx)
		if err != nil {
			return nil, fmt.Errorf("list discounts: %w", err)
		}
		for _, d := range discounts {
			if k, _ := d.CustomData["repose"].(string); k == introDiscountKey(plan) && slices.Contains(d.RestrictTo, priceID) {
				res.DiscountIntro = d.ID
				res.Found = append(res.Found, "discount intro "+d.ID)
				say("found    discount intro    %s", d.ID)
				break
			}
		}
		if res.DiscountIntro == "" {
			d, err := p.CreateIntroDiscount(ctx, plan, priceID)
			if err != nil {
				return nil, fmt.Errorf("create the introductory discount: %w", err)
			}
			res.DiscountIntro = d.ID
			res.Created = append(res.Created, "discount intro "+d.ID)
			say("created  discount intro    %s ($%d off the first %d %s charges)", d.ID, plan.IntroDiscountCents()/100, plan.IntroMonths, plan.ID)
		}
	}
	if res.ProductOverage, err = product(overageProductKey, "repose egress overage", "Egress past the plan's allowance, $0.05 a GB, one line a period"); err != nil {
		return nil, err
	}
	if o.WebhookURL != "" {
		settings, err := p.ListNotificationSettings(ctx)
		if err != nil {
			return nil, fmt.Errorf("list notification settings: %w", err)
		}
		for _, s := range settings {
			if s.Destination == o.WebhookURL && s.Type == "url" {
				res.WebhookID, res.WebhookSecret = s.ID, s.EndpointSecretKey
				res.Found = append(res.Found, "webhook "+s.ID)
				say("found    webhook          %s -> %s", s.ID, o.WebhookURL)
				if missing := missingEvents(s.SubscribedEvents); len(missing) > 0 {
					say("warning: the destination lacks %s; add them in Paddle's dashboard", strings.Join(missing, ", "))
				}
				break
			}
		}
		if res.WebhookID == "" {
			s, err := p.CreateNotificationSetting(ctx, o.WebhookURL, WebhookEvents)
			if err != nil {
				return nil, fmt.Errorf("create the webhook destination: %w", err)
			}
			res.WebhookID, res.WebhookSecret = s.ID, s.EndpointSecretKey
			res.Created = append(res.Created, "webhook "+s.ID)
			say("created  webhook          %s -> %s", s.ID, o.WebhookURL)
		}
	}
	return res, nil
}

func missingEvents(have []string) []string {
	set := map[string]bool{}
	for _, e := range have {
		set[e] = true
	}
	var out []string
	for _, e := range WebhookEvents {
		if !set[e] {
			out = append(out, e)
		}
	}
	return out
}

// EnvBlock is the PADDLE_* block to paste into the api's environment. The
// key is never printed; the webhook secret is, once, because it is what
// the operator came for.
func (r *BootstrapResult) EnvBlock() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Paddle %s, from repose-admin billing paddle-bootstrap\n", r.Environment)
	fmt.Fprintf(&b, "PADDLE_PRICE_SOLO=%s\n", r.PriceSolo)
	fmt.Fprintf(&b, "PADDLE_PRICE_PLUS=%s\n", r.PricePlus)
	fmt.Fprintf(&b, "PADDLE_PRICE_PRO=%s\n", r.PricePro)
	fmt.Fprintf(&b, "PADDLE_PRODUCT_OVERAGE=%s\n", r.ProductOverage)
	if r.DiscountIntro != "" {
		fmt.Fprintf(&b, "PADDLE_DISCOUNT_INTRO=%s\n", r.DiscountIntro)
	}
	if r.WebhookSecret != "" {
		fmt.Fprintf(&b, "PADDLE_WEBHOOK_SECRET=%s\n", r.WebhookSecret)
	} else if r.WebhookID != "" {
		b.WriteString("# PADDLE_WEBHOOK_SECRET: Paddle shows it on the notification destination's page\n")
	} else {
		b.WriteString("# PADDLE_WEBHOOK_SECRET: no destination created (--webhook-url)\n")
	}
	b.WriteString("# PADDLE_CLIENT_TOKEN: Paddle dashboard > Developer tools > Authentication > client-side tokens\n")
	return b.String()
}
