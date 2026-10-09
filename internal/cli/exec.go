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
	// Workdir is --workdir: where the command runs instead of the
	// checkout (DECISIONS I-608), as workdirShell reads it.
	Workdir string
}

// workdirShell is --workdir DIR as a shell word, after checkoutVar: the
// names `repose ps` shows in TREE, or a path. checkout and worktree-N
// (I-342) are the checkout and its worktrees, each with an optional
// /SUBDIR; ~ and ~/PATH are under the home folder; /PATH is itself;
// anything else is a path inside the checkout.
func workdirShell(dir string) string {
	sub := func(rest string) string {
		if rest == "" {
			return ""
		}
		return "/" + shQuote(rest)
	}
	first, rest, _ := strings.Cut(dir, "/")
	switch {
	case strings.HasPrefix(dir, "/"):
		return shQuote(dir)
	case first == "~":
		return `"$HOME"` + sub(rest)
	case first == "checkout":
		return `"$repose_co"` + sub(rest)
	case strings.HasPrefix(first, "worktree-") && allDigits(strings.TrimPrefix(first, "worktree-")):
		return `"$repose_co"` + shQuote("-"+first) + sub(rest)
	}
	return `"$repose_co"/` + shQuote(dir)
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
	return execScriptIn(slug, extra, "", argv)
}

// execScriptIn is execScript in --workdir dir ("" for the checkout). A
// folder that is not there exits 2 with one line, before the command.
func execScriptIn(slug, extra, dir string, argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shQuote(a)
	}
	name := argv[0]
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	cd := `cd "$repose_co"`
	if dir != "" {
		missing := fmt.Sprintf("%s has no folder %s.", slug, dir)
		cd = fmt.Sprintf("cd %s 2>/dev/null || { printf '%%s\\n' %s >&2; exit 2; }", workdirShell(dir), shQuote(missing))
	}
	return fmt.Sprintf(`%[1]s%[5]s
[ -r /etc/profile.d/repose.sh ] && . /etc/profile.d/repose.sh
if [ -r %[2]s ]; then . %[2]s; REPOSE_DEVSHELL_QUIET=1 _repose_devshell %[3]s; unset -f _repose_devshell _repose_devshell_done
elif command -v direnv >/dev/null 2>&1; then eval "$(direnv export bash 2>/dev/null)"; fi
exec %[4]s`, checkoutVar(slug, extra), execDevshell, shQuote(name), strings.Join(quoted, " "), cd)
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
				default:
					if v, ok := strings.CutPrefix(f, "--workdir="); ok {
						opts.Workdir = v
					}
				}
			}
			for j := 1; j < k-1; j++ {
				if args[j] == "--workdir" {
					opts.Workdir = args[j+1]
				}
			}
			return args[0], args[k+1:], true
		case "-i", "-t", "-it", "-ti", "--interactive", "--tty":
			continue
		case "--workdir":
			if k+1 < len(args) && args[k+1] != "--" {
				k++
				continue
			}
		}
		if strings.HasPrefix(args[k], "--workdir=") {
			continue
		}
		return "", nil, false
	}
	return "", nil, false
}

func newExecCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var opts ExecOptions
	cmd := &cobra.Command{
		Use:   "exec [PROJECT[:CHECKOUT]] [--] COMMAND [ARG...]",
		Short: "Run one command in the checkout on the machine",
		Long: "Run COMMAND in the checkout on PROJECT's machine (this checkout's project, by\n" +
			"default), with the project's secrets and dev shell, as an agent there has them.\n" +
			"Output streams back, and the exit code is the command's. The words pass through\n" +
			"as they are; for a pipeline, use sh -c '...'. A first word that names one of\n" +
			"your projects is PROJECT; put -- before a command that has a project's name.\n" +
			"exec's own flags go before COMMAND.\n\n" +
			"Without -i the command gets no input, and without -t no terminal; -it gives\n" +
			"both, for something interactive such as a REPL.",
		Example: "  repose exec npm test\n  repose exec todo-app git log --oneline -5\n  repose exec -it psql\n  repose exec --workdir worktree-1 npm test",
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
	cmd.Flags().StringVar(&opts.Workdir, "workdir", "", "run in `DIR`: worktree-N, checkout, ~/PATH, /PATH, or a path in the checkout")
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
	// PROJECT:CHECKOUT names one of its checkouts (I-480).
	word := opts.Command[0]
	slug, _, _ := strings.Cut(word, ":")
	for _, p := range projects {
		if p.Slug != slug {
			continue
		}
		if len(opts.Command) == 1 {
			return exitf(ExitUsage, "%q is one of your projects. Give the command after it: `repose exec %s COMMAND`, or `repose exec -- %s` to run a command of that name.", word, word, word)
		}
		opts.ProjectArg, opts.Command = word, opts.Command[1:]
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
	c := exec.CommandContext(ctx, "ssh", execSSHArgs(target, opts, execScriptIn(project.Slug, target.Checkout, opts.Workdir, opts.Command))...)
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
		Use:   "ssh [PROJECT[:CHECKOUT]]",
		Short: "Open a shell in the checkout on the machine",
		Long: "Open a login shell in the checkout on PROJECT's machine (this checkout's\n" +
			"project, by default), outside the session that attach opens: exit ends it.\n" +
			"repose exec runs one command instead.",
		SuggestFor:        []string{"shell", "connect", "console", "sh"},
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
