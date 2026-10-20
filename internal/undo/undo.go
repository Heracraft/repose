// Package undo keeps a project's files rewindable on a machine the user
// owns (DECISIONS I-814). A point is a read-only btrfs subvolume
// snapshot: 0.13 s and 32 KiB to take, and afterwards it costs only the
// blocks that change (docs/research/wrapper-undo.md). A rewind swaps the
// live subvolume for a writable snapshot of a point, so untracked files
// and the dev database come back with the code.
//
// Every operation is idempotent: a repeat of one that already happened
// succeeds without changing anything, because the CLI reaches the box
// over SSH and may resend a command it did not see the answer to.
package undo

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Point is one rewind point: a fixed moment of a project's files.
type Point struct {
	Name    string    `json:"name"`     // stable, unique, derived from the caller's id — never a wall clock
	Project string    `json:"project"`  // project slug
	Reason  string    `json:"reason"`   // manual | run | stop | scheduled
	TakenAt time.Time `json:"taken_at"` // RFC 3339 UTC
	Bytes   uint64    `json:"bytes"`    // exclusive bytes this point holds, 0 when unknown
}

// Space is the store's bytes.
type Space struct {
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
	Free  uint64 `json:"free"`
}

// Backend is the undo mechanism for one machine.
type Backend interface {
	// Provision prepares the store. Safe to call on an already-prepared one.
	Provision(ctx context.Context, sizeBytes uint64) error
	// Init creates the project's live area. Returns its path.
	Init(ctx context.Context, project string) (string, error)
	// Take fixes the project's current state as a point.
	Take(ctx context.Context, project, name, reason string) (Point, error)
	// List returns the project's points, newest first.
	List(ctx context.Context, project string) ([]Point, error)
	// Rewind replaces the live area with the point and returns the name of
	// the point it took of the pre-rewind state, so a rewind is undoable.
	// It refuses with ErrBusy unless the live area is quiesced.
	Rewind(ctx context.Context, project, name string) (string, error)
	// Fork copies a point into a new project's live area.
	Fork(ctx context.Context, srcProject, name, newProject string) error
	// Delete removes a point. Absence is success.
	Delete(ctx context.Context, project, name string) error
	// Space reports the store's bytes.
	Space(ctx context.Context) (Space, error)
	// GC deletes the oldest points until the store is under the high-water
	// mark, never deleting the newest point of any project. Returns what went.
	GC(ctx context.Context, highWaterPercent int) ([]Point, error)
}

var (
	ErrNotFound = errors.New("no such point")
	ErrBusy     = errors.New("the project is not quiesced")
	ErrNoSpace  = errors.New("not enough space for a point")
	ErrExists   = errors.New("the target project already exists")
	// ErrInvalidName is a project slug, point name or reason the store
	// will not hold. It is separate because the agent answers it with
	// invalid_argument, not not_found (SPINE.md contract 3).
	ErrInvalidName = errors.New("invalid name")
)

// The reasons a point carries. The hosted path's snapshot reasons are the
// same three plus run, which is the point taken on every `repose run`
// (internal/hostd/guest/snapshot.go).
const (
	ReasonManual    = "manual"
	ReasonRun       = "run"
	ReasonStop      = "stop"
	ReasonScheduled = "scheduled"
)

// projectRe is the slug the guest side already accepts
// (internal/guestd/project/project.go), so a project keeps one name
// whether it lives in a guest or on a box the user owns. A leading dot is
// outside it, which is what keeps `..` and the store's own transient
// directories out of a path built from a slug.
var projectRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// pointRe allows upper case too: a point name comes from an id, and ids
// are not all lower case.
var pointRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func checkProject(project string) error {
	if !projectRe.MatchString(project) {
		return fmt.Errorf("%w: project %q", ErrInvalidName, project)
	}
	return nil
}

func checkPoint(name string) error {
	if !pointRe.MatchString(name) {
		return fmt.Errorf("%w: point %q", ErrInvalidName, name)
	}
	return nil
}

func checkReason(reason string) error {
	switch reason {
	case ReasonManual, ReasonRun, ReasonStop, ReasonScheduled:
		return nil
	}
	return fmt.Errorf("%w: reason %q must be manual, run, stop or scheduled", ErrInvalidName, reason)
}

// NewPointName returns a point name for a point the backend takes
// itself, which is the one a rewind takes of the pre-rewind state. It is
// the first 12 bytes of a UUIDv7, so names sort by the millisecond the
// point was taken in and two points of the same millisecond still differ:
// a name that repeated would make Take hand back the older point and
// call it the safety point. The CLI shows a prefix of it, not all 24
// characters.
func NewPointName() string {
	id := uuid.Must(uuid.NewV7())
	return "r-" + strings.ReplaceAll(id.String(), "-", "")[:24]
}
