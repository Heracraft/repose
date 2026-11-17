package cli

import (
	"time"

	"github.com/spf13/cobra"
)

// newTriggersCmd builds `repose triggers` (DECISIONS I-1248..I-1262): a
// webhook delivery or a time starts an agent session on the machine. The
// noun is plural, as `webhooks`, `secrets` and `snapshots` are.
func newTriggersCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	root := &cobra.Command{
		Use:   "triggers",
		Short: "Start an agent when a webhook arrives or at a time you choose",
		Long: "A trigger binds an event to an agent session: the event is a delivery to one\n" +
			"of the project's webhook routes or a time, and the session is one agent in its\n" +
			"own window, started with a prompt you wrote and given the event as a file.\n" +
			"Alone, list them.\n\n" +
			"The prompt is a file in the machine and the payload is a file in the machine.\n" +
			"repose stores neither; the row holds a path.",
		Example: "  repose triggers add apply --route recruiting -p 'Read $REPOSE_PAYLOAD'\n" +
			"  repose triggers add triage --every 'weekdays 07:00' --prompt-file triage.md\n" +
			"  repose triggers runs",
		SuggestFor: []string{"trigger", "schedule", "cron"},
	}

	var (
		addRoute, addEvery       string
		addPrompt, addPromptFile string
		addWorkdir, addAgent     string
		addGate, addDedupe       string
		addTimeout               time.Duration
		addConcurrency           int
		addNoNotify              bool
		addStart, addNoStart     bool
		addStopAfter, addCatchUp bool
	)
	add := &cobra.Command{
		Use:   "add NAME",
		Short: "Add a trigger, or change one that exists",
		Long: "Add a trigger called NAME. --route NAME fires it on a delivery to that webhook\n" +
			"route, which is created and pointed at the machine if it does not exist;\n" +
			"--every SPEC fires it on a schedule (`repose help triggers` has the six shapes).\n\n" +
			"With --route it prints the URL and nothing else, so it pipes. With --every it\n" +
			"prints the spec, the zone it stored and the next fire.\n\n" +
			"Adding a name that exists changes the flags you pass and leaves the rest, and\n" +
			"keeps the route's token, so a URL already in a provider's dashboard goes on\n" +
			"working.",
		Example: "  repose triggers add apply --route recruiting --prompt-file apply.md\n" +
			"  repose triggers add apply --gate verify.sh --dedupe header:X-GitHub-Delivery\n" +
			"  repose triggers add triage --every 'weekdays 07:00' -p 'triage new issues'\n" +
			"  repose triggers add sweep --every 'daily 03:00' --start --stop-after",
		Args:              argsNOwn(1, 1, "one NAME", nil),
		ValidArgsFunction: completeTriggerName(env, g),
		RunE: func(cmd *cobra.Command, args []string) error {
			if addStart && addNoStart {
				return cobraUsageError{errStartBoth}
			}
			o := TriggerAddOptions{Route: addRoute, Every: addEvery,
				Prompt: addPrompt, PromptFile: addPromptFile}
			if cmd.Flags().Changed("workdir") {
				o.Workdir = &addWorkdir
			}
			if cmd.Flags().Changed("agent") {
				o.Agent = &addAgent
			}
			if cmd.Flags().Changed("gate") {
				o.Gate = &addGate
			}
			if cmd.Flags().Changed("dedupe") {
				o.Dedupe = &addDedupe
			}
			if cmd.Flags().Changed("timeout") {
				o.Timeout = &addTimeout
			}
			if cmd.Flags().Changed("concurrency") {
				o.Concurrency = &addConcurrency
			}
			if cmd.Flags().Changed("no-notify") {
				o.NoNotify = &addNoNotify
			}
			if addStart || addNoStart {
				v := addStart
				o.Start = &v
			}
			if cmd.Flags().Changed("stop-after") {
				o.StopAfter = &addStopAfter
			}
			if cmd.Flags().Changed("catch-up") {
				o.CatchUp = &addCatchUp
			}
			e, err := env()
			if err != nil {
				return err
			}
			return TriggersAddCmd(cmd.Context(), e, g.project, args[0], o)
		},
	}
	add.Flags().StringVar(&addRoute, "route", "", "fire on a delivery to this webhook `ROUTE`, which is made if it is not there")
	add.Flags().StringVar(&addEvery, "every", "", "fire on a `SPEC` schedule: every 15m, daily 03:00, weekdays 07:00, weekends 10:00, mon,thu 18:30, monthly 1 09:00, with an optional zone (daily 03:00 UTC)")
	add.Flags().StringVarP(&addPrompt, "prompt", "p", "", "the prompt as `TEXT`, written into the machine as .repose/triggers/NAME.md")
	add.Flags().StringVar(&addPromptFile, "prompt-file", "", "the prompt as a `PATH` in the machine (default .repose/triggers/NAME.md)")
	add.Flags().StringVar(&addWorkdir, "workdir", "", "the session's `DIR`: checkout, worktree-N, ~/PATH or /PATH")
	add.Flags().StringVar(&addAgent, "agent", "", "the `AGENT` to start: claude, codex, opencode, gemini or pi")
	add.Flags().DurationVar(&addTimeout, "timeout", time.Hour, "kill the window when the run has worked this long, at most 24h")
	add.Flags().IntVar(&addConcurrency, "concurrency", 1, "how many runs of this trigger may work at once (1 to 3)")
	add.Flags().StringVar(&addGate, "gate", "", "run `CMD` with the payload on stdin; a non-zero exit skips the run")
	add.Flags().StringVar(&addDedupe, "dedupe", "", "skip an event whose `KEY` was seen in 24 hours: header:NAME or json:FIELD")
	add.Flags().BoolVar(&addNoNotify, "no-notify", false, "do not notify when a run fails")
	add.Flags().BoolVar(&addStart, "start", false, "let a fire start the machine when it is stopped, at most once every 10 minutes")
	add.Flags().BoolVar(&addNoStart, "no-start", false, "skip the run while the machine is stopped (the default)")
	add.Flags().BoolVar(&addStopAfter, "stop-after", false, "stop the machine when the run ends, if this trigger started it")
	add.Flags().BoolVar(&addCatchUp, "catch-up", false, "run one missed fire when the machine comes back")
	add.Flags().BoolVar(&g.json, "json", false, "print every field of the trigger as JSON")

	var listQuiet bool
	list := &cobra.Command{
		Use:               "list [PROJECT]",
		Aliases:           []string{"ls"},
		Short:             "List a project's triggers",
		Args:              projectArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			if listQuiet && g.json {
				return cobraUsageError{errQuietAndJSON}
			}
			project, err := projectFrom(args, g)
			if err != nil {
				return err
			}
			e, err := env()
			if err != nil {
				return err
			}
			e.Quiet = listQuiet
			return TriggersListCmd(cmd.Context(), e, project, listQuiet)
		},
	}
	list.Flags().BoolVar(&g.json, "json", false, "print every field of every trigger as JSON")
	list.Flags().BoolVarP(&listQuiet, "quiet", "q", false, "print the names alone")

	var (
		runsLimit int
		runsQuiet bool
	)
	runs := &cobra.Command{
		Use:   "runs [NAME]",
		Short: "List what the triggers did, newest first",
		Long: "Every run and how it ended: done with the agent's own summary, failed, timeout,\n" +
			"or skipped with the reason. NAME keeps one trigger's runs.\n\n" +
			"A machine that was stopped for a week shows one row an hour with the number of\n" +
			"events it turned away, not a row for each.",
		Args:              argsN(0, 1, "no more than one NAME"),
		ValidArgsFunction: completeTriggerName(env, g),
		RunE: func(cmd *cobra.Command, args []string) error {
			if runsQuiet && g.json {
				return cobraUsageError{errQuietAndJSON}
			}
			var name string
			if len(args) == 1 {
				name = args[0]
			}
			e, err := env()
			if err != nil {
				return err
			}
			e.Quiet = runsQuiet
			return TriggersRunsCmd(cmd.Context(), e, g.project, name, runsLimit, runsQuiet)
		},
	}
	runs.Flags().IntVarP(&runsLimit, "tail", "n", 0, "how many runs to print (1 to 200, default 20)")
	runs.Flags().BoolVar(&g.json, "json", false, "print every field of every run as JSON")
	runs.Flags().BoolVarP(&runsQuiet, "quiet", "q", false, "print the run ids alone")

	var (
		firePayload string
		fireRun     string
		fireWait    bool
		fireWaitFor time.Duration
		fireDryRun  bool
	)
	fire := &cobra.Command{
		Use:   "fire NAME",
		Short: "Run a trigger now",
		Long: "Start NAME's session now, as a delivery or its schedule would. A trigger that\n" +
			"is off still fires this way: off shuts the doors, and this is you asking.\n\n" +
			"--dry-run takes the admission decision and runs the gate and starts no agent,\n" +
			"which is how a gate gets written.",
		Example: "  repose triggers fire apply\n" +
			"  repose triggers fire apply --payload ./event.json --wait\n" +
			"  repose triggers fire apply --run 019b2c71 --dry-run",
		Args:              argsN(1, 1, "one NAME"),
		ValidArgsFunction: completeTriggerName(env, g),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return TriggersFireCmd(cmd.Context(), e, g.project, args[0], TriggerFireOptions{
				Payload: firePayload, Run: fireRun, Wait: fireWait,
				WaitFor: fireWaitFor, DryRun: fireDryRun,
			})
		},
	}
	fire.Flags().StringVar(&firePayload, "payload", "", "hand the session this `FILE` as the event (- for stdin)")
	fire.Flags().StringVar(&fireRun, "run", "", "replay the payload of an earlier `RUN`")
	fire.Flags().BoolVar(&fireWait, "wait", false, "wait for the run to end and print the outcome")
	fire.Flags().DurationVar(&fireWaitFor, "wait-for", 5*time.Minute, "how long --wait follows the run")
	fire.Flags().BoolVar(&fireDryRun, "dry-run", false, "run the gate alone and start no agent")
	fire.Flags().BoolVar(&g.json, "json", false, "print every field of the run as JSON")

	on := &cobra.Command{
		Use:               "on NAME",
		Short:             "Let a trigger fire again",
		Long:              "Let NAME fire again, and arm its schedule from now: a trigger that was off for\na week fires at its next proper time, not at once.",
		Args:              argsN(1, 1, "one NAME"),
		ValidArgsFunction: completeTriggerName(env, g),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return TriggersOnOffCmd(cmd.Context(), e, g.project, args[0], true)
		},
	}
	off := &cobra.Command{
		Use:               "off NAME",
		Short:             "Keep a trigger from firing",
		Long:              "Keep NAME from firing. Its row, its route and its prompt file stay as they are,\nand `repose triggers fire` still runs it.",
		Args:              argsN(1, 1, "one NAME"),
		ValidArgsFunction: completeTriggerName(env, g),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return TriggersOnOffCmd(cmd.Context(), e, g.project, args[0], false)
		},
	}
	on.Flags().BoolVar(&g.json, "json", false, "print every field of the trigger as JSON")
	off.Flags().BoolVar(&g.json, "json", false, "print every field of the trigger as JSON")

	rm := &cobra.Command{
		Use:        "rm NAME...",
		Aliases:    []string{"remove"},
		SuggestFor: []string{"delete"},
		Short:      "Remove triggers",
		Long: "Remove each NAME. A route the trigger made goes with it, unless you made that\n" +
			"route yourself or another trigger uses it; the runs stay until they age out.",
		Args:              argsN(1, -1, "one or more NAMEs"),
		ValidArgsFunction: completeTriggerName(env, g),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return TriggersRmCmd(cmd.Context(), e, g.project, args)
		},
	}

	root.AddCommand(add, list, runs, fire, on, off, rm)
	// The bare noun lists, as `repose webhooks` and `repose secrets` do.
	root.Args = projectArgs
	root.ValidArgsFunction = completeProject(env)
	root.RunE = func(cmd *cobra.Command, args []string) error {
		project, err := projectFrom(args, g)
		if err != nil {
			return err
		}
		e, err := env()
		if err != nil {
			return err
		}
		return TriggersListCmd(cmd.Context(), e, project, false)
	}
	return root
}

// completeTriggerName completes a trigger's name from the project's own.
func completeTriggerName(env func() (*Env, error), g *globalFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		e, err := env()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		project, err := requireProject(cmd.Context(), e, g.project)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ts, err := e.Client.ListTriggers(cmd.Context(), project.ID)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names := make([]string, 0, len(ts))
		for _, t := range ts {
			names = append(names, t.Name)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}
