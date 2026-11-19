package cli

import (
	"os"

	"github.com/spf13/cobra"
)

// newHostCmd is `repose host` (DECISIONS I-1370): the hardware of the
// user's own that their projects run on. `host`, singular, because the
// plan sells one and every line reads about one machine.
func newHostCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	root := &cobra.Command{
		Use:   "host",
		Short: "Add hardware of your own, list it or take it off",
		Long: "A Mac mini or an x86 box of yours joins your account as a host, and from\n" +
			"then on your projects run on it instead of repose's servers. One host per\n" +
			"account. Alone, list it.\n\n" +
			"Snapshots of the machines on it stay on its disk, so a dead disk loses them.",
		Example: "  repose host add\n" +
			"  repose host ls\n" +
			"  repose host rm mac-mini",
		SuggestFor: []string{"hosts", "hardware", "machine"},
	}

	add := &cobra.Command{
		Use:   "add [NAME]",
		Short: "Add hardware of your own and print the install step",
		Long: "Add a machine of yours and print, on stdout alone so it pipes, the one step\n" +
			"to run on that machine. Without NAME, repose names it. The step carries a\n" +
			"token that works once.\n\n" +
			"https://repose.run/docs/own-hardware has what the machine needs first.",
		Example: "  repose host add\n" +
			"  repose host add mac-mini",
		Args: argsN(0, 1, "at most one NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return HostAddCmd(cmd.Context(), e, name)
		},
	}
	add.Flags().BoolVar(&g.json, "json", false, "print every field, the install step and the token included, as JSON")

	var listQuiet bool
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List your own hardware",
		Long: "List the hardware you have added: its state, memory, how many of your\n" +
			"machines run on it and when it last spoke to repose.",
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
			return HostLsCmd(cmd.Context(), e)
		},
	}
	list.Flags().BoolVar(&g.json, "json", false, "print every field of every host as JSON")
	list.Flags().BoolVarP(&listQuiet, "quiet", "q", false, "print the hosts' names alone")

	var yes bool
	rm := &cobra.Command{
		Use:        "rm NAME",
		Aliases:    []string{"remove"},
		SuggestFor: []string{"delete", "retire"},
		Short:      "Take your hardware off your account",
		Long: "Take NAME off your account, after one question. The machines on it stop and\n" +
			"their snapshots stay on its disk. The machine itself is untouched: nothing\n" +
			"of yours on it is deleted and it keeps running until you stop it.",
		Args: argsN(1, 1, "one NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			var confirm func(string) (bool, error)
			if !yes {
				if !canPrompt(os.Stdin) {
					// Refused before the connect, as `repose rm` does.
					return exitf(ExitUsage, "Removing %s needs a confirmation; pass --yes to skip it.", args[0])
				}
				confirm = func(prompt string) (bool, error) { return askYesNo(cmd.Context(), prompt, false, "removing") }
			}
			return HostRmCmd(cmd.Context(), e, args[0], yes, confirm)
		},
	}
	rm.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation")

	root.AddCommand(add, list, rm)
	bareRuns(root, list)
	return root
}
