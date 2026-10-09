package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// Help topics (DECISIONS I-635): `repose help environment`, `help
// exit-codes` and `help config-file` print cli.md's three reference
// tables offline, for a script that branches on an exit code or a
// person without the docs open. They are cobra help topics: commands
// with no Run, listed under root help's "Additional help topics".

var helpTopics = []struct {
	name, short, head string
	rows              []topicRow
}{
	{"environment", "Environment variables the CLI reads", "Environment variables the CLI reads:", envTopic},
	{"exit-codes", "What each exit code means", "Exit codes:", exitTopic},
	{"config-file", "The keys of ~/.config/repose/config.toml", "~/.config/repose/config.toml is optional. Its keys, with their defaults:", configTopic},
}

func newHelpTopicCmds() []*cobra.Command {
	var cmds []*cobra.Command
	for _, t := range helpTopics {
		cmds = append(cmds, &cobra.Command{Use: t.name, Short: t.short, Long: t.head + "\n\n" + topicText(t.rows)})
	}
	return cmds
}

var mdLink = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)

// plainText is a docs cell as a terminal shows it: no backticks, a link
// as its words.
func plainText(md string) string {
	s := mdLink.ReplaceAllString(md, "$1")
	s = strings.ReplaceAll(s, `\|`, "|")
	return strings.ReplaceAll(s, "`", "")
}

// topicText lays rows out as a table: each of the first cells in its
// own column, the text beside them, wrapped at helpWidth.
func topicText(rows []topicRow) string {
	var widths []int
	for _, r := range rows {
		for j, k := range r.keys {
			if j == len(widths) {
				widths = append(widths, 0)
			}
			widths[j] = max(widths[j], len(plainText(k)))
		}
	}
	indent := 2
	for _, w := range widths {
		indent += w + 2
	}
	var b strings.Builder
	for _, r := range rows {
		lead := "  "
		for j, w := range widths {
			k := ""
			if j < len(r.keys) {
				k = plainText(r.keys[j])
			}
			lead += fmt.Sprintf("%-*s  ", w, k)
		}
		for j, l := range wrapWords(plainText(r.text), max(helpWidth-indent, 30)) {
			if j == 0 {
				b.WriteString(lead + l + "\n")
			} else {
				b.WriteString(strings.Repeat(" ", indent) + l + "\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// wrapWords breaks s into lines of at most width runes, at spaces.
func wrapWords(s string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		switch {
		case line == "":
			line = w
		case len([]rune(line))+1+len([]rune(w)) <= width:
			line += " " + w
		default:
			lines = append(lines, line)
			line = w
		}
	}
	if line != "" || len(lines) == 0 {
		lines = append(lines, line)
	}
	return lines
}

// newHelpCmd is cobra's help command, except that a topic that is
// neither a command nor a help topic is a usage error, exit 2, with the
// names it could have meant (I-635): cobra's printed root help and
// exited 0, so a script could not tell.
func newHelpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "help [COMMAND]",
		Short: "Show help for a command",
		Long:  "Help for any command, or for a topic: environment, exit-codes, config-file.",
		ValidArgsFunction: func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			var names []string
			cmd, _, err := c.Root().Find(args)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			for _, sub := range cmd.Commands() {
				if (sub.IsAvailableCommand() || sub.IsAdditionalHelpTopicCommand()) && strings.HasPrefix(sub.Name(), toComplete) {
					names = append(names, sub.Name()+"\t"+sub.Short)
				}
			}
			return names, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(c *cobra.Command, args []string) error {
			cmd, rest, err := c.Root().Find(args)
			if cmd == nil || err != nil || len(rest) > 0 {
				parent := c.Root()
				if cmd != nil {
					parent = cmd
				}
				typed := strings.Join(args, " ")
				if len(rest) > 0 {
					typed = rest[0]
				}
				return cobraUsageError{fmt.Errorf("%s", unknownTopicMessage(parent, typed))}
			}
			cmd.InitDefaultHelpFlag()
			cmd.InitDefaultVersionFlag()
			return cmd.Help()
		},
	}
}

// unknownTopicMessage names the commands and topics under parent that
// typed could have meant, by the same rule a mistyped command gets.
func unknownTopicMessage(parent *cobra.Command, typed string) string {
	msg := fmt.Sprintf("No help for %q.", typed)
	hits := suggestCommands(parent, typed)
	low := strings.ToLower(typed)
	for _, c := range parent.Commands() {
		n := c.Name()
		if c.IsAdditionalHelpTopicCommand() && (editDistance(low, n) <= 2 || (len(low) >= 2 && strings.HasPrefix(n, low))) {
			hits = append(hits, n)
		}
	}
	if len(hits) > 0 {
		path := strings.TrimSpace(strings.TrimPrefix(parent.CommandPath(), parent.Root().Name()))
		quoted := make([]string, len(hits))
		for i, n := range hits {
			quoted[i] = "`" + strings.Join(strings.Fields("repose help "+path+" "+n), " ") + "`"
		}
		msg += " Did you mean " + strings.Join(quoted, " or ") + "?"
	}
	return msg + "\n`repose help` lists the commands and topics."
}
