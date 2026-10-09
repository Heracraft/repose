package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Did-you-mean for a mistyped or borrowed flag (DECISIONS I-635):
// `run --detach` is docker's spelling of run's -d, `stop --force` fly's
// and docker's of -y. pflag's refusal named only the flag.

// flagSuggestFor is the flag another tool's habit types, per command,
// and what to say: a flag of the command, or a sentence when the
// command needs none.
var flagSuggestFor = map[string]map[string]string{
	"repose run":    {"--detach": "-d"},
	"repose ls":     {"-a": "=repose ls lists every project, stopped ones too."},
	"repose sync":   {"--detach": "=repose sync does not attach.", "-d": "=repose sync does not attach."},
	"repose stop":   {"--force": "-y", "-f": "-y"},
	"repose rm":     {"--force": "-y", "-f": "-y"},
	"repose resize": {"--force": "-y", "-f": "-y"},
	"repose ps":     {"--all": "=repose ps lists every window.", "-a": "=repose ps lists every window."},
}

var (
	unknownLongRE  = regexp.MustCompile(`^unknown flag: (--[^\s=]+)`)
	unknownShortRE = regexp.MustCompile(`^unknown shorthand flag: '(.)' in -`)
)

// flagSuggestion is the sentence after an unknown-flag refusal, "" for
// none.
func flagSuggestion(cmd *cobra.Command, err error) string {
	msg := err.Error()
	typed := ""
	if m := unknownLongRE.FindStringSubmatch(msg); m != nil {
		typed = m[1]
	} else if m := unknownShortRE.FindStringSubmatch(msg); m != nil {
		typed = "-" + m[1]
	} else {
		return ""
	}
	if s, ok := flagSuggestFor[cmd.CommandPath()][typed]; ok {
		if rest, isFact := strings.CutPrefix(s, "="); isFact {
			return rest
		}
		if f := lookupFlag(cmd, s); f != nil {
			return "Did you mean " + flagName(f) + "?"
		}
	}
	var hits []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		if strings.HasPrefix(typed, "--") {
			t := strings.ToLower(strings.TrimPrefix(typed, "--"))
			limit := 2
			if len(f.Name) <= 4 {
				limit = 1
			}
			if editDistance(t, f.Name) <= limit || (len(t) >= 2 && strings.HasPrefix(f.Name, t)) {
				hits = append(hits, flagName(f))
			}
			return
		}
		// An unknown -x: the long flag that starts with x and has no
		// letter of its own (`exec -w` is --workdir).
		if f.Shorthand == "" && strings.HasPrefix(f.Name, typed[1:]) {
			hits = append(hits, flagName(f))
		}
	})
	if len(hits) == 0 || len(hits) > 2 {
		return ""
	}
	return "Did you mean " + strings.Join(hits, " or ") + "?"
}

func lookupFlag(cmd *cobra.Command, name string) *pflag.Flag {
	if short, ok := strings.CutPrefix(name, "-"); ok && !strings.HasPrefix(short, "-") {
		return cmd.Flags().ShorthandLookup(short)
	}
	return cmd.Flags().Lookup(strings.TrimPrefix(name, "--"))
}

// flagName is how a suggestion names f: `-d (--no-attach)` for one with
// a letter, `--workdir` for one without.
func flagName(f *pflag.Flag) string {
	if f.Shorthand != "" {
		return fmt.Sprintf("-%s (--%s)", f.Shorthand, f.Name)
	}
	return "--" + f.Name
}
