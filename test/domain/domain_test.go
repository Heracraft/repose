// Package domain holds the one-way check on the move to repose.run.
//
// repose answers on repose.run, api.repose.run, ssh.repose.run and
// hooks.repose.run (DECISIONS I-1156). The old repose.herakraft.co names
// keep answering while installed CLIs move, which is exactly why a stray
// old name in new code or new copy is invisible: it works. This test is
// what makes it visible.
package domain

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// oldName is the domain this product moved off.
const oldName = "repose.herakraft.co"

// recordPrefixes are paths whose old names are kept on purpose and are not
// read as instructions: a record of what was true on its date, or an
// internal name that never moved.
var recordPrefixes = []struct {
	path string
	why  string
}{
	{"docs/DECISIONS.md", "every decision is a dated record; a decision is amended, never rewritten"},
	{"docs/DECISIONS-INDEX.md", "generated from DECISIONS.md, so it carries the same recorded titles"},
	{"docs/releases/", "release notes say what shipped on their date"},
	{"docs/reviews/", "dated reviews say what was found on their date"},
	{"docs/security/", "dated security reviews say what was found on their date"},
	{"docs/proposals/", "dated proposals say what was proposed on their date"},
	{"docs/workstreams/STATUS.md", "the status log is append-only history"},
	{"docs/workstreams/status-archive/", "archived status lines, kept verbatim"},
	{"reports/", "dated reports"},
	{"ops/alerts_test.yaml", "the api's scrape target is the internal name, reached over WireGuard (ops/prometheus/wireguard-peer.conf)"},
	{"ops/prometheus/wireguard-peer.conf", "the monitoring peer resolves the internal api name with its own /etc/hosts line, never DNS"},
	{"test/domain/", "this test names the old domain to look for it"},
}

// allowed is every other line in the tree that may still say the old name,
// matched by the file and a substring of the line, with the reason.
var allowed = []struct {
	file string
	line string
	why  string
}{
	// The Logto API resource, which is an audience string, not an address.
	{"internal/cli/config.go", "const apiResource", "every token already issued carries it, so moving it locks out every installed CLI"},
	{"internal/api/app/config.go", `env("API_RESOURCE"`, "the api checks tokens against that same audience"},
	{"ops/coolify/api.env.example", "API_RESOURCE=", "the deployed value of that audience"},
	{"apps/web/.env.example", "PUBLIC_API_RESOURCE=", "the dashboard asks for that audience too (I-1158)"},
	{"ops/coolify/web.env.example", "PUBLIC_API_RESOURCE=", "the deployed value of it"},
	{"apps/web/src/lib/api/resource.ts", "api.repose.herakraft.co", "the comment that says which audience PUBLIC_API_RESOURCE holds"},
	{"docs/workstreams/08-dashboard.md", "(DECISIONS I-1156, I-1158)", "the dashboard doc records the same audience"},

	// The host-to-api gRPC name: internal, and the name on the certificate
	// the api issues from its own CA, so moving it is a host and edge switch.
	{"ops/coolify/api-grpc.env.example", "GRPC_SERVER_NAMES=", "the names that certificate carries"},
	{"ops/coolify/README.md", "`api.repose.herakraft.co,10.255.255.1,10.200.3.4`", "the same names"},
	{"ops/coolify/README.md", "pass `apiServerName =", "hosts pin that internal name"},
	{"nix/hosts/default.nix", `default = "api.repose.herakraft.co:443"`, "hostd's gRPC address"},
	{"nix/hosts/prod.nix", "apiServerName =", "the production host pins it"},
	{"nix/hosts/prod.nix", "# api.repose.herakraft.co and this address", "the comment that says why"},
	{"cmd/hostd/main.go", `"api-addr"`, "hostd's flag default for the same address"},
	{"internal/hostd/register/register.go", "APIAddr    string", "a comment giving the shape of that address"},
	{"infra/azure/modules/environment/main.tf", "`api.repose.herakraft.co:443` (nix/hosts/default.nix", "names that unmoved default"},

	// The api's /metrics router, matched on a Host header the monitoring
	// peer resolves with one /etc/hosts line, never through DNS.
	{"ops/coolify/README.md", "traefik.http.routers.api-metrics.rule=", "the Traefik label"},
	{"ops/coolify/README.md", "The monitoring server resolves", "how that name is resolved"},
	{"ops/coolify/README.md", "`api.repose.herakraft.co` after the move", "the comment that says why"},
	{"ops/coolify/README.md", "curl https://api.repose.herakraft.co/metrics", "the check that the allow-list holds"},

	// Names that never belonged to the dashboard, the api or the gateway.
	{"internal/hostd/nixbuild/nixbuild_test.go", "cache.repose.herakraft.co", "the hosts' binary cache"},
	{"docs/workstreams/03-hostd.md", "cache.repose.herakraft.co", "the same binary cache"},
	{"ops/fider/README.md", "feedback.repose.herakraft.co", "a name that was never created; the board has no custom domain"},
	{"docs/interfaces/host-conventions.md", "edge_endpoint", "the edge's WireGuard endpoint name"},
	{"docs/workstreams/01-host-nixos.md", "edge_endpoint", "the same endpoint name"},

	// The overlap: the old names keep answering until the owner retires
	// them, and an operator who does not know that deletes them early.
	{"ops/coolify/README.md", "keeps answering, `https://api.repose.herakraft.co:8080`", "the api app serves both FQDNs"},
	{"ops/coolify/README.md", "`https://repose.herakraft.co:3000`", "the web app serves both FQDNs"},
	{"ops/coolify/web.env.example", "name keeps answering", "the same, beside the app's env"},
	{"infra/README.md", "names keep answering beside it for the overlap", "the operator must leave the old records alone"},
	{"infra/README.md", "The same four names under `repose.herakraft.co`", "the records to leave alone"},
	{"infra/azure/prod/prod.tfvars", "The old repose.herakraft.co names keep answering", "why the herakraft.co zone is still live and unmanaged here"},
	{"infra/azure/prod/variables.tf", "DNS zone the repose names live in", "the same, on the variable"},
	{"infra/dns/main.tf", "repose.herakraft.co names that keep answering", "so nobody deletes them early"},
	{"internal/api/http/server.go", "wildcard is also what lets the old repose.herakraft.co", "the wildcard CORS origin is what serves the old dashboard meanwhile"},

	// Docs that name the Logto API resource. It is an audience, not an
	// address, so it did not move with the api (I-1156, I-1158).
	{"docs/DESIGN.md", "JWT access tokens for the", "DESIGN's API auth paragraph"},
	{"docs/interfaces/api.md", "an opaque audience that kept its name", "the HTTP API contract"},
	{"docs/ops/AZURE-SETUP.md", "identifier `https://api.repose.herakraft.co`", "the Logto resource the owner creates"},
	{"docs/ops/RUNBOOK.md", "(an audience, which did not move with", "the same, in the runbook"},
	{"docs/ops/coolify.md", "resource `https://api.repose.herakraft.co`", "the same, in the Coolify click path"},
	{"docs/workstreams/05-control-plane-api.md", "equals `https://api.repose.herakraft.co`", "the aud claim the api checks"},
	{"docs/workstreams/07-cli.md", "`resource=https://api.repose.herakraft.co`", "the resource the CLI asks a token for"},
	{"docs/workstreams/08-dashboard.md", "(I-1158), `scopes:", "what production sets PUBLIC_API_RESOURCE to"},

	// Docs that name the internal host-to-api gRPC address.
	{"docs/DESIGN.md", "grpc/mtls (outbound)", "the network diagram"},
	{"docs/workstreams/03-hostd.md", "`stream.Run` dials", "what hostd dials"},
	{"docs/workstreams/11-infra-opentofu.md", "where nothing", "the generic host's wrong-but-plausible default"},
	{"docs/ops/RUNBOOK.md", "curl -sv https://api.repose.herakraft.co:443", "the runbook step that tests it from a host"},
	{"docs/CHECKLIST.md", "/metrics` from outside the tunnel", "the /metrics router matches the internal name"},
	{"docs/ops/coolify.md", "so Coolify refused", "a dated observation from before the move"},

	// The wildcard the old zone answered with, kept because the record
	// that is missing is what the reader has to create.
	{"docs/ops/AZURE-SETUP.md", "reached the wrong place", "what the old name resolved to before the records existed"},
	{"docs/ops/RUNBOOK.md", "It applies to `ssh.repose.herakraft.co` too", "the symptom holds for the old name while it answers"},
	{"docs/workstreams/11-infra-opentofu.md", "resolved to Cloudflare's", "the same wildcard, in the workstream doc"},
	{"docs/workstreams/11-infra-opentofu.md", "returned `172.67.175.123`", "a dated dig, kept as the evidence for the open row"},
	{"docs/workstreams/11-infra-opentofu.md", `"Real-edge evidence" in 06`, "the name the M2 gate reached the gateway as"},

	// A dated observation, kept as the evidence for the plan-time warning.
	{"infra/README.md", "dig +short ssh.repose.herakraft.co", "what the herakraft.co wildcard answered on 2026-09-20"},
	{"infra/azure/modules/environment/main.tf", "with manage_dns off ssh.repose.herakraft.co resolved", "the same observation"},

	// The gateway's SSH host certificate carries every name users dial,
	// so while the old one answers it is on the certificate (I-1160).
	{"docs/ops/RUNBOOK.md", "only `ssh.repose.herakraft.co`, every CLI connection", "the dated failure that section was written for"},
	{"docs/ops/RUNBOOK.md", "--principal ssh.repose.run,ssh.repose.herakraft.co", "a certificate missing a live name is a name nobody can reach"},
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// hits runs `git grep` for the old name over every tracked file and returns
// one entry per matching line.
func hits(t *testing.T, root string) []struct{ file, line string } {
	t.Helper()
	cmd := exec.Command("git", "grep", "-n", "-I", "--fixed-strings", oldName, "--", ".")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		// Exit 1 with no output means no match at all, which is fine.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return nil
		}
		t.Fatalf("git grep: %v: %s", err, stderr.String())
	}
	var got []struct{ file, line string }
	for _, raw := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if raw == "" {
			continue
		}
		// path:lineno:text
		path, _, ok := strings.Cut(raw, ":")
		if !ok {
			t.Fatalf("git grep printed a line this test cannot read: %q", raw)
		}
		got = append(got, struct{ file, line string }{file: path, line: raw})
	}
	return got
}

// TestOldDomainOnlyWhereItIsAllowed fails on any `repose.herakraft.co` the
// allow-list above does not name. The old name still answers, so a stray
// one in new code or new copy works and nothing else would catch it.
func TestOldDomainOnlyWhereItIsAllowed(t *testing.T) {
	root := repoRoot(t)
	var stray []string
	for _, h := range hits(t, root) {
		if allowedHit(h.file, h.line) {
			continue
		}
		stray = append(stray, h.line)
	}
	if len(stray) > 0 {
		t.Errorf("repose answers on repose.run (DECISIONS I-1156); %d line(s) still name %s.\n"+
			"Move them, or add the line to the allow-list in %s with the reason it stays:\n  %s",
			len(stray), oldName, "test/domain/domain_test.go", strings.Join(stray, "\n  "))
	}
}

// TestAllowListHasNoDeadRows keeps the list honest: a row that matches
// nothing is a reason nobody has to read any more.
func TestAllowListHasNoDeadRows(t *testing.T) {
	root := repoRoot(t)
	got := hits(t, root)
	for _, a := range allowed {
		found := false
		for _, h := range got {
			if strings.HasPrefix(h.line, a.file+":") && strings.Contains(h.line, a.line) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("allow-list row %s %q matches no line any more; delete it", a.file, a.line)
		}
	}
	for _, p := range recordPrefixes {
		found := false
		for _, h := range got {
			if strings.HasPrefix(h.file, p.path) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("allow-listed path %s holds no %s any more; delete the row", p.path, oldName)
		}
	}
}

func allowedHit(file, line string) bool {
	for _, p := range recordPrefixes {
		if strings.HasPrefix(file, p.path) {
			return true
		}
	}
	for _, a := range allowed {
		if file == a.file && strings.Contains(line, a.line) {
			return true
		}
	}
	return false
}
