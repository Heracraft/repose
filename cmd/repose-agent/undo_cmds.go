package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxSizeGB is a petabyte. Past it --size-gb is a typo, and the bytes it
// becomes would overflow.
const maxSizeGB = 1 << 20

func cmdProvision(ctx context.Context, env *Env, args []string) error {
	fs := flagSet(env, "provision")
	sizeGB := fs.Uint64("size-gb", 0, "how much undo space the store gets")
	if err := fs.Parse(args); err != nil {
		return parseErr(err)
	}
	if *sizeGB > maxSizeGB {
		return errf(codeInvalidArgument, "--size-gb is at most %d", maxSizeGB)
	}
	b, err := env.backend()
	if err != nil {
		return err
	}
	// created says whether this call made the store. Provision is idempotent,
	// so a second run answers created:false and changes nothing, and a store
	// that is already there needs no size.
	ready, err := b.Ready(ctx)
	if err != nil {
		return fromBackend(err, "read the undo store")
	}
	if !ready && *sizeGB == 0 {
		return errf(codeInvalidArgument, "no size given; pass --size-gb")
	}
	if err := b.Provision(ctx, *sizeGB<<30); err != nil {
		return fromBackend(err, "provision the undo store")
	}
	created := !ready
	return emit(env.Stdout, wireProvision{Store: env.Store, FS: fsBtrfs, Created: created})
}

func cmdProject(ctx context.Context, env *Env, args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprint(env.Stderr, usage)
		return errf(codeInvalidArgument, "no project command given")
	}
	if args[0] != "init" {
		_, _ = fmt.Fprint(env.Stderr, usage)
		return errf(codeInvalidArgument, "unknown project command %q", args[0])
	}
	return cmdProjectInit(ctx, env, args[1:])
}

// cmdProjectInit makes the project's live area and the link the login user
// reaches it by. Nothing else: no git, no agent (SPINE.md "Contract 3").
func cmdProjectInit(ctx context.Context, env *Env, args []string) error {
	fs := flagSet(env, "project init")
	project := fs.String("project", "", "the project's slug")
	if err := fs.Parse(args); err != nil {
		return parseErr(err)
	}
	if err := checkSlug("project", *project); err != nil {
		return err
	}
	b, err := env.backend()
	if err != nil {
		return err
	}
	_, statErr := os.Lstat(liveDir(env.Store, *project))
	created := statErr != nil
	path, err := b.Init(ctx, *project)
	if err != nil {
		return fromBackend(err, "create the project's live area")
	}
	if err := linkHome(env, *project, path); err != nil {
		return err
	}
	if err := ensureSession(ctx, env, *project, path); err != nil {
		return err
	}
	return emit(env.Stdout, wireInit{Path: path, Created: created})
}

// ensureSession starts the project's detached tmux session, the one an
// agent window is opened in and an attach attaches to. On a guest a
// systemd user unit does this (nix/guest/base/tmux.nix); a machine the
// user owns has no such unit, so the agent starts it on the first
// `project init` and leaves an existing one alone.
func ensureSession(ctx context.Context, env *Env, slug, dir string) error {
	if env.Tmux == nil {
		return nil
	}
	if env.Tmux(ctx, "has-session", "-t", slug) {
		return nil
	}
	if !env.Tmux(ctx, "new-session", "-d", "-s", slug, "-c", dir) {
		return errf(codeInternal, "could not start the tmux session for %s", slug)
	}
	// Nothing is done to the session's PATH here. tmux gives a new
	// window the environment its *server* started with, and neither
	// `set-environment -t`, `set-environment -g` nor `new-window -e`
	// changes that for a window opened later (measured on tmux 3.3a,
	// 2026-10-19). The installer puts the agents on the default PATH
	// with a symlink in /usr/local/bin instead.
	return nil
}

// linkHome points ~/<slug> at the live area. A second run changes nothing. A
// ~/<slug> that is something else is refused, not replaced: it is the box
// owner's own file.
func linkHome(env *Env, slug, path string) error {
	if env.Home == "" {
		return errf(codeInternal, "the login user has no home directory")
	}
	link := filepath.Join(env.Home, slug)
	switch target, err := os.Readlink(link); {
	case err == nil && target == path:
		return nil
	case err == nil:
		return errf(codeExists, "~/%s is a link to somewhere else", slug)
	}
	if _, err := os.Lstat(link); err == nil {
		return errf(codeExists, "~/%s exists and is not a link", slug)
	}
	if err := os.Symlink(path, link); err != nil {
		return errf(codeInternal, "link ~/%s to the live area: %v", slug, err)
	}
	return nil
}

func cmdPoint(ctx context.Context, env *Env, args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprint(env.Stderr, usage)
		return errf(codeInvalidArgument, "no point command given")
	}
	switch args[0] {
	case "take":
		return cmdPointTake(ctx, env, args[1:])
	case "list":
		return cmdPointList(ctx, env, args[1:])
	case "rewind":
		return cmdPointRewind(ctx, env, args[1:])
	case "fork":
		return cmdPointFork(ctx, env, args[1:])
	case "delete":
		return cmdPointDelete(ctx, env, args[1:])
	}
	_, _ = fmt.Fprint(env.Stderr, usage)
	return errf(codeInvalidArgument, "unknown point command %q", args[0])
}

func cmdPointTake(ctx context.Context, env *Env, args []string) error {
	fs := flagSet(env, "point take")
	project := fs.String("project", "", "the project's slug")
	name := fs.String("name", "", "the point's name, derived from the caller's id")
	reason := fs.String("reason", "", "manual, run, stop or scheduled")
	if err := fs.Parse(args); err != nil {
		return parseErr(err)
	}
	if err := checkSlug("project", *project); err != nil {
		return err
	}
	if err := checkName(*name); err != nil {
		return err
	}
	if err := checkReason(*reason); err != nil {
		return err
	}
	b, err := env.backend()
	if err != nil {
		return err
	}
	p, err := b.Take(ctx, *project, *name, *reason)
	if err != nil {
		return fromBackend(err, "take a point")
	}
	return emit(env.Stdout, wireTake{Point: wirePointOf(p)})
}

func cmdPointList(ctx context.Context, env *Env, args []string) error {
	fs := flagSet(env, "point list")
	project := fs.String("project", "", "the project's slug")
	if err := fs.Parse(args); err != nil {
		return parseErr(err)
	}
	if err := checkSlug("project", *project); err != nil {
		return err
	}
	b, err := env.backend()
	if err != nil {
		return err
	}
	points, err := b.List(ctx, *project)
	if err != nil {
		return fromBackend(err, "list the project's points")
	}
	return emit(env.Stdout, wireList{Points: wirePointsOf(points)})
}

func cmdPointRewind(ctx context.Context, env *Env, args []string) error {
	fs := flagSet(env, "point rewind")
	project := fs.String("project", "", "the project's slug")
	name := fs.String("name", "", "the point to rewind to")
	if err := fs.Parse(args); err != nil {
		return parseErr(err)
	}
	if err := checkSlug("project", *project); err != nil {
		return err
	}
	if err := checkName(*name); err != nil {
		return err
	}
	b, err := env.backend()
	if err != nil {
		return err
	}
	// quiesce's own check, before Rewind makes it again: a project still
	// being written to is refused rather than rewound under its writer
	// (docs/research/wrapper-undo.md).
	holders, err := holdersOf(b, *project)
	if err != nil {
		return err
	}
	if len(holders) > 0 {
		return errf(codeBusy, "the project is not quiesced: %s", strings.Join(holderNames(holders), ", "))
	}
	// Rewind takes a point of the state it replaces, so the rewind itself
	// is undoable, and answers with that point's name.
	safety, err := b.Rewind(ctx, *project, *name)
	if err != nil {
		return fromBackend(err, "rewind the project")
	}
	return emit(env.Stdout, wireRewind{Rewound: true, SafetyPoint: safety})
}

func cmdPointFork(ctx context.Context, env *Env, args []string) error {
	fs := flagSet(env, "point fork")
	project := fs.String("project", "", "the project's slug")
	name := fs.String("name", "", "the point to fork")
	as := fs.String("as", "", "the new project's slug")
	if err := fs.Parse(args); err != nil {
		return parseErr(err)
	}
	if err := checkSlug("project", *project); err != nil {
		return err
	}
	if err := checkName(*name); err != nil {
		return err
	}
	if err := checkSlug("as", *as); err != nil {
		return err
	}
	if *as == *project {
		return errf(codeInvalidArgument, "--as names the project being forked")
	}
	b, err := env.backend()
	if err != nil {
		return err
	}
	if err := b.Fork(ctx, *project, *name, *as); err != nil {
		return fromBackend(err, "fork the point")
	}
	return emit(env.Stdout, wireFork{Path: liveDir(env.Store, *as)})
}

func cmdPointDelete(ctx context.Context, env *Env, args []string) error {
	fs := flagSet(env, "point delete")
	project := fs.String("project", "", "the project's slug")
	name := fs.String("name", "", "the point to delete")
	if err := fs.Parse(args); err != nil {
		return parseErr(err)
	}
	if err := checkSlug("project", *project); err != nil {
		return err
	}
	if err := checkName(*name); err != nil {
		return err
	}
	b, err := env.backend()
	if err != nil {
		return err
	}
	// Absence is success (Contract 1), so deleted is always true here.
	if err := b.Delete(ctx, *project, *name); err != nil {
		return fromBackend(err, "delete the point")
	}
	return emit(env.Stdout, wireDelete{Deleted: true})
}

func cmdGC(ctx context.Context, env *Env, args []string) error {
	fs := flagSet(env, "gc")
	highWater := fs.Int("high-water", 85, "the percent of the store to stay under")
	if err := fs.Parse(args); err != nil {
		return parseErr(err)
	}
	if *highWater < 1 || *highWater > 100 {
		return errf(codeInvalidArgument, "--high-water is a percent between 1 and 100")
	}
	b, err := env.backend()
	if err != nil {
		return err
	}
	deleted, err := b.GC(ctx, *highWater)
	if err != nil {
		return fromBackend(err, "collect the oldest points")
	}
	space, err := b.Space(ctx)
	if err != nil {
		return fromBackend(err, "read the store's space")
	}
	return emit(env.Stdout, wireGC{Deleted: wirePointsOf(deleted), Space: wireSpaceOf(space)})
}

func cmdSpace(ctx context.Context, env *Env, args []string) error {
	if err := noArgs(env, "space", args); err != nil {
		return err
	}
	b, err := env.backend()
	if err != nil {
		return err
	}
	space, err := b.Space(ctx)
	if err != nil {
		return fromBackend(err, "read the store's space")
	}
	return emit(env.Stdout, wireSpaceOf(space))
}
