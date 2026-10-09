package cli

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The help's layout (review C6, C9, 8.1; DECISIONS I-630): root help
// lists the commands by what you do with them, in the order you meet
// them, and ends with the docs' address; flag text wraps at helpWidth.

const (
	helpWidth = 80
	docsURL   = "https://repose.herakraft.co/docs/cli"
)

// commandGroups is root help's sections. A visible root command that is
// in none of them lands under "Additional Commands", as help, version
// and completion do; TestRootHelpIsGrouped keeps the rest here.
var commandGroups = []struct {
	id, title string
	names     []string
}{
	{"work", "Work on a machine:", []string{"run", "attach", "sync", "exec", "ssh", "code", "cp", "paste", "open", "browser"}},
	{"agents", "Follow the agents:", []string{"ps", "events", "questions", "reply", "mcp"}},
	{"projects", "Manage projects:", []string{"ls", "status", "start", "stop", "resize", "fork", "keep", "rm", "restore", "snapshots", "logs"}},
	{"setup", "Set up:", []string{"login", "logout", "config", "secrets", "notify", "scan"}},
}

func init() {
	// Root help lists commands in the order they are added, which is
	// commandGroups' order, not the alphabet's.
	cobra.EnableCommandSorting = false
	cobra.AddTemplateFunc("flagUsages", flagUsages)
	cobra.AddTemplateFunc("globalFlagUsages", globalFlagUsages)
	cobra.AddTemplateFunc("docsURL", func() string { return docsURL })
}

// groupCommands puts root's commands into commandGroups, in its order.
func groupCommands(root *cobra.Command) {
	byName := map[string]*cobra.Command{}
	for _, c := range root.Commands() {
		byName[c.Name()] = c
	}
	var ordered []*cobra.Command
	for _, g := range commandGroups {
		root.AddGroup(&cobra.Group{ID: g.id, Title: g.title})
		for _, n := range g.names {
			if c := byName[n]; c != nil {
				c.GroupID = g.id
				ordered = append(ordered, c)
				delete(byName, n)
			}
		}
	}
	for _, c := range root.Commands() {
		if byName[c.Name()] != nil {
			ordered = append(ordered, c)
		}
	}
	root.ResetCommands()
	root.AddCommand(ordered...)
	root.InitDefaultHelpCmd()
	for _, c := range root.Commands() {
		if c.Name() == "help" {
			c.Short = "Show help for a command"
		}
	}
}

// flagUsages is a flag set's help, wrapped at helpWidth. --temp's
// optional value shows as DURATION, not the "bare" the code uses for a
// --temp with none (review C3).
func flagUsages(fs *pflag.FlagSet) string {
	s := fs.FlagUsagesWrapped(helpWidth)
	return strings.ReplaceAll(s, ` string[="`+tempBare+`"]`, `[=DURATION]     `)
}

// globalFlagUsages is the Global Flags section: the inherited flags,
// without --project on a command that refuses it (I-631).
func globalFlagUsages(cmd *cobra.Command) string {
	fs := cmd.InheritedFlags()
	if !accountCommands[cmd.CommandPath()] {
		return flagUsages(fs)
	}
	kept := pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Name != "project" {
			kept.AddFlag(f)
		}
	})
	return flagUsages(kept)
}

// usageTemplate is cobra's, with flagUsages for the flags and, on root
// only, the docs' address at the end.
const usageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Available Commands:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Additional Commands:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{flagUsages .LocalFlags | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{globalFlagUsages . | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}{{if not .HasParent}}
Docs: {{docsURL}}{{end}}
`
