package billing

import (
	"errors"
	"os"
	"sort"
	"strings"
)

// Config is the PADDLE_* environment the api reads (09-billing.md §5.11,
// DECISIONS I-289). With no PADDLE_API_KEY the api starts normally, the
// billing routes answer 503 billing_disabled and the gate refuses every
// non-exempt start with subscription_required: a deploy without keys is
// safe and useless rather than free.
type Config struct {
	APIKey        string
	WebhookSecret string
	// ClientToken is the public Paddle.js token GET /billing hands the
	// dashboard.
	ClientToken string
	// PriceSolo, PricePlus and PricePro are the pri_... ids of the three
	// plans; ProductOverage the pro_... id the egress line is charged under.
	PriceSolo      string
	PricePlus      string
	PricePro       string
	ProductOverage string
	// DiscountIntro is the dsc_... id of the introductory discount that a
	// first checkout of the intro plan carries (DECISIONS I-497).
	DiscountIntro string
	// PortalReturnURL is where Paddle's customer portal sends the user back.
	PortalReturnURL string
	// DashboardURL is the dashboard's origin, which every refusal names.
	DashboardURL string
	// BaseURL overrides the API origin (tests); empty derives it from the
	// key's environment.
	BaseURL string
	// Enforce is BILLING_ENFORCE (§8). False keeps rolling up but stops
	// blocking starts and stopping machines.
	Enforce bool
	// SeatsTotal is SEATS_TOTAL for the stub seat count (0 = unlimited);
	// the real count derives it from the hosts (I-290).
	SeatsTotal int
}

// Environments.
const (
	EnvSandbox = "sandbox"
	EnvLive    = "live"

	sandboxKeyPrefix = "pdl_sdbx_"
	sandboxBaseURL   = "https://sandbox-api.paddle.com"
	liveBaseURL      = "https://api.paddle.com"
)

// Environment tells a sandbox key from a live one by its prefix.
func Environment(key string) string {
	if strings.HasPrefix(key, sandboxKeyPrefix) {
		return EnvSandbox
	}
	return EnvLive
}

// Environment is the configured key's environment.
func (c Config) Environment() string { return Environment(c.APIKey) }

// Enabled reports whether Paddle is configured at all.
func (c Config) Enabled() bool { return c.APIKey != "" }

// PlanPrice is the Paddle price id for a plan.
func (c Config) PlanPrice(plan string) string {
	switch plan {
	case Solo.ID:
		return c.PriceSolo
	case Plus.ID:
		return c.PricePlus
	case Pro.ID:
		return c.PricePro
	}
	return ""
}

// PlanForPrice is the plan a Paddle price id sells; "" for an unknown one.
func (c Config) PlanForPrice(priceID string) string {
	switch priceID {
	case "":
		return ""
	case c.PriceSolo:
		return Solo.ID
	case c.PricePlus:
		return Plus.ID
	case c.PricePro:
		return Pro.ID
	}
	return ""
}

func env(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// ConfigFromEnv reads the billing configuration; enabled is Enabled().
func ConfigFromEnv() (cfg Config, enabled bool) {
	dash := env("DASHBOARD_URL", "https://repose.herakraft.co")
	cfg = Config{
		APIKey:          strings.TrimSpace(os.Getenv("PADDLE_API_KEY")),
		WebhookSecret:   strings.TrimSpace(os.Getenv("PADDLE_WEBHOOK_SECRET")),
		ClientToken:     strings.TrimSpace(os.Getenv("PADDLE_CLIENT_TOKEN")),
		PriceSolo:       strings.TrimSpace(os.Getenv("PADDLE_PRICE_SOLO")),
		PricePlus:       strings.TrimSpace(os.Getenv("PADDLE_PRICE_PLUS")),
		PricePro:        strings.TrimSpace(os.Getenv("PADDLE_PRICE_PRO")),
		ProductOverage:  strings.TrimSpace(os.Getenv("PADDLE_PRODUCT_OVERAGE")),
		DiscountIntro:   strings.TrimSpace(os.Getenv("PADDLE_DISCOUNT_INTRO")),
		PortalReturnURL: env("PADDLE_PORTAL_RETURN_URL", dash+"/billing"),
		DashboardURL:    dash,
		Enforce:         os.Getenv("BILLING_ENFORCE") != "false",
	}
	if v := strings.TrimSpace(os.Getenv("SEATS_TOTAL")); v != "" {
		n := 0
		for _, r := range v {
			if r < '0' || r > '9' {
				n = 0
				break
			}
			n = n*10 + int(r-'0')
		}
		cfg.SeatsTotal = n
	}
	return cfg, cfg.Enabled()
}

// Validate refuses a half-configured Paddle: a key with no price ids
// would sell nothing, no webhook secret would leave every subscription
// event unverified and dropped, and no overage product would silently
// give egress away. No introductory discount would charge the full price
// to a user the dashboard promised the introductory one.
func (c Config) Validate() error {
	if !c.Enabled() {
		return nil
	}
	var missing []string
	for name, v := range map[string]string{
		"PADDLE_WEBHOOK_SECRET":  c.WebhookSecret,
		"PADDLE_PRICE_SOLO":      c.PriceSolo,
		"PADDLE_PRICE_PLUS":      c.PricePlus,
		"PADDLE_PRICE_PRO":       c.PricePro,
		"PADDLE_PRODUCT_OVERAGE": c.ProductOverage,
		"PADDLE_DISCOUNT_INTRO":  c.DiscountIntro,
	} {
		if name == "PADDLE_DISCOUNT_INTRO" {
			if _, ok := IntroPlan(); !ok {
				continue
			}
		}
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return errors.New("PADDLE_API_KEY is set but " + strings.Join(missing, ", ") + " is not")
	}
	if c.PriceSolo == c.PricePlus || c.PriceSolo == c.PricePro || c.PricePlus == c.PricePro {
		return errors.New("PADDLE_PRICE_SOLO, PADDLE_PRICE_PLUS and PADDLE_PRICE_PRO are not three different prices")
	}
	return nil
}

// BillingURL is the dashboard page every refusal points at.
func (c Config) BillingURL() string {
	dash := c.DashboardURL
	if dash == "" {
		dash = "https://repose.herakraft.co"
	}
	return strings.TrimRight(dash, "/") + "/billing"
}
