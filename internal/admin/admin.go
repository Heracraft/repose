// Package admin implements repose-admin (DECISIONS I-9, docs/ops/
// RUNBOOK.md): the operator CLI that talks to Postgres directly from
// inside the Coolify network. Long operations are ops rows the api-grpc
// process drives; the command enqueues and waits.
package admin

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/ca"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/secrets"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/fakes/kv"
	"github.com/heracraft/repose/internal/obs"
)

// Env is what the commands need from the process.
type Env struct {
	Stdout io.Writer
	Stderr io.Writer
	// KV overrides the Key Vault (tests); nil reads KEYVAULT_URL, or the
	// in-memory fake under REPOSE_DEV=1.
	KV secrets.KeyVault
	// Actor is recorded in audit_log.
	Actor string
	// RevCheck overrides how `base publish` checks its rev against the
	// repository (tests); nil asks GitHub (I-173).
	RevCheck RevChecker
	pool     *db.Pool
	sec      *secrets.Store
	ca       *ca.CA
	eng      *ops.Engine
	log      *slog.Logger
}

// Usage is the command list.
const Usage = `repose-admin <command> [args]

  db        migrate | status | rollback --to NNNN | down [n] | verify
  hosts     add --name N [--provider p --sku s --region r] [--reissue] | list | drain N | undrain N | retire N | mark-lost N | reconcile N [--fix] | rotate-cert N | rotate-wg N | smoke N
  projects  create --user HANDLE --name N [--class small|large|xl] [--host N] [--create-user] [--wait] | destroy ID|SLUG (I-113)
            list [--host N] [--sort disk|closure] | show ID|SLUG | start ID | stop ID [--no-snapshot] | restart ID | snapshot ID | resize ID --bytes B
            move ID --to N | restore ID [--snapshot SID | --latest] [--to N] | destroy ID [--wait=false] | exec ID -- ARGV...
  exec      ID -- ARGV...
  users     list | show HANDLE | suspend HANDLE --reason R | unsuspend HANDLE | exempt HANDLE | limits HANDLE [--projects N (above 100)] [--xl N] | rename OLD NEW [--github-login L] (no projects)
  certs     revoke --user HANDLE
  secrets   rewrap
  billing   show HANDLE | rollup [--hour 2026-09-17T14] | explain PROJECT 2026-09-17T14 | suspend HANDLE | unsuspend HANDLE
            overage-now HANDLE (send this period's egress line to Polar now)
            polar-bootstrap [--webhook-url URL] [--no-webhook] [--production]  (POLAR_ACCESS_TOKEN and POLAR_ENVIRONMENT in the environment)
  base      publish --rev SHA40 --changelog TEXT [--version V] [--security] [--repo URL] [--branch main] [--unverified-rev] | release ... | list | status V | rollback V
  ca        init | show | rotate [--user] [--host] | sign-host --principal P... --pubkey FILE | sign-client --name NAME [--operator] [--csr FILE] [--out DIR]
            sign-server --name NAME[,NAME...] [--ttl 43800h] [--csr FILE] [--out DIR]
  operator-cert --pubkey FILE [--ttl 8h] [--name NAME]
  edge      init --endpoint HOST:PORT --pubkey WGPUB [--out DIR] | loki [URL]
  audit     [--user HANDLE] [--since 24h] [--action A]
  abuse     list [--all] | clear ID|SLUG   (automatic miner stops, I-239; suspending the user is users suspend)
  waitlist  list | admit HANDLE | admit --next N   (seats waitlist, I-269/I-290; admit invites: a 72 h seat hold and the email; the api invites by itself as seats free up)
  seats     (total, held, free, waiting, and whether the total is SEATS_TOTAL or the hosts', I-290)
  ops       list [--project ID] [--host N] [--state S] | log OPID
  version
`

// ErrUsage means the arguments were wrong; main prints Usage.
var ErrUsage = errors.New("usage")

// Run executes one command.
func Run(ctx context.Context, e *Env, args []string) error {
	if e.Stdout == nil {
		e.Stdout = os.Stdout
	}
	if e.Stderr == nil {
		e.Stderr = os.Stderr
	}
	if e.Actor == "" {
		e.Actor = "admin"
		if u := os.Getenv("USER"); u != "" {
			e.Actor = "admin:" + u
		}
	}
	if len(args) == 0 {
		return ErrUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "db":
		return e.db(ctx, rest)
	case "hosts":
		return e.hosts(ctx, rest)
	case "projects":
		return e.projects(ctx, rest)
	case "exec":
		return e.projects(ctx, append([]string{"exec"}, rest...))
	case "users":
		return e.users(ctx, rest)
	case "certs":
		return e.certs(ctx, rest)
	case "secrets":
		return e.secretsCmd(ctx, rest)
	case "billing":
		return e.billing(ctx, rest)
	case "base":
		return e.base(ctx, rest)
	case "ca":
		return e.caCmd(ctx, rest)
	case "operator-cert":
		return e.operatorCert(ctx, rest)
	case "edge":
		return e.edge(ctx, rest)
	case "audit":
		return e.audit(ctx, rest)
	case "abuse":
		return e.abuseCmd(ctx, rest)
	case "waitlist":
		return e.waitlistCmd(ctx, rest)
	case "seats":
		return e.seatsCmd(ctx, rest)
	case "ops":
		return e.opsCmd(ctx, rest)
	}
	return fmt.Errorf("%w: unknown command %q", ErrUsage, cmd)
}

func (e *Env) connect(ctx context.Context) error {
	if e.pool != nil {
		return nil
	}
	u := os.Getenv("DATABASE_URL")
	if u == "" {
		return errors.New("DATABASE_URL is required")
	}
	pool, err := db.Connect(ctx, u)
	if err != nil {
		return err
	}
	e.pool = pool
	return nil
}

// SetPool injects a pool (tests).
func (e *Env) SetPool(p *db.Pool) { e.pool = p }

func (e *Env) secretsStore(ctx context.Context) (*secrets.Store, error) {
	if e.sec != nil {
		return e.sec, nil
	}
	if err := e.connect(ctx); err != nil {
		return nil, err
	}
	k := e.KV
	if k == nil {
		if url := os.Getenv("KEYVAULT_URL"); url != "" {
			az, err := secrets.NewAzureKV(url, envOr("KEYVAULT_KEY_NAME", "repose-dek-kek"), nil)
			if err != nil {
				return nil, err
			}
			k = az
		} else if os.Getenv("REPOSE_DEV") == "1" {
			k = kv.New()
		} else {
			return nil, errors.New("KEYVAULT_URL is required (or REPOSE_DEV=1)")
		}
	}
	e.sec = secrets.New(e.pool, k)
	return e.sec, nil
}

func (e *Env) loadCA(ctx context.Context) (*ca.CA, error) {
	if e.ca != nil {
		return e.ca, nil
	}
	sec, err := e.secretsStore(ctx)
	if err != nil {
		return nil, err
	}
	c, err := ca.Load(ctx, e.pool, sec)
	if err != nil {
		return nil, err
	}
	e.ca = c
	return c, nil
}

func (e *Env) engine(ctx context.Context) (*ops.Engine, error) {
	if e.eng != nil {
		return e.eng, nil
	}
	if err := e.connect(ctx); err != nil {
		return nil, err
	}
	// The admin only enqueues; the api-grpc process drives, so no sender,
	// CA or secrets are needed here.
	e.eng = ops.New(e.pool, nil, nil, nil, nil, nil, metrics.NewNop(), obs.NewLogger(obs.LogOptions{Component: obs.ComponentAdmin, Writer: e.Stderr, Level: slog.LevelError + 1}), ops.Config{})
	return e.eng, nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// audited writes the audit_log row and the admin_action line that
// docs/workstreams/10-observability.md §5 requires: the row is the record an
// auditor reads, the line is what a Loki query over the incident window
// shows. The detail map is not logged; it is the row's business, and it can
// carry a target's own values.
func (e *Env) audited(ctx context.Context, action, target string, detail map[string]any) (uuid.UUID, error) {
	id, err := store.Audit(ctx, e.pool, e.Actor, action, target, detail)
	result := "ok"
	if err != nil {
		result = "error"
	}
	e.logger().Log(ctx, obs.LevelNotice, "admin action", "event", "admin_action",
		"action", action, "audit_id", id.String(), "result", result)
	return id, err
}

// logger is built on first use, so a test Env needs no wiring.
func (e *Env) logger() *slog.Logger {
	if e.log == nil {
		w := e.Stderr
		if w == nil {
			w = io.Discard
		}
		e.log = obs.NewLogger(obs.LogOptions{Component: obs.ComponentAdmin, Writer: w})
	}
	return e.log
}

func (e *Env) table(rows [][]string) {
	tw := tabwriter.NewWriter(e.Stdout, 0, 2, 2, ' ', 0)
	for _, r := range rows {
		_, _ = fmt.Fprintln(tw, strings.Join(r, "\t")) // stdout
	}
	_ = tw.Flush() // stdout
}

func flagsFor(name string, args []string, define func(fs *flag.FlagSet)) (*flag.FlagSet, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	define(fs)
	// Allow flags after positionals: reorder so the flag set sees flags first.
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i:]...)
			break
		}
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				if f := fs.Lookup(strings.TrimLeft(a, "-")); f != nil {
					if b, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !b.IsBoolFlag() {
						flags = append(flags, args[i+1])
						i++
					}
				}
			}
			continue
		}
		pos = append(pos, a)
	}
	if err := fs.Parse(append(flags, pos...)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUsage, err)
	}
	return fs, nil
}

// waitOp polls an op to completion, printing progress.
func (e *Env) waitOp(ctx context.Context, id uuid.UUID, timeout time.Duration) (*store.Op, error) {
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		op, err := store.GetOp(ctx, e.pool, id)
		if err != nil {
			return nil, err
		}
		cur := fmt.Sprintf("%s step %d", op.State, op.Step)
		if cur != last {
			_, _ = fmt.Fprintf(e.Stderr, "op %s: %s\n", id, cur) // stderr
			last = cur
		}
		if op.State == "done" || op.State == "error" {
			return op, nil
		}
		if time.Now().After(deadline) {
			return op, fmt.Errorf("op %s still %s after %s (is api-grpc running?)", id, op.State, timeout)
		}
		select {
		case <-ctx.Done():
			return op, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (e *Env) enqueue(ctx context.Context, n ops.NewOp, wait bool) (*store.Op, error) {
	eng, err := e.engine(ctx)
	if err != nil {
		return nil, err
	}
	var id uuid.UUID
	err = db.InTx(ctx, e.pool, func(tx db.Tx) error {
		var err error
		id, err = eng.Enqueue(ctx, tx, n, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(e.Stdout, "op %s enqueued\n", id)
	if !wait {
		return store.GetOp(ctx, e.pool, id)
	}
	op, err := e.waitOp(ctx, id, 45*time.Minute)
	if err != nil {
		return op, err
	}
	if op.State == "error" {
		return op, fmt.Errorf("op %s failed: %v", id, op.Error)
	}
	return op, nil
}

func (e *Env) findProject(ctx context.Context, ref string) (*store.Project, error) {
	if id, err := uuid.Parse(ref); err == nil {
		return store.GetProject(ctx, e.pool, id)
	}
	slug, handle, ok := strings.Cut(ref, ".")
	if ok {
		return store.GetProjectByLogin(ctx, e.pool, slug, handle)
	}
	rows, err := e.pool.Query(ctx, "select id from projects where slug = $1 and destroyed_at is null", ref)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	switch len(ids) {
	case 0:
		return nil, fmt.Errorf("no project %q", ref)
	case 1:
		return store.GetProject(ctx, e.pool, ids[0])
	}
	return nil, fmt.Errorf("%d projects have slug %q; use <slug>.<handle> or the id", len(ids), ref)
}

func (e *Env) findHost(ctx context.Context, ref string) (*store.Host, error) {
	if id, err := uuid.Parse(ref); err == nil {
		return store.GetHost(ctx, e.pool, id)
	}
	h, err := store.GetHostByName(ctx, e.pool, ref)
	if errors.Is(err, db.ErrNotFound) {
		return nil, fmt.Errorf("no host %q", ref)
	}
	return h, err
}

func (e *Env) findUser(ctx context.Context, handle string) (*store.User, error) {
	u, err := store.GetUserByHandle(ctx, e.pool, handle)
	if errors.Is(err, db.ErrNotFound) {
		return nil, fmt.Errorf("no user %q", handle)
	}
	return u, err
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04")
}

func gb(b int64) string { return fmt.Sprintf("%.1f GB", float64(b)/(1<<30)) }
