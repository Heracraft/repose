// Package obslint is the source side of the observability rules: the checks
// docs/workstreams/10-observability.md §2 asks for ("enforced by a test that
// fails on any metric outside the repose_ namespace or any log call missing
// component") and the never-log list of docs/ops/OBSERVABILITY.md.
//
// Three of the rules are structural rather than textual, which is the point:
// a logger can only come from obs.NewLogger, so `component` cannot be
// missing; a metric can only be registered through obs/metrics.Metrics, so it
// cannot be outside the repose_ namespace or carry a per-project label. What is left
// for a source check is that nobody bypasses those two constructors, that
// every log call names an event, and that no call site passes a field name
// the never-log list forbids.
//
// It parses rather than type-checks, so it needs no build and runs in a
// unit test in milliseconds. Where parsing alone is not enough to know that
// an expression is a logger, rule loggerName closes the gap: every
// *slog.Logger declared in the repository is named so that the log-call rule
// recognises it.
package obslint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Namespace is the metric prefix obs.Namespace defines. It is repeated here
// rather than imported so that obslint parses the repository without
// depending on the package it checks.
const Namespace = "repose"

// Rule names, so a finding can be silenced in one place if it ever has to be.
const (
	RuleStdLog      = "stdlog"       // the standard library's log package
	RulePrint       = "print"        // fmt.Print* outside a CLI surface
	RuleLoggerCtor  = "logger-ctor"  // a logger built outside internal/obs
	RuleMetricsCtor = "metrics-ctor" // a registry built outside internal/obs
	RuleEvent       = "event"        // a log call with no event field
	RuleField       = "field"        // a field name on the never-log list
	RuleMetricName  = "metric-name"  // a metric outside the repose_ namespace
	RuleLoggerName  = "logger-name"  // a *slog.Logger not named like a logger
)

// Finding is one rule violation.
type Finding struct {
	File string
	Line int
	Rule string
	Msg  string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: [%s] %s", f.File, f.Line, f.Rule, f.Msg)
}

// Site is one `"event", "<name>"` pair found in the source.
type Site struct {
	Event string
	File  string
	Line  int
	// Component is the component the file belongs to, from the path.
	Component string
}

// forbiddenFields are the never-log names of docs/ops/OBSERVABILITY.md as
// they would appear as a log field name. obs.NewLogger redacts the first
// group at runtime; this list is wider because a source check can afford to
// be, and a build-time error is cheaper than a redacted line in Loki.
var forbiddenFields = []string{
	"token", "secret", "password", "authorization", "cert", "key",
	"email", "handle", "remote_url", "remote", "prompt", "args", "argv",
	"env", "cmdline", "command_line", "user_agent", "useragent",
	"ip", "ip_addr", "remote_addr", "client_ip", "card", "last4",
	"transcript", "output", "stdout", "stderr", "terminal", "pane",
	"path", "file", "filename", "dir", "cwd", "jwt", "refresh_token",
	"private_key", "public_key", "csr", "certificate", "join_token",
}

// fieldExceptions are names that contain a forbidden word but are bounded
// ids, counts or enums, and are therefore allowed. Each one is here because
// it is in use and defensible, not because it was convenient.
var fieldExceptions = map[string]bool{
	// A serial and a fingerprint identify a certificate without carrying it.
	"cert_serial": true, "cert_fingerprint": true, "key_id": true,
	// Whether a token was present, never its value.
	"token_used": true, "has_token": true,
	// Sizes and counts.
	"cert_bytes": true, "key_bytes": true, "output_bytes": true,
	"secrets_count": true, "path_count": true,
	// Host paths are not tenant paths: a store path or a unit path on the
	// host is the platform's own business. A path *inside a guest* is the
	// one the never-log list forbids, and it cannot be told apart by name,
	// so `store_path` is allowed and `path` is not.
	"store_path": true, "unit_path": true,
}

// cliOutputFiles may call fmt.Print*: they write to a human at a terminal,
// which is not logging. Every other file must go through a logger.
var cliOutputFiles = map[string]bool{
	"internal/hostdev/cli.go": true, // the operator CLI of DECISIONS I-17
	"cmd/guestd/call.go":      true, // `guestd call` prints the response as JSON (I-32)
}

// cliOutputDirs are whole packages that are a terminal surface: the user
// CLI (workstream 07) prints its results and prompts for a human.
var cliOutputDirs = []string{"internal/cli/"}

func isCLIOutput(rel string) bool {
	if cliOutputFiles[rel] {
		return true
	}
	for _, d := range cliOutputDirs {
		if strings.HasPrefix(rel, d) {
			return true
		}
	}
	return false
}

// logMethods are the slog.Logger methods a call site uses.
var logMethods = map[string]bool{
	"Debug": true, "Info": true, "Warn": true, "Error": true, "Log": true,
	"DebugContext": true, "InfoContext": true, "WarnContext": true,
	"ErrorContext": true, "LogAttrs": true,
}

// Check parses every Go file under the given directories of root and returns
// the findings, sorted by file and line.
func Check(root string, dirs []string) ([]Finding, error) {
	var out []Finding
	err := walk(root, dirs, func(rel string, fset *token.FileSet, f *ast.File) {
		out = append(out, checkFile(rel, fset, f)...)
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// Events returns every event name emitted in the source, with its call
// sites, keyed by event name.
func Events(root string, dirs []string) (map[string][]Site, error) {
	// A first pass collects the string constants, so a call site that names
	// its event through one (freeze's WarnFreezeTimeout, obs's EventReady)
	// is found as well as a literal.
	consts := map[string]string{}
	if err := walk(root, dirs, func(_ string, _ *token.FileSet, f *ast.File) {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, nm := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					if v, ok := stringValue(vs.Values[i]); ok {
						consts[nm.Name] = v
					}
				}
			}
		}
	}); err != nil {
		return nil, err
	}

	out := map[string][]Site{}
	add := func(rel string, fset *token.FileSet, pos token.Pos, raw string) {
		name, ok := resolveEvent(raw, consts)
		if !ok {
			return
		}
		out[name] = append(out[name], Site{
			Event:     name,
			File:      rel,
			Line:      fset.Position(pos).Line,
			Component: componentOf(rel),
		})
	}
	err := walk(root, dirs, func(rel string, fset *token.FileSet, f *ast.File) {
		ast.Inspect(f, func(n ast.Node) bool {
			// A slice of attributes built before the call and spread into it:
			// `attrs := []any{"event", "request", ...}; log.Log(ctx, lv, msg,
			// attrs...)`. The api's request line is written that way, and a
			// scanner that only reads call arguments cannot see it.
			if lit, ok := n.(*ast.CompositeLit); ok {
				elts := lit.Elts
				for i := 0; i+1 < len(elts); i++ {
					if k, ok := stringValue(elts[i]); ok && k == "event" {
						if v, ok := stringValue(elts[i+1]); ok {
							add(rel, fset, lit.Pos(), v)
						} else if n := lastSegment(elts[i+1]); n != "" {
							add(rel, fset, lit.Pos(), n)
						}
					}
				}
			}
			call, ok := n.(*ast.CallExpr)
			if !ok || !isLogCall(call) {
				return true
			}
			for _, raw := range eventNames(call) {
				add(rel, fset, call.Pos(), raw)
			}
			return true
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// componentOf maps a file to the component whose logs it writes, from the
// repository layout. A file that belongs to no component returns "".
func componentOf(rel string) string {
	switch {
	case strings.HasPrefix(rel, "cmd/repose-hook/"):
		return "hook"
	case strings.HasPrefix(rel, "cmd/hostdev/"), strings.HasPrefix(rel, "internal/hostdev/"):
		return "hostdev"
	case strings.HasPrefix(rel, "cmd/repose-admin/"), strings.HasPrefix(rel, "internal/admin/"):
		return "admin"
	case strings.HasPrefix(rel, "cmd/repose/"), strings.HasPrefix(rel, "internal/cli/"):
		return "cli"
	case strings.HasPrefix(rel, "cmd/hostd/"), strings.HasPrefix(rel, "internal/hostd/"):
		return "hostd"
	case strings.HasPrefix(rel, "cmd/guestd/"), strings.HasPrefix(rel, "internal/guestd/"):
		return "guestd"
	// internal/billing is the api's: the rollup, the Polar webhook, the
	// overage line and the dunning job all run in the api process and log as
	// the api (workstream 09).
	case strings.HasPrefix(rel, "cmd/api/"), strings.HasPrefix(rel, "internal/api/"), strings.HasPrefix(rel, "internal/billing/"):
		return "api"
	case strings.HasPrefix(rel, "cmd/gateway/"), strings.HasPrefix(rel, "internal/gateway/"):
		return "gateway"
	default:
		return ""
	}
}

// walk visits every non-generated, non-test-data Go file under dirs.
func walk(root string, dirs []string, visit func(rel string, fset *token.FileSet, f *ast.File)) error {
	fset := token.NewFileSet()
	for _, dir := range dirs {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		err := filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				// internal/gen holds protobuf stubs, which are generated and
				// not ours to name; testdata is fixtures.
				if info.Name() == "gen" || info.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			// Generated stubs are not ours to name. Test files are skipped
			// because a rule needs a test that breaks it: TestRules checks
			// every rule against a synthetic file instead, and an event
			// "emitted" only by a test is not emitted.
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, ".pb.go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			f, perr := parser.ParseFile(fset, p, nil, parser.ParseComments)
			if perr != nil {
				return fmt.Errorf("parse %s: %w", p, perr)
			}
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				return rerr
			}
			visit(filepath.ToSlash(rel), fset, f)
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func checkFile(rel string, fset *token.FileSet, f *ast.File) []Finding {
	var out []Finding
	add := func(pos token.Pos, rule, msg string) {
		out = append(out, Finding{File: rel, Line: fset.Position(pos).Line, Rule: rule, Msg: msg})
	}
	inObs := strings.HasPrefix(rel, "internal/obs/")
	isMain := f.Name.Name == "main"

	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if path == "log" {
			add(imp.Pos(), RuleStdLog, `imports "log"; every binary logs through internal/obs (log/slog), so a line has ts, level, component and event`)
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg := exprString(sel.X)
			name := sel.Sel.Name
			switch {
			case pkg == "fmt" && strings.HasPrefix(name, "Print"):
				// A main package prints usage and version; the CLI surfaces
				// print for a human. Everything else must log.
				if !isMain && !isCLIOutput(rel) {
					add(x.Pos(), RulePrint, fmt.Sprintf("fmt.%s outside a CLI surface; log through internal/obs instead", name))
				}
			case pkg == "slog" && (name == "New" || name == "NewJSONHandler" || name == "NewTextHandler" || name == "Default" || name == "SetDefault"):
				if !inObs {
					add(x.Pos(), RuleLoggerCtor, fmt.Sprintf("slog.%s outside internal/obs; use obs.NewLogger so the line carries component and the never-log fields are redacted", name))
				}
			case pkg == "prometheus" && (name == "NewRegistry" || name == "NewPedanticRegistry"):
				if !inObs {
					add(x.Pos(), RuleMetricsCtor, fmt.Sprintf("prometheus.%s outside internal/obs; use metrics.New (internal/obs/metrics) so the repose_ namespace and the label list are enforced", name))
				}
			case pkg == "promauto":
				if !inObs {
					add(x.Pos(), RuleMetricsCtor, "promauto registers on the default registry, which no repose binary serves; register on an obs.Metrics")
				}
			}
			if isLogCall(x) {
				out = append(out, checkLogCall(rel, fset, x)...)
			}
			return true
		case *ast.CompositeLit:
			// prometheus.<Kind>Opts{...}: the namespace is checked here as
			// well as at registration, so a metric that is never registered
			// in a test still cannot ship with the wrong name.
			sel, ok := x.Type.(*ast.SelectorExpr)
			if !ok || exprString(sel.X) != "prometheus" || !strings.HasSuffix(sel.Sel.Name, "Opts") {
				return true
			}
			if sel.Sel.Name == "ProcessCollectorOpts" || sel.Sel.Name == "HandlerOpts" {
				return true
			}
			var ns, mname string
			for _, e := range x.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				k := exprString(kv.Key)
				v, ok := stringValue(kv.Value)
				if !ok {
					// `Namespace: obs.Namespace` and `Namespace: Namespace`
					// are the constant whose value is "repose"; a parser
					// cannot follow it, and the registry check does.
					if name := exprString(kv.Value); strings.HasSuffix(name, "Namespace") {
						v = Namespace
					}
				}
				switch k {
				case "Namespace":
					ns = v
				case "Name":
					mname = v
				}
			}
			if ns != "repose" && !strings.HasPrefix(mname, "repose_") {
				add(x.Pos(), RuleMetricName, fmt.Sprintf("%s has Namespace %q: every metric is repose_* (docs/workstreams/10-observability.md §5)", sel.Sel.Name, ns))
			}
			return true
		case *ast.Field:
			// rule loggerName: a *slog.Logger must be named so that the
			// log-call rule recognises calls on it. internal/obs is exempt:
			// it passes loggers around as values (WithLogger, Logger(ctx,
			// fallback)) rather than logging through them.
			if inObs || !isSlogLoggerType(x.Type) {
				return true
			}
			for _, nm := range x.Names {
				if !looksLikeLogger(nm.Name) {
					add(nm.Pos(), RuleLoggerName, fmt.Sprintf("*slog.Logger named %q; name it log or logger so the event rule sees calls on it", nm.Name))
				}
			}
			return true
		}
		return true
	})
	return out
}

// checkLogCall requires an event field and refuses a never-log field name.
func checkLogCall(rel string, fset *token.FileSet, call *ast.CallExpr) []Finding {
	var out []Finding
	line := fset.Position(call.Pos()).Line
	keys, hasEvent, dynamic := logKeys(call)
	for _, k := range keys {
		lower := strings.ToLower(k)
		if fieldExceptions[lower] {
			continue
		}
		for _, bad := range forbiddenFields {
			if lower == bad {
				out = append(out, Finding{File: rel, Line: line, Rule: RuleField,
					Msg: fmt.Sprintf("log field %q is on the never-log list of docs/ops/OBSERVABILITY.md", k)})
				break
			}
		}
	}
	if !hasEvent && !dynamic {
		out = append(out, Finding{File: rel, Line: line, Rule: RuleEvent,
			Msg: "log call has no event field; every line names what happened (docs/workstreams/10-observability.md §5)"})
	}
	return out
}

// logKeys returns the literal field names of a log call, whether one of them
// is "event", and whether the call passes something other than literal
// key/value pairs (a slog.Attr, a spread, or a variable key), in which case
// the event rule cannot decide and stays quiet.
func logKeys(call *ast.CallExpr) (keys []string, hasEvent, dynamic bool) {
	sel := call.Fun.(*ast.SelectorExpr)
	args := call.Args
	// Skip the fixed leading arguments: (ctx, level, msg) for Log and
	// LogAttrs, (ctx, msg) for the *Context methods, (msg) otherwise.
	switch sel.Sel.Name {
	case "Log", "LogAttrs":
		if len(args) < 3 {
			return nil, false, true
		}
		args = args[3:]
	case "DebugContext", "InfoContext", "WarnContext", "ErrorContext":
		if len(args) < 2 {
			return nil, false, true
		}
		args = args[2:]
	default:
		if len(args) < 1 {
			return nil, false, true
		}
		args = args[1:]
	}
	if call.Ellipsis != token.NoPos {
		dynamic = true
	}
	for i := 0; i < len(args); i += 2 {
		k, ok := stringValue(args[i])
		if !ok {
			// slog.String("event", ...) and friends carry their own key.
			if inner, isAttr := attrKey(args[i]); isAttr {
				keys = append(keys, inner)
				if inner == "event" {
					hasEvent = true
				}
				// An Attr occupies one argument, not two.
				i--
				continue
			}
			dynamic = true
			continue
		}
		keys = append(keys, k)
		if k == "event" {
			hasEvent = true
		}
	}
	return keys, hasEvent, dynamic
}

// eventNames returns the event values a log call names, as written: a string
// literal comes back as its value, an identifier as its name for
// resolveEvent to look up. A call that computes its event from a parameter
// contributes nothing, which the coverage report shows as an event with no
// call site.
func eventNames(call *ast.CallExpr) []string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	var out []string
	args := call.Args
	switch sel.Sel.Name {
	case "Log", "LogAttrs":
		if len(args) < 3 {
			return nil
		}
		args = args[3:]
	case "DebugContext", "InfoContext", "WarnContext", "ErrorContext":
		if len(args) < 2 {
			return nil
		}
		args = args[2:]
	default:
		if len(args) < 1 {
			return nil
		}
		args = args[1:]
	}
	for i := 0; i < len(args); i++ {
		if k, ok := stringValue(args[i]); ok {
			if k == "event" && i+1 < len(args) {
				if v, ok := stringValue(args[i+1]); ok {
					out = append(out, v)
				} else if n := lastSegment(args[i+1]); n != "" {
					out = append(out, n)
				}
			}
			i++ // the value
			continue
		}
		if k, isAttr := attrKey(args[i]); isAttr && k == "event" {
			if c, ok := args[i].(*ast.CallExpr); ok && len(c.Args) > 1 {
				if v, ok := stringValue(c.Args[1]); ok {
					out = append(out, v)
				} else if n := lastSegment(c.Args[1]); n != "" {
					out = append(out, n)
				}
			}
		}
	}
	return out
}

// resolveEvent turns what eventNames found into an event name: a literal is
// itself, a known string constant is its value, and obs.EventGuestStart is
// the lower_snake of the part after Event. A name that resolves to none of
// those is a variable and is dropped.
func resolveEvent(raw string, consts map[string]string) (string, bool) {
	if raw == "" {
		return "", false
	}
	if v, ok := consts[raw]; ok {
		return v, true
	}
	if strings.HasPrefix(raw, "Event") && raw != "Event" {
		return toSnake(raw[len("Event"):]), true
	}
	// A literal arrives already lower_snake; anything else is a variable.
	if strings.ToLower(raw) == raw && !strings.Contains(raw, " ") {
		return raw, true
	}
	return "", false
}

// lastSegment renders the identifier at the end of an expression
// (obs.EventReady -> EventReady), or "" when the expression is not a name.
func lastSegment(e ast.Expr) string {
	s := exprString(e)
	if s == "" {
		return ""
	}
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

func toSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// attrKey reads the key out of slog.String("k", v) and its siblings.
func attrKey(e ast.Expr) (string, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || exprString(sel.X) != "slog" || len(call.Args) == 0 {
		return "", false
	}
	switch sel.Sel.Name {
	case "String", "Int", "Int64", "Uint64", "Float64", "Bool", "Time", "Duration", "Any", "Group":
		k, ok := stringValue(call.Args[0])
		return k, ok
	}
	return "", false
}

// isLogCall recognises a call on something that is a logger: the method name
// is one of slog's and the receiver expression mentions a log. Rule
// loggerName keeps that heuristic honest by refusing a *slog.Logger with
// another name.
func isLogCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !logMethods[sel.Sel.Name] {
		return false
	}
	recv := strings.ToLower(exprString(sel.X))
	// slog.Info() and friends log to the default handler, which no repose
	// binary configures; the logger-ctor rule catches slog.Default and this
	// keeps the package-level shorthands out of the log-call rules.
	if recv == "slog" {
		return false
	}
	return strings.Contains(recv, "log")
}

func looksLikeLogger(name string) bool {
	return strings.Contains(strings.ToLower(name), "log")
}

func isSlogLoggerType(e ast.Expr) bool {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && exprString(sel.X) == "slog" && sel.Sel.Name == "Logger"
}

func stringValue(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// exprString renders a selector chain or identifier as source text, which is
// all the rules need; anything else renders as "".
func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		if p := exprString(x.X); p != "" {
			return p + "." + x.Sel.Name
		}
		return x.Sel.Name
	case *ast.CallExpr:
		return exprString(x.Fun)
	case *ast.IndexExpr:
		return exprString(x.X)
	case *ast.StarExpr:
		return exprString(x.X)
	case *ast.ParenExpr:
		return exprString(x.X)
	}
	return ""
}
