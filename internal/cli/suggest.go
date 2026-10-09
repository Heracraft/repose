package cli

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// Did-you-mean for a mistyped command (DECISIONS I-276). cobra suggests
// only at the top level and only by a command's own name, so
// `repose projetcs` (an old name, I-273, mistyped) found nothing to
// suggest, and a group such as `repose secrets lsit` printed the group's
// help and exited 0. unknownCommand runs before cobra, on a throwaway
// command tree, and answers both with a usage error naming the command to
// type.

// unknownCommand returns the message for args that name no command, or
// "" when they do (or when cobra should report the problem itself, such
// as a bad flag). isProject, when not nil, says whether a word is one of
// the user's projects: after a group whose bare form lists a project's
// things (`repose snapshots todo-app`), the word gets the command that
// takes it instead of a guess at a subcommand (I-633).
func unknownCommand(root *cobra.Command, args []string, isProject func(string) bool) string {
	if len(args) > 0 && (args[0] == cobra.ShellCompRequestCmd || args[0] == cobra.ShellCompNoDescRequestCmd) {
		return "" // shell completion's hidden command, which cobra adds as it runs
	}
	root.InitDefaultHelpCmd()
	cmd, rest, err := root.Find(args)
	if err != nil && cmd == root {
		// cobra's own unknown-command error at the top level; the word is
		// the first positional argument.
		if typed := firstPositional(cmd, rest); typed != "" {
			return unknownCommandMessage(cmd, typed)
		}
		return ""
	}
	// A group takes no words of its own (`repose notify`, `repose
	// secrets`), so a word after it is a mistyped subcommand, --help or
	// not (review theme 7); a runnable group whose usage takes words
	// (`repose browser [PROJECT]`) reads it as one of those.
	if err != nil || cmd == nil || !cmd.HasSubCommands() || (cmd.Runnable() && cmd.Annotations[bareRunsKey] == "" && takesWords(cmd)) {
		return ""
	}
	typed := firstPositional(cmd, rest)
	if typed == "" {
		return ""
	}
	if sub := cmd.Annotations[bareRunsKey]; sub != "" && isProject != nil && len(suggestCommands(cmd, typed)) == 0 && isProject(typed) {
		return fmt.Sprintf("%s is a project: `%s %s %s`.", typed, cmd.CommandPath(), sub, typed)
	}
	return unknownCommandMessage(cmd, typed)
}

// slugShape is what a project's slug can be; any other word is not
// looked up.
var slugShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// projectWord is unknownCommand's isProject for Execute: the slugs in
// projects.json first, then the account's list (two seconds at most, on
// a path that already failed).
func projectWord(word string) bool {
	if !slugShape.MatchString(word) {
		return false
	}
	if dir, err := configDir(); err == nil {
		if c, err := loadProjectsCache(dir); err == nil {
			for _, p := range c.ByRemote {
				if p.Slug == word {
					return true
				}
			}
		}
	}
	return slices.Contains(projectSlugsForCompletion(func() (*Env, error) { return newEnv("", false, false) }), word)
}

// firstPositional is the first argument that is not a flag or a flag's
// value, as cmd would parse args; "" when there is none or the flags do
// not parse.
func firstPositional(cmd *cobra.Command, args []string) string {
	cmd.InitDefaultHelpFlag() // so `secrets remvoe --help` parses
	fs := cmd.Flags()
	if err := fs.Parse(args); err != nil {
		return ""
	}
	if pos := fs.Args(); len(pos) > 0 {
		return pos[0]
	}
	return ""
}

func unknownCommandMessage(parent *cobra.Command, typed string) string {
	msg := fmt.Sprintf("unknown command %q for %q", typed, parent.CommandPath())
	if s := suggestCommands(parent, typed); len(s) > 0 {
		quoted := make([]string, len(s))
		for i, name := range s {
			quoted[i] = "`" + parent.CommandPath() + " " + name + "`"
		}
		msg += "\nDid you mean " + strings.Join(quoted, " or ") + "?"
	}
	// One wording for every usage error's pointer to help (I-631).
	return msg + fmt.Sprintf("\n`%s --help` shows its usage.", parent.CommandPath())
}

// suggestCommands names parent's visible subcommands that typed could
// have meant: one or two edits from the command's name or one of its
// aliases (so the old `projects` and `destroy` lead to `ls` and `rm`), a
// prefix of the name, or a word listed in its SuggestFor. Closest first,
// at most three.
func suggestCommands(parent *cobra.Command, typed string) []string {
	typed = strings.ToLower(typed)
	type hit struct {
		name string
		dist int
	}
	var hits []hit
	for _, c := range parent.Commands() {
		if !c.IsAvailableCommand() {
			continue
		}
		best := -1
		consider := func(d int) {
			if best < 0 || d < best {
				best = d
			}
		}
		for _, n := range append([]string{c.Name()}, c.Aliases...) {
			n = strings.ToLower(n)
			// Two edits turn a short name into almost any other short
			// word (`lst` into `ssh`), so names of four letters or fewer
			// get one.
			limit := 2
			if len(n) <= 4 {
				limit = 1
			}
			if d := editDistance(typed, n); d <= limit {
				consider(d)
			}
			if len(typed) >= 2 && strings.HasPrefix(n, typed) {
				consider(len(n) - len(typed))
			}
		}
		for _, s := range c.SuggestFor {
			if strings.EqualFold(s, typed) {
				consider(0)
			}
		}
		if best >= 0 {
			hits = append(hits, hit{c.Name(), best})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		return hits[i].name < hits[j].name
	})
	var out []string
	for _, h := range hits {
		// An exact alias or SuggestFor word (`up` for run) beats a guess
		// at a typo (`up` one edit from cp).
		if len(out) == 3 || (len(out) > 0 && hits[0].dist == 0 && h.dist > 0) {
			break
		}
		out = append(out, h.name)
	}
	return out
}

// editDistance is the Damerau-Levenshtein distance (optimal string
// alignment): a swapped pair of letters, the commonest typo, costs one.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}

// takesWords is whether cmd's usage line has arguments of its own.
func takesWords(cmd *cobra.Command) bool {
	return len(strings.Fields(cmd.Use)) > 1
}
