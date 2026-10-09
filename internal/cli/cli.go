package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Execute is cmd/repose's entry point. version is the build's -ldflags
// value ("dev" outside a release build). It returns the process exit code
// per docs/interfaces/cli-config.md.
// noticeAfter reports whether the older-CLI notice may print after cmd:
// not after tab completion or a hidden command, whose stderr the shell
// or a script throws away, which would use up the once-per-release
// notice unseen (I-631).
func noticeAfter(cmd *cobra.Command) bool {
	if cmd == nil {
		return true
	}
	for c := cmd; c != nil; c = c.Parent() {
		if c.Hidden || c.Name() == cobra.ShellCompRequestCmd || c.Name() == cobra.ShellCompNoDescRequestCmd {
			return false
		}
	}
	return true
}

func Execute(version string) int {
	markSSHPrepared() // before any child starts (I-281)
	cliVersion = version
	// A mistyped command or subcommand is answered before cobra runs, on a
	// throwaway tree (its flag parsing leaves state behind), with the
	// command the user probably meant (DECISIONS I-276).
	if msg := unknownCommand(newRootCmd(version), os.Args[1:], projectWord); msg != "" {
		_, _ = fmt.Fprintln(os.Stderr, msg)
		return ExitUsage
	}
	root := newRootCmd(version)
	root.SilenceErrors = true
	root.SilenceUsage = true
	// Ctrl-C cancels the command's context, so a spinner line is cleared
	// and child ssh processes are ended, instead of the process dying
	// mid-line.
	// The first Ctrl-C is the command's to handle; after it the handler
	// goes, so a second one ends a command whose cleanup is stuck (the
	// forwards' teardown, a slow ssh) the way it ends any program
	// (DECISIONS I-598). Code that must restore the terminal (the input
	// proxy, a hidden prompt, a herdr client) holds its own handler.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() { <-ctx.Done(); stop() }()
	var cmd *cobra.Command
	defer func() {
		if noticeAfter(cmd) {
			noteNewerCLI()
		}
	}()
	cmd, err := root.ExecuteContextC(ctx)
	if err == nil {
		return ExitOK
	}
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Interrupted.")
		return ExitInterrupted
	}
	if usageErr, ok := err.(cobraUsageError); ok {
		_, _ = fmt.Fprintln(os.Stderr, usageErr.Error())
		return ExitUsage
	}
	// cobra's own refusals (an unknown command, a wrong argument count)
	// are usage mistakes too, not command failures.
	// Its help is the command's own, not the root's (DECISIONS I-628).
	if isCobraRefusal(err) {
		help := "repose --help"
		if cmd != nil && cmd != root {
			help = cmd.CommandPath() + " --help"
		}
		_, _ = fmt.Fprintf(os.Stderr, "%s\n`%s` shows its usage.\n", err, help)
		return ExitUsage
	}
	return exitCodeFor(err, os.Stderr)
}

// isCobraRefusal reports cobra's own usage errors, which it returns as
// plain errors: an unknown command, and ExactArgs, MaximumNArgs ("accepts
// …") and MinimumNArgs ("requires …").
func isCobraRefusal(err error) bool {
	msg := err.Error()
	return strings.HasPrefix(msg, "unknown command") || strings.HasPrefix(msg, "accepts ") || strings.HasPrefix(msg, "requires ") || strings.HasPrefix(msg, "invalid argument")
}

// cobraUsageError marks an error as a plain usage mistake (bad flags,
// wrong arg count) rather than a command failure.
type cobraUsageError struct{ error }

type globalFlags struct {
	command string // the running command's path, set before RunE, with " --project" when it takes no PROJECT argument
	project string
	apiURL  string
	json    bool
	verbose bool
}

// hintCommand is the command as the not-found hint shows it, PROJECT
// included: its usage with [PROJECT] given and the other optional words
// left out (`repose exec PROJECT COMMAND`), or, when it takes PROJECT
// as --project only, its required words and then --project PROJECT
// (`repose secrets set NAME --project PROJECT`), since a PROJECT
// argument there is refused (DECISIONS I-628).
func hintCommand(cmd *cobra.Command) string {
	// A bare group runs one of its subcommands (I-619); the hint is that
	// subcommand's.
	if sub := cmd.Annotations[bareRunsKey]; sub != "" {
		for _, c := range cmd.Commands() {
			if c.Name() == sub {
				return hintCommand(c)
			}
		}
	}
	words := []string{cmd.CommandPath()}
	positional := strings.Contains(cmd.Use, "PROJECT")
	depth := 0 // inside an optional [...] group
	for _, w := range strings.Fields(cmd.Use)[1:] {
		switch {
		case depth == 0 && (strings.HasPrefix(w, "[PROJECT") || strings.HasPrefix(w, "PROJECT")):
			words = append(words, "PROJECT")
		case depth == 0 && !strings.HasPrefix(w, "["):
			words = append(words, w)
		}
		depth += strings.Count(w, "[") - strings.Count(w, "]")
	}
	if !positional {
		words = append(words, "--project", "PROJECT")
	}
	return strings.Join(words, " ")
}

func newRootCmd(version string) *cobra.Command {
	g := &globalFlags{}
	root := &cobra.Command{
		Use:           "repose",
		Short:         "repose: a dev machine in the cloud for each project, where agents keep working",
		SilenceErrors: true,
		SilenceUsage:  true,
		// Version makes cobra accept `repose --version` (docs/CHECKLIST.md
		// "Release (M5)"); the template keeps it byte-identical to
		// `repose version`, herakraft suffix included.
		Version: version,
	}
	root.SetVersionTemplate("repose {{.Version}} (herakraft)\n")
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return cobraUsageError{fmt.Errorf("%v\n`%s --help` shows its usage.", err, cmd.CommandPath())}
	})
	root.PersistentFlags().StringVar(&g.project, "project", "", "act on project `NAME` or id (or $REPOSE_PROJECT)")
	root.PersistentFlags().StringVar(&g.apiURL, "api-url", "", "api base url (or $REPOSE_API_URL)")
	// For a test or self-hosted server; cli.md's "Other servers" has it
	// (review 8.7).
	_ = root.PersistentFlags().MarkHidden("api-url")
	root.PersistentFlags().BoolVarP(&g.verbose, "verbose", "v", false, "log each api request (status, time, request id) and each ssh to stderr")
	root.SetUsageTemplate(usageTemplate)

	env := func() (*Env, error) {
		e, err := newEnv(g.apiURL, g.json, g.verbose)
		if err != nil {
			return nil, err
		}
		e.Client.HTTP = e.httpClient
		e.Client.Warn = func(s string) { e.warn("%s", s) }
		e.Command = g.command
		return e, nil
	}
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		g.command = hintCommand(cmd)
		if g.verbose {
			setVerbose(os.Stderr)
		}
		return refuseProjectFlag(cmd)
	}
	envJSON := func(cmd *cobra.Command) (*Env, error) {
		json, _ := cmd.Flags().GetBool("json")
		g.json = json
		return env()
	}
	_ = root.RegisterFlagCompletionFunc("project", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return projectSlugsForCompletion(env), cobra.ShellCompDirectiveNoFileComp
	})

	root.AddCommand(
		newLoginCmd(env),
		newLogoutCmd(env),
		newRunCmd(env, g),
		newAttachCmd(env, g),
		newSyncCmd(env, g),
		newStartCmd(env, g),
		newStopCmd(env, g),
		newStatusCmd(envJSON, env, g),
		newOpenCmd(env, g),
		newSecretsCmd(env, g),
		newConfigCmd(env, g),
		newSnapshotsCmd(env, g),
		newRmCmd(env, g),
		newKeepCmd(env, g),
		newRestoreCmd(env, g),
		newForkCmd(envJSON, env, g),
		newLogsCmd(envJSON, env, g),
		newLsCmd(envJSON),
		newPsCmd(envJSON, env, g),
		newExecCmd(env, g),
		newSSHCmd(env, g),
		newEventsCmd(envJSON, env, g),
		newQuestionsCmd(envJSON, env, g),
		newReplyCmd(envJSON, g),
		newNotifyCmd(env, g),
		newResizeCmd(env, g),
		newVersionCmd(version),
		newCompletionCmd(),
		newMCPCmd(env, g),
		newBrowserCmd(env, g),
		newCpCmd(env, g),
		newPasteCmd(env, g),
		newScanCmd(env),
		newSessionHelperCmd(),
		newSSHPrepareCmd(env),
		newCodeCmd(env, g),
	)
	groupCommands(root)
	return root
}

// Positional PROJECT (DECISIONS I-155): every command whose object is a
// project takes it as its one argument, docker-style (`repose attach
// izma`), with --project and $REPOSE_PROJECT still working.

// projectArgs is the Args validator for those commands.
func projectArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return cobraUsageError{fmt.Errorf("%s takes at most one PROJECT, got %d arguments: %s", cmd.CommandPath(), len(args), strings.Join(args, " "))}
	}
	return nil
}

// gotArgs says what a usage error received, so a shell glob that expanded
// to many words, or a word that went missing, shows in the message
// (I-346; TestArgErrorsSayWhatTheyGot holds every command to it).
func gotArgs(args []string) string {
	switch len(args) {
	case 0:
		return "no arguments"
	case 1:
		return "1 argument: " + args[0]
	}
	return fmt.Sprintf("%d arguments: %s", len(args), strings.Join(args, " "))
}

// checkSizeFlag refuses a --size that is no size class before anything
// is sent, as a usage error (DECISIONS I-623): the api's refusal came
// back as exit 1 in its own words.
func checkSizeFlag(size string) error {
	if _, ok := classSpecs[size]; size != "" && !ok {
		return cobraUsageError{fmt.Errorf("--size must be small, large or xl, got %q", size)}
	}
	return nil
}

// argsN is an Args validator for between min and max words (max -1: no
// limit). Its refusal says what the command takes and what it got, and,
// on a command that takes PROJECT only as --project, shows the words
// again with the first as the project, the likeliest mistake (DECISIONS
// I-628): `repose secrets set todo-app FOO` is answered with `repose
// secrets set FOO --project todo-app`.
func argsN(min, max int, takes string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) >= min && (max < 0 || len(args) <= max) {
			return nil
		}
		msg := fmt.Sprintf("%s takes %s, got %s", cmd.CommandPath(), takes, gotArgs(args))
		if max >= 0 && len(args) == max+1 && !strings.Contains(cmd.Use, "PROJECT") && cmd.Flag("project") != nil {
			msg += fmt.Sprintf(". A project goes in --project: %s", strings.Join(append(append([]string{cmd.CommandPath()}, args[1:]...), "--project", args[0]), " "))
		}
		return cobraUsageError{errors.New(msg)}
	}
}

// noArgs is cobra.NoArgs as a usage error: v0.1.4 silently ignored a
// stray word (`repose attach projects` attached to the cwd's project).
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return cobraUsageError{fmt.Errorf("%s takes no arguments, got: %s", cmd.CommandPath(), strings.Join(args, " "))}
	}
	return nil
}

// projectFrom picks the project named by the positional argument or
// --project; naming two different ones is a usage error.
func projectFrom(args []string, g *globalFlags) (string, error) {
	if len(args) == 0 {
		return g.project, nil
	}
	if g.project != "" && g.project != args[0] {
		return "", cobraUsageError{fmt.Errorf("%q and --project %q name two projects; pass one", args[0], g.project)}
	}
	return args[0], nil
}

// completeProject completes the one PROJECT argument with the account's
// slugs.
func completeProject(env func() (*Env, error)) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return projectSlugsForCompletion(env), cobra.ShellCompDirectiveNoFileComp
	}
}

// projectSlugsForCompletion asks the api (two seconds at most, a shell is
// waiting) and falls back to the slugs in projects.json.
func projectSlugsForCompletion(env func() (*Env, error)) []string {
	e, err := env()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	seen := map[string]bool{}
	if projects, err := e.Client.ListProjects(ctx); err == nil {
		for _, p := range projects {
			seen[p.Slug] = true
		}
	} else {
		for _, c := range e.Cache.ByRemote {
			if c.Slug != "" {
				seen[c.Slug] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func newLoginCmd(env func() (*Env, error)) *cobra.Command {
	var noBrowser, browser, status bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in, or show the account you are logged in as",
		Long: "Log in with a code: print a link with the code in it, and open it in this\n" +
			"computer's browser when it has one. --status prints the account this laptop is\n" +
			"logged in as, its server and its plan, and exits 3 when there is none.",
		SuggestFor: []string{"whoami", "auth", "signin"},
		Args:       noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			if status {
				return loginStatus(cmd.Context(), e)
			}
			return runLogin(cmd.Context(), e.Dir, e.Cfg, e.httpClient, loginOptsFromEnv(browser, noBrowser))
		},
	}
	cmd.Flags().BoolVar(&status, "status", false, "print the account you are logged in as; exit 3 when logged out")
	cmd.Flags().BoolVar(&browser, "browser", false, "log in through a browser on this computer, where the server allows it")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the link and open no browser")
	return cmd
}

// loginOptsFromEnv is the login options the environment and flags give.
func loginOptsFromEnv(browser, noBrowser bool) loginOptions {
	display := os.Getenv(envDisplay)
	if display == "" {
		display = os.Getenv(envWaylandDisplay)
	}
	return loginOptions{
		Browser:   browser,
		NoBrowser: noBrowser || os.Getenv(envNoBrowser) == "1",
		Display:   display,
		GOOS:      goos(),
		GuestEnv:  os.Getenv(envInGuest) == "1",
	}
}

// loginFirst logs in before a `repose run` on a laptop that has never
// logged in, when a person is at the terminal (DECISIONS I-627): the run
// would only refuse with exit 3 and send them to `repose login`. It
// returns true when it logged in, so the caller builds its Env again.
func loginFirst(ctx context.Context, e *Env) (bool, error) {
	if _, ok := e.Client.Tokens.(notLoggedInSource); !ok || e.JSON || !canPrompt(os.Stdin) || !isatty(os.Stderr) {
		return false, nil
	}
	opts := loginOptsFromEnv(false, false)
	opts.Stdout = os.Stderr
	if err := runLogin(ctx, e.Dir, e.Cfg, e.httpClient, opts); err != nil {
		return false, err
	}
	return true, nil
}

func newLogoutCmd(env func() (*Env, error)) *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Log out and revoke your SSH certificates on every device",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return runLogout(cmd.Context(), e.Dir, e.Cfg, e.httpClient, purge, e.Out)
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove ~/.ssh/repose, ~/.config/repose and the Include line")
	return cmd
}

func newRunCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var opts RunOptions
	var tempRaw string
	cmd := &cobra.Command{
		Use:   "run [PROJECT]",
		Short: "Create or start this checkout's machine and attach",
		Long: "Create or start this checkout's machine and attach. A new machine gets a copy\n" +
			"of the checkout; after that, repose sync sends new work. Your tool logins and\n" +
			"settings are copied each time.\n\n" +
			"PROJECT is the machine of that name, created if there is none: a second\n" +
			"machine for this checkout, or one for a folder with no git remote.",
		Example: "  repose run\n" +
			"  repose run -p \"fix the flaky login test\"\n" +
			"  repose run -d --worktree --agent codex -p \"try approach B\"\n" +
			"  repose run spike --temp 3h",
		SuggestFor:        []string{"up", "create", "new", "init"},
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			temp, args, err := resolveTempFlag(tempRaw, args)
			if err != nil {
				return cobraUsageError{err}
			}
			opts.Temp = temp
			if err := runArgs(&opts, args, g.project, cmd.ErrOrStderr()); err != nil {
				return err
			}
			if opts.ProjectArg != "" && g.project != "" && opts.ProjectArg != g.project {
				return cobraUsageError{fmt.Errorf("%s and --project %s name two projects; pass one", opts.ProjectArg, g.project)}
			}
			if opts.ProjectArg == "" {
				opts.ProjectArg = g.project
			}
			if opts.Agent != "" && !isAgent(opts.Agent) {
				return cobraUsageError{fmt.Errorf("--agent must be one of %s, got %q", strings.Join(agentNames, ", "), opts.Agent)}
			}
			if err := checkSizeFlag(opts.Size); err != nil {
				return err
			}
			if opts.Worktree && opts.Prompt == "" {
				return cobraUsageError{fmt.Errorf("--worktree starts an agent in its own worktree and needs -p PROMPT")}
			}
			if opts.Agent != "" && opts.Prompt == "" {
				return cobraUsageError{fmt.Errorf("--agent needs -p PROMPT")}
			}
			if _, err := parseBridgeAllow(opts.BridgeAllow); err != nil {
				return cobraUsageError{fmt.Errorf("--bridge-allow %w", err)}
			}
			if err := checkMultiplexerFlag(opts.Multiplexer); err != nil {
				return err
			}
			e, err := env()
			if err != nil {
				return err
			}
			if again, err := loginFirst(cmd.Context(), e); err != nil {
				return err
			} else if again {
				if e, err = env(); err != nil {
					return err
				}
			}
			return runRun(cmd.Context(), e, opts, false)
		},
	}
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "claude|opencode|codex|gemini|pi (default: the project's agent)")
	cmd.Flags().StringVar(&opts.Size, "size", "", "`SIZE` of a new or stopped machine: small, large or xl (default: config.toml's default_size, else large)")
	cmd.Flags().StringVarP(&opts.Prompt, "prompt", "p", "", "start an agent and type this prompt into it")
	// Before I-603 the name was --name and PROJECT was the prompt; kept
	// hidden for a release.
	cmd.Flags().StringVar(&opts.Name, "name", "", "the project with this name (repose run NAME)")
	_ = cmd.Flags().MarkHidden("name")
	cmd.Flags().StringVar(&opts.On, "on", "", "add this folder to PROJECT's machine as another checkout, beside its own")
	_ = cmd.RegisterFlagCompletionFunc("on", completeProject(env))
	addTempFlag(cmd, &tempRaw)
	// Moved to `repose sync` (I-367); kept hidden for a release so a
	// script that passes them hears where they went.
	// --stash-remote and --discard-remote are their names before I-633.
	for _, name := range []string{"stash-machine", "stash-remote"} {
		cmd.Flags().BoolVar(&opts.StashRemote, name, false, "moved to repose sync")
		_ = cmd.Flags().MarkHidden(name)
	}
	for _, name := range []string{"discard-machine", "discard-remote"} {
		cmd.Flags().BoolVar(&opts.DiscardRemote, name, false, "moved to repose sync")
		_ = cmd.Flags().MarkHidden(name)
	}
	cmd.Flags().BoolVar(&opts.NoSync, "no-sync", false, "do not sync the checkout, even into a new machine")
	cmd.Flags().BoolVarP(&opts.NoAttach, "no-attach", "d", false, "do not attach; with -p, print the window the agent is in")
	cmd.Flags().BoolVar(&opts.NoPersonal, "no-personal", false, "keep your machine.nix off this machine from now on")
	addMultiplexerFlag(cmd, &opts.Multiplexer)
	cmd.Flags().BoolVar(&opts.Worktree, "worktree", false, "start the agent in its own git worktree, on branch worktree-N")
	cmd.Flags().BoolVar(&opts.Bridge, "bridge", false, "bridge this laptop's Chrome to the machine while attached")
	cmd.Flags().StringArrayVar(&opts.BridgeAllow, "bridge-allow", nil, "bridge, and let the agents open only this host (repeatable)")
	_ = cmd.RegisterFlagCompletionFunc("agent", cobra.FixedCompletions(agentNames, cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.RegisterFlagCompletionFunc("size", cobra.FixedCompletions([]string{"small", "large", "xl"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func newAttachCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var bridge bool
	var bridgeAllow []string
	var window string
	cmd := &cobra.Command{
		Use:   "attach [PROJECT[:CHECKOUT]] [WINDOW]",
		Short: "Attach to a project's session, or to one of its windows",
		Long: "Attach to PROJECT's session (this checkout's, by default) on its current window,\n" +
			"without syncing. With WINDOW (or -w), open on that window: a tmux window's name\n" +
			"or number as repose ps shows it, or on herdr an agent's name. A stopped machine\n" +
			"starts first. One word that names no project is WINDOW of this checkout's.",
		Example: "  repose attach\n  repose attach todo-app claude-2\n  repose attach -w 2",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 2 {
				return cobraUsageError{fmt.Errorf("%s takes PROJECT and WINDOW, got %s", cmd.CommandPath(), gotArgs(args))}
			}
			return nil
		},
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 {
				if window != "" && window != args[1] {
					return cobraUsageError{fmt.Errorf("%s and --window %s name two windows; pass one", args[1], window)}
				}
				window, args = args[1], args[:1]
			}
			if _, err := projectFrom(args, g); err != nil {
				return err
			}
			if _, err := parseBridgeAllow(bridgeAllow); err != nil {
				return cobraUsageError{fmt.Errorf("--bridge-allow %w", err)}
			}
			e, err := env()
			if err != nil {
				return err
			}
			return orWindow(cmd.Context(), e, args, window, g.project, func(project, window string) error {
				return runRun(cmd.Context(), e, RunOptions{ProjectArg: project, Window: window, Bridge: bridge, BridgeAllow: bridgeAllow}, true)
			})
		},
	}
	cmd.Flags().StringVarP(&window, "window", "w", "", "open on window `NAME` (a name or number, or a herdr agent)")
	cmd.Flags().BoolVar(&bridge, "bridge", false, "bridge this laptop's Chrome to the machine while attached")
	cmd.Flags().StringArrayVar(&bridgeAllow, "bridge-allow", nil, "bridge, and let the agents open only this host (repeatable)")
	return cmd
}

// newSyncCmd is `repose sync` (DECISIONS I-302): `repose run --no-attach`
// under its own name, for putting the checkout on the machine without
// attaching.
func newSyncCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var opts RunOptions
	var tempRaw string
	cmd := &cobra.Command{
		Use:   "sync [PROJECT]",
		Short: "Send this checkout's changes to its machine",
		Long: "Copy your uncommitted changes and unpushed commits over the machine's checkout,\n" +
			"creating or starting the machine if needed, without attaching. When the machine\n" +
			"changed the same files, stop with exit 6 and name them; --stash-machine clears\n" +
			"the way. Nothing comes back: git fetch repose does that.\n\n" +
			"sync.exclude in config.toml leaves out files that .gitignore does not.",
		Example:           "  repose sync\n  repose sync --stash-machine",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			temp, args, err := resolveTempFlag(tempRaw, args)
			if err != nil {
				return cobraUsageError{err}
			}
			opts.Temp = temp
			// Checked after --temp took its duration (`sync --temp 2h
			// spike`).
			if err := projectArgs(cmd, args); err != nil {
				return err
			}
			if err := checkSizeFlag(opts.Size); err != nil {
				return err
			}
			if len(args) == 1 {
				if err := positionalProject(&opts, args[0]); err != nil {
					return err
				}
			}
			if err := checkMultiplexerFlag(opts.Multiplexer); err != nil {
				return err
			}
			e, err := env()
			if err != nil {
				return err
			}
			if opts.ProjectArg != "" && g.project != "" && opts.ProjectArg != g.project {
				return cobraUsageError{fmt.Errorf("%s and --project %s name two projects; pass one", opts.ProjectArg, g.project)}
			}
			if opts.ProjectArg == "" {
				opts.ProjectArg = g.project
			}
			opts.NoAttach, opts.Sync = true, true
			return withProjectJSON(cmd.Context(), e, opts.ProjectArg, func() error { return runRun(cmd.Context(), e, opts, false) }, func() *Project { return e.acted })
		},
	}
	addProjectJSONFlag(cmd, g)
	cmd.Flags().StringVar(&opts.Size, "size", "", "`SIZE` of a new or stopped machine: small, large or xl (default: config.toml's default_size, else large)")
	// Before I-603 PROJECT had to exist and --name created; kept hidden
	// for a release.
	cmd.Flags().StringVar(&opts.Name, "name", "", "the project with this name (repose sync NAME)")
	_ = cmd.Flags().MarkHidden("name")
	addTempFlag(cmd, &tempRaw)
	cmd.Flags().BoolVar(&opts.StashRemote, "stash-machine", false, "stash the machine's uncommitted changes there before syncing")
	cmd.Flags().BoolVar(&opts.DiscardRemote, "discard-machine", false, "as --stash-machine, and also end a merge or rebase in progress there")
	// Their names before I-633: "remote" read as a git remote. Hidden
	// for a release.
	cmd.Flags().BoolVar(&opts.StashRemote, "stash-remote", false, "old name of --stash-machine")
	cmd.Flags().BoolVar(&opts.DiscardRemote, "discard-remote", false, "old name of --discard-machine")
	_ = cmd.Flags().MarkHidden("stash-remote")
	_ = cmd.Flags().MarkHidden("discard-remote")
	addMultiplexerFlag(cmd, &opts.Multiplexer)
	_ = cmd.RegisterFlagCompletionFunc("size", cobra.FixedCompletions([]string{"small", "large", "xl"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func newStartCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "start [PROJECT]",
		Short:             "Start a project, or restart one in error",
		Long:              "Start a stopped machine, or restart one in error. Nothing is synced.",
		Args:              projectArgs,
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
			return withProjectJSON(cmd.Context(), e, project, func() error { return StartCmd(cmd.Context(), e, project) }, nil)
		},
	}
	addProjectJSONFlag(cmd, g)
	return cmd
}

func newStopCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var noSnapshot, idle, yes bool
	cmd := &cobra.Command{
		Use:   "stop [PROJECT...]",
		Short: "Snapshot and stop projects",
		Long: "Snapshot and stop each PROJECT's machine (this checkout's, by default),\n" +
			"several at once. Every process on it ends. It asks first when an agent is\n" +
			"mid-turn or waiting for an answer; -y skips the question and is required\n" +
			"without a terminal. Run in the machine's checkout, it runs git fetch repose\n" +
			"first.",
		Example:           "  repose stop\n  repose stop api web\n  repose stop --unused",
		SuggestFor:        []string{"down", "halt"},
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeProjects(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			if idle && (len(args) > 0 || g.project != "") {
				return cobraUsageError{fmt.Errorf("--unused stops every unused machine; it takes no PROJECT")}
			}
			var projects []string
			if !idle {
				var err error
				if projects, err = projectsFrom(args, g); err != nil {
					return err
				}
			}
			e, err := env()
			if err != nil {
				return err
			}
			o := StopOptions{Projects: projects, Idle: idle, Snapshot: !noSnapshot, Yes: yes}
			if !yes && canPrompt(os.Stdin) {
				o.Confirm = func(prompt string) (bool, error) { return askYesNo(cmd.Context(), prompt, false, "stopping") }
			}
			return stopWithJSON(cmd.Context(), e, o)
		},
	}
	cmd.Flags().BoolVar(&noSnapshot, "no-snapshot", false, "stop without taking a snapshot")
	cmd.Flags().BoolVar(&idle, "unused", false, "stop every machine running a day with nobody on it and no agent working")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "stop without asking when an agent is working or waiting for an answer")
	cmd.Flags().BoolVar(&g.json, "json", false, "print the project as JSON when done (an array for several)")
	return cmd
}

func newStatusCmd(envJSON func(*cobra.Command) (*Env, error), env func() (*Env, error), g *globalFlags) *cobra.Command {
	var watch bool
	var wait string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:               "status [PROJECT]",
		Short:             "Show a project's machine, agents and git state",
		Args:              projectArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := projectFrom(args, g)
			if err != nil {
				return err
			}
			if watch && wait != "" {
				return cobraUsageError{fmt.Errorf("--follow and --wait are two different waits; pass one")}
			}
			if cmd.Flags().Changed("timeout") && wait == "" {
				return cobraUsageError{fmt.Errorf("--timeout goes with --wait")}
			}
			e, err := envJSON(cmd)
			if err != nil {
				return err
			}
			switch {
			case watch:
				// Under --json each refresh is one line (NDJSON, I-609).
				e.jsonLines = e.JSON
				return StatusWatch(cmd.Context(), e, project, !e.JSON && e.Out == os.Stdout && canDrawSpinner(os.Stdout))
			case wait != "":
				return StatusWait(cmd.Context(), e, project, wait, timeout)
			}
			return StatusCmd(cmd.Context(), e, project)
		},
	}
	cmd.Flags().Bool("json", false, "print the project, with its checkout's git state, as JSON")
	cmd.Flags().BoolVarP(&watch, "follow", "f", false, "refresh every 5 seconds until Ctrl-C")
	// --watch was the name until I-609; -f is what logs and events take.
	cmd.Flags().BoolVar(&watch, "watch", false, "refresh every 5 seconds until Ctrl-C")
	_ = cmd.Flags().MarkHidden("watch")
	cmd.Flags().StringVar(&wait, "wait", "", "wait until the machine is in `STATE` (running, stopped, ...), then print the status")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "how long --wait waits before exiting 1, a `DURATION` such as 5m")
	_ = cmd.RegisterFlagCompletionFunc("wait", cobra.FixedCompletions(projectStates, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

// newLsCmd is `repose ls`, which lists projects. It was `repose
// projects` until DECISIONS I-273; that name stays an alias so scripts
// keep working, and no text tells a user to type it.
func newLsCmd(envJSON func(*cobra.Command) (*Env, error)) *cobra.Command {
	var destroyed, all, quiet bool
	cmd := &cobra.Command{
		Use:        "ls",
		Aliases:    []string{"projects"},
		SuggestFor: []string{"list", "project"},
		Short:      "List your projects",
		Args:       noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := envJSON(cmd)
			if err != nil {
				return err
			}
			if all && !destroyed {
				return cobraUsageError{fmt.Errorf("--all goes with --destroyed")}
			}
			if quiet && e.JSON {
				return cobraUsageError{fmt.Errorf("-q and --json are two different outputs; pass one")}
			}
			e.Quiet = quiet
			if destroyed {
				return DestroyedCmd(cmd.Context(), e, all)
			}
			return ProjectsCmd(cmd.Context(), e)
		},
	}
	cmd.Flags().Bool("json", false, "print as JSON")
	cmd.Flags().BoolVar(&destroyed, "destroyed", false, "list destroyed projects that can still be restored, and until when")
	cmd.Flags().BoolVar(&all, "all", false, "with --destroyed: every one, not only the newest per name")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "print only the project names, one per line")
	return cmd
}

func newOpenCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var desktop, stop, noBrowser bool
	var localPort int
	cmd := &cobra.Command{
		Use:   "open [LOCAL:]PORT",
		Short: "Forward a port on the machine to the laptop",
		Long: "Forward PORT on the machine to the same port on the laptop, or to LOCAL, and\n" +
			"open it in the browser, until Ctrl-C. Database ports (5432, 3306, 6379, 27017)\n" +
			"open no browser. The project is the folder's, or --project's.",
		Example:    "  repose open 3000\n  repose open 8080:3000\n  repose open 5432 --project todo-app",
		SuggestFor: []string{"port", "forward", "tunnel", "expose"},
		Args:       argsN(0, 1, "at most one [LOCAL:]PORT"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if stop && !desktop {
				return cobraUsageError{fmt.Errorf("--stop goes with --desktop: repose open --desktop --stop (or repose browser stop)")}
			}
			e, err := env()
			if err != nil {
				return err
			}
			if desktop {
				// The old name of `repose browser` (I-292): same
				// behaviour, one line saying where it went.
				_, _ = fmt.Fprintln(e.ErrOut, "repose open --desktop is now repose browser; this still works.")
				return BrowserCmd(cmd.Context(), e, g.project, BrowserOptions{Stop: stop, NoOpen: noBrowser})
			}
			if len(args) != 1 {
				return cobraUsageError{fmt.Errorf("repose open [LOCAL:]PORT (the machine's desktop is repose browser)")}
			}
			local, port, err := parseOpenPorts(args[0])
			if err != nil {
				return cobraUsageError{err}
			}
			if local != 0 && localPort != 0 && local != localPort {
				return cobraUsageError{fmt.Errorf("%s and --local-port %d name two laptop ports; pass one", args[0], localPort)}
			}
			if local == 0 {
				local = localPort
			}
			return OpenPortCmd(cmd.Context(), e, g.project, port, local, noBrowser)
		},
	}
	cmd.Flags().BoolVar(&desktop, "desktop", false, "the old name of repose browser")
	cmd.Flags().BoolVar(&stop, "stop", false, "with --desktop: the old name of repose browser stop")
	_ = cmd.Flags().MarkHidden("desktop")
	_ = cmd.Flags().MarkHidden("stop")
	// LOCAL:PORT replaced it (I-619); hidden for a release.
	cmd.Flags().IntVar(&localPort, "local-port", 0, "local port to bind (repose open LOCAL:PORT)")
	_ = cmd.Flags().MarkHidden("local-port")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the URL instead of opening a browser")
	return cmd
}

// parseOpenPorts reads `repose open`'s argument: PORT, or LOCAL:PORT as
// ssh -L and docker -p write it. local is 0 when not given.
func parseOpenPorts(arg string) (local, port int, err error) {
	num := func(s string) (int, bool) {
		n, err := strconv.Atoi(s)
		return n, err == nil && n >= 1 && n <= 65535
	}
	if l, r, ok := strings.Cut(arg, ":"); ok {
		ln, lok := num(l)
		rn, rok := num(r)
		if !lok || !rok {
			return 0, 0, fmt.Errorf("LOCAL:PORT takes two port numbers (1-65535), got %q", arg)
		}
		return ln, rn, nil
	}
	n, ok := num(arg)
	if !ok {
		return 0, 0, fmt.Errorf("PORT must be a port number (1-65535), got %q; the project is --project NAME", arg)
	}
	return 0, n, nil
}

// newBrowserCmd is `repose browser [PROJECT]` (DECISIONS I-292): watch the
// agent's browser and take it over, in one command; `repose browser
// bridge` (I-296) is the other direction, the laptop's Chrome for the
// agents.
func newBrowserCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var stop, noOpen bool
	root := &cobra.Command{
		Use:   "browser [PROJECT]",
		Short: "Watch and drive the agents' browser on the machine",
		Long: `Start the machine's desktop viewer if needed, forward it to the laptop in the
background (port 6080, or the next free one) and open the viewer page. The page
shows the browser the agent drives, sized to your tab; click and type in it to
log in, solve a captcha or approve a passkey. The password is in the link after
the #. The view sleeps after 30 idle minutes; opening the page wakes it.

repose browser bridge goes the other way: the agents use your Chrome.`,
		Args:              projectArgs,
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
			return BrowserCmd(cmd.Context(), e, project, BrowserOptions{Stop: stop, NoOpen: noOpen})
		},
	}
	root.Flags().BoolVar(&noOpen, "no-browser", false, "print the link instead of opening a browser")
	// The old spellings (I-619): `repose browser stop` and --no-browser,
	// as on open and login. Hidden for a release.
	root.Flags().BoolVar(&stop, "stop", false, "the old name of repose browser stop")
	root.Flags().BoolVar(&noOpen, "no-open", false, "the old name of --no-browser")
	_ = root.Flags().MarkHidden("stop")
	_ = root.Flags().MarkHidden("no-open")
	root.AddCommand(&cobra.Command{
		Use:               "stop [PROJECT]",
		Short:             "Stop the browser view and its forward to this laptop",
		Args:              projectArgs,
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
			return BrowserCmd(cmd.Context(), e, project, BrowserOptions{Stop: true})
		},
	})
	// `repose browser bridge` (DECISIONS I-296): the agents on the machine
	// browse in this laptop's Chrome for as long as the command runs.
	var opts BridgeOptions
	bridge := &cobra.Command{
		Use:   "bridge [PROJECT]",
		Short: "Let the machine's agents browse in this laptop's Chrome",
		Long: "Let the agents on a machine browse in this laptop's Chrome, with your logins\n" +
			"and extensions, until Ctrl-C.\n\n" +
			"Needs Chrome 144 or newer with remote debugging turned on at\n" +
			"chrome://inspect/#remote-debugging; when it is off, the command opens that page\n" +
			"and waits. Chrome asks you to allow each connection. Nothing on the machine\n" +
			"changes: its browser tools reach your Chrome through the SSH connection for as\n" +
			"long as this runs. Needs the machine running. --allow HOST keeps the agents to\n" +
			"those hosts; each page they open is listed here.",
		Args:              projectArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := projectFrom(args, g)
			if err != nil {
				return err
			}
			if opts.CDP != "" && opts.UserDataDir != "" {
				return cobraUsageError{fmt.Errorf("--cdp names the browser; --user-data-dir is not needed with it")}
			}
			if _, err := parseBridgeAllow(opts.Allow); err != nil {
				return cobraUsageError{fmt.Errorf("--allow %w", err)}
			}
			e, err := env()
			if err != nil {
				return err
			}
			return BrowserBridgeCmd(cmd.Context(), e, project, opts)
		},
	}
	bridge.Flags().StringVar(&opts.CDP, "cdp", "", "bridge this DevTools server instead, such as http://127.0.0.1:9222")
	bridge.Flags().StringVar(&opts.UserDataDir, "user-data-dir", "", "the profile directory of a Chrome that is not Google Chrome's default one")
	bridge.Flags().BoolVar(&opts.NoBrowser, "no-inspect", false, "don't open chrome://inspect when remote debugging is off")
	// --no-browser said "print the link" on open and browser and
	// something else here (I-619); hidden for a release.
	bridge.Flags().BoolVar(&opts.NoBrowser, "no-browser", false, "the old name of --no-inspect")
	_ = bridge.Flags().MarkHidden("no-browser")
	bridge.Flags().StringArrayVar(&opts.Allow, "allow", nil, "let the agents open only this host (repeatable; *.example.com for subdomains)")
	root.AddCommand(bridge)
	return root
}

func newSecretsCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	root := &cobra.Command{Use: "secrets", Short: "Set, list or remove a project's secrets",
		Long: "Set, list or remove a project's secrets. Agents on the machine see each one\n" +
			"as an environment variable and as a file under /run/repose/secrets. Alone,\n" +
			"list them.",
		SuggestFor: []string{"env", "secret"}}
	var fromFile string
	var fromEnv bool
	set := &cobra.Command{
		Use:   "set NAME",
		Short: "Set a secret from a prompt, a pipe, a file or a variable",
		Long: "Set NAME on the project, the folder's or --project's. The value is asked for,\n" +
			"or read from a pipe (one trailing newline dropped), --from-file or --from-env;\n" +
			"it is never an argument, so it stays out of your shell history. A running\n" +
			"machine has it within seconds.",
		Example: "  repose secrets set STRIPE_KEY\n" +
			"  op read op://dev/stripe/key | repose secrets set STRIPE_KEY\n" +
			"  repose secrets set DATABASE_URL --from-env --project todo-app",
		Args: argsN(1, 1, "one NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The name is checked before any value is asked for.
			if err := checkSecretName(args[0]); err != nil {
				return err
			}
			e, err := env()
			if err != nil {
				return err
			}
			value, err := readSecretValue(args[0], fromFile, fromEnv)
			if err != nil {
				return err
			}
			return SecretsSetCmd(cmd.Context(), e, g.project, args[0], value)
		},
	}
	set.Flags().StringVar(&fromFile, "from-file", "", "read the value from the file at `PATH`")
	set.Flags().BoolVar(&fromEnv, "from-env", false, "read the value from $NAME")

	list := &cobra.Command{
		Use:               "list [PROJECT]",
		Aliases:           []string{"ls"},
		Short:             "List a project's secret names",
		Args:              projectArgs,
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
			return SecretsListCmd(cmd.Context(), e, project)
		},
	}
	list.Flags().BoolVar(&g.json, "json", false, "print the names and dates as JSON")
	rm := &cobra.Command{
		Use:        "rm NAME",
		Aliases:    []string{"remove"},
		SuggestFor: []string{"delete", "unset"},
		Short:      "Remove a secret from a project",
		Args:       argsN(1, 1, "one NAME"),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return secretNamesForCompletion(env, g.project), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return SecretsRmCmd(cmd.Context(), e, g.project, args[0])
		},
	}
	var chooseOn, chooseOff, chooseReset bool
	choose := &cobra.Command{
		Use:   "choose [NAME...]",
		Short: "Choose which laptop logins and files repose run copies",
		Long: "Choose which of your laptop's logins and files repose run copies to the\n" +
			"machine: " + strings.Join(loginNames(), ", ") + ".\n\n" +
			"With no flags, show a list to toggle in a terminal, or print it otherwise.\n" +
			"--off NAME... leaves them on your laptop, --on NAME... copies them again, and\n" +
			"--reset drops the list. Without --project the list is for every project; with\n" +
			"--project NAME it is that project's own.",
		ValidArgs: loginNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return SecretsChooseCmd(cmd.Context(), e, g.project, chooseOn, chooseOff, chooseReset, args)
		},
	}
	choose.Flags().BoolVar(&chooseOn, "on", false, "copy the NAMEs at each repose run")
	choose.Flags().BoolVar(&chooseOff, "off", false, "leave the NAMEs on your laptop, and remove the machine's copies")
	choose.Flags().BoolVar(&chooseReset, "reset", false, "drop the list (a project's: it follows the shared one)")
	root.AddCommand(set, list, rm, newSecretsImportCmd(env, g), choose)
	bareRuns(root, list)
	return root
}

func readSecretValue(name, fromFile string, fromEnv bool) ([]byte, error) {
	switch {
	case fromFile != "":
		b, err := os.ReadFile(fromFile)
		if err != nil {
			return nil, exitf(ExitUsage, "Could not read %s: %v", fromFile, err)
		}
		return b, nil
	case fromEnv:
		v, ok := os.LookupEnv(name)
		if !ok {
			return nil, cobraUsageError{fmt.Errorf("$%s is not set", name)}
		}
		return []byte(v), nil
	case !term.IsTerminal(int(os.Stdin.Fd())):
		// `op read ... | repose secrets set NAME` (I-620): the value is
		// the input, less the one newline an echo or a password manager
		// ends it with. A terminal, even with TERM=dumb, gets the hidden
		// prompt: its typing must not echo.
		return readPipedSecret(os.Stdin, name)
	default:
		_, _ = fmt.Fprintf(os.Stderr, "Value for %s: ", name)
		v, err := readHiddenLine()
		if err != nil {
			return nil, err
		}
		return v, nil
	}
}

func newConfigCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	root := &cobra.Command{Use: "config", Short: "Add packages and services to the machine",
		Long: `Add packages and services to the machine through the project's repose.nix.
Alone, print it.

With --global, each command acts on your machine.nix instead: a home-manager
module every machine of your account gets, kept at ~/.config/repose/machine.nix
and on your account. repose run pushes that file when it changed; repose config
off PROJECT keeps it off one machine.

The CLI's own settings are keys in ~/.config/repose/config.toml.`,
		Example: "  repose config add gcc air\n  repose config --global add ripgrep fd\n  repose config --global edit"}
	var global bool
	root.PersistentFlags().BoolVar(&global, "global", false, "act on your machine.nix, which every machine of your account gets")
	// globalEnv is env() with --global's refusal of a project argument.
	globalEnv := func() (*Env, error) {
		if global && g.project != "" {
			return nil, cobraUsageError{fmt.Errorf("--global takes no --project (%s): machine.nix applies to every machine of your account", g.project)}
		}
		return env()
	}
	// projectEnv is globalEnv for show and edit, which take [PROJECT]
	// (I-566).
	projectEnv := func(args []string) (*Env, string, error) {
		project, err := projectFrom(args, g)
		if err != nil {
			return nil, "", err
		}
		if global && project != "" {
			return nil, "", cobraUsageError{fmt.Errorf("--global takes no project (%s): machine.nix applies to every machine of your account", project)}
		}
		e, err := env()
		return e, project, err
	}
	var showRevisions bool
	show := &cobra.Command{
		Use:               "show [PROJECT]",
		Short:             "Print the project's repose.nix",
		Args:              projectArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, project, err := projectEnv(args)
			if err != nil {
				return err
			}
			if global {
				return GlobalShowCmd(cmd.Context(), e, showRevisions)
			}
			return ConfigShowCmd(cmd.Context(), e, project, showRevisions)
		},
	}
	// `repose config revisions` replaced it (I-622); hidden for a release.
	show.Flags().BoolVar(&showRevisions, "revisions", false, "the old name of repose config revisions")
	_ = show.Flags().MarkHidden("revisions")

	var revQuiet bool
	revisions := &cobra.Command{
		Use:               "revisions [PROJECT]",
		Short:             "List the configuration's revisions, newest first",
		Args:              projectArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			if revQuiet && g.json {
				return cobraUsageError{fmt.Errorf("-q and --json are two different outputs; pass one")}
			}
			e, project, err := projectEnv(args)
			if err != nil {
				return err
			}
			e.Quiet = revQuiet
			if global {
				return GlobalShowCmd(cmd.Context(), e, true)
			}
			return ConfigRevisionsCmd(cmd.Context(), e, project)
		},
	}
	revisions.Flags().BoolVar(&g.json, "json", false, "print as JSON")
	revisions.Flags().BoolVarP(&revQuiet, "quiet", "q", false, "print only the revision ids, one per line")

	edit := &cobra.Command{
		Use:               "edit [PROJECT]",
		Short:             "Edit the project's repose.nix in $EDITOR and apply it",
		Args:              projectArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, project, err := projectEnv(args)
			if err != nil {
				return err
			}
			if global {
				return GlobalEditCmd(cmd.Context(), e, openInEditor)
			}
			return ConfigEditCmd(cmd.Context(), e, project, openInEditor)
		},
	}
	var revision string
	apply := &cobra.Command{
		Use:   "apply [PATH]",
		Short: "Apply a repose.nix file or an earlier revision",
		Long: "Apply PATH (default ./repose.nix) to the machine, or with neither, the current\n" +
			"configuration again. --revision ID switches the running machine back to an\n" +
			"earlier revision that built. With --global, push PATH (default\n" +
			"~/.config/repose/machine.nix) to your account.",
		Example: "  repose config apply\n  repose config apply --revision 3f2a9c1e\n  repose config --global apply",
		Args:    argsN(0, 1, "at most one PATH"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if revision != "" && (len(args) > 0 || global) {
				return cobraUsageError{fmt.Errorf("--revision applies one of the project's revisions; it takes no PATH and no --global")}
			}
			e, err := globalEnv()
			if err != nil {
				return err
			}
			if revision != "" {
				return ConfigApplyRevisionCmd(cmd.Context(), e, g.project, revision)
			}
			path := ""
			if len(args) == 1 {
				path = args[0]
			}
			if global {
				return GlobalApplyCmd(cmd.Context(), e, path)
			}
			return ConfigApplyCmd(cmd.Context(), e, g.project, path)
		},
	}
	apply.Flags().StringVar(&revision, "revision", "", "switch the machine to this earlier revision (an `ID` that revisions shows)")
	_ = apply.RegisterFlagCompletionFunc("revision", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return revisionIDsForCompletion(env, g.project), cobra.ShellCompDirectiveNoFileComp
	})
	add := &cobra.Command{
		Use:   "add [PACKAGE...]",
		Short: "Add menu entries or nixpkgs packages to the machine",
		Long: `Add packages to the project's repose.nix and rebuild the machine.

A name the dashboard's menu has (bun, postgresql, portless, ...) adds that
menu entry, services included. Any other name is a nixpkgs attribute: gcc,
air, nodejs_22, python312Packages.black, nodePackages.typescript. Find names
at https://search.nixos.org/packages.

With no names, it lists the menu's entries by group.

A project whose repose.nix was edited by hand has no menu; add packages there
with repose config edit.

With --global, the names go into the home.packages list of your
machine.nix, every machine of your account gets them, and the menu's
services are not available.`,
		Example: "  repose config add gcc air\n  repose config add postgresql python312Packages.black\n  repose config --global add ripgrep",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := globalEnv()
			if err != nil {
				return err
			}
			if len(args) == 0 && !global {
				// The names the menu has, for the user who asked (I-633).
				return ConfigMenuListCmd(cmd.Context(), e)
			}
			if global {
				return GlobalPackagesCmd(cmd.Context(), e, args, true)
			}
			return ConfigAddCmd(cmd.Context(), e, g.project, args)
		},
	}
	remove := &cobra.Command{
		Use:        "remove PACKAGE...",
		Aliases:    []string{"rm"},
		SuggestFor: []string{"delete"},
		Short:      "Remove packages added with config add",
		Example:    "  repose config remove air\n  repose config --global remove ripgrep",
		Args:       argsN(1, -1, "one or more package names"),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := globalEnv()
			if err != nil {
				return err
			}
			if global {
				return GlobalPackagesCmd(cmd.Context(), e, args, false)
			}
			return ConfigRemoveCmd(cmd.Context(), e, g.project, args)
		},
	}
	// `repose config --global on|off [PROJECT]` (I-622): machine.nix on or
	// off for one machine; `repose run --no-personal` is off's shortcut.
	switchCmd := func(on bool) *cobra.Command {
		use, short := "off [PROJECT]", "Keep your machine.nix off the project's machine"
		if on {
			use, short = "on [PROJECT]", "Give the project's machine your machine.nix again"
		}
		return &cobra.Command{
			Use:               use,
			Short:             short,
			Args:              projectArgs,
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
				return PersonalSwitchCmd(cmd.Context(), e, project, on)
			},
		}
	}
	// `config set` and `config get` are what a user of git or npm types for
	// the CLI's own settings, which are config.toml keys (DECISIONS
	// I-622); hidden, they say where those are.
	settings := func(verb string) *cobra.Command {
		return &cobra.Command{
			Use:                verb,
			Hidden:             true,
			DisableFlagParsing: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				return cobraUsageError{fmt.Errorf("repose config changes the machine's Nix packages; the CLI's settings (default_agent, default_size, editor, ...) are keys in %s", displayConfigTOML())}
			},
		}
	}
	root.AddCommand(show, revisions, edit, apply, add, remove, switchCmd(true), switchCmd(false), settings("set"), settings("get"))
	bareRuns(root, show)
	return root
}

func openInEditor(path string) error {
	editor := os.Getenv(envVisual)
	if editor == "" {
		editor = os.Getenv(envEditor)
	}
	if editor == "" {
		editor = "vi"
	}
	// $EDITOR is often "code --wait" or "emacsclient -t": a command line,
	// not a path.
	fields := strings.Fields(editor)
	cmd := exec.Command(fields[0], append(fields[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func newSnapshotsCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	root := &cobra.Command{Use: "snapshots", Short: "List, take or restore a project's snapshots",
		Long: "List, take or restore a project's snapshots. Alone, list them."}
	var as string
	var yes bool
	var quiet bool
	list := &cobra.Command{
		Use:               "list [PROJECT]",
		Aliases:           []string{"ls"},
		Short:             "List a project's snapshots",
		Args:              projectArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			if quiet && g.json {
				return cobraUsageError{fmt.Errorf("-q and --json are two different outputs; pass one")}
			}
			project, err := projectFrom(args, g)
			if err != nil {
				return err
			}
			e, err := env()
			if err != nil {
				return err
			}
			e.Quiet = quiet
			return SnapshotsListCmd(cmd.Context(), e, project)
		},
	}
	list.Flags().BoolVar(&g.json, "json", false, "print as JSON")
	list.Flags().BoolVarP(&quiet, "quiet", "q", false, "print only the snapshot ids, one per line")
	create := &cobra.Command{
		Use:               "create [PROJECT]",
		Short:             "Take a snapshot now",
		Args:              projectArgs,
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
			return SnapshotsCreateCmd(cmd.Context(), e, project)
		},
	}
	create.Flags().BoolVar(&g.json, "json", false, "print the snapshot as JSON when done")
	restore := &cobra.Command{
		Use:   "restore [PROJECT] SNAPSHOT_ID",
		Short: "Restore a snapshot over a stopped project, or into a new one",
		Long: "Replace a stopped project's disk with one of its snapshots, after a question.\n" +
			"With --as NAME, restore the snapshot into a new project called NAME, which\n" +
			"starts. SNAPSHOT_ID is an id as repose snapshots list shows it, or 4 or more of\n" +
			"its first or last characters.",
		Example:           "  repose snapshots restore todo-app 9c1e04ab\n  repose snapshots restore todo-app 9c1e04ab --as todo-app-old",
		Args:              snapshotRestoreArgs,
		ValidArgsFunction: completeSnapshotRestore(env, g),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := projectFrom(args[:len(args)-1], g)
			if err != nil {
				return err
			}
			if project == "" {
				// The no-project hint keeps the id the user typed.
				g.command = "repose snapshots restore PROJECT " + args[len(args)-1]
			}
			e, err := env()
			if err != nil {
				return err
			}
			id, err := resolveSnapshotID(cmd.Context(), e, project, args[len(args)-1])
			if err != nil {
				return err
			}
			var confirm func(string) (bool, error)
			if !yes {
				confirm = func(prompt string) (bool, error) {
					return askYesNo(cmd.Context(), prompt, false, "restoring in place")
				}
			}
			return SnapshotsRestoreCmd(cmd.Context(), e, project, id, as, confirm)
		},
	}
	restore.Flags().StringVar(&as, "as", "", "restore into a new project with this name instead of replacing this one")
	// --as-new was this flag's name; `repose restore` says --as (I-619).
	// Hidden for a release.
	restore.Flags().StringVar(&as, "as-new", "", "the old name of --as")
	_ = restore.Flags().MarkHidden("as-new")
	restore.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation")
	root.AddCommand(list, create, restore)
	bareRuns(root, list)
	return root
}

// snapshotRestoreArgs is `restore [PROJECT] SNAPSHOT_ID` (I-566):
// the api finds a snapshot under its project, so the project
// comes first when named, as on every command whose object is one.
func snapshotRestoreArgs(cmd *cobra.Command, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return cobraUsageError{fmt.Errorf("%s takes [PROJECT] SNAPSHOT_ID, got %s", cmd.CommandPath(), gotArgs(args))}
	}
	return nil
}

// addTempFlag is run's and sync's --temp [DURATION] (DECISIONS I-351).
// Bare, it is 24h; `--temp 3h` takes the argument after it when that
// reads as a duration, `--temp=3h` always does.
func addTempFlag(cmd *cobra.Command, raw *string) {
	cmd.Flags().StringVar(raw, "temp", "", "a new machine, destroyed with no snapshot after DURATION (10m to 24h, default 24h)")
	cmd.Flags().Lookup("temp").NoOptDefVal = tempBare
	_ = cmd.RegisterFlagCompletionFunc("temp", cobra.FixedCompletions(tempDurations, cobra.ShellCompDirectiveNoFileComp))
}

// newKeepCmd is `repose keep`: a temporary machine becomes a normal one
// (DECISIONS I-347), or, with a duration, goes that long from now (I-612).
func newKeepCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "keep [PROJECT] [DURATION]",
		Short: "Keep a temporary machine for good, or for DURATION from now",
		Long: "Make a temporary machine a normal one, which is no longer destroyed. With\n" +
			"DURATION (10m to 24h), keep it temporary and destroy it that long from now\n" +
			"instead. One argument that is a number and a unit (s, m, h, d or w) is\n" +
			"DURATION.",
		Example:           "  repose keep\n  repose keep tmp-k3f9 3h",
		Args:              keepArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			var d time.Duration
			if n := len(args); n > 0 && (n == 2 || looksLikeDuration(args[0])) {
				var err error
				if d, err = parseKeepDuration(args[n-1]); err != nil {
					return cobraUsageError{err}
				}
				args = args[:n-1]
			}
			project, err := projectFrom(args, g)
			if err != nil {
				return err
			}
			e, err := env()
			if err != nil {
				return err
			}
			return KeepCmd(cmd.Context(), e, project, d)
		},
	}
}

// keepArgs allows `keep [PROJECT] [DURATION]`.
func keepArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 2 {
		return cobraUsageError{fmt.Errorf("%s takes at most PROJECT and DURATION, got %d arguments: %s", cmd.CommandPath(), len(args), strings.Join(args, " "))}
	}
	return nil
}

// newRmCmd is `repose rm`, which destroys projects. It was `repose
// destroy` until DECISIONS I-273; that name stays an alias.
func newRmCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var yes, wait bool
	cmd := &cobra.Command{
		Use:        "rm [PROJECT...]",
		Aliases:    []string{"destroy"},
		SuggestFor: []string{"delete", "remove"},
		Short:      "Destroy projects, keeping a final snapshot for 30 days",
		Long: "Destroy each PROJECT (this checkout's, by default), after one question. A final\n" +
			"snapshot of each is kept for 30 days, and repose restore brings it back; a\n" +
			"temporary project gets none.\n\n" +
			"With PROJECT:CHECKOUT, remove only that checkout, one repose run --on added,\n" +
			"with its worktrees, from the running machine. The machine and its own checkout\n" +
			"stay.",
		Example:           "  repose rm todo-app-fork-1 todo-app-fork-2\n  repose rm -y tmp-k3f9\n  repose rm todo-app:api",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeProjects(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			projects, err := projectsFrom(args, g)
			if err != nil {
				return err
			}
			e, err := env()
			if err != nil {
				return err
			}
			var confirm func(string) (bool, error)
			if !yes {
				confirm = func(prompt string) (bool, error) { return askYesNo(cmd.Context(), prompt, false, "destroying") }
			}
			if len(projects) == 1 && strings.Contains(e.resolveArg(projects[0]), ":") && !yes && !canPrompt(os.Stdin) {
				// Refused before the connect (I-618).
				confirm = nil
			}
			return DestroyProjectsCmd(cmd.Context(), e, projects, yes, wait, confirm)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation")
	cmd.Flags().BoolVar(&wait, "wait", false, "wait until the destroy is done and report how it ended (for scripts)")
	return cmd
}

// projectsFrom is projectFrom for commands that take several PROJECTs:
// --project is one more, unless it repeats one of them.
func projectsFrom(args []string, g *globalFlags) ([]string, error) {
	if len(args) <= 1 {
		p, err := projectFrom(args, g)
		return []string{p}, err
	}
	if g.project != "" && !slices.Contains(args, g.project) {
		return nil, cobraUsageError{fmt.Errorf("--project %q and %s name different projects; pass them all as arguments", g.project, strings.Join(args, " "))}
	}
	return args, nil
}

// completeProjects completes any number of PROJECTs, leaving out the ones
// already given.
func completeProjects(env func() (*Env, error)) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		var out []string
		for _, s := range projectSlugsForCompletion(env) {
			if !slices.Contains(args, s) {
				out = append(out, s)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

func newRestoreCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var as, snapshot string
	cmd := &cobra.Command{
		Use:   "restore [NAME]",
		Short: "Bring back a destroyed project from a snapshot",
		Long: "Restore NAME, a project destroyed in the last 30 days (or one that still\n" +
			"exists), from its newest snapshot into a new project called NAME, or --as\n" +
			"NEW-NAME when that name is in use. Without NAME, inside a checkout, restore the\n" +
			"destroyed project with this checkout's remote. repose ls --destroyed lists what\n" +
			"can be restored; repose snapshots restore restores over a project in place.",
		Example:    "  repose restore todo-app\n  repose restore todo-app --as todo-app-2\n  repose ls --destroyed",
		SuggestFor: []string{"undo", "undelete", "recover"},
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return cobraUsageError{fmt.Errorf("%s takes one NAME, got %d arguments: %s", cmd.CommandPath(), len(args), strings.Join(args, " "))}
			}
			return nil
		},
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return destroyedSlugsForCompletion(env), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			// --project NAME is NAME, as on every command whose object
			// is a project (I-619); it was ignored here.
			name, err := projectFrom(args, g)
			if err != nil {
				return err
			}
			var ask func(string) (string, error)
			if canPrompt(os.Stdin) {
				ask = func(prompt string) (string, error) { return askLine(cmd.Context(), prompt) }
			}
			return RestoreCmd(cmd.Context(), e, name, as, snapshot, ask)
		},
	}
	cmd.Flags().StringVar(&as, "as", "", "name the restored project `NEW-NAME` (default: its old name)")
	cmd.Flags().StringVar(&snapshot, "snapshot", "", "`ID` of the snapshot to restore (default: the newest)")
	_ = cmd.RegisterFlagCompletionFunc("snapshot", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return snapshotIDsForCompletion(env, args[0]), cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

func newForkCmd(envJSON func(*cobra.Command) (*Env, error), env func() (*Env, error), g *globalFlags) *cobra.Command {
	opts := ForkOptions{Count: 1}
	cmd := &cobra.Command{
		Use:   "fork [PROJECT]",
		Short: "Copy a project into new projects, each on its own machine",
		Long: "Snapshot PROJECT (this checkout's, by default) and restore the snapshot into\n" +
			"--count new projects, NAME-1, NAME-2, ... (NAME defaults to PROJECT-fork), each\n" +
			"running on its own machine with the same files, configuration and secrets.\n" +
			"PROJECT keeps running. In its checkout, each fork gets a git remote of its\n" +
			"name, so git fetch NAME-1 brings its commits back.\n\n" +
			"Each fork counts toward your plan's disk by what it holds, and toward its memory\n" +
			"while it runs; --no-start creates them stopped.",
		Example: "  repose fork -n 3 --prompt \"try a different approach to the cache\"\n" +
			"  git fetch todo-app-fork-2\n" +
			"  repose rm todo-app-fork-1 todo-app-fork-3",
		SuggestFor:        []string{"clone", "copy"},
		Args:              projectArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := projectFrom(args, g)
			if err != nil {
				return err
			}
			if opts.Agent != "" && !isAgent(opts.Agent) {
				return cobraUsageError{fmt.Errorf("--agent must be one of %s, got %q", strings.Join(agentNames, ", "), opts.Agent)}
			}
			if opts.Agent != "" && opts.Prompt == "" {
				return cobraUsageError{fmt.Errorf("--agent goes with --prompt")}
			}
			if err := checkSizeFlag(opts.Size); err != nil {
				return err
			}
			e, err := envJSON(cmd)
			if err != nil {
				return err
			}
			opts.ProjectArg = project
			return ForkCmd(cmd.Context(), e, opts)
		},
	}
	cmd.Flags().IntVarP(&opts.Count, "count", "n", 1, "how many forks (1 to 10)")
	cmd.Flags().StringVar(&opts.Name, "name", "", "prefix for the forks' names (default: PROJECT-fork)")
	cmd.Flags().StringVar(&opts.Size, "size", "", "`SIZE` of the forks: small, large or xl (default: PROJECT's)")
	cmd.Flags().StringVar(&opts.SnapshotID, "snapshot", "", "fork from this snapshot of PROJECT instead of taking one now")
	cmd.Flags().StringVar(&opts.Prompt, "prompt", "", "start the agent in every fork with this prompt")
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "with --prompt: claude|opencode|codex|gemini|pi (default: PROJECT's)")
	cmd.Flags().BoolVar(&opts.NoStart, "no-start", false, "create the forks stopped; they take no plan memory until started")
	cmd.Flags().Bool("json", false, "print the forks as JSON")
	_ = cmd.RegisterFlagCompletionFunc("agent", cobra.FixedCompletions(agentNames, cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.RegisterFlagCompletionFunc("size", cobra.FixedCompletions([]string{"small", "large", "xl"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func newResizeCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var size string
	var yes bool
	cmd := &cobra.Command{
		Use:   "resize [PROJECT] [DISK]",
		Short: "Grow a project's disk, or change its size",
		Long: "With DISK (80G, 1T), grow the project's disk; disks can't shrink. With --size,\n" +
			"change the project's size: small, large or xl. A stopped project starts at the\n" +
			"new size; a running one is stopped, changed and started again, which ends every\n" +
			"process on it, after a question that -y skips. PROJECT defaults to this\n" +
			"checkout's project; one argument that reads as a size is DISK.",
		Example:           "  repose resize 80G\n  repose resize todo-app --size xl\n  repose resize todo-app 160G --size xl -y",
		Args:              resizeArgs,
		ValidArgsFunction: completeResize(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, bytes, err := parseResizeArgs(args, g)
			if err != nil {
				return err
			}
			if bytes == 0 && size == "" {
				return cobraUsageError{fmt.Errorf("resize needs a disk size (e.g. 80G), --size small|large|xl, or both")}
			}
			if size != "" {
				if _, ok := classSpecs[size]; !ok {
					return cobraUsageError{fmt.Errorf("--size must be small, large or xl, got %q", size)}
				}
			}
			e, err := env()
			if err != nil {
				return err
			}
			return withProjectJSON(cmd.Context(), e, project, func() error {
				if bytes > 0 {
					if err := ResizeCmd(cmd.Context(), e, project, bytes); err != nil {
						return err
					}
				}
				if size == "" {
					return nil
				}
				var confirm func(string) (bool, error)
				if !yes {
					confirm = func(prompt string) (bool, error) {
						return askYesNo(cmd.Context(), prompt, false, "restarting the machine")
					}
				}
				return ResizeClassCmd(cmd.Context(), e, project, size, confirm)
			}, nil)
		},
	}
	addProjectJSONFlag(cmd, g)
	cmd.Flags().StringVar(&size, "size", "", "change the project's `SIZE`: small, large or xl")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "with --size on a running project, restart it without asking")
	_ = cmd.RegisterFlagCompletionFunc("size", cobra.FixedCompletions([]string{"small", "large", "xl"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

// resizeArgs allows `resize [PROJECT] [DISK]`: the project is positional
// like every other command's (I-155), and a disk size can follow it.
func resizeArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 2 {
		return cobraUsageError{fmt.Errorf("%s takes at most PROJECT and DISK, got %d arguments: %s", cmd.CommandPath(), len(args), strings.Join(args, " "))}
	}
	return nil
}

// parseResizeArgs reads `[PROJECT] [DISK]`. With two arguments the first is
// the project and the second must be a size. One argument is DISK when it
// reads as a size (`repose resize 80G` keeps working) and PROJECT otherwise
// (`repose resize izma --size xl`); a project whose name reads as a size is
// named with --project.
func parseResizeArgs(args []string, g *globalFlags) (string, int64, error) {
	var projectArgs []string
	var disk string
	switch len(args) {
	case 2:
		projectArgs, disk = args[:1], args[1]
	case 1:
		// A bare number is a disk size missing its unit, not a project.
		var unitErr *sizeUnitError
		if _, err := parseSize(args[0]); err == nil || errors.As(err, &unitErr) {
			disk = args[0]
		} else {
			projectArgs = args
		}
	}
	project, err := projectFrom(projectArgs, g)
	if err != nil {
		return "", 0, err
	}
	var bytes int64
	if disk != "" {
		if bytes, err = parseSize(disk); err != nil {
			return "", 0, cobraUsageError{err}
		}
	}
	return project, bytes, nil
}

// completeResize offers the account's slugs for the first argument only.
func completeResize(env func() (*Env, error)) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return projectSlugsForCompletion(env), cobra.ShellCompDirectiveNoFileComp
	}
}

func newLogsCmd(envJSON func(*cobra.Command) (*Env, error), env func() (*Env, error), g *globalFlags) *cobra.Command {
	var kind, since string
	var follow bool
	var tail int
	cmd := &cobra.Command{
		Use:   "logs [PROJECT]",
		Short: "Show a machine's console, build or operation log",
		Long: "Show one of the machine's logs (--kind):\n" +
			"  console  what it printed during a boot that failed; a clean boot leaves none\n" +
			"  build    the last configuration build's log\n" +
			"  ops      one line per operation: kind, how it ended, how long it took\n\n" +
			"repose events shows what the agents did.",
		Example:           "  repose logs --kind ops --since 2d\n  repose logs --kind build -f",
		Args:              projectArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := projectFrom(args, g)
			if err != nil {
				return err
			}
			switch kind {
			case "", "console", "build", "ops":
			default:
				return cobraUsageError{fmt.Errorf("--kind must be console, build or ops, got %q", kind)}
			}
			e, err := envJSON(cmd)
			if err != nil {
				return err
			}
			return LogsCmd(cmd.Context(), e, project, kind, since, tail, follow, nil)
		},
	}
	cmd.Flags().Bool("json", false, "print each line as JSON, one per line")
	cmd.Flags().StringVar(&kind, "kind", "console", "console|build|ops")
	cmd.Flags().StringVar(&since, "since", "", "only lines after this: 90m, 2d, 1w, 2026-10-01 or an RFC 3339 time")
	cmd.Flags().IntVarP(&tail, "tail", "n", 0, "only the last N lines (then new ones with -f)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "poll for new lines every 2s")
	_ = cmd.RegisterFlagCompletionFunc("kind", cobra.FixedCompletions([]string{"console", "build", "ops"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func newEventsCmd(envJSON func(*cobra.Command) (*Env, error), env func() (*Env, error), g *globalFlags) *cobra.Command {
	var since string
	var follow bool
	cmd := &cobra.Command{
		Use:   "events [PROJECT]",
		Short: "Show what agents and repose did on a project",
		Long: "Show a project's events, oldest first: what its agents finished, asked, said or\n" +
			"hit, and what happened to its machine. Outside a checkout, with no PROJECT, show\n" +
			"every project's.",
		Example:           "  repose events\n  repose events todo-app --since 3d\n  repose events -f",
		Args:              projectArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := projectFrom(args, g)
			if err != nil {
				return err
			}
			e, err := envJSON(cmd)
			if err != nil {
				return err
			}
			if since == "" {
				since = "24h"
			}
			return EventsCmd(cmd.Context(), e, project, since, follow, nil)
		},
	}
	cmd.Flags().Bool("json", false, "print each event as JSON, one per line")
	cmd.Flags().StringVar(&since, "since", "24h", "only events after this: 90m, 2d, 1w, 2026-10-01 or an RFC 3339 time")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "poll for new events every 10s")
	return cmd
}

func newQuestionsCmd(envJSON func(*cobra.Command) (*Env, error), env func() (*Env, error), g *globalFlags) *cobra.Command {
	var quiet bool
	cmd := &cobra.Command{
		Use:   "questions [PROJECT]",
		Short: "List the questions agents on any project are waiting on you to answer",
		Long: "List the questions agents on any of your projects, or on PROJECT, are waiting on\n" +
			"you to answer with repose reply, then the agents waiting at a prompt in their\n" +
			"terminal, which only attaching answers.",
		Args:              projectArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := projectFrom(args, g)
			if err != nil {
				return err
			}
			e, err := envJSON(cmd)
			if err != nil {
				return err
			}
			if quiet && e.JSON {
				return cobraUsageError{fmt.Errorf("-q and --json are two different outputs; pass one")}
			}
			e.Quiet = quiet
			return QuestionsCmd(cmd.Context(), e, project)
		},
	}
	cmd.Flags().Bool("json", false, "print the questions as JSON")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "print only the question ids, one per line")
	return cmd
}

func newReplyCmd(envJSON func(*cobra.Command) (*Env, error), g *globalFlags) *cobra.Command {
	var question string
	cmd := &cobra.Command{
		Use:   "reply [PROJECT] [--] [ANSWER...]",
		Short: "Answer a question an agent asked with repose-ask",
		Long: "Answer a waiting question. A first word that names one of your projects is\n" +
			"PROJECT; otherwise every word is the answer, which works while one question is\n" +
			"waiting. Put -- before an answer that starts with a project's name. With no\n" +
			"answer, ask for one. When the question has options, the answer is one of them.",
		Example: "  repose reply yes\n  repose reply todo-app \"use the staging database\"\n  repose reply --question 7f3a -- todo-app is fine",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := envJSON(cmd)
			if err != nil {
				return err
			}
			// `--` ends the project: before it at most one word, the
			// project; after it every word is the answer (I-608).
			project, mayName := g.project, true
			switch dash := cmd.ArgsLenAtDash(); {
			case dash == 0:
				mayName = false
			case dash == 1:
				if project != "" && project != args[0] {
					return cobraUsageError{fmt.Errorf("%q and --project %q name two projects; pass one", args[0], project)}
				}
				project, args, mayName = args[0], args[1:], false
			case dash > 1:
				return cobraUsageError{fmt.Errorf("only PROJECT goes before --: repose reply [PROJECT] -- ANSWER")}
			}
			return replyCmd(cmd.Context(), e, args, project, question, os.Stdin, canPrompt(os.Stdin), mayName)
		},
	}
	cmd.Flags().Bool("json", false, "print the answered question as JSON")
	cmd.Flags().StringVar(&question, "question", "", "the question's `ID`, or its last characters, as questions shows it")
	_ = cmd.RegisterFlagCompletionFunc("question", completeQuestionID(func() (*Env, error) { return envJSON(cmd) }))
	return cmd
}

func newNotifyCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var showJSON bool
	root := &cobra.Command{
		Use:   "notify",
		Short: "Show or change where agent notifications go",
		Long: "Show where notifications go: email and ntfy. Agents on a machine send their own\n" +
			"with repose-notify and repose-ask.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			g.json = showJSON
			e, err := env()
			if err != nil {
				return err
			}
			return NotifyShowCmd(cmd.Context(), e)
		},
	}
	root.Flags().BoolVar(&showJSON, "json", false, "print the settings as JSON")
	var emailFlag, ntfyFlag string
	set := &cobra.Command{
		Use:   "set",
		Short: "Change where notifications go",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if emailFlag != "" && emailFlag != "on" && emailFlag != "off" {
				return cobraUsageError{fmt.Errorf("--email is on or off, got %q", emailFlag)}
			}
			e, err := env()
			if err != nil {
				return err
			}
			var email *bool
			if emailFlag != "" {
				b := emailFlag == "on"
				email = &b
			}
			var ntfy *string
			if ntfyFlag != "" {
				ntfy = &ntfyFlag
			}
			return NotifySetCmd(cmd.Context(), e, email, ntfy)
		},
	}
	set.Flags().StringVar(&emailFlag, "email", "", "`on|off`: notify by email, to your account's address")
	set.Flags().StringVar(&ntfyFlag, "ntfy", "", "`URL|off`: notify on this ntfy topic")
	_ = set.RegisterFlagCompletionFunc("email", cobra.FixedCompletions([]string{"on", "off"}, cobra.ShellCompDirectiveNoFileComp))
	test := &cobra.Command{
		Use:   "test",
		Short: "Send a test notification on every channel that is on",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return NotifyTestCmd(cmd.Context(), e)
		},
	}
	root.AddCommand(set, test)
	return root
}

// newVersionCmd prints "repose <version> (herakraft)" (DECISIONS I-15: the
// suffix lets a user tell this binary apart from Arch's unrelated
// `repose` package on the same PATH).
func newVersionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI's version",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("repose %s (herakraft)\n", version)
			return nil
		},
	}
}

// newMCPCmd is `repose mcp`: the MCP servers of the agents on a machine.
func newMCPCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	root := &cobra.Command{Use: "mcp", Short: "List, forward or remove the agents' MCP servers",
		Long: "List, forward or remove the MCP servers of the agents on a machine. Alone, list\n" +
			"them."}
	// `repose mcp forward` (DECISIONS I-557). Names only: the project comes
	// from the folder or --project, since names and a PROJECT cannot share
	// positions without a guess (an exception to I-155).
	var remove bool
	forward := &cobra.Command{
		Use:   "forward NAME... [-- COMMAND [ARG...]]",
		Short: "Let the machine's agents use MCP servers on this laptop",
		Long: `Let the agents on a machine use MCP servers that run on this laptop, until
Ctrl-C.

NAME is a server in your Claude Code, Claude Desktop, Codex or Gemini CLI config
on this laptop, or the command after --. It runs here, with your apps, files and
tokens; each agent session on the machine gets its own copy over SSH. Agents
started after the first forward list NAME; while nothing forwards it, its tools
answer that your laptop isn't connected. To forward whenever you're attached,
add NAME to [mcp] forward in config.toml. The project is the folder's, or
--project's. Needs the machine running.`,
		Example: "  repose mcp forward apple-notes\n" +
			"  repose mcp forward notes -- node ~/mcp/notes.js\n" +
			"  repose mcp forward figma --project todo-app",
		Args: func(cmd *cobra.Command, args []string) error {
			names := args
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				names = args[:dash]
				if dash < len(args) && len(names) != 1 {
					return cobraUsageError{fmt.Errorf("a command after -- goes with one NAME, got %s", gotArgs(names))}
				}
			}
			if len(names) == 0 {
				return cobraUsageError{fmt.Errorf("%s needs a server NAME, got %s", cmd.CommandPath(), gotArgs(args))}
			}
			for _, n := range names {
				if err := mcpForwardName(n); err != nil {
					return cobraUsageError{fmt.Errorf("%w; got %s", err, gotArgs(args))}
				}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := MCPForwardOptions{Names: args, Remove: remove}
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				opts.Names, opts.Inline = args[:dash], args[dash:]
			}
			if remove && len(opts.Inline) > 0 {
				return cobraUsageError{fmt.Errorf("repose mcp rm takes names only, not a command after --")}
			}
			e, err := env()
			if err != nil {
				return err
			}
			return MCPForwardCmd(cmd.Context(), e, g.project, opts)
		},
	}
	// `repose mcp rm` replaced it (I-619); hidden for a release.
	forward.Flags().BoolVar(&remove, "remove", false, "the old name of repose mcp rm")
	_ = forward.Flags().MarkHidden("remove")
	rm := &cobra.Command{
		Use:        "rm NAME...",
		Aliases:    []string{"remove"},
		SuggestFor: []string{"delete"},
		Short:      "Take forwarded MCP servers off the machine's agents",
		Long: `Take forwarded MCP servers off the machine's agents; they drop them at their
next start. The project is the folder's, or --project's. Needs the machine
running.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cobraUsageError{fmt.Errorf("%s needs a server NAME, got %s", cmd.CommandPath(), gotArgs(args))}
			}
			for _, n := range args {
				if err := mcpForwardName(n); err != nil {
					return cobraUsageError{fmt.Errorf("%w; got %s", err, gotArgs(args))}
				}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return MCPForwardCmd(cmd.Context(), e, g.project, MCPForwardOptions{Names: args, Remove: true})
		},
	}
	// `repose mcp list` (DECISIONS I-558): what each agent on the machine
	// has, read from its configs over SSH; starts no server.
	list := &cobra.Command{
		Use:     "list [PROJECT]",
		Aliases: []string{"ls"},
		Short:   "List the machine's MCP servers, and those your laptop kept",
		Long: `List the MCP servers the agents on a machine have, and those your laptop kept.

FROM is repose (the browser tools), laptop (copied from your laptop's Claude
Code, or kept there, with the reason in STATE), forward (repose mcp forward),
project (a checkout's .mcp.json) or machine (added on the machine). STATE
starts with the checkout for a server from one, and is otherwise empty when
the server needs nothing. Piped, it prints one tab-separated line per server
with no header. Needs the machine running.`,
		Args:              projectArgs,
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
			return MCPListCmd(cmd.Context(), e, project)
		},
	}
	list.Flags().BoolVar(&g.json, "json", false, "print JSON")
	root.AddCommand(list, forward, rm)
	bareRuns(root, list)
	return root
}

// Interactive helpers. Kept small and separate from the pure command
// functions above so those stay unit-testable without a terminal.

func goos() string {
	if v := os.Getenv("REPOSE_TEST_GOOS"); v != "" {
		return v
	}
	return runtime.GOOS
}

// askYesNo asks prompt on stderr and reads the answer from stdin; an
// empty answer is defaultYes, as the [Y/n] or [y/N] in the prompt says.
// v0.1.4's helper treated an empty answer as yes even for [y/N]. Without
// a terminal on stdin there is nobody to ask, which is a usage error
// naming --yes rather than a silent default. EOF is no; Ctrl-C (ctx
// done) ends the question at once with errPromptInterrupted, where it
// used to wait for a second Ctrl-C (DECISIONS I-614).
func askYesNo(ctx context.Context, prompt string, defaultYes bool, what string) (bool, error) {
	if !canPrompt(os.Stdin) {
		return false, exitf(ExitUsage, "No terminal to confirm %s on; pass --yes.", what)
	}
	return askYesNoFrom(ctx, os.Stdin, os.Stderr, prompt, defaultYes)
}

func askYesNoFrom(ctx context.Context, in io.Reader, w io.Writer, prompt string, defaultYes bool) (bool, error) {
	line, eof, err := readAnswer(ctx, in, w, prompt)
	if err != nil || eof {
		// EOF is nobody answering, never the default yes.
		return false, err
	}
	switch strings.TrimSpace(strings.ToLower(line)) {
	case "":
		return defaultYes, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// askLine asks prompt on stderr and reads one line from stdin. EOF is an
// empty answer; Ctrl-C is errPromptInterrupted.
func askLine(ctx context.Context, prompt string) (string, error) {
	return askLineFrom(ctx, os.Stdin, os.Stderr, prompt)
}

func askLineFrom(ctx context.Context, in io.Reader, w io.Writer, prompt string) (string, error) {
	line, _, err := readAnswer(ctx, in, w, prompt)
	return line, err
}

// readAnswer prints prompt and reads one line; eof is true when the input
// ended before any answer.
func readAnswer(ctx context.Context, in io.Reader, w io.Writer, prompt string) (line string, eof bool, err error) {
	_, _ = fmt.Fprint(w, prompt)
	type answer struct {
		line string
		err  error
	}
	got := make(chan answer, 1)
	go func() {
		// Left blocked on stdin after a Ctrl-C; the process ends next.
		line, err := bufio.NewReader(in).ReadString('\n')
		got <- answer{strings.TrimRight(line, "\r\n"), err}
	}()
	select {
	case <-ctx.Done():
		_, _ = fmt.Fprintln(w)
		return "", false, errPromptInterrupted
	case a := <-got:
		if a.err != nil && a.line == "" {
			_, _ = fmt.Fprintln(w)
			return "", true, nil
		}
		return a.line, false, nil
	}
}

// errPromptInterrupted is Ctrl-C at a question: exit 130. A command
// that has a line for "nothing happened" says it instead of
// "Interrupted." (confirmOr).
var errPromptInterrupted = &exitError{code: ExitInterrupted, msg: "Interrupted."}

// confirmOr asks confirm(prompt) and returns nil on a yes. Anything else
// ends the command with no on stderr: exit 1 for a no, an empty answer
// or EOF, so `repose rm x && next` stops at a no; 130 for Ctrl-C
// (DECISIONS I-614). An error from confirm (no terminal) is returned as
// it is.
func confirmOr(confirm func(string) (bool, error), prompt, no string) error {
	ok, err := confirm(prompt)
	switch {
	case errors.Is(err, errPromptInterrupted):
		return exitf(ExitInterrupted, "%s", no)
	case err != nil:
		return err
	case !ok:
		return exitf(ExitGeneric, "%s", no)
	}
	return nil
}

func readLine() (string, error) {
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

// readEchoOffLine reads a line with the terminal's echo off (stty, which
// every macOS and Linux laptop has): readHiddenLine's fallback when the
// terminal cannot go into raw mode. Echo comes back on however the read
// ends, Ctrl-C included.
func readEchoOffLine() ([]byte, error) {
	if runtime.GOOS != "windows" && stty("-echo") == nil {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		done := make(chan struct{})
		go func() {
			select {
			case <-sig:
				_ = stty("echo")
				_, _ = fmt.Fprintln(os.Stderr)
				os.Exit(ExitInterrupted)
			case <-done:
			}
		}()
		defer func() {
			close(done)
			signal.Stop(sig)
			_ = stty("echo")
			_, _ = fmt.Fprintln(os.Stderr)
		}()
	}
	line, err := readLine()
	if err != nil && line == "" {
		return nil, exitf(ExitUsage, "No value given.")
	}
	return []byte(line), nil
}

func stty(arg string) error {
	cmd := exec.Command("stty", arg)
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// sizeUnitError is a disk size typed without a unit: `repose resize izma
// 100` once grew nothing, reading 100 bytes (I-613).
type sizeUnitError struct{ in, n string }

func (e *sizeUnitError) Error() string { return fmt.Sprintf("%q needs a unit, like %sG", e.in, e.n) }

// parseSize reads a disk size as typed: a whole number and M, G or T, with
// an optional B or iB (80G, 80GB, 80GiB, 1T), all binary units. A number
// without a unit is a *sizeUnitError; errors quote the input as typed.
func parseSize(in string) (int64, error) {
	s := strings.ToUpper(strings.TrimSpace(in))
	s = strings.TrimSuffix(s, "B")
	s = strings.TrimSuffix(s, "I")
	var mult int64
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"T", 1 << 40}, {"G", 1 << 30}, {"M", 1 << 20}} {
		if strings.HasSuffix(s, u.suffix) {
			mult, s = u.mult, strings.TrimSuffix(s, u.suffix)
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if mult == 0 && err == nil && n > 0 {
		return 0, &sizeUnitError{in: strings.TrimSpace(in), n: s}
	}
	if mult == 0 || err != nil || n <= 0 || n > math.MaxInt64/mult {
		return 0, fmt.Errorf("%q is not a size like 80G", strings.TrimSpace(in))
	}
	return n * mult, nil
}

// diskSize prints a disk size the way it is typed: 80G, 1T, or 1.5 GB
// for one that is not a whole number of gigabytes.
func diskSize(n int64) string {
	switch {
	case n > 0 && n%(1<<40) == 0:
		return fmt.Sprintf("%dT", n>>40)
	case n > 0 && n%(1<<30) == 0:
		return fmt.Sprintf("%dG", n>>30)
	}
	return humanBytes(n)
}
