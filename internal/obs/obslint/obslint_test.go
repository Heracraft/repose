package obslint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is the checkout, three levels up from internal/obs/obslint.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve the repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("no go.mod at %s: %v", root, err)
	}
	return root
}

// TestRepository is the check docs/workstreams/10-observability.md §2 asks
// for: it runs over every Go file in the repository and fails on any metric
// outside the repose_ namespace, any log call that does not name its event,
// any never-log field name, and any logger or registry built outside
// internal/obs.
func TestRepository(t *testing.T) {
	findings, err := Check(repoRoot(t), []string{"cmd", "internal"})
	if err != nil {
		t.Fatalf("check the repository: %v", err)
	}
	for _, f := range findings {
		t.Errorf("%s", f)
	}
	if len(findings) > 0 {
		t.Logf("%d finding(s); each one is a line docs/ops/OBSERVABILITY.md or §5 forbids", len(findings))
	}
}

// TestRules checks each rule against a snippet that breaks it and one that
// does not, so a rule cannot quietly stop working.
func TestRules(t *testing.T) {
	cases := []struct {
		name string
		rel  string
		src  string
		want string // the rule expected, or "" for no finding
	}{
		{
			name: "std log import",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport \"log\"\nfunc f() { log.Print(\"x\") }\n",
			want: RuleStdLog,
		},
		{
			name: "slog is not the std log",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport \"log/slog\"\nvar _ = slog.LevelInfo\n",
			want: "",
		},
		{
			name: "fmt.Print in a library",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport \"fmt\"\nfunc f() { fmt.Println(\"x\") }\n",
			want: RulePrint,
		},
		{
			name: "fmt.Print in a main package",
			rel:  "cmd/thing/main.go",
			src:  "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"usage\") }\n",
			want: "",
		},
		{
			name: "logger built outside obs",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport (\"log/slog\"\n\"os\")\nfunc f() *slog.Logger { return slog.New(slog.NewJSONHandler(os.Stdout, nil)) }\n",
			want: RuleLoggerCtor,
		},
		{
			name: "registry built outside obs",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport \"github.com/prometheus/client_golang/prometheus\"\nfunc f() { _ = prometheus.NewRegistry() }\n",
			want: RuleMetricsCtor,
		},
		{
			name: "log call with no event",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport \"log/slog\"\nfunc f(log *slog.Logger) { log.Info(\"something happened\", \"guest_id\", \"g1\") }\n",
			want: RuleEvent,
		},
		{
			name: "log call with an event",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport \"log/slog\"\nfunc f(log *slog.Logger) { log.Info(\"guest started\", \"event\", \"guest_start\", \"guest_id\", \"g1\") }\n",
			want: "",
		},
		{
			name: "log call with an event as a slog.Attr",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport \"log/slog\"\nfunc f(log *slog.Logger) { log.LogAttrs(nil, slog.LevelInfo, \"m\", slog.String(\"event\", \"ready\")) }\n",
			want: "",
		},
		{
			name: "never-log field",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport \"log/slog\"\nfunc f(log *slog.Logger) { log.Info(\"m\", \"event\", \"cert_issue\", \"cert\", \"---BEGIN---\") }\n",
			want: RuleField,
		},
		{
			name: "a certificate serial is not a certificate",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport \"log/slog\"\nfunc f(log *slog.Logger) { log.Info(\"m\", \"event\", \"cert_issue\", \"cert_serial\", \"7\") }\n",
			want: "",
		},
		{
			name: "a view ticket",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport \"log/slog\"\nfunc f(log *slog.Logger) { log.Info(\"m\", \"event\", \"browser_view_open\", \"ticket\", \"3f9c\") }\n",
			want: RuleField,
		},
		{
			name: "a view ticket's row id is not the ticket",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport \"log/slog\"\nfunc f(log *slog.Logger) { log.Info(\"m\", \"event\", \"browser_view_open\", \"ticket_id\", \"7\") }\n",
			want: "",
		},
		{
			name: "guest path",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport \"log/slog\"\nfunc f(log *slog.Logger) { log.Info(\"m\", \"event\", \"exec\", \"path\", \"/home/dev/app/src/secret.ts\") }\n",
			want: RuleField,
		},
		{
			name: "metric outside the namespace",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport \"github.com/prometheus/client_golang/prometheus\"\nvar g = prometheus.NewGauge(prometheus.GaugeOpts{Name: \"guests\"})\n",
			want: RuleMetricName,
		},
		{
			name: "metric in the namespace",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport \"github.com/prometheus/client_golang/prometheus\"\nvar g = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: \"repose\", Name: \"guests\"})\n",
			want: "",
		},
		{
			name: "logger with an unrecognisable name",
			rel:  "internal/thing/a.go",
			src:  "package thing\nimport \"log/slog\"\ntype S struct { lg *slog.Logger }\n",
			want: RuleLoggerName,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, filepath.Base(c.rel), c.src, parser.ParseComments)
			if err != nil {
				t.Fatalf("parse the snippet: %v", err)
			}
			got := checkFile(c.rel, fset, f)
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("want no finding, got %v", got)
				}
				return
			}
			for _, g := range got {
				if g.Rule == c.want {
					return
				}
			}
			t.Fatalf("want rule %s, got %v", c.want, got)
		})
	}
}

// TestEventsFindsCallSites proves the scanner the event-coverage test relies
// on actually reads the repository, and that it attributes a file to a
// component by path.
func TestEventsFindsCallSites(t *testing.T) {
	events, err := Events(repoRoot(t), []string{"cmd", "internal"})
	if err != nil {
		t.Fatalf("scan events: %v", err)
	}
	if len(events) < 20 {
		t.Fatalf("found %d event names in the repository; the scanner is not reading it", len(events))
	}
	sites, ok := events["guest_start"]
	if !ok {
		t.Fatal("no guest_start call site; hostd emits it")
	}
	for _, s := range sites {
		if s.Component != "hostd" {
			continue
		}
		if !strings.HasPrefix(s.File, "internal/hostd/") && !strings.HasPrefix(s.File, "cmd/hostd/") {
			t.Errorf("guest_start attributed to hostd from %s", s.File)
		}
		return
	}
	t.Error("guest_start is emitted by no file attributed to hostd")
}

// TestComponentOf pins the path-to-component mapping the coverage report
// uses, including the two binaries that are not their own component.
func TestComponentOf(t *testing.T) {
	for path, want := range map[string]string{
		"internal/hostd/app/app.go":    "hostd",
		"cmd/hostd/main.go":            "hostd",
		"internal/guestd/guestd.go":    "guestd",
		"cmd/repose-hook/main.go":      "hook",
		"internal/hostdev/server.go":   "hostdev",
		"cmd/api/main.go":              "api",
		"cmd/gateway/main.go":          "gateway",
		"internal/obs/logger.go":       "",
		"internal/vsockrpc/rpc.go":     "",
		"cmd/repose-admin/main.go":     "admin",
		"internal/admin/admin.go":      "admin",
		"internal/api/http/server.go":  "api",
		"internal/fakes/hostd/f.go":    "",
		"internal/obs/obslint/l.go":    "",
		"internal/gen/x/y.pb.go":       "",
		"cmd/repose/main.go":           "cli",
		"internal/hostd/stream/s.go":   "hostd",
		"internal/guestd/warn/warn.go": "guestd",
	} {
		if got := componentOf(path); got != want {
			t.Errorf("componentOf(%q) = %q, want %q", path, got, want)
		}
	}
}

var _ = ast.Inspect
