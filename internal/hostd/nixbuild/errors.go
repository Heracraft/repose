package nixbuild

import (
	"regexp"
	"strconv"
	"strings"
)

// Error is a mapped Nix failure, per docs/interfaces/grpc-hostd.md: the
// code, a message whose first line is a summary followed by a blank line
// and the verbatim Nix output (capped at 32 KB), and the fragment line when
// the location parsed. The summaries are the exact first lines of
// docs/workstreams/12-nix-config-pipeline.md "The five canonical error
// cases"; the CLI prefixes them by code (docs/interfaces/nix-build-contract.md
// "What the user reads").
type Error struct {
	Code         string
	Message      string
	FragmentLine int32
}

func (e *Error) Error() string { return e.Code + ": " + firstLine(e.Message) }

// MessageCap is the verbatim-output cap from the interface doc.
const MessageCap = 32 << 10

var (
	fragLocRe   = regexp.MustCompile(`fragment\.nix:(\d+):(\d+)`)
	errorLineRe = regexp.MustCompile(`(?m)^\s*error: (.*)$`)
	didYouMean  = regexp.MustCompile(`(?m)^\s*(Did you mean .*\?)\s*$`)
	buildingRe  = regexp.MustCompile(`(?m)building '(/nix/store/[^']+\.drv)'`)
	drvFailRe   = regexp.MustCompile(`(?m)(?:Cannot build|builder for) '(/nix/store/[^']+\.drv)'`)
	assertionRe = regexp.MustCompile(`(?m)^\s*- (.*)$`)
	hmOptionRe  = regexp.MustCompile("The option `home-manager\\.users\\.dev\\.([^']+)' does not exist")
	nixPathRe   = regexp.MustCompile(`cannot look up '(<[^>]+>)' in pure evaluation mode`)
	uriRe       = regexp.MustCompile(`access to URI '([^']+)' is forbidden`)
)

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func capVerbatim(stderr string) string {
	if len(stderr) > MessageCap {
		return stderr[len(stderr)-MessageCap:]
	}
	return stderr
}

func compose(summary, stderr string) string {
	return summary + "\n\n" + strings.TrimRight(capVerbatim(stderr), "\n")
}

// lastError returns the text of the final `error:` line and the byte
// offset where it starts.
func lastError(stderr string) (string, int) {
	ms := errorLineRe.FindAllStringSubmatchIndex(stderr, -1)
	if len(ms) == 0 {
		return "", -1
	}
	m := ms[len(ms)-1]
	return strings.TrimSpace(stderr[m[2]:m[3]]), m[0]
}

// fragmentLine finds the location Nix reported for the final error: the
// first fragment.nix mention after the last `error:` line, else the last
// mention anywhere (the trace), else 0.
func fragmentLine(stderr string) (int32, string) {
	_, off := lastError(stderr)
	if off >= 0 {
		if m := fragLocRe.FindStringSubmatch(stderr[off:]); m != nil {
			l, _ := strconv.Atoi(m[1])
			return int32(l), m[1] + ":" + m[2]
		}
	}
	ms := fragLocRe.FindAllStringSubmatch(stderr, -1)
	if len(ms) > 0 {
		m := ms[len(ms)-1]
		l, _ := strconv.Atoi(m[1])
		return int32(l), m[1] + ":" + m[2]
	}
	return 0, ""
}

// optionHint follows an unknown-option summary. A fragment is a
// home-manager module, so NixOS options such as environment.systemPackages
// or services.* fail; it names where each goes instead (DECISIONS I-483:
// the old hint named `repose config menu`, which the CLI has never had).
const optionHint = "; a fragment is a home-manager module: packages go in home.packages, databases come from `repose config add` or repose.system"

// flakeOutputs are the top-level attributes of a flake.nix. One of them
// reported as an unknown option means the file applied is a flake.
var flakeOutputs = map[string]bool{"description": true, "inputs": true, "outputs": true, "nixConfig": true}

// flakeHint is the whole summary for a flake.nix applied as a fragment.
const flakeHint = "this file is a Nix flake; `repose config apply` takes a home-manager module such as repose.nix (https://repose.herakraft.co/docs/config#write-it-in-nix), and agents already load a flake's dev shell from the checkout"

// MapEvalError turns `nix eval` stderr into an eval_failed Error.
func MapEvalError(stderr string) *Error {
	msg, _ := lastError(stderr)
	if msg == "" {
		msg = "evaluation failed"
	}
	line, loc := fragmentLine(stderr)
	at := ""
	if loc != "" {
		at = " at fragment.nix:" + loc
	}
	var summary string
	switch {
	case strings.HasPrefix(msg, "syntax error, "):
		// "syntax error, unexpected ';'" -> "syntax error at fragment.nix:1:32, unexpected ';'"
		summary = "syntax error" + at + ", " + strings.TrimPrefix(msg, "syntax error, ")
	case uriRe.MatchString(msg) && strings.Contains(msg, "restricted mode"):
		summary = "eval-time fetch not allowed" + at + "; use pkgs.fetchurl { url = ...; hash = ...; }"
	case strings.Contains(msg, "cannot fetch") && strings.Contains(msg, "pure evaluation mode"),
		strings.Contains(msg, "doesn't fetch unlocked input"):
		summary = "eval-time fetch not allowed" + at + "; use pkgs.fetchgit or pkgs.fetchurl with a hash instead of builtins.fetch*"
	case strings.Contains(msg, "access to absolute path") && strings.Contains(msg, "pure evaluation mode"):
		summary = msg + at + "; a fragment may only read files it carries"
	case nixPathRe.MatchString(msg):
		m := nixPathRe.FindStringSubmatch(msg)
		summary = m[1] + " is not available" + at + "; use the pkgs argument, which is the platform's pinned nixpkgs"
	case strings.Contains(msg, "allow-import-from-derivation"):
		summary = "import-from-derivation is not allowed" + at + "; a fragment cannot import a file that a build produces"
	case hmOptionRe.MatchString(msg) && flakeOutputs[hmOptionRe.FindStringSubmatch(msg)[1]]:
		// A flake.nix applied as a fragment: its top-level attributes
		// reach home-manager as options. The did-you-mean that follows is
		// about home-manager and only misleads here (DECISIONS I-483).
		return &Error{Code: "eval_failed", Message: compose(flakeHint, stderr), FragmentLine: line}
	case hmOptionRe.MatchString(msg):
		m := hmOptionRe.FindStringSubmatch(msg)
		summary = "option '" + m[1] + "' does not exist in a fragment" + at + optionHint
	case strings.Contains(msg, "does not exist") && strings.Contains(msg, "The option"):
		summary = msg + at + optionHint
	case strings.Contains(stderr, "Failed assertions:"):
		// The fragment contract's own refusals (nix/guest/fragment.nix) and
		// the base's assertions arrive as a list; the first is the summary.
		if m := assertionRe.FindStringSubmatch(stderr[strings.Index(stderr, "Failed assertions:"):]); m != nil {
			summary = strings.TrimSpace(m[1])
		} else {
			summary = msg
		}
	default:
		summary = msg + at
	}
	if m := didYouMean.FindStringSubmatch(stderr); m != nil {
		summary += " (" + strings.ToLower(m[1][:1]) + m[1][1:] + ")"
	}
	return &Error{Code: "eval_failed", Message: compose(summary, stderr), FragmentLine: line}
}

// EvalTimeout is the eval_failed Error for a `timeout` exit.
func EvalTimeout(evalS uint32) *Error {
	s := "evaluation exceeded " + strconv.FormatUint(uint64(evalS), 10) + " s"
	return &Error{Code: "eval_failed", Message: s}
}

// LastDerivation names the derivation a build log was last building.
func LastDerivation(log string) string {
	ms := buildingRe.FindAllStringSubmatch(log, -1)
	if len(ms) == 0 {
		return ""
	}
	return drvName(ms[len(ms)-1][1])
}

// FailedDerivation is the store path of the derivation Nix reported as
// failed, or "".
func FailedDerivation(stderr string) string {
	if m := drvFailRe.FindStringSubmatch(stderr); m != nil {
		return m[1]
	}
	return ""
}

func drvName(p string) string {
	base := p[strings.LastIndexByte(p, '/')+1:]
	base = strings.TrimSuffix(base, ".drv")
	if i := strings.IndexByte(base, '-'); i >= 0 && i == 32 {
		return base[i+1:]
	}
	return base
}

// MapBuildError turns `nix build` output into a build_failed Error.
func MapBuildError(stderr string) *Error {
	summary := "build failed"
	if d := FailedDerivation(stderr); d != "" {
		summary = "build of " + drvName(d) + " failed"
	} else if msg, _ := lastError(stderr); msg != "" {
		summary = msg
	}
	if strings.Contains(stderr, "Killed") || strings.Contains(stderr, "killed by signal 9") {
		summary += "; build exceeded 16 GB RAM"
	}
	return &Error{Code: "build_failed", Message: compose(summary, stderr)}
}

// duration prints a cap the way the workstream doc's messages do: whole
// minutes when the cap is one, seconds otherwise.
func duration(secs uint32) string {
	if secs >= 60 && secs%60 == 0 {
		m := secs / 60
		if m == 1 {
			return "1 minute"
		}
		return strconv.FormatUint(uint64(m), 10) + " minutes"
	}
	return strconv.FormatUint(uint64(secs), 10) + " s"
}

// BuildTimeout is the build_timeout Error for a `timeout` exit:
// "build timed out after 30 minutes while building sleep-forever-1.0".
func BuildTimeout(buildS uint32, log string) *Error {
	s := "build timed out after " + duration(buildS)
	if d := LastDerivation(log); d != "" {
		s += " while building " + d
	}
	return &Error{Code: "build_timeout", Message: compose(s, log)}
}

// ClosureTooLarge is the closure_too_large Error:
// "closure is 31.2 GB, limit is 20 GB; largest paths:" then ten lines.
func ClosureTooLarge(size, limit uint64, largest string) *Error {
	return &Error{Code: "closure_too_large", Message: "closure is " + humanBytes(size) + ", limit is " + humanBytes(limit) + "; largest paths:\n" + strings.TrimRight(largest, "\n")}
}
