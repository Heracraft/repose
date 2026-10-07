package admin_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/admin"
	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/hostmgr"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
	"golang.org/x/crypto/ssh"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

func run(t *testing.T, e *admin.Env, args ...string) (string, error) {
	t.Helper()
	var out, errb bytes.Buffer
	e.Stdout, e.Stderr = &out, &errb
	err := admin.Run(context.Background(), e, args)
	return out.String() + errb.String(), err
}

// Every subcommand named in DECISIONS I-9 and the runbook exists and
// does its work against the database; ops-backed commands complete
// through the harness's engine and fake host.
func TestAdminSurface(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	e := &admin.Env{KV: h.KV, Actor: "admin:test"}
	e.SetPool(h.Pool)
	ctx := h.Ctx
	// db
	out, err := run(t, e, "db", "status")
	if err != nil || !strings.Contains(out, "pending: []") {
		t.Fatalf("db status: %s %v", out, err)
	}
	if out, err := run(t, e, "db", "verify"); err != nil || !strings.Contains(out, "users") {
		t.Fatalf("db verify: %s %v", out, err)
	}
	if _, err := run(t, e, "db", "down", "1"); err != nil {
		t.Fatal(err)
	}
	// The newest migration is the one pending, whichever number another
	// workstream has reached.
	ms, err := db.Migrations()
	if err != nil || len(ms) == 0 {
		t.Fatalf("migrations: %v %v", ms, err)
	}
	if out, err := run(t, e, "db", "status"); err != nil || !strings.Contains(out, fmt.Sprintf("pending: [%d]", ms[len(ms)-1].Version)) {
		t.Fatalf("after down: %s %v", out, err)
	}
	if _, err := run(t, e, "db", "migrate"); err != nil {
		t.Fatal(err)
	}
	// ca
	if _, err := run(t, e, "ca", "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, e, "ca", "init"); err == nil {
		t.Fatal("second ca init should refuse")
	}
	// operator-cert --pubkey - reads the key from stdin, the shape
	// `docker exec -i` gives ops/dev/operator-cert.sh (I-177).
	opPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(opPub)
	if err != nil {
		t.Fatal(err)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = pw.Write(ssh.MarshalAuthorizedKey(sshPub))
	_ = pw.Close()
	oldStdin := os.Stdin
	os.Stdin = pr
	out, err = run(t, e, "operator-cert", "--pubkey", "-", "--name", "conductor", "--ttl", "1h")
	os.Stdin = oldStdin
	if err != nil || !strings.HasPrefix(out, "ssh-ed25519-cert-v01@openssh.com ") {
		t.Fatalf("operator-cert from stdin: %q %v", out, err)
	}
	// hosts add mints a single-use token and refuses a second without --reissue once registered.
	out, err = run(t, e, "hosts", "add", "--name", "host-02", "--sku", "Standard_D16s_v7")
	if err != nil || len(strings.TrimSpace(strings.Split(out, "\n")[0])) < 40 {
		t.Fatalf("hosts add: %q %v", out, err)
	}
	if out, err := run(t, e, "hosts", "list"); err != nil || !strings.Contains(out, "host-02") || !strings.Contains(out, "host-01") {
		t.Fatalf("hosts list: %s %v", out, err)
	}
	if _, err := run(t, e, "hosts", "add", "--name", "host-01"); err == nil {
		t.Fatal("hosts add for a registered host without --reissue should refuse")
	}
	if _, err := run(t, e, "hosts", "add", "--name", "host-02", "--reissue"); err != nil {
		t.Fatalf("reissue: %v", err)
	}
	// users
	u := h.NewUser("zed")
	if out, err := run(t, e, "users", "list"); err != nil || !strings.Contains(out, "zed") {
		t.Fatalf("users list: %s %v", out, err)
	}
	if _, err := run(t, e, "users", "exempt", "zed"); err != nil {
		t.Fatal(err)
	}
	// --projects only raises the account cap (I-569).
	if _, err := run(t, e, "users", "limits", "zed", "--projects", "10"); err == nil {
		t.Fatal("--projects below the cap should refuse")
	}
	if out, err := run(t, e, "users", "limits", "zed", "--projects", "150", "--xl", "10"); err != nil || !strings.Contains(out, "zed: 150 projects, 10 xl") {
		t.Fatalf("limits: %s %v", out, err)
	}
	// rename: only while the user has no projects; the new handle must be
	// a valid handle (I-100).
	h.NewUser("user-abc123")
	if _, err := run(t, e, "users", "rename", "user-abc123", "Not Valid"); err == nil {
		t.Fatal("rename to an invalid handle should refuse")
	}
	if out, err := run(t, e, "users", "rename", "user-abc123", "octocat"); err != nil || !strings.Contains(out, "is now octocat") {
		t.Fatalf("rename: %s %v", out, err)
	}
	if out, err := run(t, e, "users", "show", "octocat"); err != nil || !strings.Contains(out, "octocat") {
		t.Fatalf("show after rename: %s %v", out, err)
	}
	uu, _ := store.GetUser(ctx, h.Pool, u.ID)
	if uu.BillingStatus != "exempt" || uu.ProjectLimit != 150 || uu.XLLimit != 10 {
		t.Fatalf("user after admin: %+v", uu)
	}
	// projects driven through the engine
	p := h.CreateRunning(u, "zp")
	if out, err := run(t, e, "projects", "show", p.ID.String()); err != nil || !strings.Contains(out, "running") {
		t.Fatalf("projects show: %s %v", out, err)
	}
	if out, err := run(t, e, "projects", "list", "--host", "host-01"); err != nil || !strings.Contains(out, "zp") {
		t.Fatalf("projects list: %s %v", out, err)
	}
	if _, err := run(t, e, "projects", "snapshot", "zp"); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if out, err := run(t, e, "exec", "zp", "--", "uptime"); err != nil || !strings.Contains(out, "fake") {
		t.Fatalf("exec: %s %v", out, err)
	}
	if out, err := run(t, e, "audit", "--action", "exec"); err != nil || !strings.Contains(out, "uptime") {
		t.Fatalf("audit: %s %v", out, err)
	}
	if _, err := run(t, e, "projects", "restart", "zp"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if h.Project(p.ID).State != "running" {
		t.Fatal("not running after restart")
	}
	if _, err := run(t, e, "projects", "stop", "zp", "--no-snapshot"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := run(t, e, "projects", "restore", "zp", "--latest"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if h.Project(p.ID).State != "running" {
		t.Fatal("not running after restore")
	}
	if out, err := run(t, e, "ops", "list", "--project", "zp"); err != nil || !strings.Contains(out, "restore") {
		t.Fatalf("ops list: %s %v", out, err)
	}
	if _, err := run(t, e, "users", "rename", "zed", "zed2"); err == nil {
		t.Fatal("rename of a user with projects should refuse")
	}
	// destroy: the DELETE /projects op from the admin, for a row an owner
	// cannot or will not remove (I-100).
	p2 := h.CreateRunning(u, "zq")
	if out, err := run(t, e, "projects", "destroy", "zq"); err != nil || !strings.Contains(out, "destroy zq: done") {
		t.Fatalf("destroy: %s %v", out, err)
	}
	if st := h.Project(p2.ID).State; st != "destroyed" {
		t.Fatalf("state after destroy: %s", st)
	}
	// A destroyed project no longer pins the handle.
	u3 := h.NewUser("user-tmp3")
	p3 := h.CreateRunning(u3, "zr")
	if _, err := run(t, e, "projects", "destroy", p3.ID.String()); err != nil {
		t.Fatalf("destroy zr: %v", err)
	}
	if out, err := run(t, e, "users", "rename", "user-tmp3", "tmp3"); err != nil || !strings.Contains(out, "is now tmp3") {
		t.Fatalf("rename after destroy: %s %v", out, err)
	}
	if _, err := run(t, e, "users", "suspend", "zed", "--reason", "abuse"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	h.WaitFor("project stopped after suspend", func() bool { return h.Project(p.ID).State == "stopped" })
	if _, err := run(t, e, "users", "unsuspend", "zed"); err != nil {
		t.Fatal(err)
	}
	// base
	// A short sha is refused before anything is written (I-173); a full
	// one the repository has on main is published.
	if out, err := run(t, e, "base", "publish", "--rev", "abc123", "--changelog", "first", "--version", "2026.09.20"); err == nil || !strings.Contains(err.Error(), "not a full commit sha") {
		t.Fatalf("short rev: %s %v", out, err)
	}
	fullRev := strings.Repeat("ab", 20)
	var checked string
	e.RevCheck = func(_ context.Context, repo, branch, sha string) error {
		checked = repo + " " + branch + " " + sha
		return nil
	}
	if _, err := run(t, e, "base", "publish", "--rev", fullRev, "--changelog", "first", "--version", "2026.09.20"); err != nil {
		t.Fatal(err)
	}
	if checked != admin.DefaultBaseRepo+" main "+fullRev {
		t.Fatalf("checked %q", checked)
	}
	if out, err := run(t, e, "base", "list"); err != nil || !strings.Contains(out, "2026.09.20") {
		t.Fatalf("base list: %s %v", out, err)
	}
	if out, err := run(t, e, "base", "status", "2026.09.20"); err != nil || !strings.Contains(out, "zp") || strings.Contains(out, "0x") {
		t.Fatalf("base status (the BASE column printed a pointer on host-01): %s %v", out, err)
	}
	// Without --version a publish is named for the UTC date; a second one
	// the same day takes date.1, then date.2 (the bare date collided on the
	// primary key, 2026-09-23).
	day := time.Now().UTC().Format("2006.01.02")
	for _, want := range []string{day, day + ".1", day + ".2"} {
		if out, err := run(t, e, "base", "publish", "--rev", fullRev, "--changelog", "same day"); err != nil || !strings.Contains(out, "base "+want+" ") {
			t.Fatalf("same-day publish, want %s: %s %v", want, out, err)
		}
	}
	// projects create (I-113): a synthetic exempt user gets a project and
	// a create op the engine drives; the slug follows the api's rule.
	if _, err := run(t, e, "projects", "create", "--user", "repose-m3", "--name", "Iso_A", "--host", "host-01"); err == nil {
		t.Fatal("projects create for an unknown user without --create-user should refuse")
	}
	out, err = run(t, e, "projects", "create", "--user", "repose-m3", "--create-user", "--name", "Iso_A", "--class", "small", "--host", "host-01", "--wait")
	if err != nil || !strings.Contains(out, "user repose-m3 created") || !strings.Contains(out, "\nrunning") {
		t.Fatalf("projects create: %s %v", out, err)
	}
	var createdSlug, createdState, createdBilling string
	if err := h.Pool.QueryRow(ctx, "select p.slug, p.state, u.billing_status from projects p join users u on u.id = p.user_id where u.handle = 'repose-m3'").Scan(&createdSlug, &createdState, &createdBilling); err != nil {
		t.Fatal(err)
	}
	if createdSlug != "iso-a" || createdState != "running" || createdBilling != "exempt" {
		t.Fatalf("created project: slug %q state %q billing %q", createdSlug, createdState, createdBilling)
	}
	if out, err := run(t, e, "audit", "--action", "project_create"); err != nil || !strings.Contains(out, "project_create") {
		t.Fatalf("audit project_create: %s %v", out, err)
	}
	if _, err := run(t, e, "projects", "create", "--user", "repose-m3", "--name", "bad class", "--class", "huge"); err == nil {
		t.Fatal("projects create with a bad class should refuse")
	}
	// projects destroy is the counterpart: the destroy op runs and the row
	// keeps its destroyed_at (no delete of projects rows, db-schema.md).
	if _, err := run(t, e, "projects", "destroy", "iso-a"); err != nil {
		t.Fatalf("projects destroy: %v", err)
	}
	var destroyedState string
	var destroyedAt *time.Time
	if err := h.Pool.QueryRow(ctx, "select state, destroyed_at from projects where slug = 'iso-a'").Scan(&destroyedState, &destroyedAt); err != nil {
		t.Fatal(err)
	}
	if destroyedAt == nil || destroyedState != "destroyed" {
		t.Fatalf("after destroy: state %q destroyed_at %v", destroyedState, destroyedAt)
	}
	// A destroyed row is history: restoring or starting it in place ran a
	// guest nothing listed and whose snapshots expired (found live on
	// 2026-09-23). The id still resolves, so the refusal must be explicit.
	var isoID string
	if err := h.Pool.QueryRow(ctx, "select id::text from projects where slug = 'iso-a'").Scan(&isoID); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range [][]string{{"projects", "restore", isoID, "--latest"}, {"projects", "start", isoID}} {
		if _, err := run(t, e, cmd...); err == nil || !strings.Contains(err.Error(), "repose restore iso-a") {
			t.Fatalf("%v on a destroyed project: err %v, want a refusal naming `repose restore iso-a`", cmd, err)
		}
	}
	// smoke: create, snapshot, stop, start, destroy on host-01
	if out, err := run(t, e, "hosts", "smoke", "host-01"); err != nil || !strings.Contains(out, "destroy   ok") {
		t.Fatalf("smoke: %s %v", out, err)
	}
	// drain, reconcile, retire refusing while projects remain, mark-lost
	if _, err := run(t, e, "hosts", "drain", "host-01"); err != nil {
		t.Fatalf("drain: %v", err)
	}
	h.WaitFor("host draining", func() bool {
		hr, _ := store.GetHost(ctx, h.Pool, h.HostID)
		return hr != nil && hr.Draining
	})
	if out, err := run(t, e, "hosts", "reconcile", "host-01"); err != nil || !strings.Contains(out, "zp") {
		t.Fatalf("reconcile: %s %v", out, err)
	}
	if _, err := run(t, e, "hosts", "retire", "host-01"); err == nil {
		t.Fatal("retire with projects should refuse")
	}
	if out, err := run(t, e, "hosts", "undrain", "host-01"); err != nil || !strings.Contains(out, "hostd") {
		t.Fatalf("undrain: %s %v", out, err)
	}
	// secrets rewrap and certs revoke
	if _, err := run(t, e, "secrets", "rewrap"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, e, "certs", "revoke", "--user", "zed"); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, e, "billing", "rollup"); err != nil || !strings.Contains(out, "rolled up") {
		t.Fatalf("rollup: %s %v", out, err)
	}
	if _, err := run(t, e, "edge", "init", "--endpoint", "1.2.3.4:51820", "--pubkey", "abc="); err != nil {
		t.Fatal(err)
	}
	// ca show prints the public halves only; sign-server issues a leaf the
	// CA certificate verifies, with IP names as IP SANs.
	if out, err := run(t, e, "ca", "show"); err != nil || !strings.Contains(out, "-----BEGIN CERTIFICATE-----") || strings.Contains(out, "PRIVATE KEY") || !strings.Contains(out, "# user ca: ssh-ed25519") {
		t.Fatalf("ca show: %q %v", out, err)
	}
	if out, err := run(t, e, "ca", "sign-server", "--name", "10.255.0.1,edge.internal"); err != nil || !strings.Contains(out, "-----BEGIN CERTIFICATE-----") || !strings.Contains(out, "PRIVATE KEY") {
		t.Fatalf("ca sign-server: %q %v", out, err)
	} else {
		leaf, _ := pem.Decode([]byte(out))
		cert, err := x509.ParseCertificate(leaf.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		if len(cert.IPAddresses) != 1 || cert.IPAddresses[0].String() != "10.255.0.1" || len(cert.DNSNames) != 1 {
			t.Fatalf("sign-server SANs: ip %v dns %v", cert.IPAddresses, cert.DNSNames)
		}
		show, _ := run(t, e, "ca", "show")
		caBlock, _ := pem.Decode([]byte(show[strings.Index(show, "-----BEGIN"):]))
		caCert, err := x509.ParseCertificate(caBlock.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		if err := cert.CheckSignatureFrom(caCert); err != nil {
			t.Fatalf("leaf not signed by the shown CA: %v", err)
		}
	}
	if _, err := run(t, e, "ca", "sign-server"); err == nil {
		t.Fatal("sign-server without --name should refuse")
	}
	// --csr signs a request made elsewhere and prints the certificate alone:
	// the private key never travels (the edge's gateway key, I-92).
	csrKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csrDER, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, csrKey)
	csrFile := filepath.Join(t.TempDir(), "gateway.csr")
	if err := os.WriteFile(csrFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"ca", "sign-client", "--name", "gateway", "--csr", csrFile}, {"ca", "sign-server", "--name", "10.255.0.1", "--csr", csrFile}} {
		out, err := run(t, e, args...)
		if err != nil || !strings.Contains(out, "-----BEGIN CERTIFICATE-----") || strings.Contains(out, "PRIVATE KEY") {
			t.Fatalf("%v: %q %v", args, out, err)
		}
		blk, _ := pem.Decode([]byte(out[strings.Index(out, "-----BEGIN"):]))
		crt, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		if !csrKey.PublicKey.Equal(crt.PublicKey) {
			t.Fatalf("%v: certificate is not for the request's key", args)
		}
	}
	if v, _ := store.Setting(ctx, h.Pool, "edge_wg_endpoint"); v != "1.2.3.4:51820" {
		t.Fatalf("edge setting %q", v)
	}
	// edge loki: prints, records, refuses a URL with no scheme, clears
	// (DECISIONS I-95). Every host's Fluent Bit output address, so a typo
	// here is a fleet that ships nowhere.
	if out, err := run(t, e, "edge", "loki"); err != nil || !strings.Contains(out, "no Loki recorded") {
		t.Fatalf("edge loki (empty): %q %v", out, err)
	}
	if _, err := run(t, e, "edge", "loki", "10.255.0.3:3100"); err == nil {
		t.Fatal("edge loki should refuse a URL with no scheme")
	}
	if out, err := run(t, e, "edge", "loki", "http://10.255.0.3:3100"); err != nil || !strings.Contains(out, "Loki recorded") {
		t.Fatalf("edge loki: %q %v", out, err)
	}
	if v, _ := store.Setting(ctx, h.Pool, hostmgr.SettingLokiURL); v != "http://10.255.0.3:3100" {
		t.Fatalf("loki setting %q", v)
	}
	if out, err := run(t, e, "edge", "loki"); err != nil || !strings.Contains(out, "http://10.255.0.3:3100") {
		t.Fatalf("edge loki (show): %q %v", out, err)
	}
	if _, err := run(t, e, "hosts", "mark-lost", "host-02"); err != nil {
		t.Fatal(err)
	}
	// A lost host keeps no certificate serial the api would admit, and
	// rotate-cert refuses it (I-432).
	if lost, err := store.GetHostByName(ctx, h.Pool, "host-02"); err != nil || lost.CertSerial != nil || lost.PrevCertSerial != nil {
		t.Fatalf("host-02 after mark-lost: %+v %v", lost, err)
	}
	if _, err := run(t, e, "hosts", "rotate-cert", "host-02"); err == nil || !strings.Contains(err.Error(), "--reissue") {
		t.Fatalf("rotate-cert on a lost host: %v", err)
	}
	if _, err := run(t, e, "nope"); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("unknown command: %v", err)
	}
	var audits int
	_ = h.Pool.QueryRow(ctx, "select count(*) from audit_log where actor = 'admin:test'").Scan(&audits)
	if audits < 15 {
		t.Fatalf("admin actions audited: %d", audits)
	}
}
