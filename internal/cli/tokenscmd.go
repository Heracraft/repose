package cli

import (
	"github.com/spf13/cobra"
)

// newTokensCmd is `repose tokens` (DECISIONS I-1314): the API tokens an
// agent or a script calls the api with. `tokens`, not `keys`: a key
// unlocks, a token stands in for a login, and the api's own words are
// `Authorization: Bearer`.
func newTokensCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	root := &cobra.Command{
		Use:   "tokens",
		Short: "Make, list or remove API tokens for agents and scripts",
		Long: "An API token lets an agent or a script call repose's api as you, for the\n" +
			"areas its scopes name, for one project or all, until it expires. Set it as\n" +
			"REPOSE_TOKEN and this CLI uses it in place of your login; or send it as\n" +
			"`Authorization: Bearer` to the api. Alone, list them.\n\n" +
			"No token reaches your account, billing, SSH, the browser view or other\n" +
			"tokens.",
		Example: "  repose tokens create ci --scope read,triggers\n" +
			"  repose tokens create bot --scope read,projects --project todo-app\n" +
			"  REPOSE_TOKEN=$(cat token) repose ls",
		SuggestFor: []string{"token", "keys", "apikey", "api-key"},
	}

	var scopes []string
	var expires string
	create := &cobra.Command{
		Use:   "create NAME",
		Short: "Make a token and print it, once",
		Long: "Make a token called NAME and print it, and nothing else on stdout, so it\n" +
			"pipes. It is not shown again. --project binds it to one project; --expires\n" +
			"is its life in days, 30 unless given, 365 at most. An account holds 20 live\n" +
			"tokens.\n\n" +
			"--scope says what it may do, one or more of:" + tokenScopeLines(),
		Example: "  repose tokens create ci --scope read,triggers\n" +
			"  repose tokens create bot --scope read,projects --project todo-app\n" +
			"  repose tokens create ci --scope read --expires 90 > token",
		Args: argsN(1, 1, "one NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return TokensCreateCmd(cmd.Context(), e, args[0], scopes, g.project, expires)
		},
	}
	create.Flags().StringArrayVar(&scopes, "scope", nil, "what the token may do (`SCOPE`): read, projects, config, secrets, triggers or agents (repeatable, or comma-separated)")
	create.Flags().StringVar(&expires, "expires", "", "the token's life in days (`DAYS`), 1 to 365; 30 unless given")
	create.Flags().BoolVar(&g.json, "json", false, "print every field, the token included, as JSON")

	var listQuiet bool
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List your API tokens",
		Long: "List your tokens with their scopes, project, end and last use, live ones\n" +
			"first; a revoked one stays listed for 30 days. The token itself is never\n" +
			"shown.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if listQuiet && g.json {
				return cobraUsageError{errQuietAndJSON}
			}
			e, err := env()
			if err != nil {
				return err
			}
			e.Quiet = listQuiet
			return TokensListCmd(cmd.Context(), e)
		},
	}
	list.Flags().BoolVar(&g.json, "json", false, "print every field of every token as JSON")
	list.Flags().BoolVarP(&listQuiet, "quiet", "q", false, "print the live tokens' names alone")

	rm := &cobra.Command{
		Use:        "rm NAME...",
		Aliases:    []string{"remove"},
		SuggestFor: []string{"delete", "revoke"},
		Short:      "Revoke tokens",
		Long:       "Revoke each NAME. A call with it is refused from that moment.",
		Args:       argsN(1, -1, "one or more NAMEs"),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return TokensRmCmd(cmd.Context(), e, args)
		},
	}

	root.AddCommand(create, list, rm)
	bareRuns(root, list)
	return root
}
