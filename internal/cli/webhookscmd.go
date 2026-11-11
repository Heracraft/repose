package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

// errStartBoth is the refusal for `--start --no-start`, which says two
// opposite things about the same route.
var errStartBoth = errors.New("--start and --no-start say opposite things; pass one")

// errQuietAndJSON is the refusal for `-q --json`, two different outputs.
var errQuietAndJSON = errors.New("-q and --json are two different outputs; pass one")

// newWebhooksCmd builds `repose webhooks` (DECISIONS I-1110). The noun is
// `webhooks` rather than `hooks`: agent hooks own that word already
// (`repose-hook`, the edge's /hooks, Claude Code's `hooks` key).
func newWebhooksCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	root := &cobra.Command{
		Use:   "webhooks",
		Short: "Add, list, rotate or remove a project's public webhook URLs",
		Long: "A webhook route is a public HTTPS URL a provider can POST to, forwarded to a\n" +
			"port and path inside the machine. Each route has its own unguessable token.\n" +
			"Alone, list them.\n\n" +
			"A frozen machine wakes for a delivery. A stopped one answers 503 unless the\n" +
			"route was added with --start.",
		Example:    "  repose webhooks add stripe 3000/api/webhooks/stripe\n  repose webhooks url stripe",
		SuggestFor: []string{"hook", "hooks", "webhook"},
	}

	var addStart, addNoStart bool
	add := &cobra.Command{
		Use:   "add NAME TARGET",
		Short: "Add a route, or change an existing one's target",
		Long: "Add a route called NAME that forwards to TARGET, which is PORT or PORT/PATH\n" +
			"inside the machine. It prints the URL and nothing else, so it pipes.\n\n" +
			"Adding a name that exists changes its target and keeps its token, so a URL\n" +
			"already pasted into a provider's dashboard goes on working.",
		Example: "  repose webhooks add stripe 3000/api/webhooks/stripe\n" +
			"  repose webhooks add github 3000/api/webhooks/github --start",
		Args: argsNOwn(2, 2, "a NAME and a TARGET", nil),
		RunE: func(cmd *cobra.Command, args []string) error {
			if addStart && addNoStart {
				return cobraUsageError{errStartBoth}
			}
			var start *bool
			if addStart || addNoStart {
				v := addStart
				start = &v
			}
			e, err := env()
			if err != nil {
				return err
			}
			return WebhooksAddCmd(cmd.Context(), e, g.project, args[0], args[1], start)
		},
	}
	add.Flags().BoolVar(&addStart, "start", false, "let a delivery start the machine when it is stopped, at most once every 10 minutes")
	add.Flags().BoolVar(&addNoStart, "no-start", false, "answer 503 while the machine is stopped (the default)")
	add.Flags().BoolVar(&g.json, "json", false, "print every field of the route as JSON")

	var listQuiet bool
	list := &cobra.Command{
		Use:               "list [PROJECT]",
		Aliases:           []string{"ls"},
		Short:             "List a project's webhook routes",
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
			return WebhooksListCmd(cmd.Context(), e, project)
		},
	}
	list.Flags().BoolVar(&g.json, "json", false, "print every field of every route as JSON")
	list.Flags().BoolVarP(&listQuiet, "quiet", "q", false, "print the names alone")

	url := &cobra.Command{
		Use:               "url NAME",
		Short:             "Print a route's URL",
		Args:              argsN(1, 1, "one NAME"),
		ValidArgsFunction: completeWebhookName(env, g),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return WebhooksURLCmd(cmd.Context(), e, g.project, args[0])
		},
	}
	url.Flags().BoolVar(&g.json, "json", false, "print every field of the route as JSON")

	rotate := &cobra.Command{
		Use:   "rotate NAME",
		Short: "Replace a route's token and print the new URL",
		Long: "Replace NAME's token. The old URL answers 410 from that moment, for 30 days.\n" +
			"For a change with no gap, add a second route to the same target, paste its\n" +
			"URL into the provider, then remove the first.",
		Args:              argsN(1, 1, "one NAME"),
		ValidArgsFunction: completeWebhookName(env, g),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return WebhooksRotateCmd(cmd.Context(), e, g.project, args[0])
		},
	}
	rotate.Flags().BoolVar(&g.json, "json", false, "print every field of the route as JSON")

	rm := &cobra.Command{
		Use:               "rm NAME...",
		Aliases:           []string{"remove"},
		SuggestFor:        []string{"delete"},
		Short:             "Remove routes",
		Long:              "Remove each NAME. Its URL answers 410 from that moment, for 30 days.",
		Args:              argsN(1, -1, "one or more NAMEs"),
		ValidArgsFunction: completeWebhookName(env, g),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return WebhooksRmCmd(cmd.Context(), e, g.project, args)
		},
	}

	root.AddCommand(add, list, url, rotate, rm)
	bareRuns(root, list)
	return root
}

// completeWebhookName completes a route name from the project's routes.
func completeWebhookName(env func() (*Env, error), g *globalFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return webhookNamesForCompletion(env, g.project), cobra.ShellCompDirectiveNoFileComp
	}
}
