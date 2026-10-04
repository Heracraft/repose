package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/curve25519"

	"github.com/heracraft/repose/internal/api/auth"
	"github.com/heracraft/repose/internal/api/ca"
	"github.com/heracraft/repose/internal/api/hostmgr"
	httpapi "github.com/heracraft/repose/internal/api/http"
	"github.com/heracraft/repose/internal/api/meter"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/pki"
	"github.com/heracraft/repose/internal/api/scheduler"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/ca/sshca"
	"github.com/heracraft/repose/internal/db"
)

// --- db -----------------------------------------------------------------

func (e *Env) db(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return ErrUsage
	}
	if err := e.connect(ctx); err != nil {
		return err
	}
	switch args[0] {
	case "migrate":
		applied, err := db.MigrateUp(ctx, e.pool)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stdout, "applied %d migration(s): %v\n", len(applied), applied)
		return nil
	case "status":
		st, err := db.MigrateStatus(ctx, e.pool)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stdout, "applied: %v\npending: %v\n", st.Applied, st.Pending)
		return nil
	case "down":
		n := 1
		if len(args) > 1 {
			v, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("%w: down takes a count", ErrUsage)
			}
			n = v
		}
		reverted, err := db.MigrateDown(ctx, e.pool, n)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stdout, "reverted %v\n", reverted)
		return nil
	case "rollback":
		fs, err := flagsFor("rollback", args[1:], func(fs *flag.FlagSet) { fs.Int("to", -1, "version to keep") })
		if err != nil {
			return err
		}
		to := fs.Lookup("to").Value.(flag.Getter).Get().(int)
		if to < 0 {
			return fmt.Errorf("%w: rollback needs --to NNNN", ErrUsage)
		}
		reverted, err := db.MigrateTo(ctx, e.pool, to)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stdout, "reverted %v; newest applied is now %d\n", reverted, to)
		return nil
	case "verify":
		var rows [][]string
		rows = append(rows, []string{"TABLE", "ROWS"})
		for _, t := range []string{"users", "hosts", "projects", "config_revisions", "ops", "secrets", "certificates", "snapshots", "events", "usage_hours", "audit_log", "base_versions"} {
			var n int64
			if err := e.pool.QueryRow(ctx, "select count(*) from "+t).Scan(&n); err != nil {
				return fmt.Errorf("%s: %w", t, err)
			}
			rows = append(rows, []string{t, strconv.FormatInt(n, 10)})
		}
		var last *time.Time
		_ = e.pool.QueryRow(ctx, "select max(hour) from usage_hours").Scan(&last)
		rows = append(rows, []string{"last rollup hour", fmtTime(last)})
		e.table(rows)
		return nil
	}
	return fmt.Errorf("%w: db %s", ErrUsage, args[0])
}

// --- hosts ------------------------------------------------------------------

func (e *Env) hosts(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return ErrUsage
	}
	if err := e.connect(ctx); err != nil {
		return err
	}
	switch args[0] {
	case "add":
		fs, err := flagsFor("hosts add", args[1:], func(fs *flag.FlagSet) {
			fs.String("name", "", "host name, e.g. host-01")
			fs.String("provider", "azure", "provider")
			fs.String("sku", "", "sku")
			fs.String("region", "", "region")
			fs.Bool("reissue", false, "mint a new token for a re-imaged host")
		})
		if err != nil {
			return err
		}
		name := fs.Lookup("name").Value.String()
		if name == "" && fs.NArg() > 0 {
			name = fs.Arg(0)
		}
		if name == "" {
			return fmt.Errorf("%w: hosts add needs --name", ErrUsage)
		}
		reissue := fs.Lookup("reissue").Value.String() == "true"
		token, err := hostmgr.MintJoinToken(ctx, e.pool, name, fs.Lookup("provider").Value.String(), fs.Lookup("sku").Value.String(), fs.Lookup("region").Value.String(), reissue)
		if err != nil {
			return err
		}
		if _, err := e.audited(ctx, "host_add", name, map[string]any{"reissue": reissue}); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stderr, "join token for %s (valid %s, single use; put it in infra/azure/prod/prod.local.tfvars or /run/repose/join-token):\n", name, hostmgr.JoinTokenValidity)
		_, _ = fmt.Fprintln(e.Stdout, token) // stdout
		return nil
	case "list":
		hosts, err := store.ListHosts(ctx, e.pool)
		if err != nil {
			return err
		}
		rows := [][]string{{"NAME", "STATE", "SKU", "MEM", "FREE", "RESERVED", "GUESTS", "POOL FREE", "HEARTBEAT", "GUEST CIDR"}}
		for _, h := range hosts {
			var reserved int64
			var guests int
			_ = e.pool.QueryRow(ctx, "select reserved_bytes, guests from host_reservations where host_id = $1", h.ID).Scan(&reserved, &guests)
			sku := "-"
			if h.SKU != nil {
				sku = *h.SKU
			}
			cidr := "-"
			if h.GuestCIDR != nil {
				cidr = h.GuestCIDR.String()
			}
			state := h.State
			if h.Draining && state == "ready" {
				state = "draining"
			}
			rows = append(rows, []string{h.Name, state, sku, gb(h.MemBytes), gb(h.FreeMemBytes), gb(reserved), strconv.Itoa(guests), gb(h.PoolFreeBytes), fmtTime(h.LastHeartbeatAt), cidr})
		}
		e.table(rows)
		return nil
	case "drain", "undrain":
		if len(args) < 2 {
			return ErrUsage
		}
		h, err := e.findHost(ctx, args[1])
		if err != nil {
			return err
		}
		if args[0] == "undrain" {
			if _, err := e.pool.Exec(ctx, "update hosts set draining = false, state = case when state = 'draining' then 'ready' else state end where id = $1", h.ID); err != nil {
				return err
			}
			_, err = e.audited(ctx, "host_undrain", h.Name, nil)
			_, _ = fmt.Fprintf(e.Stdout, "%s marked ready on the api side. Drain has no inverse on the wire (docs/interfaces/grpc-hostd.md): hostd keeps refusing placements and reports draining=true in every heartbeat until `hostd undrain` runs on the host or hostd restarts.\n", h.Name)
			return err
		}
		if _, err := e.pool.Exec(ctx, "update hosts set draining = true, state = case when state = 'ready' then 'draining' else state end where id = $1", h.ID); err != nil {
			return err
		}
		if _, err := e.audited(ctx, "host_drain", h.Name, nil); err != nil {
			return err
		}
		hid := h.ID
		_, err = e.enqueue(ctx, ops.NewOp{Kind: ops.KindDrain, HostID: &hid, Phases: ops.PlanDrain()}, false)
		_, _ = fmt.Fprintf(e.Stdout, "%s draining: no new placements; Drain sent to hostd\n", h.Name)
		return err
	case "retire":
		if len(args) < 2 {
			return ErrUsage
		}
		h, err := e.findHost(ctx, args[1])
		if err != nil {
			return err
		}
		projects, err := store.ListProjectsOnHost(ctx, e.pool, h.ID)
		if err != nil {
			return err
		}
		if len(projects) > 0 {
			return fmt.Errorf("%s still holds %d project(s); move them first (projects move)", h.Name, len(projects))
		}
		if _, err := e.pool.Exec(ctx, "update hosts set state = 'retired', draining = true, cert_serial = null, prev_cert_serial = null where id = $1", h.ID); err != nil {
			return err
		}
		_, err = e.audited(ctx, "host_retire", h.Name, nil)
		_, _ = fmt.Fprintf(e.Stdout, "%s retired\n", h.Name)
		return err
	case "mark-lost":
		if len(args) < 2 {
			return ErrUsage
		}
		h, err := e.findHost(ctx, args[1])
		if err != nil {
			return err
		}
		projects, err := store.ListProjectsOnHost(ctx, e.pool, h.ID)
		if err != nil {
			return err
		}
		if _, err := e.pool.Exec(ctx, "update hosts set state = 'lost', draining = true, cert_serial = null, prev_cert_serial = null where id = $1", h.ID); err != nil {
			return err
		}
		for _, p := range projects {
			if _, err := e.pool.Exec(ctx, "update projects set state = 'error', last_error = 'host_lost', host_unreachable = true where id = $1", p.ID); err != nil {
				return err
			}
			if _, err := e.pool.Exec(ctx, "insert into events (id, project_id, ts, ts_second, kind, summary, source) values ($1, $2, now(), extract(epoch from now())::bigint, 'host_moved', $3, 'api')", store.NewID(), p.ID, "the host holding "+p.Slug+" was lost; the platform will restore it from the latest snapshot"); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(e.Stdout, "%s (%s): error host_lost; restore with: repose-admin projects restore %s --latest --to <host>\n", p.Slug, p.ID, p.ID)
		}
		_, err = e.audited(ctx, "host_mark_lost", h.Name, map[string]any{"projects": len(projects)})
		return err
	case "reconcile":
		fs, err := flagsFor("reconcile", args[1:], func(fs *flag.FlagSet) { fs.Bool("fix", false, "apply the host's view") })
		if err != nil || fs.NArg() < 1 {
			return ErrUsage
		}
		h, err := e.findHost(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		fix := fs.Lookup("fix").Value.String() == "true"
		projects, err := store.ListProjectsOnHost(ctx, e.pool, h.ID)
		if err != nil {
			return err
		}
		rows := [][]string{{"PROJECT", "API STATE", "HOST STATE (last sample)", "AGE", "ACTION"}}
		for _, p := range projects {
			l, ok, err := meter.LatestSample(ctx, e.pool, p.ID)
			if err != nil {
				return err
			}
			hostState, age, action := "-", "-", ""
			if ok {
				hostState = l.State
				age = time.Since(l.TS).Round(time.Second).String()
				open, _ := store.OpenOpsForProject(ctx, e.pool, p.ID)
				if len(open) == 0 && time.Since(l.TS) < 3*time.Minute && l.State != p.State && (l.State == "running" || l.State == "stopped") && (p.State == "running" || p.State == "stopped" || p.State == "error") {
					action = "set " + l.State
					if fix {
						if err := store.SetProjectState(ctx, e.pool, p.ID, l.State); err != nil {
							return err
						}
						action += " (applied)"
					}
				}
			}
			rows = append(rows, []string{p.Slug, p.State, hostState, age, action})
		}
		e.table(rows)
		var reserved int64
		_ = e.pool.QueryRow(ctx, "select coalesce(reserved_bytes,0) from host_reservations where host_id = $1", h.ID).Scan(&reserved)
		_, _ = fmt.Fprintf(e.Stdout, "reserved memory recomputed: %s\n", gb(reserved))
		_, err = e.audited(ctx, "host_reconcile", h.Name, map[string]any{"fix": fix})
		return err
	case "rotate-cert":
		if len(args) < 2 {
			return ErrUsage
		}
		h, err := e.findHost(ctx, args[1])
		if err != nil {
			return err
		}
		if h.State == "lost" || h.State == "retired" {
			// The api refuses these hosts whatever certificate they hold
			// (I-432); a re-imaged machine joins again with a new token.
			return fmt.Errorf("%s is %s; a re-imaged machine registers again with `hosts add --name %s --reissue`", h.Name, h.State, h.Name)
		}
		c, err := e.loadCA(ctx)
		if err != nil {
			return err
		}
		certPEM, keyPEM, serial, err := c.X509().IssueClient(h.ID.String(), pki.HostCertValidity)
		if err != nil {
			return err
		}
		if _, err := e.pool.Exec(ctx, "update hosts set prev_cert_serial = cert_serial, cert_serial = $2, cert_expires_at = $3 where id = $1", h.ID, serial, time.Now().Add(pki.HostCertValidity)); err != nil {
			return err
		}
		if _, err := e.audited(ctx, "host_rotate_cert", h.Name, map[string]any{"serial": serial}); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stderr, "write these to /var/lib/repose/hostd/cert.pem and key.pem on %s (via the edge jump), then systemctl restart hostd\n", h.Name)
		_, _ = fmt.Fprint(e.Stdout, string(certPEM)) // stdout
		_, _ = fmt.Fprint(e.Stdout, string(keyPEM))
		return nil
	case "rotate-wg":
		if len(args) < 2 {
			return ErrUsage
		}
		h, err := e.findHost(ctx, args[1])
		if err != nil {
			return err
		}
		var k [32]byte
		if _, err := rand.Read(k[:]); err != nil {
			return err
		}
		k[0] &= 248
		k[31] &= 127
		k[31] |= 64
		pub, err := curve25519.X25519(k[:], curve25519.Basepoint)
		if err != nil {
			return err
		}
		if _, err := e.pool.Exec(ctx, "update hosts set wg_pubkey = $2 where id = $1", h.ID, base64.StdEncoding.EncodeToString(pub)); err != nil {
			return err
		}
		if _, err := e.audited(ctx, "host_rotate_wg", h.Name, nil); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stderr, "new public key stored; the edge's wgsync picks it up within 30 s. Put this private key in host.json wg.private_key on %s and systemctl restart repose-host-net:\n", h.Name)
		_, _ = fmt.Fprintln(e.Stdout, base64.StdEncoding.EncodeToString(k[:])) // stdout
		return nil
	case "smoke":
		if len(args) < 2 {
			return ErrUsage
		}
		return e.smoke(ctx, args[1])
	}
	return fmt.Errorf("%w: hosts %s", ErrUsage, args[0])
}

// smoke creates, starts, snapshots and destroys a throwaway guest on one
// host under the exempt repose-smoke user.
func (e *Env) smoke(ctx context.Context, hostRef string) error {
	h, err := e.findHost(ctx, hostRef)
	if err != nil {
		return err
	}
	u, err := store.GetUserByHandle(ctx, e.pool, "repose-smoke")
	if errors.Is(err, db.ErrNotFound) {
		u, err = e.createExemptUser(ctx, "repose-smoke")
	}
	if err != nil {
		return err
	}
	name := "smoke-" + time.Now().UTC().Format("20060102-150405")
	pid, opID, err := e.createProject(ctx, u, name, "small", h)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.Stdout, "smoke project %s (%s) on %s\n", name, pid, h.Name)
	steps := []struct {
		label string
		op    func() (uuid.UUID, error)
	}{
		{"create", func() (uuid.UUID, error) { return opID, nil }},
		{"snapshot", func() (uuid.UUID, error) {
			op, err := e.enqueue(ctx, ops.NewOp{Kind: ops.KindSnapshot, ProjectID: &pid, Params: map[string]any{"reason": "manual"}, Phases: ops.PlanSnapshot()}, false)
			if err != nil {
				return uuid.Nil, err
			}
			return op.ID, nil
		}},
		{"stop", func() (uuid.UUID, error) {
			op, err := e.enqueue(ctx, ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Params: map[string]any{"snapshot": false}, Phases: ops.PlanStop()}, false)
			if err != nil {
				return uuid.Nil, err
			}
			return op.ID, nil
		}},
		{"start", func() (uuid.UUID, error) {
			op, err := e.enqueue(ctx, ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(false)}, false)
			if err != nil {
				return uuid.Nil, err
			}
			return op.ID, nil
		}},
		{"destroy", func() (uuid.UUID, error) {
			p, err := store.GetProject(ctx, e.pool, pid)
			if err != nil {
				return uuid.Nil, err
			}
			op, err := e.enqueue(ctx, ops.NewOp{Kind: ops.KindDestroy, ProjectID: &pid, Phases: ops.PlanDestroy(p)}, false)
			if err != nil {
				return uuid.Nil, err
			}
			return op.ID, nil
		}},
	}
	for _, st := range steps {
		start := time.Now()
		id, err := st.op()
		if err != nil {
			return fmt.Errorf("%s: %w", st.label, err)
		}
		op, err := e.waitOp(ctx, id, 45*time.Minute)
		if err != nil {
			return fmt.Errorf("%s: %w", st.label, err)
		}
		if op.State != "done" {
			return fmt.Errorf("%s failed: %v", st.label, op.Error)
		}
		_, _ = fmt.Fprintf(e.Stdout, "%-9s ok in %s\n", st.label, time.Since(start).Round(time.Millisecond))
	}
	_, err = e.audited(ctx, "host_smoke", h.Name, map[string]any{"project_id": pid.String()})
	return err
}

var handleRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// createExemptUser inserts a billing-exempt account with the limits
// `hosts smoke` gives repose-smoke (DECISIONS I-16, I-113): no Logto
// identity, so nothing can sign in as it; it exists for guests an operator
// drives through repose-admin.
func (e *Env) createExemptUser(ctx context.Context, handle string) (*store.User, error) {
	if !handleRe.MatchString(handle) {
		return nil, fmt.Errorf("%w: handle must match [a-z0-9-]{1,32}", ErrUsage)
	}
	uid := store.NewID()
	if _, err := e.pool.Exec(ctx, "insert into users (id, handle, billing_status, has_card, project_limit, xl_limit) values ($1, $2, 'exempt', true, 100, 100)", uid, handle); err != nil {
		return nil, err
	}
	return store.GetUser(ctx, e.pool, uid)
}

// createProject inserts the project and its empty revision the way POST
// /projects does (05 §5.3) and enqueues the create op, pinned to host when
// one is given; the api-grpc process drives it.
func (e *Env) createProject(ctx context.Context, u *store.User, name, class string, host *store.Host) (uuid.UUID, uuid.UUID, error) {
	if !projectNameRe.MatchString(name) {
		return uuid.Nil, uuid.Nil, fmt.Errorf("%w: project name must match [A-Za-z0-9._-]{1,64}", ErrUsage)
	}
	slug := httpapi.Slug(name)
	if slug == "" {
		return uuid.Nil, uuid.Nil, fmt.Errorf("%w: %q has no slug", ErrUsage, name)
	}
	pid, rid := store.NewID(), store.NewID()
	eng, err := e.engine(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	params := map[string]any{}
	if host != nil {
		params["host_id"] = host.ID.String()
	}
	var opID uuid.UUID
	err = db.InTx(ctx, e.pool, func(tx db.Tx) error {
		if _, err := tx.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes, config_revision_id) values ($1, $2, $3, $4, $5, 'creating', $6, $7)", pid, u.ID, name, slug, class, scheduler.DefaultVolume(class), rid); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "insert into config_revisions (id, project_id, fragment, status) values ($1, $2, $3, 'building')", rid, pid, httpapi.DefaultFragment); err != nil {
			return err
		}
		var err error
		opID, err = eng.Enqueue(ctx, tx, ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Params: params, Phases: ops.PlanCreate()}, false)
		return err
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return pid, opID, nil
}

var projectNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// --- projects ---------------------------------------------------------------

func (e *Env) projects(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return ErrUsage
	}
	if err := e.connect(ctx); err != nil {
		return err
	}
	switch args[0] {
	case "create":
		fs, err := flagsFor("create", args[1:], func(fs *flag.FlagSet) {
			fs.String("user", "", "handle of the owning user")
			fs.Bool("create-user", false, "create the user as a billing-exempt account when the handle does not exist (DECISIONS I-16, I-113)")
			fs.String("name", "", "project name ([A-Za-z0-9._-]{1,64})")
			fs.String("class", "small", "small|large|xl")
			fs.String("host", "", "pin the placement to this host instead of the scheduler's pick")
			fs.Bool("wait", false, "wait for the create op (build and boot) to finish")
		})
		if err != nil {
			return err
		}
		handle, name := fs.Lookup("user").Value.String(), fs.Lookup("name").Value.String()
		class := fs.Lookup("class").Value.String()
		if handle == "" || name == "" {
			return fmt.Errorf("%w: projects create needs --user and --name", ErrUsage)
		}
		if !scheduler.ValidClass(class) {
			return fmt.Errorf("%w: class is small, large or xl", ErrUsage)
		}
		var host *store.Host
		if hn := fs.Lookup("host").Value.String(); hn != "" {
			if host, err = e.findHost(ctx, hn); err != nil {
				return err
			}
		}
		u, err := e.findUser(ctx, handle)
		if err != nil {
			if fs.Lookup("create-user").Value.String() != "true" {
				return err
			}
			if u, err = e.createExemptUser(ctx, handle); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(e.Stdout, "user %s created (exempt, limits 100/100)\n", handle)
		}
		pid, opID, err := e.createProject(ctx, u, name, class, host)
		if err != nil {
			return err
		}
		detail := map[string]any{"user": u.Handle, "class": class}
		if host != nil {
			detail["host"] = host.Name
		}
		if _, err := e.audited(ctx, "project_create", pid.String(), detail); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stdout, "project %s (%s) created for %s, op %s\n", name, pid, u.Handle, opID)
		if fs.Lookup("wait").Value.String() != "true" {
			return nil
		}
		op, err := e.waitOp(ctx, opID, 45*time.Minute)
		if err != nil {
			return err
		}
		if op.State != "done" {
			return fmt.Errorf("create failed: %v", op.Error)
		}
		_, _ = fmt.Fprintln(e.Stdout, "running")
		return nil
	case "list":
		fs, err := flagsFor("list", args[1:], func(fs *flag.FlagSet) {
			fs.String("host", "", "only this host")
			fs.String("sort", "", "disk | closure")
		})
		if err != nil {
			return err
		}
		q := "select p.id, p.slug, u.handle, p.class, p.state, coalesce(h.name,'-'), p.volume_bytes, coalesce((select max(disk_used) from meter_samples m where m.project_id = p.id and m.ts > now() - interval '1 hour'), 0), coalesce((select closure_bytes from config_revisions r where r.id = p.config_revision_id), 0) from projects p join users u on u.id = p.user_id left join hosts h on h.id = p.host_id where p.destroyed_at is null and u.handle <> 'repose-platform'"
		var qargs []any
		if hn := fs.Lookup("host").Value.String(); hn != "" {
			q += " and h.name = $1"
			qargs = append(qargs, hn)
		}
		switch fs.Lookup("sort").Value.String() {
		case "disk":
			q += " order by 8 desc"
		case "closure":
			q += " order by 9 desc"
		default:
			q += " order by p.created_at"
		}
		rows, err := e.pool.Query(ctx, q, qargs...)
		if err != nil {
			return err
		}
		defer rows.Close()
		out := [][]string{{"ID", "SLUG", "USER", "CLASS", "STATE", "HOST", "VOLUME", "DISK USED", "CLOSURE"}}
		for rows.Next() {
			var id uuid.UUID
			var slug, handle, class, state, host string
			var vol, used, closure int64
			if err := rows.Scan(&id, &slug, &handle, &class, &state, &host, &vol, &used, &closure); err != nil {
				return err
			}
			out = append(out, []string{id.String(), slug, handle, class, state, host, gb(vol), gb(used), gb(closure)})
		}
		e.table(out)
		return nil
	case "show":
		if len(args) < 2 {
			return ErrUsage
		}
		p, err := e.findProject(ctx, args[1])
		if err != nil {
			return err
		}
		u, _ := store.GetUser(ctx, e.pool, p.UserID)
		host := "-"
		if p.HostID != nil {
			if h, err := store.GetHost(ctx, e.pool, *p.HostID); err == nil {
				host = h.Name + " (" + h.State + ")"
			}
		}
		var lastSnap *time.Time
		_ = e.pool.QueryRow(ctx, "select max(taken_at) from snapshots where project_id = $1 and deleted_at is null", p.ID).Scan(&lastSnap)
		var costToday int64
		_ = e.pool.QueryRow(ctx, "select coalesce(sum(cost_cents),0) from usage_hours where project_id = $1 and hour >= date_trunc('day', now())", p.ID).Scan(&costToday)
		rows := [][]string{
			{"id", p.ID.String()}, {"slug", p.Slug}, {"user", u.Handle + " (" + u.ID.String() + ")"}, {"class", p.Class}, {"state", p.State},
			{"host", host}, {"guest_id", fmt.Sprint(p.GuestID)}, {"guest_ip", fmt.Sprint(p.GuestIP)}, {"base", derefOr(p.BaseVersion, "-")},
			{"hold_base_updates", strconv.FormatBool(p.HoldBaseUpdates)}, {"volume", gb(p.VolumeBytes)}, {"last snapshot", fmtTime(lastSnap)},
			{"last error", fmt.Sprint(p.LastError)}, {"cost today", fmt.Sprintf("$%.2f", float64(costToday)/100)}, {"created", fmtTime(&p.CreatedAt)},
		}
		if l, ok, _ := meter.LatestSample(ctx, e.pool, p.ID); ok {
			rows = append(rows, []string{"signals", fmt.Sprintf("ssh=%d tmux=%d docker=%d guestd_ok=%v agents=%v (%s ago)", l.SSHSessions, l.TmuxClients, l.DockerContainers, l.GuestdOK, l.Agents, time.Since(l.TS).Round(time.Second))})
		}
		e.table(rows)
		return nil
	case "start", "stop", "restart", "snapshot":
		if len(args) < 2 {
			return ErrUsage
		}
		p, err := e.findLiveProject(ctx, args[1])
		if err != nil {
			return err
		}
		pid := p.ID
		if _, err := e.audited(ctx, "project_"+args[0], pid.String(), nil); err != nil {
			return err
		}
		switch args[0] {
		case "start":
			_, err = e.enqueue(ctx, ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(true)}, true)
		case "stop":
			snap := !containsFlag(args, "--no-snapshot")
			_, err = e.enqueue(ctx, ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Params: map[string]any{"snapshot": snap}, Phases: ops.PlanStop()}, true)
		case "restart":
			if p.State == "running" || p.State == "error" {
				if _, err := e.enqueue(ctx, ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Params: map[string]any{"snapshot": false}, Phases: ops.PlanStop()}, true); err != nil {
					return err
				}
			}
			_, err = e.enqueue(ctx, ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(true)}, true)
		case "snapshot":
			_, err = e.enqueue(ctx, ops.NewOp{Kind: ops.KindSnapshot, ProjectID: &pid, Params: map[string]any{"reason": "manual"}, Phases: ops.PlanSnapshot()}, true)
		}
		return err
	case "destroy":
		// The same op DELETE /projects/:id enqueues: final snapshot (or stop)
		// then DestroyGuest; the last snapshot is kept 30 days (R4-11). For a
		// project stuck in `error` whose owner cannot or will not click.
		fs, err := flagsFor("projects destroy", args[1:], func(fs *flag.FlagSet) {
			fs.Bool("wait", true, "wait for the op to finish")
		})
		if err != nil || fs.NArg() < 1 {
			return fmt.Errorf("%w: projects destroy ID|SLUG", ErrUsage)
		}
		p, err := e.findProject(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		pid := p.ID
		if _, err := e.audited(ctx, "project_destroy", pid.String(), map[string]any{"state": p.State}); err != nil {
			return err
		}
		op, err := e.enqueue(ctx, ops.NewOp{Kind: ops.KindDestroy, ProjectID: &pid, Phases: ops.PlanDestroy(p)}, fs.Lookup("wait").Value.String() == "true")
		if err != nil {
			return err
		}
		if op != nil {
			_, _ = fmt.Fprintf(e.Stdout, "destroy %s: %s\n", p.Slug, op.State)
		}
		return nil
	case "resize":
		fs, err := flagsFor("resize", args[1:], func(fs *flag.FlagSet) { fs.Int64("bytes", 0, "new volume size") })
		if err != nil || fs.NArg() < 1 {
			return ErrUsage
		}
		p, err := e.findLiveProject(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		nb := fs.Lookup("bytes").Value.(flag.Getter).Get().(int64)
		if nb <= p.VolumeBytes {
			return fmt.Errorf("volumes only grow (current %d)", p.VolumeBytes)
		}
		pid := p.ID
		if _, err := e.audited(ctx, "project_resize", pid.String(), map[string]any{"bytes": nb}); err != nil {
			return err
		}
		_, err = e.enqueue(ctx, ops.NewOp{Kind: ops.KindResize, ProjectID: &pid, Params: map[string]any{"volume_bytes": float64(nb)}, Phases: ops.PlanResize()}, true)
		return err
	case "move":
		fs, err := flagsFor("move", args[1:], func(fs *flag.FlagSet) { fs.String("to", "", "target host") })
		if err != nil || fs.NArg() < 1 || fs.Lookup("to").Value.String() == "" {
			return ErrUsage
		}
		p, err := e.findLiveProject(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		h, err := e.findHost(ctx, fs.Lookup("to").Value.String())
		if err != nil {
			return err
		}
		pid := p.ID
		if _, err := e.audited(ctx, "project_move", pid.String(), map[string]any{"to": h.Name}); err != nil {
			return err
		}
		if p.State == "running" {
			if _, err := e.enqueue(ctx, ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Params: map[string]any{"snapshot": true}, Phases: ops.PlanStop()}, true); err != nil {
				return err
			}
		}
		return e.restore(ctx, p, uuid.Nil, &h.ID, true)
	case "restore":
		fs, err := flagsFor("restore", args[1:], func(fs *flag.FlagSet) {
			fs.String("snapshot", "", "snapshot id")
			fs.Bool("latest", false, "newest snapshot")
			fs.String("to", "", "target host")
		})
		if err != nil || fs.NArg() < 1 {
			return ErrUsage
		}
		p, err := e.findLiveProject(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		var sid uuid.UUID
		if s := fs.Lookup("snapshot").Value.String(); s != "" {
			sid, err = uuid.Parse(s)
			if err != nil {
				return fmt.Errorf("%w: --snapshot is not an id", ErrUsage)
			}
		}
		var to *uuid.UUID
		if t := fs.Lookup("to").Value.String(); t != "" {
			h, err := e.findHost(ctx, t)
			if err != nil {
				return err
			}
			to = &h.ID
		}
		if _, err := e.audited(ctx, "project_restore", p.ID.String(), map[string]any{"snapshot": sid.String()}); err != nil {
			return err
		}
		return e.restore(ctx, p, sid, to, true)
	case "exec":
		return e.exec(ctx, args[1:])
	}
	return fmt.Errorf("%w: projects %s", ErrUsage, args[0])
}

func containsFlag(args []string, f string) bool {
	for _, a := range args {
		if a == f {
			return true
		}
	}
	return false
}

func (e *Env) restore(ctx context.Context, p *store.Project, sid uuid.UUID, to *uuid.UUID, start bool) error {
	if sid == uuid.Nil {
		snaps, err := store.ListSnapshots(ctx, e.pool, p.ID)
		if err != nil {
			return err
		}
		if len(snaps) == 0 {
			return fmt.Errorf("%s has no snapshot", p.Slug)
		}
		sid = snaps[0].ID
	}
	fresh, err := store.GetProject(ctx, e.pool, p.ID)
	if err != nil {
		return err
	}
	hasClosure := false
	if fresh.ConfigRevisionID != nil {
		if rev, err := store.GetRevision(ctx, e.pool, *fresh.ConfigRevisionID); err == nil && rev.SystemClosure != nil {
			hasClosure = true
		}
	}
	params := map[string]any{"start": start}
	if to != nil {
		params["to_host_id"] = to.String()
	}
	pid := fresh.ID
	// A lost host cannot destroy the old guest; skip that phase.
	phases := ops.PlanRestore(fresh, hasClosure, start)
	if fresh.HostID != nil {
		if h, err := store.GetHost(ctx, e.pool, *fresh.HostID); err == nil && (h.State == "lost" || h.State == "unreachable" || h.State == "retired") {
			var kept []string
			for _, ph := range phases {
				if ph != ops.PhaseDestroyGuest {
					kept = append(kept, ph)
				}
			}
			phases = kept
		}
	}
	_, err = e.enqueue(ctx, ops.NewOp{Kind: ops.KindRestore, ProjectID: &pid, SnapshotID: &sid, Params: params, Phases: phases}, true)
	return err
}

// exec runs an audited command in a guest: audit row first, then the op.
func (e *Env) exec(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return ErrUsage
	}
	ref := args[0]
	var argv []string
	for i, a := range args[1:] {
		if a == "--" {
			argv = args[i+2:]
			break
		}
	}
	if len(argv) == 0 {
		return fmt.Errorf("%w: exec needs an id and, after --, the command to run", ErrUsage)
	}
	p, err := e.findProject(ctx, ref)
	if err != nil {
		return err
	}
	aid, err := e.audited(ctx, "exec", p.ID.String(), map[string]any{"argv": argv})
	if err != nil {
		return err
	}
	pid := p.ID
	argvAny := make([]any, len(argv))
	for i, a := range argv {
		argvAny[i] = a
	}
	op, err := e.enqueue(ctx, ops.NewOp{Kind: ops.KindExec, ProjectID: &pid, AuditID: &aid, Params: map[string]any{"argv": argvAny, "timeout_s": float64(120)}, Phases: ops.PlanExec()}, true)
	if err != nil {
		return err
	}
	if s, ok := op.Result["stdout"].(string); ok {
		b, _ := base64.StdEncoding.DecodeString(s)
		_, _ = fmt.Fprint(e.Stdout, string(b))
	}
	if s, ok := op.Result["stderr"].(string); ok {
		b, _ := base64.StdEncoding.DecodeString(s)
		_, _ = fmt.Fprint(e.Stderr, string(b))
	}
	if code, ok := op.Result["exit_code"].(float64); ok && code != 0 {
		return fmt.Errorf("exit status %d", int(code))
	}
	return nil
}

// --- users ---------------------------------------------------------------------

func (e *Env) users(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return ErrUsage
	}
	if err := e.connect(ctx); err != nil {
		return err
	}
	switch args[0] {
	case "list":
		users, err := store.ListUsers(ctx, e.pool)
		if err != nil {
			return err
		}
		rows := [][]string{{"HANDLE", "ID", "BILLING", "CARD", "PROJECTS", "LIMITS", "SUSPENDED", "CREATED"}}
		for _, u := range users {
			var n int
			_ = e.pool.QueryRow(ctx, "select count(*) from projects where user_id = $1 and destroyed_at is null", u.ID).Scan(&n)
			rows = append(rows, []string{u.Handle, u.ID.String(), u.BillingStatus, strconv.FormatBool(u.HasCard), strconv.Itoa(n), fmt.Sprintf("%d/%d", u.ProjectLimit, u.XLLimit), fmtTime(u.SuspendedAt), fmtTime(&u.CreatedAt)})
		}
		e.table(rows)
		return nil
	case "show":
		if len(args) < 2 {
			return ErrUsage
		}
		u, err := e.findUser(ctx, args[1])
		if err != nil {
			return err
		}
		projects, _ := store.ListUserProjects(ctx, e.pool, u.ID)
		rows := [][]string{{"handle", u.Handle}, {"id", u.ID.String()}, {"billing", u.BillingStatus}, {"has_card", strconv.FormatBool(u.HasCard)},
			{"trial_credit_cents", strconv.FormatInt(u.TrialCreditCents, 10)}, {"limits", fmt.Sprintf("%d projects, %d xl", u.ProjectLimit, u.XLLimit)},
			{"suspended", fmtTime(u.SuspendedAt)}, {"cancelled", fmtTime(u.CancelledAt)}, {"projects", strconv.Itoa(len(projects))}}
		for _, p := range projects {
			rows = append(rows, []string{"  " + p.Slug, p.Class + " " + p.State + " " + p.ID.String()})
		}
		e.table(rows)
		return nil
	case "suspend":
		fs, err := flagsFor("suspend", args[1:], func(fs *flag.FlagSet) {
			fs.String("reason", "", "reason")
			fs.Bool("retain", false, "keep data beyond the 30-day schedule")
		})
		if err != nil || fs.NArg() < 1 {
			return ErrUsage
		}
		reason := fs.Lookup("reason").Value.String()
		if reason == "" {
			return fmt.Errorf("%w: suspend needs --reason", ErrUsage)
		}
		u, err := e.findUser(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		if _, err := e.pool.Exec(ctx, "update users set suspended_at = now(), suspended_reason = $2, billing_status = 'suspended' where id = $1", u.ID, reason); err != nil {
			return err
		}
		c, err := e.loadCA(ctx)
		if err == nil {
			if _, err := c.RevokeAll(ctx, u.ID, e.Actor); err != nil {
				return err
			}
		} else {
			_, _ = fmt.Fprintf(e.Stderr, "warning: certificates not revoked: %v\n", err)
		}
		projects, _ := store.ListUserProjects(ctx, e.pool, u.ID)
		for _, p := range projects {
			if p.State == "running" || p.State == "starting" {
				pid := p.ID
				if _, err := e.enqueue(ctx, ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Params: map[string]any{"snapshot": true}, Phases: ops.PlanStop()}, false); err != nil {
					_, _ = fmt.Fprintf(e.Stderr, "warning: stop %s: %v\n", p.Slug, err)
				}
			}
		}
		_, err = e.audited(ctx, "user_suspend", u.Handle, map[string]any{"reason": reason, "retain": fs.Lookup("retain").Value.String() == "true", "projects": len(projects)})
		_, _ = fmt.Fprintf(e.Stdout, "%s suspended; %d project(s) stopping\n", u.Handle, len(projects))
		return err
	case "unsuspend":
		if len(args) < 2 {
			return ErrUsage
		}
		u, err := e.findUser(ctx, args[1])
		if err != nil {
			return err
		}
		if _, err := e.pool.Exec(ctx, "update users set suspended_at = null, suspended_reason = null, billing_status = case when billing_status = 'suspended' then 'trial' else billing_status end where id = $1", u.ID); err != nil {
			return err
		}
		_, err = e.audited(ctx, "user_unsuspend", u.Handle, nil)
		_, _ = fmt.Fprintf(e.Stdout, "%s unsuspended\n", u.Handle)
		return err
	case "rename":
		// The handle is fixed at first sign-in from the identity the provider
		// reported (auth.Provisioner), so a first sign-in without a GitHub
		// identity leaves a `user-<sub>` handle for ever, and a user row
		// cannot be deleted (the credit ledger references it and is
		// append-only). Rename while the user has no projects: the handle is
		// the SSH login suffix and the certificate key_id, both minted per
		// project (I-100).
		fs, err := flagsFor("users rename", args[1:], func(fs *flag.FlagSet) {
			fs.String("github-login", "", "also record the GitHub login (defaults to the new handle)")
		})
		if err != nil || fs.NArg() < 2 {
			return fmt.Errorf("%w: users rename OLD NEW [--github-login L]", ErrUsage)
		}
		u, err := e.findUser(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		newHandle := fs.Arg(1)
		if auth.DeriveHandle(newHandle) != newHandle {
			return fmt.Errorf("%w: %q is not a valid handle (lowercase [a-z0-9-], at most 32)", ErrUsage, newHandle)
		}
		var n int
		if err := e.pool.QueryRow(ctx, "select count(*) from projects where user_id = $1 and state <> 'destroyed'", u.ID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%s has %d projects; a handle with projects is not renamed", u.Handle, n)
		}
		gh := fs.Lookup("github-login").Value.String()
		if gh == "" {
			gh = newHandle
		}
		if _, err := e.pool.Exec(ctx, "update users set handle = $2, github_login = $3, updated_at = now() where id = $1", u.ID, newHandle, gh); err != nil {
			return err
		}
		if _, err := e.audited(ctx, "user_rename", newHandle, map[string]any{"from": u.Handle}); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stdout, "%s is now %s (github_login %s)\n", u.Handle, newHandle, gh)
		return nil
	case "exempt":
		if len(args) < 2 {
			return ErrUsage
		}
		u, err := e.findUser(ctx, args[1])
		if err != nil {
			return err
		}
		if _, err := e.pool.Exec(ctx, "update users set billing_status = 'exempt' where id = $1", u.ID); err != nil {
			return err
		}
		_, err = e.audited(ctx, "user_exempt", u.Handle, nil)
		_, _ = fmt.Fprintf(e.Stdout, "%s is billing-exempt (DECISIONS I-16)\n", u.Handle)
		return err
	case "limits":
		fs, err := flagsFor("limits", args[1:], func(fs *flag.FlagSet) {
			fs.Int("projects", -1, "project limit")
			fs.Int("xl", -1, "xl limit")
		})
		if err != nil || fs.NArg() < 1 {
			return ErrUsage
		}
		u, err := e.findUser(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		pl := fs.Lookup("projects").Value.(flag.Getter).Get().(int)
		xl := fs.Lookup("xl").Value.(flag.Getter).Get().(int)
		if pl < 0 {
			pl = u.ProjectLimit
		}
		if xl < 0 {
			xl = u.XLLimit
		}
		if _, err := e.pool.Exec(ctx, "update users set project_limit = $2, xl_limit = $3 where id = $1", u.ID, pl, xl); err != nil {
			return err
		}
		_, err = e.audited(ctx, "user_limits", u.Handle, map[string]any{"projects": pl, "xl": xl})
		_, _ = fmt.Fprintf(e.Stdout, "%s: %d projects, %d xl\n", u.Handle, pl, xl)
		return err
	}
	return fmt.Errorf("%w: users %s", ErrUsage, args[0])
}

// --- certs, secrets, billing ----------------------------------------------------

func (e *Env) certs(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "revoke" {
		return ErrUsage
	}
	fs, err := flagsFor("certs revoke", args[1:], func(fs *flag.FlagSet) { fs.String("user", "", "handle") })
	if err != nil || fs.Lookup("user").Value.String() == "" {
		return ErrUsage
	}
	if err := e.connect(ctx); err != nil {
		return err
	}
	u, err := e.findUser(ctx, fs.Lookup("user").Value.String())
	if err != nil {
		return err
	}
	c, err := e.loadCA(ctx)
	if err != nil {
		return err
	}
	n, err := c.RevokeAll(ctx, u.ID, e.Actor)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.Stdout, "revoked %d certificate(s) of %s; the gateway sees them within 30 s\n", n, u.Handle)
	return nil
}

func (e *Env) secretsCmd(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "rewrap" {
		return ErrUsage
	}
	sec, err := e.secretsStore(ctx)
	if err != nil {
		return err
	}
	n, err := sec.Rewrap(ctx)
	if err != nil {
		return err
	}
	if _, err := e.audited(ctx, "secrets_rewrap", "", map[string]any{"rows": n}); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.Stdout, "rewrapped %d row(s) under the current key version; ciphertext untouched\n", n)
	return nil
}

func (e *Env) billing(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return ErrUsage
	}
	if args[0] == "paddle-bootstrap" {
		// Talks to Paddle only; no database (DECISIONS I-289).
		return e.billingPaddleBootstrap(ctx, args[1:])
	}
	if err := e.connect(ctx); err != nil {
		return err
	}
	switch args[0] {
	case "rollup":
		return e.billingRollup(ctx, args[1:])
	case "explain":
		return e.billingExplain(ctx, args[1:])
	case "suspend":
		// The billing-reason suspension is the same action as `users
		// suspend --reason billing`; one implementation, two spellings, so
		// the runbook's billing section and the user section agree.
		if len(args) < 2 {
			return ErrUsage
		}
		return e.users(ctx, []string{"suspend", "--reason", "billing", args[1]})
	case "unsuspend":
		if len(args) < 2 {
			return ErrUsage
		}
		return e.users(ctx, []string{"unsuspend", args[1]})
	case "show":
		return e.billingShow(ctx, args[1:])
	case "overage-now":
		return e.billingOverageNow(ctx, args[1:])
	}
	return fmt.Errorf("%w: billing %s", ErrUsage, args[0])
}

// billingRollup runs the hourly rollup of 09-billing.md §5.4 by hand.
func (e *Env) billingRollup(ctx context.Context, args []string) error {
	fs, err := flagsFor("rollup", args, func(fs *flag.FlagSet) { fs.String("hour", "", "2026-09-17T14 (UTC); omit for every due hour") })
	if err != nil {
		return err
	}
	r := billing.NewRollup(e.pool, metrics.NewNop(), e.logger())
	if hs := fs.Lookup("hour").Value.String(); hs != "" {
		h, err := time.Parse("2006-01-02T15", hs)
		if err != nil {
			return fmt.Errorf("%w: --hour is 2026-09-17T14", ErrUsage)
		}
		rows, err := r.Hour(ctx, h)
		if err != nil {
			return err
		}
		out := [][]string{{"PROJECT", "CLASS", "RUNNING S", "GB", "EGRESS", "PERIOD", "GAP"}}
		for _, row := range rows {
			out = append(out, []string{row.ProjectID.String(), row.Class, strconv.Itoa(row.RunningSeconds), strconv.FormatInt(row.GBAlloc, 10), gb(row.EgressBytes),
				row.Period.Start.Format("2006-01-02"), strconv.FormatBool(row.Gap)})
		}
		e.table(out)
		_, err = e.audited(ctx, "billing_rollup", hs, map[string]any{"rows": len(rows)})
		return err
	}
	n, err := r.Due(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.Stdout, "rolled up %d hour(s)\n", n)
	_, err = e.audited(ctx, "billing_rollup", "due", map[string]any{"hours": n})
	return err
}

// billingExplain prints every input of one usage_hours row and the
// period's overage arithmetic around it.
func (e *Env) billingExplain(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("%w: billing explain PROJECT 2026-09-17T14", ErrUsage)
	}
	p, err := e.findProject(ctx, args[0])
	if err != nil {
		return err
	}
	h, err := parseHour(args[1])
	if err != nil {
		return err
	}
	ex, err := billing.Explain(ctx, e.pool, p.ID, h)
	if err != nil {
		return err
	}
	_, err = ex.WriteTo(e.Stdout)
	return err
}

// parseHour accepts the two spellings an operator types.
func parseHour(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15", time.RFC3339, "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: the hour is 2026-09-17T14", ErrUsage)
}

// --- base -----------------------------------------------------------------------

func (e *Env) base(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return ErrUsage
	}
	if err := e.connect(ctx); err != nil {
		return err
	}
	switch args[0] {
	case "publish", "release":
		fs, err := flagsFor("base publish", args[1:], func(fs *flag.FlagSet) {
			fs.String("rev", "", "git revision of nix/")
			fs.String("nix-rev", "", "alias of --rev")
			fs.String("version", "", "version label (default YYYY.MM.DD)")
			fs.String("changelog", "", "changelog line")
			fs.Bool("security", false, "security release: applied within hours, not a day")
			fs.String("repo", "", "repository hosts clone bases from (default $REPOSE_BASE_REPO, else "+DefaultBaseRepo+")")
			fs.String("branch", "main", "the branch the rev must be on")
			fs.Bool("unverified-rev", false, "skip the repository check (the repository host is down); the rev must still be a full sha")
		})
		if err != nil {
			return err
		}
		rev := fs.Lookup("rev").Value.String()
		if rev == "" {
			rev = fs.Lookup("nix-rev").Value.String()
		}
		if rev == "" && fs.NArg() > 0 {
			rev = fs.Arg(0)
		}
		if rev == "" {
			return fmt.Errorf("%w: base publish needs --rev", ErrUsage)
		}
		version := fs.Lookup("version").Value.String()
		if version == "" && fs.NArg() > 0 && rev != fs.Arg(0) {
			version = fs.Arg(0)
		}
		repo := fs.Lookup("repo").Value.String()
		if repo == "" {
			repo = os.Getenv("REPOSE_BASE_REPO")
		}
		if repo == "" {
			repo = DefaultBaseRepo
		}
		branch := fs.Lookup("branch").Value.String()
		unverified := fs.Lookup("unverified-rev").Value.String() == "true"
		if unverified {
			if !fullSHA.MatchString(strings.ToLower(rev)) {
				return fmt.Errorf("%w: --rev %q is not a full commit sha (40 hex characters), --unverified-rev or not", ErrUsage, rev)
			}
			rev = strings.ToLower(rev)
		} else if rev, err = e.checkBaseRev(ctx, rev, repo, branch); err != nil {
			return err
		}
		if version == "" {
			if version, err = e.nextBaseVersion(ctx, time.Now().UTC()); err != nil {
				return err
			}
		}
		changelog := fs.Lookup("changelog").Value.String()
		if strings.HasPrefix(changelog, "@") {
			b, err := os.ReadFile(changelog[1:])
			if err != nil {
				return err
			}
			changelog = string(b)
		}
		security := fs.Lookup("security").Value.String() == "true"
		if _, err := e.pool.Exec(ctx, "insert into base_versions (version, nix_rev, changelog, security) values ($1, $2, $3, $4)", version, rev, changelog, security); err != nil {
			return err
		}
		if _, err := e.audited(ctx, "base_publish", version, map[string]any{"rev": rev, "security": security, "unverified_rev": unverified}); err != nil {
			return err
		}
		when := "the 04:00 UTC sweep"
		if security {
			when = "the next security sweep (within 10 minutes)"
		}
		_, _ = fmt.Fprintf(e.Stdout, "base %s (%s) published; unheld projects rebuild at %s\n", version, rev, when)
		return nil
	case "list":
		bases, err := store.ListBases(ctx, e.pool)
		if err != nil {
			return err
		}
		rows := [][]string{{"VERSION", "REV", "RELEASED", "SECURITY", "PROJECTS ON IT"}}
		for _, b := range bases {
			var n int
			_ = e.pool.QueryRow(ctx, "select count(*) from projects where base_version = $1 and destroyed_at is null", b.Version).Scan(&n)
			rows = append(rows, []string{b.Version, b.NixRev, fmtTime(&b.ReleasedAt), strconv.FormatBool(b.Security), strconv.Itoa(n)})
		}
		e.table(rows)
		return nil
	case "status":
		if len(args) < 2 {
			return ErrUsage
		}
		v := args[1]
		if _, err := store.GetBase(ctx, e.pool, v); err != nil {
			return fmt.Errorf("no base %s", v)
		}
		rows := [][]string{{"PROJECT", "STATE", "BASE", "HOLD", "NEWEST REVISION"}}
		projects, err := store.ListAllProjects(ctx, e.pool)
		if err != nil {
			return err
		}
		for _, p := range projects {
			revs, _ := store.ListRevisions(ctx, e.pool, p.ID)
			newest := "-"
			if len(revs) > 0 {
				newest = revs[0].Status
				if revs[0].BaseVersion != nil {
					newest += " on " + *revs[0].BaseVersion
				}
				if revs[0].Error != nil {
					newest += ": " + firstLine(*revs[0].Error)
				}
			}
			base := "-"
			if p.BaseVersion != nil {
				base = *p.BaseVersion // fmt.Sprint of the pointer printed an address on host-01 (2026-09-21)
			}
			rows = append(rows, []string{p.Slug, p.State, base, strconv.FormatBool(p.HoldBaseUpdates), newest})
		}
		e.table(rows)
		return nil
	case "rollback":
		if len(args) < 2 {
			return ErrUsage
		}
		v := args[1]
		b, err := store.GetBase(ctx, e.pool, v)
		if err != nil {
			return fmt.Errorf("no base %s", v)
		}
		tag, err := e.pool.Exec(ctx, "delete from base_versions where released_at > $1", b.ReleasedAt)
		if err != nil {
			return err
		}
		if _, err := e.audited(ctx, "base_rollback", v, map[string]any{"removed": tag.RowsAffected()}); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stdout, "%d newer base(s) withdrawn; %s is the latest again and unheld projects on a newer base rebuild at the next sweep\n", tag.RowsAffected(), v)
		return nil
	}
	return fmt.Errorf("%w: base %s", ErrUsage, args[0])
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// --- ca, operator-cert, edge ---------------------------------------------------------

func (e *Env) caCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return ErrUsage
	}
	sec, err := e.secretsStore(ctx)
	if err != nil {
		return err
	}
	switch args[0] {
	case "init":
		if err := ca.Init(ctx, sec); err != nil {
			return err
		}
		if _, err := e.audited(ctx, "ca_init", "", nil); err != nil {
			return err
		}
		c, err := e.loadCA(ctx)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stdout, "user ca: %s\nhost ca: %s\nx509 host ca: initialised\n", c.UserCAPub(), c.HostCAPub())
		return nil
	case "rotate":
		fs, err := flagsFor("ca rotate", args[1:], func(fs *flag.FlagSet) {
			fs.Bool("user", false, "rotate the User CA")
			fs.Bool("host", false, "rotate the Host CA and the x509 host CA")
		})
		if err != nil {
			return err
		}
		user := fs.Lookup("user").Value.String() == "true"
		host := fs.Lookup("host").Value.String() == "true"
		if !user && !host {
			user = true
		}
		if err := ca.Rotate(ctx, sec, user, host); err != nil {
			return err
		}
		if _, err := e.audited(ctx, "ca_rotate", "", map[string]any{"user": user, "host": host}); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(e.Stdout, "rotated; restart the api so it loads the new keys. User certificates signed by the old CA expire within 24 h; guests get new host certificates at their next start; hosts need `hosts rotate-cert` after a host CA rotation.")
		return nil
	case "sign-host":
		fs, err := flagsFor("sign-host", args[1:], func(fs *flag.FlagSet) {
			fs.String("principal", "", "comma-separated principals")
			fs.String("pubkey", "", "public key file")
			fs.String("ttl", "43800h", "validity")
		})
		if err != nil {
			return err
		}
		return e.signSSH(ctx, fs, false)
	case "sign-client":
		fs, err := flagsFor("sign-client", args[1:], func(fs *flag.FlagSet) {
			fs.String("name", "gateway", "client name (CN)")
			fs.Bool("operator", false, "sign an operator SSH certificate instead (reads --pubkey)")
			fs.String("pubkey", "", "public key file (with --operator)")
			fs.String("csr", "", "PEM certificate request to sign instead of generating a key; the key stays where it was made")
			fs.String("out", "", "directory for <name>.crt and <name>.key")
		})
		if err != nil {
			return err
		}
		if fs.Lookup("operator").Value.String() == "true" {
			return e.signSSH(ctx, fs, true)
		}
		c, err := e.loadCA(ctx)
		if err != nil {
			return err
		}
		name := fs.Lookup("name").Value.String()
		if csrFile := fs.Lookup("csr").Value.String(); csrFile != "" {
			return e.signCSR(ctx, c, csrFile, false, 2*365*24*time.Hour, name)
		}
		certPEM, keyPEM, serial, err := c.X509().IssueClient(name, 2*365*24*time.Hour)
		if err != nil {
			return err
		}
		if _, err := e.audited(ctx, "ca_sign_client", name, map[string]any{"serial": serial}); err != nil {
			return err
		}
		if dir := fs.Lookup("out").Value.String(); dir != "" {
			if err := os.WriteFile(filepath.Join(dir, name+".crt"), certPEM, 0o644); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, name+".key"), keyPEM, 0o600); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(e.Stdout, "wrote %s/%s.crt and %s.key (mTLS client for /internal and the gRPC listener)\n", dir, name, name)
			return nil
		}
		_, _ = fmt.Fprint(e.Stdout, string(certPEM), string(keyPEM))
		return nil
	case "show":
		// The public halves: what a host's `repose.host.apiCA` and the
		// edge's api-ca.pem carry (the x509 host CA certificate), and the
		// two SSH CA lines. Never a key.
		c, err := e.loadCA(ctx)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stdout, "# user ca: %s\n# host ca: %s\n%s", c.UserCAPub(), c.HostCAPub(), string(c.X509().CertPEM))
		return nil
	case "sign-server":
		// A server certificate from the x509 host CA for a listener that
		// hosts, the gateway or guests verify against api-ca.pem: the edge's
		// hook-ingest listener (06 §5.7). The api's own gRPC and /internal
		// listeners issue theirs at start (I-87).
		fs, err := flagsFor("sign-server", args[1:], func(fs *flag.FlagSet) {
			fs.String("name", "", "comma-separated DNS names or IPs; the first is the CN")
			fs.String("ttl", "43800h", "validity")
			fs.String("csr", "", "PEM certificate request to sign instead of generating a key; the key stays where it was made")
			fs.String("out", "", "directory for <first name>.crt and .key")
		})
		if err != nil {
			return err
		}
		names := strings.Split(fs.Lookup("name").Value.String(), ",")
		if len(names) == 0 || names[0] == "" {
			return fmt.Errorf("%w: sign-server needs --name", ErrUsage)
		}
		ttl, err := time.ParseDuration(fs.Lookup("ttl").Value.String())
		if err != nil {
			return fmt.Errorf("%w: --ttl is a duration", ErrUsage)
		}
		c, err := e.loadCA(ctx)
		if err != nil {
			return err
		}
		if csrFile := fs.Lookup("csr").Value.String(); csrFile != "" {
			return e.signCSR(ctx, c, csrFile, true, ttl, names...)
		}
		certPEM, keyPEM, err := c.X509().IssueServer(ttl, names...)
		if err != nil {
			return err
		}
		if _, err := e.audited(ctx, "ca_sign_server", names[0], map[string]any{"names": len(names)}); err != nil {
			return err
		}
		if dir := fs.Lookup("out").Value.String(); dir != "" {
			if err := os.WriteFile(filepath.Join(dir, names[0]+".crt"), certPEM, 0o644); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, names[0]+".key"), keyPEM, 0o600); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(e.Stdout, "wrote %s/%s.crt and %s.key\n", dir, names[0], names[0])
			return nil
		}
		_, _ = fmt.Fprint(e.Stdout, string(certPEM), string(keyPEM))
		return nil
	}
	return fmt.Errorf("%w: ca %s", ErrUsage, args[0])
}

// signCSR signs a certificate request from a file (or stdin as /dev/stdin)
// and prints the certificate alone: the private key never travels
// (DECISIONS I-92, the edge's gateway key).
func (e *Env) signCSR(ctx context.Context, c *ca.CA, csrFile string, server bool, ttl time.Duration, names ...string) error {
	csrPEM, err := os.ReadFile(csrFile)
	if err != nil {
		return err
	}
	certPEM, serial, err := c.X509().SignCSR(csrPEM, server, ttl, names...)
	if err != nil {
		return err
	}
	action := "ca_sign_client"
	if server {
		action = "ca_sign_server"
	}
	if _, err := e.audited(ctx, action, names[0], map[string]any{"serial": serial, "csr": true}); err != nil {
		return err
	}
	_, _ = fmt.Fprint(e.Stdout, string(certPEM))
	return nil
}

func (e *Env) signSSH(ctx context.Context, fs *flag.FlagSet, operator bool) error {
	c, err := e.loadCA(ctx)
	if err != nil {
		return err
	}
	pkFile := fs.Lookup("pubkey").Value.String()
	if pkFile == "" {
		return fmt.Errorf("%w: --pubkey FILE is required", ErrUsage)
	}
	b, err := os.ReadFile(pkFile)
	if err != nil {
		return err
	}
	pub, err := sshca.ParsePublicKey(string(b))
	if err != nil {
		return err
	}
	if operator {
		name := fs.Lookup("name").Value.String()
		line, err := c.SignOperatorCert(ctx, pub, name, 8*time.Hour)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(e.Stdout, line)
		return nil
	}
	principals := strings.Split(fs.Lookup("principal").Value.String(), ",")
	if principals[0] == "" {
		return fmt.Errorf("%w: --principal is required", ErrUsage)
	}
	ttl, err := time.ParseDuration(fs.Lookup("ttl").Value.String())
	if err != nil {
		return fmt.Errorf("%w: --ttl", ErrUsage)
	}
	line, err := c.SignHostCert(ctx, pub, principals, ttl)
	if err != nil {
		return err
	}
	if _, err := e.audited(ctx, "ca_sign_host", strings.Join(principals, ","), nil); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(e.Stdout, line)
	return nil
}

func (e *Env) operatorCert(ctx context.Context, args []string) error {
	fs, err := flagsFor("operator-cert", args, func(fs *flag.FlagSet) {
		fs.String("pubkey", "", "public key file, or - for stdin (default ~/.ssh/id_ed25519.pub)")
		fs.String("ttl", "8h", "validity")
		fs.String("name", "", "operator name (default $USER)")
	})
	if err != nil {
		return err
	}
	c, err := e.loadCA(ctx)
	if err != nil {
		return err
	}
	pkFile := fs.Lookup("pubkey").Value.String()
	if pkFile == "" {
		home, _ := os.UserHomeDir()
		pkFile = filepath.Join(home, ".ssh", "id_ed25519.pub")
	}
	var b []byte
	if pkFile == "-" {
		// `docker exec -i <api> repose-admin operator-cert --pubkey - <
		// ~/.ssh/id_ed25519.pub`: the key is on the operator's machine,
		// not in the container (ops/dev/operator-cert.sh, I-177).
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 16<<10))
	} else {
		b, err = os.ReadFile(pkFile)
	}
	if err != nil {
		return err
	}
	pub, err := sshca.ParsePublicKey(string(b))
	if err != nil {
		return err
	}
	ttl, err := time.ParseDuration(fs.Lookup("ttl").Value.String())
	if err != nil {
		return fmt.Errorf("%w: --ttl", ErrUsage)
	}
	name := fs.Lookup("name").Value.String()
	if name == "" {
		name = envOr("USER", "operator")
	}
	line, err := c.SignOperatorCert(ctx, pub, name, ttl)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(e.Stdout, line)
	if pkFile != "-" {
		_, _ = fmt.Fprintf(e.Stderr, "save as %s-cert.pub next to the key, or `ssh-add` the key after writing it; valid %s\n", strings.TrimSuffix(pkFile, ".pub"), ttl)
	}
	return nil
}

func (e *Env) edge(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return ErrUsage
	}
	if args[0] == "loki" {
		return e.edgeLoki(ctx, args[1:])
	}
	if args[0] != "init" {
		return ErrUsage
	}
	fs, err := flagsFor("edge init", args[1:], func(fs *flag.FlagSet) {
		fs.String("endpoint", "", "WireGuard endpoint host:port")
		fs.String("pubkey", "", "the edge's WireGuard public key")
		fs.String("out", "", "directory for gateway.crt and gateway.key")
	})
	if err != nil {
		return err
	}
	endpoint, pub := fs.Lookup("endpoint").Value.String(), fs.Lookup("pubkey").Value.String()
	if endpoint == "" || pub == "" {
		return fmt.Errorf("%w: edge init needs --endpoint and --pubkey", ErrUsage)
	}
	if err := e.connect(ctx); err != nil {
		return err
	}
	if err := store.SetSetting(ctx, e.pool, hostmgr.SettingEdgeWGEndpoint, endpoint); err != nil {
		return err
	}
	if err := store.SetSetting(ctx, e.pool, hostmgr.SettingEdgeWGPubkey, pub); err != nil {
		return err
	}
	if _, err := e.audited(ctx, "edge_init", endpoint, nil); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.Stdout, "edge hub recorded: %s; hosts registering from now on receive it\n", endpoint)
	out := fs.Lookup("out").Value.String()
	if out == "" {
		return nil
	}
	return e.caCmd(ctx, []string{"sign-client", "--name", "gateway", "--out", out})
}

// edgeLoki records, or prints, the Loki every host's Fluent Bit ships to.
// A setting rather than an api environment variable: it names a machine
// outside this deployment, and moving a log sink should not need a
// redeploy of the api (DECISIONS I-95). A host learns it at `Register`,
// and an already-registered host at its next `Rotate` or by an edit of
// its host.json (ops/RUNBOOK.md "FluentBitStuck").
func (e *Env) edgeLoki(ctx context.Context, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("%w: edge loki takes one URL, or none to print the current one", ErrUsage)
	}
	if err := e.connect(ctx); err != nil {
		return err
	}
	if len(args) == 0 {
		cur, err := store.Setting(ctx, e.pool, hostmgr.SettingLokiURL)
		if err != nil {
			return err
		}
		if cur == "" {
			_, _ = fmt.Fprintln(e.Stdout, "no Loki recorded; hosts ship nothing")
			return nil
		}
		_, _ = fmt.Fprintln(e.Stdout, cur)
		return nil
	}
	url := args[0]
	if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("%w: the Loki URL carries its scheme, e.g. http://10.255.0.3:3100", ErrUsage)
	}
	if err := store.SetSetting(ctx, e.pool, hostmgr.SettingLokiURL, url); err != nil {
		return err
	}
	if _, err := e.audited(ctx, "edge_loki", url, nil); err != nil {
		return err
	}
	if url == "" {
		_, _ = fmt.Fprintln(e.Stdout, "Loki cleared; hosts registering from now on ship nothing")
		return nil
	}
	_, _ = fmt.Fprintf(e.Stdout, "Loki recorded: %s; hosts registering or rotating from now on receive it\n", url)
	return nil
}

// --- audit, ops -------------------------------------------------------------------

func (e *Env) audit(ctx context.Context, args []string) error {
	fs, err := flagsFor("audit", args, func(fs *flag.FlagSet) {
		fs.String("user", "", "handle")
		fs.String("since", "24h", "window")
		fs.String("action", "", "action filter")
	})
	if err != nil {
		return err
	}
	if err := e.connect(ctx); err != nil {
		return err
	}
	since, err := time.ParseDuration(fs.Lookup("since").Value.String())
	if err != nil {
		return fmt.Errorf("%w: --since is a duration", ErrUsage)
	}
	q := "select ts, actor, action, target, detail from audit_log where ts > $1"
	qargs := []any{time.Now().Add(-since)}
	if h := fs.Lookup("user").Value.String(); h != "" {
		u, err := e.findUser(ctx, h)
		if err != nil {
			return err
		}
		q += " and (actor = $2 or target = $3 or target in (select id::text from projects where user_id = $4))"
		qargs = append(qargs, "user:"+u.ID.String(), u.Handle, u.ID)
	}
	if a := fs.Lookup("action").Value.String(); a != "" {
		q += fmt.Sprintf(" and action = $%d", len(qargs)+1)
		qargs = append(qargs, a)
	}
	q += " order by ts desc limit 500"
	rows, err := e.pool.Query(ctx, q, qargs...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := [][]string{{"TS", "ACTOR", "ACTION", "TARGET", "DETAIL"}}
	for rows.Next() {
		var ts time.Time
		var actor, action, target string
		var detail map[string]any
		if err := rows.Scan(&ts, &actor, &action, &target, &detail); err != nil {
			return err
		}
		out = append(out, []string{ts.UTC().Format(time.RFC3339), actor, action, target, fmt.Sprint(detail)})
	}
	e.table(out)
	return nil
}

func (e *Env) opsCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return ErrUsage
	}
	if err := e.connect(ctx); err != nil {
		return err
	}
	switch args[0] {
	case "list":
		fs, err := flagsFor("ops list", args[1:], func(fs *flag.FlagSet) {
			fs.String("project", "", "project id or slug")
			fs.String("host", "", "host name")
			fs.String("state", "", "pending|running|done|error")
		})
		if err != nil {
			return err
		}
		q := "select o.id, o.kind, o.state, o.step, coalesce(p.slug,'-'), coalesce(h.name,'-'), o.created_at, o.finished_at, o.error from ops o left join projects p on p.id = o.project_id left join hosts h on h.id = o.host_id where true"
		var qargs []any
		if pr := fs.Lookup("project").Value.String(); pr != "" {
			p, err := e.findProject(ctx, pr)
			if err != nil {
				return err
			}
			qargs = append(qargs, p.ID)
			q += fmt.Sprintf(" and o.project_id = $%d", len(qargs))
		}
		if hn := fs.Lookup("host").Value.String(); hn != "" {
			qargs = append(qargs, hn)
			q += fmt.Sprintf(" and h.name = $%d", len(qargs))
		}
		if st := fs.Lookup("state").Value.String(); st != "" {
			qargs = append(qargs, st)
			q += fmt.Sprintf(" and o.state = $%d", len(qargs))
		}
		q += " order by o.created_at desc limit 200"
		rows, err := e.pool.Query(ctx, q, qargs...)
		if err != nil {
			return err
		}
		defer rows.Close()
		out := [][]string{{"OP", "KIND", "STATE", "STEP", "PROJECT", "HOST", "CREATED", "FINISHED", "ERROR"}}
		for rows.Next() {
			var id uuid.UUID
			var kind, state, slug, host string
			var step int
			var created time.Time
			var finished *time.Time
			var errObj map[string]any
			if err := rows.Scan(&id, &kind, &state, &step, &slug, &host, &created, &finished, &errObj); err != nil {
				return err
			}
			es := ""
			if errObj != nil {
				es = fmt.Sprint(errObj["code"]) + ": " + firstLine(fmt.Sprint(errObj["message"]))
			}
			out = append(out, []string{id.String(), kind, state, strconv.Itoa(step), slug, host, fmtTime(&created), fmtTime(finished), es})
		}
		e.table(out)
		return nil
	case "log":
		if len(args) < 2 {
			return ErrUsage
		}
		id, err := uuid.Parse(args[1])
		if err != nil {
			return fmt.Errorf("%w: ops log OPID", ErrUsage)
		}
		rows, err := e.pool.Query(ctx, "select seq, line from build_logs where op_id = $1 order by seq", id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var seq int64
			var line string
			if err := rows.Scan(&seq, &line); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(e.Stdout, line)
		}
		op, err := store.GetOp(ctx, e.pool, id)
		if err == nil && op.Error != nil {
			_, _ = fmt.Fprintf(e.Stderr, "op %s: %v\n", id, op.Error)
		}
		return rows.Err()
	}
	return fmt.Errorf("%w: ops %s", ErrUsage, args[0])
}

// findLiveProject is findProject for commands that act on a guest: a
// destroyed project's row is history, and acting on it in place (a
// restore, a start) ran a guest the rest of the platform treats as gone —
// invisible to lists, its snapshots expiring in 30 days. A destroyed
// project comes back as a new one, the way the user's `repose restore`
// does it.
func (e *Env) findLiveProject(ctx context.Context, ref string) (*store.Project, error) {
	p, err := e.findProject(ctx, ref)
	if err != nil {
		return nil, err
	}
	if p.DestroyedAt != nil {
		return nil, fmt.Errorf("%s was destroyed on %s; the owner brings it back as a new project with `repose restore %s`", p.Slug, p.DestroyedAt.UTC().Format("2006-01-02 15:04"), p.Slug)
	}
	return p, nil
}

func derefOr(s *string, def string) string {
	if s == nil || *s == "" {
		return def
	}
	return *s
}

// nextBaseVersion names a base published without --version: the UTC date,
// then date.1, date.2 ... for further publishes the same day. The bare date
// alone made the second publish of a day fail on the primary key.
func (e *Env) nextBaseVersion(ctx context.Context, now time.Time) (string, error) {
	day := now.Format("2006.01.02")
	rows, err := e.pool.Query(ctx, "select version from base_versions where version = $1 or version like $1 || '.%'", day)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	taken, next := false, 1
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return "", err
		}
		if v == day {
			taken = true
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(v, day+".")); err == nil && n >= next {
			next = n + 1
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if !taken && next == 1 {
		return day, nil
	}
	return fmt.Sprintf("%s.%d", day, next), nil
}
