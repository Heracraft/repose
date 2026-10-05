// Package config validates fragments before a revision is stored
// (05-control-plane-api.md §5.7): size, and a syntax check with
// `nix-instantiate --parse` in the api container, which has Nix for this
// purpose only. The fragment is written to a temporary file so the error
// location parses as fragment.nix:L:C.
package config

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MaxFragmentBytes is the PUT /config cap.
const MaxFragmentBytes = 256 << 10

// ParseError is a syntax error with its fragment line.
type ParseError struct {
	Message string
	Line    int
}

func (e *ParseError) Error() string { return e.Message }

// ErrParserUnavailable is returned when nix-instantiate is not on PATH.
var ErrParserUnavailable = errors.New("nix-instantiate not available")

// ErrTooLarge is returned for a fragment over MaxFragmentBytes.
var ErrTooLarge = errors.New("fragment exceeds 256 KB")

// Parser checks fragments.
type Parser struct {
	bin string
}

// NewParser finds nix-instantiate ($NIX_INSTANTIATE or PATH); available
// reports whether it did.
func NewParser() (p *Parser, available bool) {
	bin := os.Getenv("NIX_INSTANTIATE")
	if bin == "" {
		var err error
		bin, err = exec.LookPath("nix-instantiate")
		if err != nil {
			return &Parser{}, false
		}
	}
	return &Parser{bin: bin}, true
}

var locRe = regexp.MustCompile(`fragment\.nix:(\d+):(\d+)`)

// Check validates size and syntax. A syntax error comes back as
// *ParseError with the line; ErrParserUnavailable when Nix is missing.
func (p *Parser) Check(ctx context.Context, fragment string) error {
	return p.CheckNamed(ctx, fragment, "fragment.nix")
}

// PersonalName is the file name a personal layer's errors carry
// (DECISIONS I-490): machine.nix, the name users know it by.
const PersonalName = "machine.nix"

// CheckNamed is Check with the file written, and every location
// reported, as name ("fragment.nix" or PersonalName).
func (p *Parser) CheckNamed(ctx context.Context, fragment, name string) error {
	if len(fragment) > MaxFragmentBytes {
		return ErrTooLarge
	}
	if p.bin == "" {
		return ErrParserUnavailable
	}
	dir, err := os.MkdirTemp("", "repose-fragment-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }() // temp dir; nothing to report
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(fragment), 0o600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.bin, "--parse", path)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "NIX_STATE_DIR=" + dir, "NIX_STORE_DIR=" + filepath.Join(dir, "store")}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = nil
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if ctx.Err() != nil {
			return &ParseError{Message: "parse check timed out"}
		}
		if name != "fragment.nix" {
			// One code path for the summary: the temporary file's name
			// becomes fragment.nix, is summarised, and comes back as name.
			msg = strings.ReplaceAll(msg, name, "fragment.nix")
			return &ParseError{Message: strings.ReplaceAll(summarise(msg), "fragment.nix", name), Line: lineOf(msg)}
		}
		return &ParseError{Message: summarise(msg), Line: lineOf(msg)}
	}
	return nil
}

func lineOf(msg string) int {
	m := locRe.FindStringSubmatch(msg)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// summarise keeps the first "error:" line of Nix's output and the
// fragment.nix:L:C location that follows it, with the temporary path
// replaced by fragment.nix.
func summarise(msg string) string {
	lines := strings.Split(msg, "\n")
	first := ""
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "error:") {
			first = strings.TrimSpace(strings.TrimPrefix(l, "error:"))
			break
		}
	}
	if first == "" && len(lines) > 0 {
		first = strings.TrimSpace(lines[0])
	}
	if first == "" {
		first = "syntax error"
	}
	first = cleanPath(first)
	loc := locRe.FindString(msg)
	// The contract's first line for a syntax error is hostd's,
	// `syntax error at fragment.nix:L:C, unexpected ';'`
	// (nix-build-contract.md "What the user reads"); Nix itself prints
	// `syntax error, unexpected ';'` and the location on the next line. A
	// user sees the same words whether the parse check or the host refused
	// the fragment (I-126).
	if rest, ok := strings.CutPrefix(first, "syntax error, "); ok && loc != "" {
		return "syntax error at " + loc + ", " + rest
	}
	if loc != "" && !strings.Contains(first, loc) {
		first += " at " + loc
	}
	return first
}

var pathRe = regexp.MustCompile(`/\S*/fragment\.nix`)

func cleanPath(s string) string { return pathRe.ReplaceAllString(s, "fragment.nix") }

// CheckFuncFor adapts a Parser for callers that only need a function.
func (p *Parser) CheckFuncFor() func(context.Context, string) error { return p.Check }

// Fmt renders a parse error for the api's error envelope.
// The line travels in the error's `detail.fragment_line`; the message is
// the contract's summary line alone (I-126).
func Fmt(e *ParseError) string { return e.Message }
