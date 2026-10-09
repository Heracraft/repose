package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// The CLI's grammar (DECISIONS I-619): one spelling for each idea across
// commands. A group noun alone runs its listing, an undo is a verb, each
// rm also answers to remove, and --project goes only where a project is
// meant.

// accountCommands act on the account or the laptop, never on a project,
// so --project there is a mistake that would otherwise be ignored.
var accountCommands = map[string]bool{
	"repose ls":          true,
	"repose login":       true,
	"repose logout":      true,
	"repose version":     true,
	"repose completion":  true,
	"repose notify":      true,
	"repose notify set":  true,
	"repose notify test": true,
}

// refuseProjectFlag is a usage error for --project on a command that acts
// on no project. $REPOSE_PROJECT is ambient and stays quiet there.
func refuseProjectFlag(cmd *cobra.Command) error {
	f := cmd.Flags().Lookup("project")
	if f == nil || !f.Changed || !accountCommands[cmd.CommandPath()] {
		return nil
	}
	return cobraUsageError{fmt.Errorf("%s acts on no project; it takes no --project (got %s)", cmd.CommandPath(), f.Value.String())}
}

// bareRunsKey marks a group whose bare form runs one of its subcommands
// (`repose secrets` is `repose secrets list`). suggest.go reads it: a
// word after such a group is still a mistyped subcommand, never an
// argument.
const bareRunsKey = "repose.bare"

// bareRuns makes the group's bare form run sub with no arguments, and
// keeps its subcommand typos usage errors.
func bareRuns(group, sub *cobra.Command) {
	if group.Annotations == nil {
		group.Annotations = map[string]string{}
	}
	group.Annotations[bareRunsKey] = sub.Name()
	group.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			msg := strings.TrimSpace(unknownCommandMessage(cmd, args[0]))
			first, rest, _ := strings.Cut(msg, "\n")
			return cobraUsageError{fmt.Errorf("%s (got %s)\n%s", first, gotArgs(args), rest)}
		}
		return nil
	}
	group.RunE = func(cmd *cobra.Command, args []string) error {
		sub.SetContext(cmd.Context())
		return sub.RunE(sub, nil)
	}
}

// displayConfigTOML is config.toml's path as a user types it: under ~
// when it is there.
func displayConfigTOML() string {
	dir := filepath.Join("~", ".config", "repose")
	if base := os.Getenv(envXDGConfigHome); base != "" {
		dir = filepath.Join(base, "repose")
	}
	p := filepath.Join(dir, "config.toml")
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return p
}

// setupLine is `repose status`'s line for the settings a flag or the
// dashboard made that differ from a new project's (DECISIONS I-622): the
// agent `run -p` starts, machine.nix kept off, base updates held. ""
// when every one is the default.
func setupLine(p *Project) string {
	var parts []string
	if p.AgentDefault != "" && p.AgentDefault != "claude" {
		parts = append(parts, "agent "+p.AgentDefault)
	}
	if p.PersonalOptOut {
		parts = append(parts, "machine.nix off")
	}
	if p.HoldBaseUpdates {
		parts = append(parts, "base updates held")
	}
	if len(parts) == 0 {
		return ""
	}
	return "setup: " + strings.Join(parts, ", ")
}
