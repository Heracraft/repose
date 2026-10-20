package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/heracraft/repose/internal/undo"
)

// The error codes of SPINE.md "Contract 3", in the vocabulary of
// docs/interfaces/README.md. These six are the whole set the agent prints.
const (
	codeInvalidArgument = "invalid_argument"
	codeNotFound        = "not_found"
	codeBusy            = "busy"
	codeNoSpace         = "no_space"
	codeExists          = "exists"
	codeInternal        = "internal"
)

// fsBtrfs is the store's filesystem. internal/undo is btrfs (I-814), so a
// store it provisioned is btrfs or Provision failed.
const fsBtrfs = "btrfs"

// agentError is a failure with one of the six codes. It is the only error
// shape the agent prints.
type agentError struct {
	Code    string
	Message string
}

func (e *agentError) Error() string { return e.Code + ": " + e.Message }

func errf(code, format string, a ...any) *agentError {
	return &agentError{Code: code, Message: fmt.Sprintf(format, a...)}
}

// fromBackend maps internal/undo's sentinels onto the codes. Anything else
// the backend returns is internal.
func fromBackend(err error, what string) *agentError {
	code := codeInternal
	switch {
	case errors.Is(err, undo.ErrInvalidName):
		code = codeInvalidArgument
	case errors.Is(err, undo.ErrNotFound):
		code = codeNotFound
	case errors.Is(err, undo.ErrBusy):
		code = codeBusy
	case errors.Is(err, undo.ErrNoSpace):
		code = codeNoSpace
	case errors.Is(err, undo.ErrExists):
		code = codeExists
	}
	return errf(code, "%s: %v", what, err)
}

type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error wireError `json:"error"`
}

// wirePoint is a Point as the CLI reads it. internal/undo tags Point for
// JSON already; the agent keeps its own shape so taken_at is RFC 3339 UTC
// to the second whatever clock the store read (docs/interfaces/README.md).
type wirePoint struct {
	Name    string `json:"name"`
	Project string `json:"project"`
	Reason  string `json:"reason"`
	TakenAt string `json:"taken_at"`
	Bytes   uint64 `json:"bytes"`
}

func wirePointOf(p undo.Point) wirePoint {
	return wirePoint{
		Name:    p.Name,
		Project: p.Project,
		Reason:  p.Reason,
		TakenAt: p.TakenAt.UTC().Format(time.RFC3339),
		Bytes:   p.Bytes,
	}
}

// wirePointsOf is never null: a project with no points answers [].
func wirePointsOf(ps []undo.Point) []wirePoint {
	out := make([]wirePoint, 0, len(ps))
	for _, p := range ps {
		out = append(out, wirePointOf(p))
	}
	return out
}

type wireSpace struct {
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
	Free  uint64 `json:"free"`
}

func wireSpaceOf(s undo.Space) wireSpace {
	return wireSpace{Total: s.Total, Used: s.Used, Free: s.Free}
}

type wireProvision struct {
	Store   string `json:"store"`
	FS      string `json:"fs"`
	Created bool   `json:"created"`
}

type wireInit struct {
	Path    string `json:"path"`
	Created bool   `json:"created"`
}

type wireTake struct {
	Point wirePoint `json:"point"`
}

type wireList struct {
	Points []wirePoint `json:"points"`
}

type wireRewind struct {
	Rewound bool `json:"rewound"`
	// SafetyPoint is the point Rewind took of the state it replaced, so the
	// rewind is itself undoable. Empty when the backend took none.
	SafetyPoint string `json:"safety_point"`
}

type wireFork struct {
	Path string `json:"path"`
}

type wireDelete struct {
	Deleted bool `json:"deleted"`
}

type wireGC struct {
	Deleted []wirePoint `json:"deleted"`
	Space   wireSpace   `json:"space"`
}

type wireQuiesce struct {
	// Stopped and Busy name processes, never a path and never an argv: a log
	// field may carry a process name and nothing a tenant typed (CLAUDE.md).
	Stopped []string `json:"stopped"`
	Busy    []string `json:"busy"`
}

// wireMachine is /etc/repose/machine.json, written by the installer
// (SPINE.md "Contract 5", step 6).
type wireMachine struct {
	Name  string `json:"name"`
	ID    string `json:"id"`
	Store string `json:"store"`
}

type wireProject struct {
	Slug   string `json:"slug"`
	Points int    `json:"points"`
}

type wireStatus struct {
	Machine  wireMachine   `json:"machine"`
	Projects []wireProject `json:"projects"`
	Space    wireSpace     `json:"space"`
}

type wireVersion struct {
	Version string `json:"version"`
}

// emit prints the command's one JSON object.
func emit(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return errf(codeInternal, "encode the answer: %v", err)
	}
	if _, err := fmt.Fprintln(w, string(b)); err != nil {
		return errf(codeInternal, "write the answer: %v", err)
	}
	return nil
}
