package cli

import (
	"errors"
	"fmt"
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

	var join, joinFile string
	add := &cobra.Command{
		Use:   "add [NAME]",
		Short: "Add a machine of your own",
		Long: "Add a machine of yours. On a Mac with Apple silicon this installs repose\n" +
			"here and waits for the Mac to join your account: hostd, which starts when\n" +
			"the Mac boots, before anyone logs in, and runs each machine as a virtual\n" +
			"machine of its own; sudo asks for your password once to install it.\n" +
			"Anywhere else it prints, on stdout alone so it pipes, the\n" +
			"one step to run on the machine being added, which carries a token that\n" +
			"works once. Without NAME, repose names it.\n\n" +
			"https://repose.run/docs/own-hardware has what the machine needs first.",
		Example: "  repose host add\n" +
			"  repose host add mac-mini\n" +
			"  repose host add --join-file ~/join.txt   # on the Mac\n" +
			"  pbpaste | repose host add --join -       # the same, from the clipboard",
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
			if join != "" || joinFile != "" {
				if name != "" {
					return cobraUsageError{errors.New("--join takes the argument `repose host add` printed, and no NAME")}
				}
				argument, err := readJoinArgument(join, joinFile, cmd.InOrStdin())
				if err != nil {
					return err
				}
				return macHostInstall(cmd.Context(), e, argument, "", joinFile)
			}
			return HostAddCmd(cmd.Context(), e, name)
		},
	}
	add.Flags().BoolVar(&g.json, "json", false, "print the host as JSON; the join argument goes to a file, never to the output")
	add.Flags().StringVar(&join, "join", "", "install on this Mac with the `argument` that repose host add printed elsewhere; `-` reads it from standard input")
	add.Flags().StringVar(&joinFile, "join-file", "", "install on this Mac with the argument in `PATH`, which keeps it off the command line")

	// `host run` was what design 1's launchd job ran (I-1376). A Mac that
	// still has that job runs this binary at every boot; it says what to
	// do once, in the job's log, and waits, rather than fail and be
	// started again every ten seconds (I-1684).
	run := &cobra.Command{
		Use:    "run",
		Short:  "What design 1's appliance job ran; it no longer runs anything",
		Hidden: true,
		Args:   cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), d1LeftLine)
			<-cmd.Context().Done()
			return nil
		},
	}

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

	var yes, local bool
	rm := &cobra.Command{
		Use:        "rm [NAME]",
		Aliases:    []string{"remove"},
		SuggestFor: []string{"delete", "retire"},
		Short:      "Take your hardware off your account",
		Long: "Take NAME off your account, after one question. The machines on it stop and\n" +
			"their snapshots stay on its disk. On the Mac that is NAME, repose is removed\n" +
			"from it too, every machine's disk and snapshot included, and its memory and\n" +
			"disk are the Mac's again; elsewhere the machine itself is untouched. --local\n" +
			"removes repose from this Mac and nothing on your account.",
		Args: argsN(0, 1, "one NAME, or --local"),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if name == "" && !local {
				return cobraUsageError{errors.New("rm takes one NAME, or --local")}
			}
			if name == "" && !onAppleSilicon() {
				// Nothing is removed off a Mac, so nothing is asked.
				return HostRmLocalCmd(cmd.Context(), e, true, nil)
			}
			var confirm func(string) (bool, error)
			if !yes {
				if !canPrompt(os.Stdin) {
					// Refused before the connect, as `repose rm` does.
					what := name
					if what == "" {
						what = "repose on this Mac"
					}
					return exitf(ExitUsage, "Removing %s needs a confirmation; pass --yes to skip it.", what)
				}
				confirm = func(prompt string) (bool, error) { return askYesNo(cmd.Context(), prompt, false, "removing") }
			}
			if name == "" {
				return HostRmLocalCmd(cmd.Context(), e, yes, confirm)
			}
			return HostRmCmd(cmd.Context(), e, name, yes, confirm)
		},
	}
	rm.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation")
	rm.Flags().BoolVar(&local, "local", false, "remove repose from this Mac, every machine's disk included, and nothing on your account; on a Linux host, print the installer's uninstall line")

	var restartYes, restartForce bool
	restart := &cobra.Command{
		Use:   "restart",
		Short: "Restart this Mac, keeping every machine's memory",
		Long: "Park every machine on this Mac, each one's memory saved to a file, then\n" +
			"restart the Mac; when you log in again the machines come back where they\n" +
			"were. A machine that\n" +
			"cannot be parked stops the restart, and the parked ones run again;\n" +
			"--force restarts anyway, and those machines start fresh.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			if !restartYes {
				if !canPrompt(os.Stdin) {
					return exitf(ExitUsage, "Restarting this Mac needs a confirmation; pass --yes to skip it.")
				}
				ok, err := askYesNo(cmd.Context(), "Restart this Mac? Its machines keep their memory.", false, "restarting")
				if err != nil {
					return err
				}
				if !ok {
					return exitf(ExitGeneric, "Nothing restarted.")
				}
			}
			return HostRestartCmd(cmd.Context(), e, restartForce)
		},
	}
	restart.Flags().BoolVarP(&restartYes, "yes", "y", false, "skip the confirmation")
	restart.Flags().BoolVar(&restartForce, "force", false, "restart even if a machine could not be parked; it starts fresh")

	root.AddCommand(add, run, list, rm, restart)
	bareRuns(root, list)
	return root
}
