package cli

import (
	"errors"
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
	// Strings, not ints: an int flag's zero value is also "not given",
	// and pflag's parse error is Go's sentence (ENG-38, I-1432).
	var diskArg, memoryArg, cpusArg string
	add := &cobra.Command{
		Use:   "add [NAME]",
		Short: "Add a machine of your own",
		Long: "Add a machine of yours. On a Mac with Apple silicon this installs repose's\n" +
			"virtual machine here and starts it: vfkit and the appliance image are\n" +
			"fetched, the disk file is made, and a launchd job runs `repose host run`\n" +
			"whenever the Mac boots, before anyone logs in; sudo asks for your password\n" +
			"once to install it, and without sudo the job runs at your login instead.\n" +
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
			ao, err := applianceOptionsFrom(diskArg, memoryArg, cpusArg)
			if err != nil {
				return err
			}
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
				return applianceInstall(cmd.Context(), e, argument, "", ao)
			}
			return HostAddCmd(cmd.Context(), e, name, ao)
		},
	}
	add.Flags().BoolVar(&g.json, "json", false, "print every field, the install step and the token included, as JSON")
	add.Flags().StringVar(&join, "join", "", "install on this Mac with the `argument` that repose host add printed elsewhere; `-` reads it from standard input")
	add.Flags().StringVar(&joinFile, "join-file", "", "install on this Mac with the argument in `PATH`, which keeps it off the command line")
	add.Flags().StringVar(&diskArg, "disk", "", "the appliance's disk in `GB` (default 100, at least 40, bounded by the volume's free space less 20)")
	add.Flags().StringVar(&memoryArg, "memory", "", "how much memory the appliance gets, in `MIB` (default three quarters of the Mac's, at least 8192)")
	add.Flags().StringVar(&cpusArg, "cpus", "", "how many `CORES` the appliance gets (default all but two, at least 4)")

	run := &cobra.Command{
		Use:   "run",
		Short: "Hold the appliance on this Mac (what launchd runs)",
		Long: "Run repose's virtual machine on this Mac in the foreground, until it is\n" +
			"stopped. The launchd job `repose host add` installs runs this when the Mac\n" +
			"boots, or at your login without sudo. A stop asks the appliance to keep\n" +
			"every running machine's memory in an encrypted image on its disk and power\n" +
			"off, and the next run brings\n" +
			"the machines back where they were; the key for those images is one item\n" +
			"in your keychain, made the first time it is needed.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return applianceRun(cmd.Context(), e)
		},
	}

	boot := &cobra.Command{
		Use:   "boot",
		Short: "Start the appliance on this Mac when it boots, before anyone logs in",
		Long: "Move the appliance on this Mac from a launchd agent, which starts it at\n" +
			"your login, to a launchd daemon, which starts it when the Mac boots with\n" +
			"nobody logged in. It runs as you. sudo asks for your password once. The\n" +
			"appliance restarts, and the machines on it start fresh.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return ApplianceBootCmd(e)
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
			"their snapshots stay on its disk. On the Mac that runs NAME as its appliance,\n" +
			"the appliance is removed too, disk included; elsewhere the machine itself is\n" +
			"untouched. --local removes the appliance on this Mac and nothing on your\n" +
			"account.",
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
			var confirm func(string) (bool, error)
			if !yes {
				if !canPrompt(os.Stdin) {
					// Refused before the connect, as `repose rm` does.
					what := name
					if what == "" {
						what = "the appliance on this Mac"
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
	rm.Flags().BoolVar(&local, "local", false, "remove the appliance on this Mac, disk included, and nothing on your account")

	root.AddCommand(add, run, boot, list, rm)
	bareRuns(root, list)
	return root
}
