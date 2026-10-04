package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// `repose secrets import [FILE]` (DECISIONS I-277): every NAME=VALUE of a
// dotenv file becomes a named secret, through the same PUT `secrets set`
// uses, so values go where every secret goes (Postgres, encrypted) and
// nowhere else. The file is only read; nothing is written, and no value
// is printed or logged: the summary names names.

// dotenvEntry is one NAME=VALUE from a file, with the line it starts on.
type dotenvEntry struct {
	Name  string
	Value string
	Line  int
}

// dotenvError is a line the parser cannot read; it names the line, never
// its content, which may be a value.
type dotenvError struct {
	Line int
	Why  string
}

func (e *dotenvError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Why) }

// parseDotenv reads the dotenv format as docker compose and the dotenv
// libraries share it:
//
//	# a comment, and blank lines, are skipped
//	export NAME=value        "export " is allowed and ignored
//	NAME=value # comment     unquoted: trimmed, " #" starts a comment
//	NAME='literal $x \n'     single quotes: exactly as written
//	NAME="a\nb \"q\""        double quotes: \n \r \t \" \\ \$ escapes
//	NAME="line one
//	line two"                quoted values may span lines (PEM keys)
//
// No ${VAR} expansion: a value is what the file says. A name given twice
// takes its last value, as sourcing the file in a shell would.
func parseDotenv(src string) ([]dotenvEntry, error) {
	src = strings.TrimPrefix(src, "\ufeff")
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(src, "\n")
	var out []dotenvEntry
	index := map[string]int{}
	for i := 0; i < len(lines); i++ {
		lineNo := i + 1
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "export "); ok {
			line = strings.TrimLeft(rest, " \t")
		}
		name, rest, ok := strings.Cut(line, "=")
		if !ok {
			return nil, &dotenvError{lineNo, "no = in it (lines are NAME=VALUE)"}
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, &dotenvError{lineNo, "no name before ="}
		}
		rest = strings.TrimLeft(rest, " \t")
		var value string
		switch {
		case strings.HasPrefix(rest, `"`) || strings.HasPrefix(rest, `'`):
			q := rest[0]
			body := rest[1:]
			var b strings.Builder
			closed := false
			for !closed {
				j := 0
				for j < len(body) {
					c := body[j]
					if q == '"' && c == '\\' && j+1 < len(body) {
						switch body[j+1] {
						case 'n':
							b.WriteByte('\n')
						case 'r':
							b.WriteByte('\r')
						case 't':
							b.WriteByte('\t')
						case '"', '\\', '$', '`':
							b.WriteByte(body[j+1])
						default:
							b.WriteByte('\\')
							b.WriteByte(body[j+1])
						}
						j += 2
						continue
					}
					if c == q {
						closed = true
						break
					}
					b.WriteByte(c)
					j++
				}
				if closed {
					after := strings.TrimSpace(body[j+1:])
					if after != "" && !strings.HasPrefix(after, "#") {
						return nil, &dotenvError{lineNo, "text after the closing quote"}
					}
					break
				}
				// The value goes on to the next line.
				i++
				if i >= len(lines) {
					return nil, &dotenvError{lineNo, fmt.Sprintf("the %c quote is never closed", q)}
				}
				b.WriteByte('\n')
				body = lines[i]
			}
			value = b.String()
		default:
			value = rest
			if k := strings.Index(value, " #"); k >= 0 {
				value = value[:k]
			} else if k := strings.Index(value, "\t#"); k >= 0 {
				value = value[:k]
			}
			value = strings.TrimSpace(value)
		}
		if k, seen := index[name]; seen {
			out[k].Value, out[k].Line = value, lineNo
			continue
		}
		index[name] = len(out)
		out = append(out, dotenvEntry{Name: name, Value: value, Line: lineNo})
	}
	return out, nil
}

// validateImport refuses the whole file before anything is sent when a
// name or a value would be refused: half an import is worse than none.
func validateImport(entries []dotenvEntry) error {
	var bad []string
	for _, en := range entries {
		switch {
		case reservedSecretNames[en.Name]:
			bad = append(bad, fmt.Sprintf("line %d: %s is reserved for the machine's sshd", en.Line, en.Name))
		case shellSecretNames[en.Name]:
			bad = append(bad, fmt.Sprintf("line %d: %s is reserved: the machine uses it to keep each command's secrets current", en.Line, en.Name))
		case !secretNameRe.MatchString(en.Name):
			bad = append(bad, fmt.Sprintf("line %d: %s is not a secret name (uppercase letters, digits and _, starting with a letter, up to 64)", en.Line, en.Name))
		case len(en.Value) > secretMaxBytes:
			bad = append(bad, fmt.Sprintf("line %d: %s's value is over 64 KB", en.Line, en.Name))
		}
	}
	if len(bad) > 0 {
		return exitf(ExitUsage, "Nothing imported:\n  %s", strings.Join(bad, "\n  "))
	}
	return nil
}

// secretMaxBytes is the api's limit on one value (api.md "Secrets").
const secretMaxBytes = 64 << 10

// SecretsImportOptions are `repose secrets import`'s arguments.
type SecretsImportOptions struct {
	ProjectArg string
	File       string // "-" is stdin
	DryRun     bool
}

func newSecretsImportCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var opts SecretsImportOptions
	cmd := &cobra.Command{
		Use:   "import [FILE]",
		Short: "Set a secret for every NAME=VALUE in a .env file (default ./.env; - reads stdin)",
		Long: "Reads FILE (./.env by default, - for stdin) in the dotenv format and sets each NAME=VALUE\n" +
			"in it as a secret of the project, replacing one of the same name. Names are checked before\n" +
			"anything is sent; values are never printed. --dry-run lists what would be set.",
		Example: "  repose secrets import\n  repose secrets import .env.production\n  op inject -i .env.tpl | repose secrets import -",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.ProjectArg = g.project
			opts.File = ".env"
			if len(args) == 1 {
				opts.File = args[0]
			}
			e, err := env()
			if err != nil {
				return err
			}
			return SecretsImportCmd(cmd.Context(), e, opts, os.Stdin)
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "list the names that would be set, and send nothing")
	return cmd
}

// SecretsImportCmd implements `repose secrets import`.
func SecretsImportCmd(ctx context.Context, e *Env, opts SecretsImportOptions, stdin io.Reader) error {
	var raw []byte
	var err error
	from := opts.File
	if opts.File == "-" {
		raw, err = io.ReadAll(stdin)
		from = "stdin"
	} else {
		raw, err = os.ReadFile(opts.File)
	}
	if err != nil {
		return exitf(ExitUsage, "Could not read %s: %v", from, err)
	}
	entries, err := parseDotenv(string(raw))
	if err != nil {
		return exitf(ExitUsage, "Could not read %s: %v. Nothing imported.", from, err)
	}
	if len(entries) == 0 {
		return exitf(ExitUsage, "%s has no NAME=VALUE lines. Nothing imported.", from)
	}
	if err := validateImport(entries); err != nil {
		return err
	}
	project, err := requireProject(ctx, e, opts.ProjectArg)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	metas, err := e.Client.ListSecrets(ctx, project.ID)
	if err != nil {
		return err
	}
	for _, m := range metas {
		existing[m.Name] = true
	}
	label := func(n string) string {
		if existing[n] {
			return n + " (replaced)"
		}
		return n
	}
	if opts.DryRun {
		names := make([]string, len(entries))
		for i, en := range entries {
			names[i] = label(en.Name)
		}
		_, _ = fmt.Fprintf(e.Out, "Would set %d on %s from %s: %s\nNothing sent (--dry-run).\n", len(entries), project.Slug, from, strings.Join(names, ", "))
		return nil
	}
	var set []string
	pushed := false
	for _, en := range entries {
		res, err := e.Client.PutSecret(ctx, project.ID, en.Name, []byte(en.Value))
		if err != nil {
			if len(set) > 0 {
				_, _ = fmt.Fprintf(e.ErrOut, "Set before the failure: %s. Running the import again sets them all.\n", strings.Join(set, ", "))
			}
			return stepFailed(fmt.Sprintf("set %s (line %d of %s)", en.Name, en.Line, from), err, "")
		}
		pushed = pushed || (res != nil && res.Pushed)
		set = append(set, label(en.Name))
	}
	where := "delivered at the next start"
	if pushed {
		where = "pushed to the running machine"
	}
	_, _ = fmt.Fprintf(e.Out, "Set %d on %s from %s (%s): %s\n", len(set), project.Slug, from, where, strings.Join(set, ", "))
	return nil
}
