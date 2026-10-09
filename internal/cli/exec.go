package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

// `repose exec` and `repose ssh` (DECISIONS I-275). exec runs one command
// in the checkout on the machine, docker exec style: no terminal and no
// stdin unless asked (-i, -t), output streamed, and once the command runs
// the exit code is its own. It sees the environment an agent sees: the
// machine's /etc/profile.d/repose.sh (project variables and named
// secrets) and the checkout's dev environment, loaded by the same
// /etc/repose/devshell.sh the agent wrappers source (I-259). ssh opens an
// interactive login shell in the checkout, outside tmux.

// ExecOptions are `repose exec`'s arguments.
type ExecOptions struct {
	ProjectArg  string
	Command     []string
	Interactive bool // -i: pass this terminal's stdin
	TTY         bool // -t: allocate a terminal on the machine
	// MayNameProject: the words came with no -- and no --project, so a
	// first word that is one of the account's projects is PROJECT (I-411).
	MayNameProject bool
}

// execDevshell is where a base with I-275 keeps the agent wrappers'
// dev environment loader; an older base has no such file and exec falls
// back to what an interactive shell does, `direnv export` for an allowed
// .envrc.
const execDevshell = "/etc/repose/devshell.sh"

// execScript is the remote command line: cd into the checkout (home, with
// a note on stderr, when it is not there yet), load the environment, then
// exec the command, each argument quoted so the guest's shell passes it
// through unchanged, as docker exec does. A shell pipeline is `sh -c`'s job.
func execScript(slug, extra string, argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shQuote(a)
	}
	name := argv[0]
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return fmt.Sprintf(`%[1]scd "$repose_co"
[ -r /etc/profile.d/repose.sh ] && . /etc/profile.d/repose.sh
if [ -r %[2]s ]; then . %[2]s; REPOSE_DEVSHELL_QUIET=1 _repose_devshell %[3]s; unset -f _repose_devshell _repose_devshell_done
elif command -v direnv >/dev/null 2>&1; then eval "$(direnv export bash 2>/dev/null)"; fi
exec %[4]s`, checkoutVar(slug, extra), execDevshell, shQuote(name), strings.Join(quoted, " "))
}

// execSSHArgs are ssh's arguments for opts on target.
func execSSHArgs(t sshTarget, opts ExecOptions, remote string) []string {
	var args []string
	if opts.TTY {
		// -tt: a terminal even when this side's stdin is not one, as
		// docker exec -t gives one regardless.
		args = append(args, "-tt")
	}
	args = append(args, t.Args...)
	return append(args, remote)
}

// execSeparated reports whether args (pflag stopped at the first word,
// so the rest is unparsed) are the I-275 form PROJECT [-i] [-t] -- COMMAND:
// a "--" after exactly one word and exec's own flags. Any other "--"
// belongs to the command (`repose exec git log -- main.go`).
func execSeparated(args []string, opts *ExecOptions) (project string, command []string, ok bool) {
	for k := 1; k < len(args); k++ {
		switch args[k] {
		case "--":
			for _, f := range args[1:k] {
				switch f {
				case "-i", "--interactive":
					opts.Interactive = true
				case "-t", "--tty":
					opts.TTY = true
				case "-it", "-ti":
					opts.Interactive, opts.TTY = true, true
				}
			}
			return args[0], args[k+1:], true
		case "-i", "-t", "-it", "-ti", "--interactive", "--tty":
			continue
		}
		return "", nil, false
	}
	return "", nil, false
}

func newExecCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var opts ExecOptions
	cmd := &cobra.Command{
		Use:   "exec [PROJECT] [--] COMMAND [ARG...]",
		Short: "Run one command in the checkout on the machine",
		Long: "Runs COMMAND in the checkout on PROJECT's machine (this checkout's project, by default), with\n" +
			"the environment an agent there has: the project's secrets and its dev shell. Output streams\n" +
			"back; the exit code is the command's. The words are passed through as they are; for a\n" +
			"pipeline use sh -c '...'. A first word that names one of your projects is PROJECT; put --\n" +
			"before COMMAND to run a command that has a project's name. exec's own flags go before\n" +
			"COMMAND.\n\n" +
			"Without -i the command gets no input, and without -t no terminal; -it gives both, for\n" +
			"something interactive such as a REPL.",
		Example: "  repose exec npm test\n  repose exec todo-app git log --oneline -5\n  repose exec -it psql",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || cmd.ArgsLenAtDash() == len(args) {
				return cobraUsageError{fmt.Errorf("no command: repose exec [PROJECT] [--] COMMAND")}
			}
			if _, c, ok := execSeparated(args, &ExecOptions{}); ok && len(c) == 0 {
				return cobraUsageError{fmt.Errorf("no command after --: repose exec [PROJECT] -- COMMAND")}
			}
			return nil
		},
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			var words []string
			switch project, command, ok := execSeparated(args, &opts); {
			case cmd.ArgsLenAtDash() == 0:
				// `repose exec -- COMMAND`: no PROJECT, no guessing.
				opts.Command = args
			case ok:
				words, opts.Command = []string{project}, command
			default:
				opts.Command, opts.MayNameProject = args, g.project == ""
			}
			project, err := projectFrom(words, g)
			if err != nil {
				return err
			}
			opts.ProjectArg = project
			e, err := env()
			if err != nil {
				return err
			}
			return ExecCmd(cmd.Context(), e, opts, os.Stdin)
		},
	}
	// Everything from the first word on is the command's, flags included
	// (`repose exec grep -n x`), as with docker exec (I-411).
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().BoolVarP(&opts.Interactive, "interactive", "i", false, "pass this terminal's input to the command")
	cmd.Flags().BoolVarP(&opts.TTY, "tty", "t", false, "give the command a terminal (with -i, for interactive programs)")
	return cmd
}

// execProjectWord moves a first word that names one of the account's
// projects from Command to ProjectArg (I-411). A lone word that names a
// project is refused: it is almost always a missing command, and running
// it on the machine prints only "command not found".
func execProjectWord(ctx context.Context, e *Env, opts *ExecOptions) error {
	if !opts.MayNameProject || opts.ProjectArg != "" {
		return nil
	}
	projects, err := e.Client.ListProjects(ctx)
	if err != nil {
		return nil // a real api failure is reported by the connect that follows
	}
	for _, p := range projects {
		if p.Slug != opts.Command[0] {
			continue
		}
		if len(opts.Command) == 1 {
			return exitf(ExitUsage, "%q is one of your projects. Give the command after it: `repose exec %s COMMAND`, or `repose exec -- %s` to run a command of that name.", p.Slug, p.Slug, p.Slug)
		}
		opts.ProjectArg, opts.Command = p.Slug, opts.Command[1:]
		return nil
	}
	return nil
}

// ExecCmd implements `repose exec`. Before the command runs, a failure is
// one of repose's exit codes with a message; after, the exit code is the
// command's (ssh's own failure is 255).
func ExecCmd(ctx context.Context, e *Env, opts ExecOptions, stdin io.Reader) error {
	if err := execProjectWord(ctx, e, &opts); err != nil {
		return err
	}
	project, target, err := connectRunning(ctx, e, opts.ProjectArg)
	if err != nil {
		return err
	}
	c := exec.CommandContext(ctx, "ssh", execSSHArgs(target, opts, execScript(project.Slug, target.Checkout, opts.Command))...)
	c.Stdout, c.Stderr = e.Out, e.ErrOut
	if opts.Interactive || opts.TTY {
		c.Stdin = stdin
	}
	c.WaitDelay = sshWaitDelay
	err = c.Run()
	if err != nil && errors.Is(err, exec.ErrWaitDelay) && c.ProcessState != nil && c.ProcessState.Success() {
		err = nil
	}
	if err == nil {
		return nil
	}
	var xe *exec.ExitError
	if errors.As(err, &xe) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return silent(xe.ExitCode())
	}
	return exitf(ExitGeneric, "Could not run ssh: %v. repose exec needs the OpenSSH client on your PATH.", err)
}

// sshShellScript is `repose ssh`'s remote command: the user's login shell,
// interactive, in the checkout (home when there is none yet).
func sshShellScript(slug, extra string) string {
	return checkoutVar(slug, extra) + `cd "$repose_co"; exec "${SHELL:-bash}" -l`
}

func newSSHCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh [PROJECT]",
		Short: "Open a shell on the machine, in the checkout (outside tmux)",
		Long: "Opens an interactive login shell in the checkout on PROJECT's machine (this checkout's\n" +
			"project, by default), outside the tmux session: exit ends it. `repose attach` opens the tmux\n" +
			"session instead, and `repose exec PROJECT COMMAND` runs one command.",
		Args:              sshArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := projectFrom(args, g)
			if err != nil {
				return err
			}
			e, err := env()
			if err != nil {
				return err
			}
			return SSHCmd(cmd.Context(), e, project)
		},
	}
}

// sshArgs is projectArgs for `repose ssh`, whose second word is a
// command a peer's ssh would run: the refusal names exec, which runs it
// (DECISIONS I-275 keeps ssh a shell; I-628).
func sshArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return cobraUsageError{fmt.Errorf("%s takes at most one PROJECT and no command, got %s. To run a command: repose exec %s", cmd.CommandPath(), gotArgs(args), strings.Join(args, " "))}
	}
	return nil
}

// SSHCmd implements `repose ssh`: the certificate and config as for
// attach, then ssh replaces this process, so its exit code is ssh's.
func SSHCmd(ctx context.Context, e *Env, projectArg string) error {
	project, target, err := connectRunning(ctx, e, projectArg)
	if err != nil {
		return err
	}
	return execReplaceSSH(target, []string{"-t"}, sshShellScript(project.Slug, target.Checkout))
}
