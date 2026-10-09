// Package app assembles the api process from its environment
// (05-control-plane-api.md §5.1, §5.14): the HTTP app, the gRPC app with
// the ops driver and background loops, or both in one process for
// development and tests.
package app

import (
	"errors"
	"fmt"
	"github.com/heracraft/repose/internal/api/secrets"
	"os"
	"strconv"
	"strings"
)

// Config is read from the environment Coolify injects.
type Config struct {
	Mode           string // http | grpc | all
	Listen         string
	GRPCListen     string
	InternalListen string
	MetricsListen  string
	Migrate        bool
	// KeyVault, when set, replaces the Azure Key Vault (or the in-memory
	// dev vault); tests use it to share one vault across processes.
	KeyVault secrets.KeyVault
	// Reseal tunes the background pass that binds name-only secret rows
	// to their project at start (DECISIONS I-474); tests shorten it.
	Reseal secrets.ResealOptions

	DatabaseURL     string
	LogtoIssuer     string
	LogtoM2MID      string
	LogtoM2MSecret  string
	APIResource     string
	KeyVaultURL     string
	KeyVaultKeyName string
	BlobAccountURL  string
	BlobContainer   string
	GRPCServerCert  string
	GRPCServerKey   string
	GRPCServerNames []string
	GatewayHost     string
	GatewayPort     int
	ResendAPIKey    string
	NotifyFrom      string
	DashboardURL    string
	BaseRef         string
	// CLIReleasesURL is CLI_RELEASES_URL: the redirect naming the newest
	// CLI release (DECISIONS I-626). Empty means GitHub's releases/latest,
	// except with REPOSE_DEV=1; "off" reads none.
	CLIReleasesURL string
	ReplicaID      string
	// SeatsTotal is SEATS_TOTAL: the fleet's seats when the operator sets
	// it (DECISIONS I-290); 0 derives the count from the ready hosts. There
	// is no off switch: a fleet that must never waitlist sets it very high.
	SeatsTotal int
	// WaitlistPercentSet says WAITLIST_PERCENT was in the environment; it
	// is ignored since I-290 and logged once so the operator removes it.
	WaitlistPercentSet bool
	// Dev enables the in-memory Key Vault and self-issued certificates;
	// it is refused unless REPOSE_DEV=1 and never in a container with a
	// KEYVAULT_URL.
	Dev bool
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// FromEnv reads the configuration.
func FromEnv() (Config, error) {
	c := Config{
		Mode:            env("API_MODE", "all"),
		Listen:          env("API_LISTEN", ":8080"),
		GRPCListen:      env("API_GRPC_LISTEN", ":8443"),
		InternalListen:  env("API_INTERNAL_LISTEN", ":8444"),
		MetricsListen:   env("API_METRICS_LISTEN", ":9103"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		LogtoIssuer:     strings.TrimRight(os.Getenv("LOGTO_ISSUER"), "/"),
		LogtoM2MID:      os.Getenv("LOGTO_M2M_CLIENT_ID"),
		LogtoM2MSecret:  os.Getenv("LOGTO_M2M_CLIENT_SECRET"),
		APIResource:     env("API_RESOURCE", "https://api.repose.herakraft.co"),
		KeyVaultURL:     os.Getenv("KEYVAULT_URL"),
		KeyVaultKeyName: env("KEYVAULT_KEY_NAME", "repose-dek-kek"),
		BlobAccountURL:  os.Getenv("BLOB_ACCOUNT_URL"),
		BlobContainer:   env("BLOB_CONTAINER", "repose-snapshots"),
		GRPCServerCert:  os.Getenv("GRPC_SERVER_CERT"),
		GRPCServerKey:   os.Getenv("GRPC_SERVER_KEY"),
		GatewayHost:     env("GATEWAY_HOST", "ssh.repose.herakraft.co"),
		ResendAPIKey:    os.Getenv("RESEND_API_KEY"),
		NotifyFrom:      env("NOTIFY_FROM", "repose <notify@repose.herakraft.co>"),
		DashboardURL:    env("DASHBOARD_URL", "https://repose.herakraft.co"),
		BaseRef:         os.Getenv("BASE_REF"),
		CLIReleasesURL:  os.Getenv("CLI_RELEASES_URL"),
		ReplicaID:       env("REPLICA_ID", ""),
		Dev:             os.Getenv("REPOSE_DEV") == "1",
		// On by default: a Coolify pre-deployment command runs in the
		// previous container and is skipped when there is none, so the
		// process that serves the new schema is the one that has to apply
		// it (DECISIONS I-90). db.MigrateUp serialises replicas on an
		// advisory lock.
		Migrate: os.Getenv("API_MIGRATE") != "0",
	}
	if names := os.Getenv("GRPC_SERVER_NAMES"); names != "" {
		c.GRPCServerNames = strings.Split(names, ",")
	}
	port, err := strconv.Atoi(env("GATEWAY_PORT", "22"))
	if err != nil {
		return c, errors.New("GATEWAY_PORT is not a number")
	}
	c.GatewayPort = port
	seats, err := strconv.Atoi(env("SEATS_TOTAL", "0"))
	if err != nil || seats < 0 {
		return c, errors.New("SEATS_TOTAL must be a whole number, 0 to derive the seats from the hosts")
	}
	c.SeatsTotal = seats
	c.WaitlistPercentSet = os.Getenv("WAITLIST_PERCENT") != ""
	if c.ReplicaID == "" {
		h, err := os.Hostname()
		if err != nil || h == "" {
			h = "replica"
		}
		c.ReplicaID = h
	}
	return c, c.Validate()
}

// Validate checks what every mode needs.
func (c Config) Validate() error {
	switch c.Mode {
	case "http", "grpc", "all":
	default:
		return fmt.Errorf("API_MODE must be http, grpc or all (got %q)", c.Mode)
	}
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	if c.KeyVaultURL == "" && !c.Dev {
		return errors.New("KEYVAULT_URL is required (or REPOSE_DEV=1 for the in-memory key vault)")
	}
	if c.Mode != "grpc" && c.LogtoIssuer == "" && !c.Dev {
		return errors.New("LOGTO_ISSUER is required for the http app")
	}
	if c.Mode != "grpc" && (c.LogtoM2MID == "" || c.LogtoM2MSecret == "") && !c.Dev {
		return errors.New("LOGTO_M2M_CLIENT_ID and LOGTO_M2M_CLIENT_SECRET are required for the http app")
	}
	if (c.GRPCServerCert == "") != (c.GRPCServerKey == "") {
		return errors.New("GRPC_SERVER_CERT and GRPC_SERVER_KEY go together")
	}
	// With no files the grpc listener's certificate is issued from the
	// api's own CA in Postgres for GRPC_SERVER_NAMES at start
	// (hostmgr.TLSConfig, pki.ServerTLS), which is what hosts verify
	// against anyway; nothing else can sign one (DECISIONS I-87). Files
	// remain for a certificate somebody else issued.
	if c.Mode != "http" && c.GRPCServerCert == "" && len(c.GRPCServerNames) == 0 && !c.Dev {
		return errors.New("GRPC_SERVER_NAMES is required for the grpc app when GRPC_SERVER_CERT is not set")
	}
	return nil
}
