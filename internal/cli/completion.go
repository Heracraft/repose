package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "completion bash|zsh|fish|powershell",
		Short:     "Generate shell completion",
		Args:      cobra.MatchAll(argsN(1, 1, "one of bash, zsh, fish or powershell"), cobra.OnlyValidArgs),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(os.Stdout)
			case "zsh":
				return root.GenZshCompletion(os.Stdout)
			case "fish":
				return root.GenFishCompletion(os.Stdout, true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return fmt.Errorf("unknown shell %q", args[0])
			}
		},
	}
}

// completeCpArg completes `repose cp`'s arguments (DECISIONS I-621): a
// word that can still be a project gets the projects as PROJECT:, and a
// path on the machine gets nothing, since the laptop's files are no
// help there. A local path gets the shell's file completion.
func completeCpArg(env func() (*Env, error)) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if parseCpSide(toComplete).Remote {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		if toComplete == "" || strings.ContainsAny(toComplete, `/\.~`) {
			return nil, cobra.ShellCompDirectiveDefault
		}
		var out []string
		for _, s := range projectSlugsForCompletion(env) {
			if strings.HasPrefix(s, toComplete) {
				out = append(out, s+":")
			}
		}
		if len(out) == 0 {
			return nil, cobra.ShellCompDirectiveDefault
		}
		return out, cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveNoFileComp
	}
}

// completeQuestionID completes --question with the ids of the questions
// waiting, as `repose questions` shows them.
func completeQuestionID(env func() (*Env, error)) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		e, err := env()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		qs, err := e.Client.ListQuestions(ctx)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		out := make([]string, 0, len(qs))
		for _, q := range qs {
			out = append(out, shortID(q.ID))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// tempDurations are --temp's usual values, offered by completion.
var tempDurations = []string{"1h", "3h", "24h"}
