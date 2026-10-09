package billing

import (
	"errors"
	"os"
	"sort"
	"strings"
)

// Config is the POLAR_* environment the api reads (09-billing.md §5.11,
// DECISIONS I-289, I-604). With no POLAR_ACCESS_TOKEN the api starts
// normally, the billing routes answer 503 billing_disabled and the gate
// refuses every non-exempt start with subscription_required: a deploy
// without a token is safe and useless rather than free.
type Config struct {
	AccessToken string
	// Env is POLAR_ENVIRONMENT, sandbox or production. Polar's tokens do
	// not say which they belong to, so it is required with the token.
	Env           string
	WebhookSecret string
	// ProductSolo, ProductPlus and ProductPro are the Polar product ids of
	// the three plans; each carries the plan's monthly price, the metered
	// egress overage price and the trial.
	ProductSolo string
	ProductPlus string
	ProductPro  string
	// DiscountIntro is the id of the introductory discount that a first
	// checkout of the intro plan carries (DECISIONS I-497).
	DiscountIntro string
	// PortalReturnURL is where Polar's customer portal sends the user back.
	PortalReturnURL string
	// DashboardURL is the dashboard's origin, which every refusal names.
	DashboardURL string
	// BaseURL overrides the API origin (tests); empty derives it from Env.
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
	EnvSandbox    = "sandbox"
	EnvProduction = "production"

	sandboxBaseURL    = "https://sandbox-api.polar.sh/v1"
	productionBaseURL = "https://api.polar.sh/v1"
)

// Environment is sandbox or production.
func (c Config) Environment() string { return c.Env }

// Enabled reports whether Polar is configured at all.
func (c Config) Enabled() bool { return c.AccessToken != "" }

// PlanProduct is the Polar product id for a plan.
func (c Config) PlanProduct(plan string) string {
	switch plan {
	case Solo.ID:
		return c.ProductSolo
	case Plus.ID:
		return c.ProductPlus
	case Pro.ID:
		return c.ProductPro
	}
	return ""
}

// PlanForProduct is the plan a Polar product sells; "" for an unknown one.
func (c Config) PlanForProduct(productID string) string {
	switch productID {
	case "":
		return ""
	case c.ProductSolo:
		return Solo.ID
	case c.ProductPlus:
		return Plus.ID
	case c.ProductPro:
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
		AccessToken:     strings.TrimSpace(os.Getenv("POLAR_ACCESS_TOKEN")),
		Env:             strings.TrimSpace(os.Getenv("POLAR_ENVIRONMENT")),
		WebhookSecret:   strings.TrimSpace(os.Getenv("POLAR_WEBHOOK_SECRET")),
		ProductSolo:     strings.TrimSpace(os.Getenv("POLAR_PRODUCT_SOLO")),
		ProductPlus:     strings.TrimSpace(os.Getenv("POLAR_PRODUCT_PLUS")),
		ProductPro:      strings.TrimSpace(os.Getenv("POLAR_PRODUCT_PRO")),
		DiscountIntro:   strings.TrimSpace(os.Getenv("POLAR_DISCOUNT_INTRO")),
		PortalReturnURL: env("POLAR_PORTAL_RETURN_URL", dash+"/billing"),
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

// Validate refuses a half-configured Polar: a token with no environment
// could charge real cards from a sandbox deploy or the reverse, no
// product ids would sell nothing, no webhook secret would leave every
// subscription event unverified and dropped, and no introductory
// discount would charge the full price to a user the dashboard promised
// the introductory one.
func (c Config) Validate() error {
	if !c.Enabled() {
		return nil
	}
	if c.Env != EnvSandbox && c.Env != EnvProduction {
		return errors.New("POLAR_ACCESS_TOKEN is set but POLAR_ENVIRONMENT is not sandbox or production")
	}
	var missing []string
	for name, v := range map[string]string{
		"POLAR_WEBHOOK_SECRET": c.WebhookSecret,
		"POLAR_PRODUCT_SOLO":   c.ProductSolo,
		"POLAR_PRODUCT_PLUS":   c.ProductPlus,
		"POLAR_PRODUCT_PRO":    c.ProductPro,
		"POLAR_DISCOUNT_INTRO": c.DiscountIntro,
	} {
		if name == "POLAR_DISCOUNT_INTRO" {
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
		return errors.New("POLAR_ACCESS_TOKEN is set but " + strings.Join(missing, ", ") + " is not")
	}
	if c.ProductSolo == c.ProductPlus || c.ProductSolo == c.ProductPro || c.ProductPlus == c.ProductPro {
		return errors.New("POLAR_PRODUCT_SOLO, POLAR_PRODUCT_PLUS and POLAR_PRODUCT_PRO are not three different products")
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
