// Command repose-agent is the on-box command the repose CLI invokes over SSH
// on a machine the user owns (docs/workstreams/SPINE.md, "Contract 3"). It
// drives the undo store of internal/undo and quiesces a project so a rewind
// cannot lose an agent's last writes.
//
// Every command prints one JSON object on stdout and nothing else there;
// anything for a human goes to stderr. A failure prints
// {"error":{"code":...,"message":...}} on stdout and exits 1, or 2 when the
// arguments were wrong.
//
// Workstream: docs/workstreams/SPINE.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev" // set by -ldflags at release

const usage = `usage: repose-agent <command> [flags]

Commands:
  provision --size-gb N
  project init --project SLUG
  point take [--project SLUG --name N] --reason manual|run|stop|scheduled|rewind
  point list --project SLUG
  point rewind --project SLUG --name N
  point fork --project SLUG --name N --as SLUG
  point delete --project SLUG --name N
  gc --high-water PERCENT
  space
  quiesce --project SLUG
  holders --root PATH
  status
  version
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, defaultEnv(), os.Args[1:]))
}

// run dispatches one command and returns the process's exit code. The error
// envelope is the command's stdout, so a caller over SSH reads the code from
// the JSON and never from the exit status alone.
func run(ctx context.Context, env *Env, args []string) int {
	err := dispatch(ctx, env, args)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	ae := &agentError{Code: codeInternal, Message: err.Error()}
	var found *agentError
	if errors.As(err, &found) {
		ae = found
	}
	out, merr := json.Marshal(errorEnvelope{Error: wireError{Code: ae.Code, Message: ae.Message}})
	if merr != nil {
		out = []byte(`{"error":{"code":"internal","message":"encode the error"}}`)
	}
	_, _ = fmt.Fprintln(env.Stdout, string(out))
	if ae.Code == codeInvalidArgument {
		return 2
	}
	return 1
}

func dispatch(ctx context.Context, env *Env, args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprint(env.Stderr, usage)
		return errf(codeInvalidArgument, "no command given")
	}
	switch args[0] {
	case "provision":
		return cmdProvision(ctx, env, args[1:])
	case "project":
		return cmdProject(ctx, env, args[1:])
	case "point":
		return cmdPoint(ctx, env, args[1:])
	case "gc":
		return cmdGC(ctx, env, args[1:])
	case "space":
		return cmdSpace(ctx, env, args[1:])
	case "quiesce":
		return cmdQuiesce(ctx, env, args[1:])
	case "holders":
		return cmdHolders(env, args[1:])
	case "status":
		return cmdStatus(ctx, env, args[1:])
	case "version":
		return cmdVersion(env, args[1:])
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(env.Stderr, usage)
		return nil
	}
	_, _ = fmt.Fprint(env.Stderr, usage)
	return errf(codeInvalidArgument, "unknown command %q", args[0])
}

// flagSet is one command's flags. Its errors and its defaults go to stderr,
// where help belongs; stdout stays the JSON object.
func flagSet(env *Env, name string) *flag.FlagSet {
	fs := flag.NewFlagSet("repose-agent "+name, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	return fs
}

// parseErr turns a flag failure into invalid_argument. flag has already
// written what was wrong, and the defaults, to stderr.
func parseErr(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return err
	}
	return errf(codeInvalidArgument, "%v", err)
}

// noArgs refuses what a command with no flags was given anyway.
func noArgs(env *Env, name string, args []string) error {
	fs := flagSet(env, name)
	if err := fs.Parse(args); err != nil {
		return parseErr(err)
	}
	if fs.NArg() > 0 {
		return errf(codeInvalidArgument, "%s takes no arguments", name)
	}
	return nil
}

func cmdVersion(env *Env, args []string) error {
	if err := noArgs(env, "version", args); err != nil {
		return err
	}
	return emit(env.Stdout, wireVersion{Version: version})
}
