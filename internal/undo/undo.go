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
//
// There is no size field. btrfs only tells a caller what a snapshot
// holds through qgroups, which means quotas on, and quotas on a store
// of thousands of snapshots cost every write (I-814); so the figure
// was 0 on every real box. A column of zeroes is worse than no column.
type Point struct {
	Name    string    `json:"name"`     // stable, unique, derived from the caller's id — never a wall clock
	Project string    `json:"project"`  // project slug
	Reason  string    `json:"reason"`   // manual | run | stop | scheduled | rewind
	TakenAt time.Time `json:"taken_at"` // RFC 3339 UTC
}

// Space is the store's bytes. On an image-backed store the total is
// bounded by the filesystem the image lives on, not by the image's own
// size: a sparse file promises space the box may not have (I-814).
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
	// mark, within the floors of GCFloors. Returns what went and what it
	// would not touch.
	GC(ctx context.Context, highWaterPercent int) (GCResult, error)
}

// MinPointAge is how young a point is safe from collection whatever the
// store's pressure. An agent that fills the store in ten minutes must
// not take the morning's points with it: those are the ones a user
// reaches for (SPINE.md, "undo that is safe to rely on").
const MinPointAge = 6 * time.Hour

// MinPointsPerProject is the points GC leaves every project, newest
// first. One point is one chance; three is a history to choose from.
const MinPointsPerProject = 3

// GCFloors are what GC will not collect however full the store is. The
// zero value is no floor at all, which only a test asks for; a nil
// *GCFloors on the backend means the box's own, MinPointAge and
// MinPointsPerProject.
type GCFloors struct {
	MinAge  time.Duration
	MinKeep int
}

// GCResult is one collection: what went, and what it would not touch.
// The counts are why a store can stay over its mark after a GC, which
// is otherwise a silent no-op.
type GCResult struct {
	// Deleted is the points that went, oldest first.
	Deleted []Point `json:"deleted"`
	// Young is the points left because they are inside MinPointAge.
	Young int `json:"young"`
	// Kept is the points left because their project is at its floor of
	// MinPointsPerProject.
	Kept int `json:"kept"`
	// Stalled is true when a delete freed no bytes and GC stopped. The
	// space is held by the live areas, not by the points, so deleting
	// more points would empty the history and free nothing.
	Stalled bool `json:"stalled"`
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
// (internal/hostd/guest/snapshot.go), and rewind, which is the point a
// rewind takes of the state it replaces.
//
// ReasonRewind exists so that point can be told apart. Without it a
// second `repose rewind` picked the newest point, which is the safety
// point of the first, and restored the wreck the user had just escaped.
const (
	ReasonManual    = "manual"
	ReasonRun       = "run"
	ReasonStop      = "stop"
	ReasonScheduled = "scheduled"
	ReasonRewind    = "rewind"
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
	case ReasonManual, ReasonRun, ReasonStop, ReasonScheduled, ReasonRewind:
		return nil
	}
	return fmt.Errorf("%w: reason %q must be manual, run, stop, scheduled or rewind", ErrInvalidName, reason)
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
