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

	var join string
	var ao applianceOptions
	add := &cobra.Command{
		Use:   "add [NAME]",
		Short: "Add a machine of your own",
		Long: "Add a machine of yours. On a Mac with Apple silicon this installs repose's\n" +
			"virtual machine here and starts it: vfkit and the appliance image are\n" +
			"fetched, the disk file is made, and a launchd agent runs `repose host run`\n" +
			"at every login. Anywhere else it prints, on stdout alone so it pipes, the\n" +
			"one step to run on the machine being added, which carries a token that\n" +
			"works once. Without NAME, repose names it.\n\n" +
			"https://repose.run/docs/own-hardware has what the machine needs first.",
		Example: "  repose host add\n" +
			"  repose host add mac-mini\n" +
			"  repose host add --join eyJ2IjoxLC...   # on the Mac",
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
			if join != "" {
				if name != "" {
					return cobraUsageError{errors.New("--join takes the argument `repose host add` printed, and no NAME")}
				}
				return applianceInstall(cmd.Context(), e, join, "", ao)
			}
			return HostAddCmd(cmd.Context(), e, name, ao)
		},
	}
	add.Flags().BoolVar(&g.json, "json", false, "print every field, the install step and the token included, as JSON")
	add.Flags().StringVar(&join, "join", "", "install on this Mac with the `argument` that repose host add printed elsewhere")
	add.Flags().IntVar(&ao.DiskGB, "disk", 0, "the appliance's disk in GB (default 100, at least 40, bounded by the volume's free space less 20)")
	add.Flags().IntVar(&ao.MemoryMiB, "memory", 0, "the appliance's memory in MiB (default three quarters of the Mac's)")
	add.Flags().IntVar(&ao.CPUs, "cpus", 0, "the appliance's vCPUs (default all but two)")

	run := &cobra.Command{
		Use:   "run",
		Short: "Hold the appliance on this Mac (what launchd runs)",
		Long: "Run repose's virtual machine on this Mac in the foreground, until it is\n" +
			"stopped. The launchd agent `repose host add` installs runs this at every\n" +
			"login; a stop asks the appliance to power off cleanly first.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return applianceRun(cmd.Context(), e)
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
				return HostRmLocalCmd(e, yes, confirm)
			}
			return HostRmCmd(cmd.Context(), e, name, yes, confirm)
		},
	}
	rm.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation")
	rm.Flags().BoolVar(&local, "local", false, "remove the appliance on this Mac, disk included, and nothing on your account")

	root.AddCommand(add, run, list, rm)
	bareRuns(root, list)
	return root
}
