package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Execute is cmd/repose's entry point. version is the build's -ldflags
// value ("dev" outside a release build). It returns the process exit code
// per docs/interfaces/cli-config.md.
func Execute(version string) int {
	markSSHPrepared() // before any child starts (I-281)
	cliVersion = version
	// A mistyped command or subcommand is answered before cobra runs, on a
	// throwaway tree (its flag parsing leaves state behind), with the
	// command the user probably meant (DECISIONS I-276).
	if msg := unknownCommand(newRootCmd(version), os.Args[1:]); msg != "" {
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
	err := root.ExecuteContext(ctx)
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
	if isCobraRefusal(err) {
		_, _ = fmt.Fprintf(os.Stderr, "%s\nRun `repose --help` for the commands.\n", err)
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

// hintCommand is the command as the not-found hint shows it: its path,
// with " --project" when it takes PROJECT as that flag only (`secrets
// set`, `mcp forward`), since a PROJECT argument there is refused.
func hintCommand(cmd *cobra.Command) string {
	if strings.Contains(cmd.Use, "PROJECT") {
		return cmd.CommandPath()
	}
	return cmd.CommandPath() + " --project"
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
		return cobraUsageError{fmt.Errorf("%v (`%s --help` lists its flags)", err, cmd.CommandPath())}
	})
	root.PersistentFlags().StringVar(&g.project, "project", "", "project name or id (or $REPOSE_PROJECT); most commands also take it as their argument")
	root.PersistentFlags().StringVar(&g.apiURL, "api-url", "", "api base url (or $REPOSE_API_URL)")
	root.PersistentFlags().BoolVarP(&g.verbose, "verbose", "v", false, "debug logging to stderr")

	env := func() (*Env, error) {
		e, err := newEnv(g.apiURL, g.json, g.verbose)
		if err != nil {
			return nil, err
		}
		e.Client.HTTP = e.httpClient
		e.Command = g.command
		return e, nil
	}
	root.PersistentPreRun = func(cmd *cobra.Command, args []string) { g.command = hintCommand(cmd) }
	envJSON := func(cmd *cobra.Command) (*Env, error) {
		json, _ := cmd.Flags().GetBool("json")
		g.json = json
		return env()
	}
	_ = root.RegisterFlagCompletionFunc("project", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return projectSlugsForCompletion(env), cobra.ShellCompDirectiveNoFileComp
	})

	root.AddCommand(
		newLoginCmd(),
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
		newRestoreCmd(env),
		newForkCmd(envJSON, env, g),
		newLogsCmd(envJSON, env, g),
		newLsCmd(envJSON),
		newPsCmd(envJSON, env, g),
		newExecCmd(env, g),
		newSSHCmd(env, g),
		newEventsCmd(envJSON, env, g),
		newQuestionsCmd(envJSON, env, g),
		newReplyCmd(envJSON, g),
		newNotifyCmd(env),
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

func newLoginCmd() *cobra.Command {
	var noBrowser, browser bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to repose in your browser",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := newEnv("", false, false)
			if err != nil {
				return err
			}
			opts := loginOptions{
				Browser:   browser,
				NoBrowser: noBrowser || os.Getenv(envNoBrowser) == "1",
				Display:   os.Getenv("DISPLAY"),
				GOOS:      goos(),
				GuestEnv:  os.Getenv(envInGuest) == "1",
			}
			return runLogin(cmd.Context(), e.Dir, e.Cfg, e.httpClient, opts)
		},
	}
	cmd.Flags().BoolVar(&browser, "browser", false, "use the loopback browser flow (PKCE) instead of the device code; needs a Logto application with loopback redirect URIs")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "device-code flow (the default since v0.1.2; kept for scripts)")
	return cmd
}

func newLogoutCmd(env func() (*Env, error)) *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Log out",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return runLogout(cmd.Context(), e.Dir, e.Cfg, e.httpClient, purge)
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
		Long: "Create or start this checkout's machine and attach. A new machine gets a copy of the checkout first.\n\n" +
			"PROJECT is the machine of that name, created if there is none: a second machine for this\n" +
			"checkout, or one for a folder with no git remote.",
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
			return runRun(cmd.Context(), e, opts, false)
		},
	}
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "claude|opencode|codex|gemini|pi")
	cmd.Flags().StringVar(&opts.Size, "size", "", "small|large|xl")
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
	cmd.Flags().BoolVar(&opts.StashRemote, "stash-remote", false, "moved to repose sync")
	cmd.Flags().BoolVar(&opts.DiscardRemote, "discard-remote", false, "moved to repose sync")
	_ = cmd.Flags().MarkHidden("stash-remote")
	_ = cmd.Flags().MarkHidden("discard-remote")
	cmd.Flags().BoolVar(&opts.NoSync, "no-sync", false, "do not sync the checkout, even into a new machine")
	cmd.Flags().BoolVarP(&opts.NoAttach, "no-attach", "d", false, "do not attach; with -p, print the window the agent is in")
	cmd.Flags().BoolVar(&opts.NoPersonal, "no-personal", false, "keep your machine.nix (repose config --global) off this machine, from now on")
	addMultiplexerFlag(cmd, &opts.Multiplexer)
	cmd.Flags().BoolVar(&opts.Worktree, "worktree", false, "start the agent in its own git worktree, ~/<slug>-worktree-<N> on branch worktree-<N>")
	cmd.Flags().BoolVar(&opts.Bridge, "bridge", false, "also bridge this laptop's Chrome to the machine while attached (repose browser bridge)")
	cmd.Flags().StringArrayVar(&opts.BridgeAllow, "bridge-allow", nil, "with --bridge: the agents may open only this host in your Chrome (repeatable; *.example.com for subdomains); implies --bridge")
	_ = cmd.RegisterFlagCompletionFunc("agent", cobra.FixedCompletions(agentNames, cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.RegisterFlagCompletionFunc("size", cobra.FixedCompletions([]string{"small", "large", "xl"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func newAttachCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var bridge bool
	var bridgeAllow []string
	var window string
	cmd := &cobra.Command{
		Use:   "attach [PROJECT] [WINDOW]",
		Short: "Attach to a project's tmux session (this checkout's, or PROJECT), or one of its windows",
		Long: "Attaches to PROJECT's tmux session (this checkout's, by default), on its current window.\n" +
			"With WINDOW (or -w), on that window: a tmux window's name or number as `repose ps` shows\n" +
			"it, or on herdr an agent's name.",
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
			project, err := projectFrom(args, g)
			if err != nil {
				return err
			}
			if _, err := parseBridgeAllow(bridgeAllow); err != nil {
				return cobraUsageError{fmt.Errorf("--bridge-allow %w", err)}
			}
			e, err := env()
			if err != nil {
				return err
			}
			return runRun(cmd.Context(), e, RunOptions{ProjectArg: project, Window: window, Bridge: bridge, BridgeAllow: bridgeAllow}, true)
		},
	}
	cmd.Flags().StringVarP(&window, "window", "w", "", "open on window `NAME` (a tmux name or number, or a herdr agent's name)")
	cmd.Flags().BoolVar(&bridge, "bridge", false, "also bridge this laptop's Chrome to the machine while attached (repose browser bridge)")
	cmd.Flags().StringArrayVar(&bridgeAllow, "bridge-allow", nil, "with --bridge: the agents may open only this host in your Chrome (repeatable; *.example.com for subdomains); implies --bridge")
	return cmd
}

// newSyncCmd is `repose sync` (DECISIONS I-302): `repose run --no-attach`
// under its own name, for putting the checkout on the machine without
// attaching.
func newSyncCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var opts RunOptions
	var tempRaw string
	cmd := &cobra.Command{
		Use:               "sync [PROJECT]",
		Short:             "Sync this checkout to its machine, creating or starting it if needed, without attaching",
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
			return runRun(cmd.Context(), e, opts, false)
		},
	}
	cmd.Flags().StringVar(&opts.Size, "size", "", "small|large|xl, for a machine this creates")
	// Before I-603 PROJECT had to exist and --name created; kept hidden
	// for a release.
	cmd.Flags().StringVar(&opts.Name, "name", "", "the project with this name (repose sync NAME)")
	_ = cmd.Flags().MarkHidden("name")
	addTempFlag(cmd, &tempRaw)
	cmd.Flags().BoolVar(&opts.StashRemote, "stash-remote", false, "stash the guest's uncommitted changes before syncing")
	cmd.Flags().BoolVar(&opts.DiscardRemote, "discard-remote", false, "discard the guest's uncommitted changes before syncing")
	addMultiplexerFlag(cmd, &opts.Multiplexer)
	_ = cmd.RegisterFlagCompletionFunc("size", cobra.FixedCompletions([]string{"small", "large", "xl"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func newStartCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:               "start [PROJECT]",
		Short:             "Start a project's machine without syncing (restarts one in error)",
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
			return StartCmd(cmd.Context(), e, project)
		},
	}
}

func newStopCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var noSnapshot bool
	cmd := &cobra.Command{
		Use:               "stop [PROJECT]",
		Short:             "Snapshot and stop a project's machine",
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
			return StopCmd(cmd.Context(), e, project, !noSnapshot)
		},
	}
	cmd.Flags().BoolVar(&noSnapshot, "no-snapshot", false, "stop without taking a snapshot")
	return cmd
}

func newStatusCmd(envJSON func(*cobra.Command) (*Env, error), env func() (*Env, error), g *globalFlags) *cobra.Command {
	var watch bool
	cmd := &cobra.Command{
		Use:               "status [PROJECT]",
		Short:             "Show a project's status",
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
			if !watch {
				return StatusCmd(cmd.Context(), e, project)
			}
			for {
				if err := StatusCmd(cmd.Context(), e, project); err != nil {
					return err
				}
				if err := sleepOrDone(cmd.Context(), 5*time.Second); err != nil {
					return nil
				}
			}
		},
	}
	cmd.Flags().Bool("json", false, "print the Project object as JSON")
	cmd.Flags().BoolVar(&watch, "watch", false, "refresh every 5 seconds")
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
		Short:      "List every project",
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
	cmd.Flags().BoolVar(&all, "all", false, "with --destroyed: every destroyed project, not only the one repose restore NAME picks per name")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "print only the project names, one per line")
	return cmd
}

func newOpenCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var desktop, stop, noBrowser bool
	var localPort int
	cmd := &cobra.Command{
		Use:   "open PORT",
		Short: "Forward a port on the machine to the laptop",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if stop && !desktop {
				return cobraUsageError{fmt.Errorf("--stop goes with --desktop: repose open --desktop --stop (or repose browser --stop)")}
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
				return cobraUsageError{fmt.Errorf("repose open PORT (the machine's desktop is repose browser)")}
			}
			port, err := strconv.Atoi(args[0])
			if err != nil || port < 1 || port > 65535 {
				return cobraUsageError{fmt.Errorf("PORT must be a port number (1-65535), got %q; the project is --project NAME", args[0])}
			}
			return OpenPortCmd(cmd.Context(), e, g.project, port, localPort, noBrowser)
		},
	}
	cmd.Flags().BoolVar(&desktop, "desktop", false, "the old name of repose browser")
	cmd.Flags().BoolVar(&stop, "stop", false, "with --desktop: the old name of repose browser --stop")
	_ = cmd.Flags().MarkHidden("desktop")
	_ = cmd.Flags().MarkHidden("stop")
	cmd.Flags().IntVar(&localPort, "local-port", 0, "local port to bind (defaults to PORT)")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the URL instead of opening a browser")
	return cmd
}

// newBrowserCmd is `repose browser [PROJECT]` (DECISIONS I-292): watch the
// agent's browser and take it over, in one command; `repose browser
// bridge` (I-296) is the other direction, the laptop's Chrome for the
// agents.
func newBrowserCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var stop, noOpen bool
	root := &cobra.Command{
		Use:   "browser [PROJECT]",
		Short: "Watch the agent's browser on the machine and take it over; bridge: your Chrome for the agents",
		Long: `Starts the machine's desktop viewer if needed, forwards it to the laptop
in the background (port 6080, or the next free one) and opens the viewer
page. The page shows the browser the agent drives, sized to your tab; click
and type in it to log in, solve a captcha or approve a passkey. The
password is in the link after the #. The view sleeps after 30 idle minutes; opening the page wakes it.`,
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
	root.Flags().BoolVar(&stop, "stop", false, "stop the viewer on the machine and the forward on the laptop")
	root.Flags().BoolVar(&noOpen, "no-open", false, "print the link instead of opening a browser")
	// `repose browser bridge` (DECISIONS I-296): the agents on the machine
	// browse in this laptop's Chrome for as long as the command runs.
	var opts BridgeOptions
	bridge := &cobra.Command{
		Use:   "bridge [PROJECT]",
		Short: "Let the agents on a machine browse in this laptop's Chrome, until Ctrl-C",
		Long: "Let the agents on a machine browse in this laptop's Chrome, with your logins and extensions, until Ctrl-C.\n\n" +
			"Chrome 144 or newer with remote debugging turned on at chrome://inspect/#remote-debugging; the command\n" +
			"opens that page and waits when it is off. Chrome asks you to allow each connection. Nothing on the\n" +
			"machine changes: its browser tools reach your Chrome through the SSH connection for as long as this runs.\n" +
			"Needs the machine running. --allow HOST keeps the agents to those hosts; each page they open is listed here.",
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
	bridge.Flags().StringVar(&opts.CDP, "cdp", "", "bridge this DevTools server instead (a browser started with --remote-debugging-port), e.g. http://127.0.0.1:9222")
	bridge.Flags().StringVar(&opts.UserDataDir, "user-data-dir", "", "the profile directory of a Chrome that is not Google Chrome's default one")
	bridge.Flags().BoolVar(&opts.NoBrowser, "no-browser", false, "don't open chrome://inspect when remote debugging is off")
	bridge.Flags().StringArrayVar(&opts.Allow, "allow", nil, "the agents may open only this host in your Chrome (repeatable; *.example.com is example.com and its subdomains)")
	root.AddCommand(bridge)
	return root
}

func newSecretsCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	root := &cobra.Command{Use: "secrets", Short: "Manage project secrets"}
	var fromFile string
	var fromEnv bool
	set := &cobra.Command{
		Use:   "set NAME",
		Short: "Set a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
	set.Flags().StringVar(&fromFile, "from-file", "", "read the value from a file")
	set.Flags().BoolVar(&fromEnv, "from-env", false, "read the value from $NAME")

	list := &cobra.Command{
		Use:               "list [PROJECT]",
		Aliases:           []string{"ls"},
		Short:             "List secret names",
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
	rm := &cobra.Command{
		Use:   "rm NAME",
		Short: "Remove a secret",
		Args:  cobra.ExactArgs(1),
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
		Short: "Choose which of your laptop's logins and .env files repose run copies to the machine",
		Long: "Choose which of your laptop's logins and files repose run copies to the machine: " +
			strings.Join(loginNames(), ", ") + ". With no flags it shows a list to toggle in a terminal " +
			"and prints the list otherwise. --off NAME... leaves them on your laptop, --on NAME... copies " +
			"them again, --reset drops the list. Without --project the list is for every project; with " +
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
	choose.Flags().BoolVar(&chooseOff, "off", false, "leave the NAMEs on your laptop, and remove the copies an earlier run left on the machine")
	choose.Flags().BoolVar(&chooseReset, "reset", false, "drop the list: a project follows the list for every project; without --project every login is copied")
	root.AddCommand(set, list, rm, newSecretsImportCmd(env, g), choose)
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
	default:
		if !isTerminal(os.Stdin) {
			return nil, exitf(ExitUsage, "No terminal to type %s's value into; use --from-file PATH or --from-env.", name)
		}
		_, _ = fmt.Fprintf(os.Stderr, "Value for %s: ", name)
		v, err := readHiddenLine()
		if err != nil {
			return nil, err
		}
		return v, nil
	}
}

func newConfigCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	root := &cobra.Command{Use: "config", Short: "Manage the machine's Nix configuration",
		Long: `Manage the machine's Nix configuration.

Without --global, each command acts on the project's configuration
(repose.nix). With --global, it acts on your machine.nix instead: a
home-manager module every machine of your account gets, kept at
~/.config/repose/machine.nix and on your account. ` + "`repose run`" + ` pushes
that file when it changed; ` + "`repose run --no-personal`" + ` keeps it off one machine.`,
		Example: "  repose config --global add ripgrep fd\n  repose config --global edit\n  repose config --global show"}
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
		Short:             "Print the current fragment (with --global, your machine.nix)",
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
	show.Flags().BoolVar(&showRevisions, "revisions", false, "list revisions instead")

	edit := &cobra.Command{
		Use:               "edit [PROJECT]",
		Short:             "Edit the fragment in $EDITOR (with --global, ~/.config/repose/machine.nix)",
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
	apply := &cobra.Command{
		Use:   "apply [PATH]",
		Short: "Apply a fragment file (default ./repose.nix; with --global, push ~/.config/repose/machine.nix)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := globalEnv()
			if err != nil {
				return err
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
	add := &cobra.Command{
		Use:   "add <package>...",
		Short: "Add catalog entries or any nixpkgs package to the machine",
		Long: `Add packages to the project's menu and rebuild the machine.

A name the catalog has (bun, postgresql, portless, ... as the dashboard's
menu lists them) adds that catalog entry, services included. Any other name
is a nixpkgs attribute: gcc, air, nodejs_22, python312Packages.black,
nodePackages.typescript. Find names at https://search.nixos.org/packages.

A project whose fragment was edited by hand has no menu; add packages there
with ` + "`repose config edit`" + `.

With --global, the names go into the home.packages list of your
machine.nix, every machine of your account gets them, and catalog
services are not available.`,
		Example: "  repose config add gcc air\n  repose config add postgresql python312Packages.black\n  repose config --global add ripgrep",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := globalEnv()
			if err != nil {
				return err
			}
			if global {
				return GlobalPackagesCmd(cmd.Context(), e, args, true)
			}
			return ConfigAddCmd(cmd.Context(), e, g.project, args)
		},
	}
	remove := &cobra.Command{
		Use:     "remove <package>...",
		Aliases: []string{"rm"},
		Short:   "Remove packages added with config add",
		Example: "  repose config remove air\n  repose config --global remove ripgrep",
		Args:    cobra.MinimumNArgs(1),
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
	root.AddCommand(show, edit, apply, add, remove)
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
	root := &cobra.Command{Use: "snapshots", Short: "Manage snapshots"}
	var asNew string
	var yes bool
	var quiet bool
	list := &cobra.Command{
		Use:               "list [PROJECT]",
		Aliases:           []string{"ls"},
		Short:             "List snapshots",
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
		Short:             "Take a manual snapshot",
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
	restore := &cobra.Command{
		Use:               "restore [PROJECT] SNAPSHOT_ID",
		Short:             "Restore a snapshot",
		Args:              snapshotRestoreArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := projectFrom(args[:len(args)-1], g)
			if err != nil {
				return err
			}
			e, err := env()
			if err != nil {
				return err
			}
			var confirm func() (bool, error)
			if !yes {
				confirm = func() (bool, error) {
					return askYesNo("Restore over the current volume? Anything since the snapshot is lost. [y/N] ", false, "restoring in place")
				}
			}
			return SnapshotsRestoreCmd(cmd.Context(), e, project, args[len(args)-1], asNew, confirm)
		},
	}
	restore.Flags().StringVar(&asNew, "as-new", "", "restore into a new project instead of replacing this one")
	restore.Flags().BoolVar(&yes, "yes", false, "skip the confirmation")
	root.AddCommand(list, create, restore)
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
	cmd.Flags().StringVar(raw, "temp", "", "a new temporary machine, destroyed with no snapshot after DURATION (10m to 24h, default 24h)")
	cmd.Flags().Lookup("temp").NoOptDefVal = tempBare
}

// newKeepCmd is `repose keep`: a temporary machine becomes a normal one
// (DECISIONS I-347).
func newKeepCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:               "keep [PROJECT]",
		Short:             "Keep a temporary machine: it is no longer destroyed when its time runs out",
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
			return KeepCmd(cmd.Context(), e, project)
		},
	}
}

// newRmCmd is `repose rm`, which destroys a project. It was `repose
// destroy` until DECISIONS I-273; that name stays an alias.
func newRmCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var yes, wait bool
	cmd := &cobra.Command{
		Use:               "rm [PROJECT]",
		Aliases:           []string{"destroy"},
		SuggestFor:        []string{"delete", "remove"},
		Short:             "Destroy a project (a final snapshot is kept for 30 days)",
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
			var confirm func(string) (bool, error)
			if !yes {
				confirm = func(prompt string) (bool, error) { return askYesNo(prompt, false, "destroying") }
			}
			return DestroyCmd(cmd.Context(), e, project, yes, wait, confirm)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation")
	cmd.Flags().BoolVar(&wait, "wait", false, "wait until the destroy is done and report how it ended (for scripts)")
	return cmd
}

func newRestoreCmd(env func() (*Env, error)) *cobra.Command {
	var as, snapshot string
	cmd := &cobra.Command{
		Use:   "restore [NAME]",
		Short: "Bring back a destroyed project from its newest snapshot (kept 30 days)",
		Long: "Restores NAME, a project you destroyed in the last 30 days (or one that still exists), from its\n" +
			"newest snapshot into a new project called NAME, or --as NEW-NAME when that name is in use.\n" +
			"Without NAME, inside a checkout, it restores the destroyed project with this checkout's remote.\n" +
			"`repose ls --destroyed` lists what can be restored. `repose snapshots restore` still\n" +
			"restores a given snapshot over a stopped project in place.",
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
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			var ask func(string) (string, error)
			if isTerminal(os.Stdin) {
				ask = func(prompt string) (string, error) {
					_, _ = fmt.Fprint(os.Stderr, prompt)
					line, err := readLine()
					if err != nil && line == "" {
						_, _ = fmt.Fprintln(os.Stderr)
						return "", nil
					}
					return line, nil
				}
			}
			return RestoreCmd(cmd.Context(), e, name, as, snapshot, ask)
		},
	}
	cmd.Flags().StringVar(&as, "as", "", "name for the restored project (default: its old name)")
	cmd.Flags().StringVar(&snapshot, "snapshot", "", "restore this snapshot instead of the newest (`repose snapshots list ID` lists them)")
	return cmd
}

func newForkCmd(envJSON func(*cobra.Command) (*Env, error), env func() (*Env, error), g *globalFlags) *cobra.Command {
	opts := ForkOptions{Count: 1}
	cmd := &cobra.Command{
		Use:   "fork [PROJECT]",
		Short: "Snapshot a project and start copies of it as new projects, one machine each",
		Long: "Snapshots PROJECT (this checkout's, by default) and restores the snapshot into --count new\n" +
			"projects, NAME-1, NAME-2, ... (NAME defaults to PROJECT-fork), each running on its own machine\n" +
			"with the same files, configuration and secrets. PROJECT keeps running and stays the project\n" +
			"`repose run` uses in its checkout. With --prompt, the agent starts in every fork with that prompt.\n" +
			"Each fork is a project: it counts toward the 100 projects an account can have, toward your\n" +
			"plan's disk by what it holds (at first what PROJECT holds), and toward the plan's memory while\n" +
			"it runs.",
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
			e, err := envJSON(cmd)
			if err != nil {
				return err
			}
			opts.ProjectArg = project
			return ForkCmd(cmd.Context(), e, opts)
		},
	}
	cmd.Flags().IntVarP(&opts.Count, "count", "n", 1, "how many forks (1 to 10)")
	cmd.Flags().StringVar(&opts.Name, "name", "", "name the forks NAME-1, NAME-2, ... (default: PROJECT-fork)")
	cmd.Flags().StringVar(&opts.Size, "size", "", "small|large|xl for the forks (default: PROJECT's)")
	cmd.Flags().StringVar(&opts.SnapshotID, "snapshot", "", "fork from this snapshot of PROJECT instead of taking one now")
	cmd.Flags().StringVar(&opts.Prompt, "prompt", "", "start the agent in every fork with this prompt")
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "with --prompt: claude|opencode|codex|gemini|pi (default: PROJECT's)")
	cmd.Flags().Bool("json", false, "print the forks as JSON")
	_ = cmd.RegisterFlagCompletionFunc("agent", cobra.FixedCompletions(agentNames, cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.RegisterFlagCompletionFunc("size", cobra.FixedCompletions([]string{"small", "large", "xl"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func newResizeCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var size string
	var yes bool
	cmd := &cobra.Command{
		Use:   "resize [PROJECT] [DISK] [--size small|large|xl]",
		Short: "Grow the project's disk (e.g. 80G), or change its size with --size",
		Long: "With DISK, grows the project's disk (e.g. 80G); disks can't shrink.\n" +
			"With --size, changes the project's size: small, large or xl. A stopped project starts at the\n" +
			"new size; a running one is stopped, changed and started again, after a\n" +
			"confirmation that --yes skips.\n" +
			"PROJECT defaults to this checkout's project; one argument that reads as a size is DISK.",
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
				confirm = func(prompt string) (bool, error) { return askYesNo(prompt, false, "restarting the machine") }
			}
			return ResizeClassCmd(cmd.Context(), e, project, size, confirm)
		},
	}
	cmd.Flags().StringVar(&size, "size", "", "small|large|xl: change the project's size")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "with --size on a running project: stop and start it without asking")
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
		if _, err := parseSize(args[0]); err == nil {
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
	cmd := &cobra.Command{
		Use:               "logs [PROJECT]",
		Short:             "Show a project's logs",
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
			return LogsCmd(cmd.Context(), e, project, kind, since, follow, nil)
		},
	}
	cmd.Flags().Bool("json", false, "print each line as JSON")
	cmd.Flags().StringVar(&kind, "kind", "", "console|build|ops")
	cmd.Flags().StringVar(&since, "since", "", "e.g. 1h")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "poll for new lines every 2s")
	_ = cmd.RegisterFlagCompletionFunc("kind", cobra.FixedCompletions([]string{"console", "build", "ops"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func newEventsCmd(envJSON func(*cobra.Command) (*Env, error), env func() (*Env, error), g *globalFlags) *cobra.Command {
	var since string
	var follow bool
	cmd := &cobra.Command{
		Use:               "events [PROJECT]",
		Short:             "Show a project's events",
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
	cmd.Flags().Bool("json", false, "print each event as JSON")
	cmd.Flags().StringVar(&since, "since", "24h", "e.g. 24h")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "poll for new events every 10s")
	return cmd
}

func newQuestionsCmd(envJSON func(*cobra.Command) (*Env, error), env func() (*Env, error), g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "questions [PROJECT]",
		Short:             "List the questions agents are waiting on you to answer",
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
			return QuestionsCmd(cmd.Context(), e, project)
		},
	}
	cmd.Flags().Bool("json", false, "print the questions as JSON")
	return cmd
}

func newReplyCmd(envJSON func(*cobra.Command) (*Env, error), g *globalFlags) *cobra.Command {
	var question string
	cmd := &cobra.Command{
		Use:   "reply [PROJECT] [--] [ANSWER...]",
		Short: "Answer a question an agent asked with repose-ask",
		Long: "Answers a waiting question. A first word that names one of your projects is PROJECT; put --\n" +
			"before an answer that starts with a project's name.",
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
			return replyCmd(cmd.Context(), e, args, project, question, os.Stdin, isTerminal(os.Stdin), mayName)
		},
	}
	cmd.Flags().Bool("json", false, "print the answered question as JSON")
	cmd.Flags().StringVar(&question, "question", "", "the question's id (or its last characters, as `repose questions` shows)")
	return cmd
}

func newNotifyCmd(env func() (*Env, error)) *cobra.Command {
	root := &cobra.Command{Use: "notify", Short: "Notification settings"}
	var emailFlag, ntfyFlag string
	set := &cobra.Command{
		Use:   "set",
		Short: "Change notification settings",
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
	set.Flags().StringVar(&emailFlag, "email", "", "on|off")
	set.Flags().StringVar(&ntfyFlag, "ntfy", "", "a URL, or none")
	test := &cobra.Command{
		Use:   "test",
		Short: "Send a test notification on every configured channel",
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
		Short: "Print the version",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("repose %s (herakraft)\n", version)
			return nil
		},
	}
}

// newMCPCmd is `repose mcp`: the MCP servers of the agents on a machine.
func newMCPCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	root := &cobra.Command{Use: "mcp", Short: "MCP servers for the agents on a machine"}
	// `repose mcp forward` (DECISIONS I-557). Names only: the project comes
	// from the folder or --project, since names and a PROJECT cannot share
	// positions without a guess (an exception to I-155).
	var remove bool
	forward := &cobra.Command{
		Use:   "forward NAME... [-- COMMAND [ARG...]]",
		Short: "Let the agents on a machine use MCP servers that run on this laptop, until Ctrl-C",
		Long: `Let the agents on a machine use MCP servers that run on this laptop, until Ctrl-C.

NAME is a server in your Claude Code, Claude Desktop, Codex or Gemini CLI config
on this laptop, or the command after --. It runs here, with your apps, files and
tokens; each agent session on the machine gets its own copy over SSH. Agents
started after the first forward list NAME; while nothing forwards it, its tools
answer that your laptop isn't connected. To forward whenever you're attached,
add NAME to [mcp] forward in config.toml. The project is the folder's, or
--project's. Needs the machine running.`,
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
				return cobraUsageError{fmt.Errorf("--remove takes names only, not a command after --")}
			}
			e, err := env()
			if err != nil {
				return err
			}
			return MCPForwardCmd(cmd.Context(), e, g.project, opts)
		},
	}
	forward.Flags().BoolVar(&remove, "remove", false, "take NAME off the machine's agents")
	root.AddCommand(forward)
	// `repose mcp list` (DECISIONS I-558): what each agent on the machine
	// has, read from its configs over SSH; starts no server.
	list := &cobra.Command{
		Use:     "list [PROJECT]",
		Aliases: []string{"ls"},
		Short:   "List the MCP servers the agents on a machine have, and those your laptop kept",
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
	root.AddCommand(list)
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
// naming --yes rather than a silent default.
func askYesNo(prompt string, defaultYes bool, what string) (bool, error) {
	if !isTerminal(os.Stdin) {
		return false, exitf(ExitUsage, "No terminal to confirm %s on; pass --yes.", what)
	}
	_, _ = fmt.Fprint(os.Stderr, prompt)
	line, err := readLine()
	if err != nil && line == "" {
		_, _ = fmt.Fprintln(os.Stderr)
		return false, nil
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

func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	s = strings.TrimSuffix(s, "B")
	s = strings.TrimSuffix(s, "I")
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "T"):
		mult = 1 << 40
		s = strings.TrimSuffix(s, "T")
	case strings.HasSuffix(s, "G"):
		mult = 1 << 30
		s = strings.TrimSuffix(s, "G")
	case strings.HasSuffix(s, "M"):
		mult = 1 << 20
		s = strings.TrimSuffix(s, "M")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%q is not a size like 80G", s)
	}
	return n * mult, nil
}
