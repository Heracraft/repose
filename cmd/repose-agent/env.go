package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/undo"
)

// DefaultStore is the undo store's mountpoint on the box, and
// DefaultMachineFile the record the installer leaves (SPINE.md "Contract 5").
const (
	DefaultStore       = "/var/lib/repose/undo"
	DefaultMachineFile = "/etc/repose/machine.json"
)

// KillGrace is how long a process of the project has between SIGTERM and
// SIGKILL, and pollInterval how often quiesce rescans the live area.
const (
	KillGrace    = 5 * time.Second
	pollInterval = 100 * time.Millisecond
)

// undoStore is what the agent needs of internal/undo: Contract 1's Backend,
// plus the two calls the package exposes beside it — Ready, which says
// whether provision has anything left to do, and Holders, the quiescence
// check a rewind refuses on.
type undoStore interface {
	undo.Backend
	Ready(ctx context.Context) (bool, error)
	Holders(ctx context.Context, project string) ([]undo.Holder, error)
}

// Env is everything the agent touches outside its own process, so every
// command runs in a test without a box under it.
type Env struct {
	Stdout io.Writer
	Stderr io.Writer

	Store       string // the undo store's mountpoint
	MachineFile string // the installer's machine record
	Home        string // the login user's home, where project init puts the link

	// Open opens the undo store at a mountpoint.
	Open func(store string) (undoStore, error)
	// Tmux runs tmux and reports whether it exited 0.
	Tmux func(ctx context.Context, args ...string) bool
	// Signal sends sig to a pid.
	Signal func(pid int, sig os.Signal) error
	// Grace is the SIGTERM-to-SIGKILL wait.
	Grace time.Duration
}

func defaultEnv() *Env {
	// An unreadable home is empty, and project init is the only command
	// that needs one.
	home, _ := os.UserHomeDir()
	return &Env{
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Store:       DefaultStore,
		MachineFile: DefaultMachineFile,
		Home:        home,
		Open:        openBtrfs,
		Tmux:        runTmux,
		Signal:      signalPID,
		Grace:       KillGrace,
	}
}

// The store the agent opens is internal/undo's btrfs backend.
var _ undoStore = (*undo.Btrfs)(nil)

// openBtrfs is internal/undo's btrfs store, running real processes. It is
// the one place the agent names an implementation.
func openBtrfs(store string) (undoStore, error) {
	b := undo.New(store, undo.Exec{})
	b.Scan = sudoScan
	return b, nil
}

// sudoScan is the holder scan with root's view of /proc, run by this
// same binary under sudo. The agent runs as the box's login user, and
// /proc/<pid>/cwd, /proc/<pid>/fd and /proc/<pid>/maps of another user's
// process deny that user outright — so a root-owned writer in the live
// area was not refused, it was invisible, and a rewind over it lost its
// writes. The installer requires passwordless sudo for exactly the
// things it cannot do without it (machine-install.sh step 1).
//
// A sudo that will not run is not a failure: the caller keeps the
// in-process scan, which still refuses every holder it did find.
func sudoScan(ctx context.Context, root string) ([]undo.Holder, error) {
	if os.Geteuid() == 0 {
		return nil, nil // the in-process scan already sees everything
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	out, err := exec.CommandContext(ctx, "sudo", "-n", exe, "holders", "--root", root).Output()
	if err != nil {
		return nil, err
	}
	var answer struct {
		Holders []undo.Holder `json:"holders"`
		Error   *struct{}     `json:"error"`
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		return nil, err
	}
	if answer.Error != nil {
		return nil, errors.New("the privileged scan refused")
	}
	return answer.Holders, nil
}

// runTmux runs tmux as the login user, which is the user the agent runs as:
// the session belongs to that user's server, not to root's.
func runTmux(ctx context.Context, args ...string) bool {
	return exec.CommandContext(ctx, "tmux", args...).Run() == nil
}

func signalPID(pid int, sig os.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(sig)
}

// backend opens the store, which every command but version needs.
func (e *Env) backend() (undoStore, error) {
	b, err := e.Open(e.Store)
	if err != nil {
		return nil, errf(codeInternal, "open the undo store: %v", err)
	}
	return b, nil
}

// liveDir is the project's live area: the path `project init` answers with
// (SPINE.md "Contract 3"), and the directory whose absence means the store
// holds no such project. internal/undo builds the same path from the slug.
func liveDir(store, slug string) string { return filepath.Join(store, "projects", slug) }

// slugRe and nameRe are the shapes internal/undo accepts (projectRe and
// pointRe there). The agent checks them itself because it builds a path and
// a tmux target out of a slug before the store ever sees it; a leading dot
// is outside both, which is what keeps `..` out.
var (
	slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// reasons are the Reason values of SPINE.md "Contract 1", rewind
// included: the point a rewind takes of the state it replaces carries
// its own reason so nothing picks it as a target (internal/undo).
var reasons = []string{
	undo.ReasonManual, undo.ReasonRun, undo.ReasonStop,
	undo.ReasonScheduled, undo.ReasonRewind,
}

func checkSlug(flagName, slug string) error {
	if slug == "" {
		return errf(codeInvalidArgument, "no project given; pass --%s", flagName)
	}
	if !slugRe.MatchString(slug) {
		return errf(codeInvalidArgument, "--%s is not a project slug", flagName)
	}
	return nil
}

func checkName(name string) error {
	if name == "" {
		return errf(codeInvalidArgument, "no point given; pass --name")
	}
	if !nameRe.MatchString(name) {
		return errf(codeInvalidArgument, "--name is not a point name")
	}
	return nil
}

func checkReason(reason string) error {
	if !slices.Contains(reasons, reason) {
		return errf(codeInvalidArgument, "--reason is one of %s", strings.Join(reasons, ", "))
	}
	return nil
}
