package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/heracraft/repose/internal/api/abuse"
	"github.com/heracraft/repose/internal/api/auth"
	"github.com/heracraft/repose/internal/api/basebump"
	"github.com/heracraft/repose/internal/api/buildlog"
	"github.com/heracraft/repose/internal/api/ca"
	"github.com/heracraft/repose/internal/api/config"
	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/hostmgr"
	httpapi "github.com/heracraft/repose/internal/api/http"
	"github.com/heracraft/repose/internal/api/idle"
	"github.com/heracraft/repose/internal/api/meter"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/notify"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/pki"
	"github.com/heracraft/repose/internal/api/questions"
	"github.com/heracraft/repose/internal/api/secrets"
	"github.com/heracraft/repose/internal/api/snapshots"
	"github.com/heracraft/repose/internal/api/temp"
	"github.com/heracraft/repose/internal/api/waitlist"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/fakes/kv"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/obs"
	"github.com/heracraft/repose/internal/obs/instrument"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"

	"github.com/google/uuid"
)

// App is the assembled process.
type App struct {
	cfg       Config
	log       *slog.Logger
	pool      *db.Pool
	reg       *prometheus.Registry
	m         *metrics.M
	sec       *secrets.Store
	ca        *ca.CA
	hostMgr   *hostmgr.Server
	engine    *ops.Engine
	logs      *buildlog.Store
	events    *events.Ingest
	meterIn   *meter.Ingest
	abuse     *abuse.Guard
	questions *questions.Service
	outbox    *notify.Outbox
	paddle    *billing.Paddle
	billing   *billing.Service
	overage   *billing.Overage
	hooks     *billing.Webhooks
	gate      *billing.Gate
	seats     *waitlist.Service
	bcfg      billing.Config
	server    *httpapi.Server
	version   string
	otelOff   func(context.Context) error
}

// New wires the process. Nothing listens yet.
func New(ctx context.Context, cfg Config, version string) (*App, error) {
	level := slog.LevelInfo
	if lv, err := obs.ParseLevel(os.Getenv("LOG_LEVEL")); err == nil {
		level = lv
	}
	log := obs.NewLogger(obs.LogOptions{Component: obs.ComponentAPI, Level: level})
	a := &App{cfg: cfg, log: log, version: version}
	// One tracing setup for every binary (DECISIONS I-59): no exporter and no
	// connection when OTEL_EXPORTER_OTLP_ENDPOINT is unset, OTLP over HTTP or
	// gRPC as OTEL_EXPORTER_OTLP_PROTOCOL asks.
	_, off, err := instrument.SetupTracing(ctx, instrument.TraceOptions{
		Component: obs.ComponentAPI, Version: version, Insecure: true,
	})
	if err != nil {
		return nil, err
	}
	a.otelOff = off
	enabled := instrument.TracingEnabled()
	log.Info("starting", "event", "start", "mode", cfg.Mode, "version", version, "otel", enabled, "dev", cfg.Dev)
	if cfg.WaitlistPercentSet {
		log.Warn("WAITLIST_PERCENT is ignored since DECISIONS I-290; the seats waitlist uses SEATS_TOTAL (0 derives it from the hosts). Remove the variable.", "event", "config_deprecated", "name", "WAITLIST_PERCENT")
	}
	a.pool, err = db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if cfg.Migrate {
		applied, err := db.MigrateUp(ctx, a.pool)
		if err != nil {
			return nil, fmt.Errorf("migrate: %w", err)
		}
		log.Info("migrations applied", "event", "migrate", "count", len(applied))
	}
	st, err := db.MigrateStatus(ctx, a.pool)
	if err != nil {
		return nil, err
	}
	if len(st.Pending) > 0 {
		log.Warn("migrations pending; /healthz reports unhealthy until `repose-admin db migrate` runs", "event", "migrate_pending", "count", len(st.Pending))
	} else if err := db.EnsurePartitions(ctx, a.pool, time.Now()); err != nil {
		return nil, err
	}
	// The registry from internal/obs/metrics refuses a metric outside the
	// repose_ namespace or with a label outside the low-cardinality list, so
	// every series in internal/api/metrics is checked at startup
	// (docs/workstreams/10-observability.md §5).
	om := obsmetrics.NewVersion(obs.ComponentAPI, version)
	a.reg = om.Registry()
	a.m = metrics.New(om)
	var kvs secrets.KeyVault
	if cfg.KeyVault != nil {
		kvs = cfg.KeyVault
	} else if cfg.KeyVaultURL != "" {
		azkv, err := secrets.NewAzureKV(cfg.KeyVaultURL, cfg.KeyVaultKeyName, nil)
		if err != nil {
			return nil, err
		}
		kvs = azkv
	} else {
		log.Warn("REPOSE_DEV=1: using the in-memory key vault; secrets do not survive a restart", "event", "dev_kv")
		kvs = kv.New()
	}
	a.sec = secrets.New(a.pool, kvs)
	a.ca, err = ca.Load(ctx, a.pool, a.sec)
	if errors.Is(err, ca.ErrNotInitialised) {
		a.ca, err = a.initCA(ctx, log)
	}
	if err != nil {
		return nil, err
	}
	a.logs = buildlog.New(a.pool, log)
	a.events = events.New(a.pool, a.m, log)
	a.meterIn = meter.New(a.pool, a.m, log)
	a.hostMgr = hostmgr.New(a.pool, a.ca.X509(), cfg.ReplicaID, a.m, log)
	a.hostMgr.SetHostCAPub(a.ca.HostCAPub)
	abuse.TermsURL = strings.TrimSuffix(cfg.DashboardURL, "/") + "/terms"
	a.engine = ops.New(a.pool, a.hostMgr, a.ca, a.sec, a.logs, a.events, a.m, log, ops.Config{BaseRef: cfg.BaseRef})
	a.abuse = abuse.New(a.pool, a.engine, a.events, a.m, log)
	a.questions = questions.New(a.pool, a.events, a.hostMgr, log)
	a.events.SetQuestions(a.questions)
	a.hostMgr.SetHandlers(hostmgr.Handlers{
		Hello: a.engine.OnHello,
		Result: func(ctx context.Context, hostID uuid.UUID, r *hostdv1.Result) {
			if a.questions.OnResult(ctx, hostID, r) { // an AnswerQuestion delivery (I-245)
				return
			}
			a.engine.OnResult(ctx, hostID, r)
		},
		Samples: func(ctx context.Context, hostID uuid.UUID, s *hostdv1.Samples) {
			a.meterIn.OnSamples(ctx, hostID, s)
			a.abuse.OnSamples(ctx, hostID, s) // DECISIONS I-239: a miner stops its guest
		},
		Event: a.events.OnEvent,
		BuildLog: func(ctx context.Context, hostID uuid.UUID, l *hostdv1.BuildLog) {
			if opID, ok := a.logs.OpFor(l.CommandId); ok {
				a.logs.Append(opID, int64(l.Seq), l.Line)
			}
		},
	})
	senders := map[string]notify.Sender{"email": &notify.Email{APIKey: cfg.ResendAPIKey, From: cfg.NotifyFrom}, "ntfy": &notify.Ntfy{}}
	a.outbox = notify.New(a.pool, senders, a.m, log)
	a.outbox.Dashboard = cfg.DashboardURL
	a.outbox.APIBase = cfg.APIResource
	unsub, err := notify.LoadOrCreateUnsubscriber(ctx, a.sec)
	if err != nil {
		log.Warn("unsubscribe key unavailable; email unsubscribe links are disabled", "event", "notify_unsub_unavailable", "err", err.Error())
	} else {
		a.outbox.Unsub = unsub
	}
	// Billing (workstream 09, DECISIONS I-289). With no PADDLE_API_KEY the
	// api starts normally, the billing routes answer 503 billing_disabled
	// and the gate refuses every non-exempt start with
	// subscription_required; with one, a half-configured Paddle is refused
	// rather than silently selling nothing.
	bcfg, paddleOn := billing.ConfigFromEnv()
	bcfg.DashboardURL = cfg.DashboardURL
	if err := bcfg.Validate(); err != nil {
		return nil, fmt.Errorf("billing: %w", err)
	}
	a.bcfg = bcfg
	a.gate = billing.NewGate(a.pool, bcfg, a.m, log)
	// The seat count (I-290): live subscriptions and unexpired invitations
	// against the hosts' 8 GB blocks or SEATS_TOTAL; checkout asks it, the
	// webhook tells it, /public/seats reads it.
	a.seats = &waitlist.Service{Pool: a.pool, Total: cfg.SeatsTotal, M: a.m, Log: log}
	seats := a.seats
	if paddleOn {
		a.paddle = billing.NewPaddle(bcfg, log)
		a.overage = billing.NewOverage(a.pool, a.paddle, bcfg, a.engine, a.m, log)
		a.billing = billing.NewService(a.pool, a.paddle, bcfg, seats, a.overage, log)
		a.hooks = billing.NewWebhooks(a.pool, bcfg, a.m, log)
		a.hooks.Stop = a.engine
		a.hooks.Seats = seats
		log.Info("billing enabled", "event", "billing_enabled", "enforced", bcfg.Enforce, "environment", bcfg.Environment())
	} else {
		a.overage = billing.NewOverage(a.pool, nil, bcfg, a.engine, a.m, log)
		log.Info("billing disabled until PADDLE_API_KEY is set (DECISIONS I-16, I-289)", "event", "billing_disabled")
	}
	if _, err := billing.RecordEnforcement(ctx, a.pool, bcfg.Enforce, "api", log); err != nil {
		return nil, err
	}

	parser, ok := config.NewParser()
	if !ok {
		log.Warn("nix-instantiate not found; fragments are accepted without a parse check", "event", "config_parse_unavailable")
		parser = nil
	}
	var verifier *auth.Verifier
	var users *auth.Provisioner
	if cfg.LogtoIssuer != "" {
		verifier = auth.NewVerifier(cfg.LogtoIssuer, cfg.APIResource, nil)
		users = auth.NewProvisioner(a.pool, auth.NewLogtoManagement(cfg.LogtoIssuer, cfg.LogtoM2MID, cfg.LogtoM2MSecret, nil))
	}
	a.server = httpapi.New(httpapi.Deps{
		Pool: a.pool, Verifier: verifier, Users: users, CA: a.ca, Secrets: a.sec, Engine: a.engine, Logs: a.logs, Events: a.events, Outbox: a.outbox, Unsub: unsub, Questions: a.questions,
		Parser: parser, Metrics: a.m, Registry: a.reg, Log: log, Billing: a.billing, Webhooks: a.hooks, Gate: a.gate, BillingEnforce: bcfg.Enforce,
		Gateway: httpapi.Gateway{Host: cfg.GatewayHost, Port: cfg.GatewayPort},
		Seats:   a.seats,
		Migrations: func(ctx context.Context) (int, error) {
			st, err := db.MigrateStatus(ctx, a.pool)
			return len(st.Pending), err
		},
	})
	return a, nil
}

// initCA creates the platform CA on the first start against an empty
// database. Until 2026-09-20 this was `repose-admin ca init`, a human step
// after the first deploy; on Coolify that step needs a running container,
// and a container whose api exits on ErrNotInitialised never runs long
// enough to exec into (DECISIONS I-90). Idempotent: ca.Init refuses when
// the CA exists, which is treated as done. Replicas serialise on an
// advisory lock: the one that gets it generates the keys, the others wait
// and load what it wrote. The keys are wrapped by the same Key Vault key
// as every other platform secret, so nothing about their storage changes.
func (a *App) initCA(ctx context.Context, log *slog.Logger) (*ca.CA, error) {
	if a.cfg.Dev {
		log.Warn("REPOSE_DEV=1: initialising the CA in the in-memory key vault", "event", "dev_ca")
	} else {
		log.Info("CA not initialised; generating the platform CA", "event", "ca_init")
	}
	for {
		release, ok, err := db.TryLock(ctx, a.pool, db.LockCAInit)
		if err != nil {
			return nil, err
		}
		if ok {
			err := ca.Init(ctx, a.sec)
			release()
			if err != nil && !errors.Is(err, ca.ErrAlreadyInitialised) {
				return nil, fmt.Errorf("ca init: %w", err)
			}
			return ca.Load(ctx, a.pool, a.sec)
		}
		// Another replica holds the lock and is generating; poll until its
		// secrets are readable.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		c, err := ca.Load(ctx, a.pool, a.sec)
		if err == nil {
			return c, nil
		}
		if !errors.Is(err, ca.ErrNotInitialised) {
			return nil, err
		}
	}
}

// HostCA exposes the X.509 host authority (tests, repose-admin).
func (a *App) HostCA() *pki.CA { return a.ca.X509() }

// Run serves until ctx ends, then drains.
func (a *App) Run(ctx context.Context) error {
	errCh := make(chan error, 8)
	var httpSrv, internalSrv, metricsSrv *http.Server
	var gs interface {
		GracefulStop()
		Stop()
	}
	bg, cancelBG := context.WithCancel(ctx)
	defer cancelBG()
	go a.logs.Run(bg)

	if a.cfg.Mode == "http" || a.cfg.Mode == "all" {
		if a.cfg.LogtoIssuer == "" {
			a.log.Warn("REPOSE_DEV=1 without LOGTO_ISSUER: user routes will refuse every token", "event", "dev_no_logto")
		}
		httpSrv = &http.Server{Addr: a.cfg.Listen, Handler: a.server.Handler(), ReadHeaderTimeout: 10 * time.Second}
		go func() { errCh <- serve(httpSrv, "http", a.log) }()
	}
	if a.cfg.Mode == "grpc" || a.cfg.Mode == "all" {
		var serverCert *tls.Certificate
		if a.cfg.GRPCServerCert != "" {
			c, err := tls.LoadX509KeyPair(a.cfg.GRPCServerCert, a.cfg.GRPCServerKey)
			if err != nil {
				return fmt.Errorf("grpc server certificate: %w", err)
			}
			serverCert = &c
		}
		names := a.cfg.GRPCServerNames
		if len(names) == 0 {
			names = []string{"localhost", "127.0.0.1"}
		}
		tlsCfg, err := a.hostMgr.TLSConfig(serverCert, names...)
		if err != nil {
			return err
		}
		grpcSrv := a.hostMgr.GRPCServer(tlsCfg)
		gs = grpcSrv
		ln, err := net.Listen("tcp", a.cfg.GRPCListen)
		if err != nil {
			return fmt.Errorf("grpc listen: %w", err)
		}
		a.log.Info("grpc listening", "event", "listen", "addr", a.cfg.GRPCListen)
		go func() { errCh <- grpcSrv.Serve(ln) }()
		// /internal over HTTPS with the gateway's client certificate, and
		// only that one: a host's certificate comes from the same CA
		// (I-431).
		itls := pki.RequireClientName(tlsCfg, pki.GatewayClientName)
		internalSrv = &http.Server{Addr: a.cfg.InternalListen, Handler: a.server.InternalHandler(), TLSConfig: itls, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			a.log.Info("internal listening", "event", "listen", "addr", a.cfg.InternalListen)
			err := internalSrv.ListenAndServeTLS("", "")
			if !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("internal: %w", err)
			}
		}()
		go a.engine.Run(bg)
		go a.outbox.Run(bg)
		go a.questions.Run(bg)
		go a.loops(bg)
	}
	if a.cfg.MetricsListen != "" && a.cfg.MetricsListen != "off" {
		metricsSrv = &http.Server{Addr: a.cfg.MetricsListen, Handler: a.server.MetricsHandler(), ReadHeaderTimeout: 10 * time.Second}
		go func() { errCh <- serve(metricsSrv, "metrics", a.log) }()
	}
	a.server.SetReady(true)
	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errCh:
		if runErr != nil {
			a.log.Error("listener failed", "event", "listen_fail", "err", runErr.Error())
		}
	}
	a.log.Info("shutting down", "event", "shutdown")
	a.server.SetReady(false)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if httpSrv != nil {
		_ = httpSrv.Shutdown(shutdownCtx) // best effort during drain
	}
	if internalSrv != nil {
		_ = internalSrv.Shutdown(shutdownCtx)
	}
	if metricsSrv != nil {
		_ = metricsSrv.Shutdown(shutdownCtx)
	}
	if gs != nil {
		done := make(chan struct{})
		go func() { gs.GracefulStop(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			gs.Stop()
		}
	}
	cancelBG()
	if a.otelOff != nil {
		_ = a.otelOff(shutdownCtx)
	}
	a.pool.Close()
	return runErr
}

func serve(s *http.Server, name string, log *slog.Logger) error {
	log.Info(name+" listening", "event", "listen", "addr", s.Addr)
	err := s.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}

// loops runs the periodic jobs of the grpc app.
func (a *App) loops(ctx context.Context) {
	var blob snapshots.BlobStore
	if a.cfg.BlobAccountURL != "" {
		b, err := snapshots.NewAzureBlob(a.cfg.BlobAccountURL, a.cfg.BlobContainer, nil)
		if err != nil {
			a.log.Error("blob store", "event", "blob_fail", "err", err.Error())
		} else {
			blob = b
		}
	}
	if blob == nil {
		a.log.Warn("BLOB_ACCOUNT_URL unset: snapshot expiry only marks rows", "event", "blob_unset")
		blob = markOnly{}
	}
	expiry := snapshots.New(a.pool, blob, a.m, a.log)
	go expiry.Run(ctx, 24*time.Hour)
	rollup := billing.NewRollup(a.pool, a.m, a.log)
	dunning := billing.NewDunning(a.pool, a.engine, a.events, a.bcfg, a.m, a.log)
	bump := basebump.New(a.pool, a.engine, a.events, a.log)
	idleWarn := &idle.Warner{Pool: a.pool, Events: a.events}
	reaper := &temp.Reaper{Pool: a.pool, Engine: a.engine, Events: a.events, Log: a.log}
	inviter := &waitlist.Inviter{Pool: a.pool, Total: a.cfg.SeatsTotal, M: a.m, Log: a.log}
	a.engine.SetOnFinished(bump.OnOpFinished)
	go bump.Run(ctx)
	// The abuse gauges (BusyUnattended, EgressHigh, held projects) are
	// read-only aggregates; every replica exports them and the alerts take
	// the max (DECISIONS I-239).
	abuseTick := time.NewTicker(5 * time.Minute)
	defer abuseTick.Stop()
	refreshAbuse := func() {
		if err := a.abuse.Refresh(ctx); err != nil && ctx.Err() == nil {
			a.log.Error("abuse gauges", "event", "abuse_refresh_fail", "err", err.Error())
		}
	}
	refreshAbuse()
	sweep := time.NewTicker(15 * time.Second)
	hourly := time.NewTicker(time.Minute)
	daily := time.NewTicker(24 * time.Hour)
	limiters := time.NewTicker(10 * time.Minute)
	defer sweep.Stop()
	defer hourly.Stop()
	defer daily.Stop()
	defer limiters.Stop()
	lastRollup := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-sweep.C:
			if _, err := a.hostMgr.Sweep(ctx); err != nil && ctx.Err() == nil {
				a.log.Error("host sweep", "event", "sweep_fail", "err", err.Error())
			}
		case now := <-hourly.C:
			// The seats waitlist expires holds that ran out and invites
			// the oldest waiting users while a seat is free, every minute,
			// one replica at a time (DECISIONS I-269, I-290).
			if release, ok, err := db.TryLock(ctx, a.pool, db.LockWaitlistTick); err == nil && ok {
				invited, expired, err := inviter.Run(ctx, now)
				if err != nil && ctx.Err() == nil {
					a.log.Error("waitlist tick", "event", "waitlist_invite_fail", "err", err.Error())
				}
				if invited > 0 {
					a.log.Info("waitlisted users invited", "event", "waitlist_invite", "count", invited)
				}
				if expired > 0 {
					a.log.Info("waitlist holds expired", "event", "waitlist_expire", "count", expired)
				}
				release()
			}
			// Temporary machines: the hour's warning and the destroy at
			// expiry, every minute, one replica at a time (DECISIONS
			// I-347, I-350). The daily ticker would not do: a redeploy
			// restarts it (I-112).
			if release, ok, err := db.TryLock(ctx, a.pool, db.LockSweeper); err == nil && ok {
				res, err := reaper.Run(ctx, now)
				if err != nil && ctx.Err() == nil {
					a.log.Error("temporary machines", "event", "temp_reap_fail", "err", err.Error())
				}
				if res.Warned > 0 || res.Destroyed > 0 {
					a.log.Info("temporary machines", "event", "temp_reap", "warned", res.Warned, "destroyed", res.Destroyed, "waiting", res.Waiting)
				}
				release()
			}
			// Rollup at :05 past each hour, under the advisory lock.
			if now.Minute() < 5 || now.Sub(lastRollup) < 50*time.Minute {
				continue
			}
			release, ok, err := db.TryLock(ctx, a.pool, db.LockRollup)
			if err != nil || !ok {
				if !ok {
					a.log.Info("rollup: not leader", "event", "rollup_skip")
				}
				continue
			}
			if _, err := rollup.Due(ctx); err != nil && ctx.Err() == nil {
				a.log.Error("rollup", "event", "rollup_fail", "err", err.Error())
			}
			// Past-due accounts are stopped from the same hourly tick and
			// under the same lock, so only one replica acts (§5.6); the
			// egress overage line and hard stop run beside it (I-289).
			if _, err := dunning.Run(ctx); err != nil && ctx.Err() == nil {
				a.log.Error("dunning", "event", "dunning_fail", "err", err.Error())
			}
			if _, _, err := a.overage.Run(ctx); err != nil && ctx.Err() == nil {
				a.log.Error("overage", "event", "overage_fail", "err", err.Error())
			}
			// The idle-cost warning: one notification per idle stretch,
			// never a stop (DECISIONS I-262, R1-5).
			if n, err := idleWarn.Run(ctx, now); err != nil && ctx.Err() == nil {
				a.log.Error("idle warning", "event", "idle_warn_fail", "err", err.Error())
			} else if n > 0 {
				a.log.Info("idle warnings raised", "event", "idle_warn", "count", n)
			}
			release()
			lastRollup = now
		case <-daily.C:
			release, ok, err := db.TryLock(ctx, a.pool, db.LockCertCleanup)
			if err != nil || !ok {
				continue
			}
			if n, err := a.ca.Prune(ctx, 30*24*time.Hour); err == nil {
				a.log.Info("certificates pruned", "event", "cert_prune", "count", n)
			}
			if err := db.EnsurePartitions(ctx, a.pool, time.Now()); err != nil {
				a.m.PartitionDropFailTotal.Inc()
				a.log.Error("partitions", "event", "partition_fail", "err", err.Error())
			}
			if dropped, err := db.DropExpiredPartitions(ctx, a.pool, time.Now()); err != nil {
				a.m.PartitionDropFailTotal.Inc()
				a.log.Error("partition drop", "event", "partition_drop_fail", "err", err.Error())
			} else if len(dropped) > 0 {
				a.log.Info("partitions dropped", "event", "partition_drop", "count", len(dropped))
			}
			if n, err := a.logs.Trim(ctx, 20); err == nil && n > 0 {
				a.log.Info("build logs trimmed", "event", "buildlog_trim", "rows", n)
			}
			release()
		case <-limiters.C:
			a.server.SweepLimiters()
		case <-abuseTick.C:
			refreshAbuse()
		}
	}
}

// markOnly is the blob store when no account is configured: rows are
// marked deleted and the Blob lifecycle rules (workstream 11) reclaim.
type markOnly struct{}

func (markOnly) Delete(context.Context, string) error { return nil }
