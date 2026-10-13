package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
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
	// MCP sets the secrets the MCP carry templated, from the laptop's
	// Claude Code config (I-556); File is then unused.
	MCP bool
	// Yes replaces secrets the project already has without asking
	// (--mcp only).
	Yes bool
	// Confirm asks once before --mcp replaces a secret the project
	// has; nil asks on the terminal.
	Confirm func(prompt string) (bool, error)
	// Global sets the account's secrets, for every project (I-697),
	// instead of the project's; with MCP it reads the user scope alone.
	Global bool
}

// importTarget is where an import's secrets go: a project, or the
// account (--global, I-697).
type importTarget struct {
	where string // "on todo-app", "for every project"
	owner string // "todo-app", "your account": who already has a name
	list  func() ([]SecretMeta, error)
	// put sets one and reports whether a running machine has it now.
	put func(name string, value []byte) (bool, error)
	// pushedText says where the values went.
	pushedText func(pushed bool) string
}

func resolveImportTarget(ctx context.Context, e *Env, opts SecretsImportOptions) (*importTarget, error) {
	if opts.Global {
		return &importTarget{
			where: "for every project",
			owner: "Your account",
			list: func() ([]SecretMeta, error) {
				s, err := e.Client.ListAccountSecrets(ctx)
				return s, olderAPI(err)
			},
			put: func(name string, value []byte) (bool, error) {
				res, err := e.Client.PutAccountSecret(ctx, name, value)
				return err == nil && res.Pushed > 0, err
			},
			pushedText: func(pushed bool) string {
				if pushed {
					return "pushed to the running machines"
				}
				return "delivered at each machine's next start"
			},
		}, nil
	}
	project, err := requireProject(ctx, e, opts.ProjectArg)
	if err != nil {
		return nil, err
	}
	return &importTarget{
		where: "on " + project.Slug,
		owner: project.Slug,
		list:  func() ([]SecretMeta, error) { return e.Client.ListSecrets(ctx, project.ID) },
		put: func(name string, value []byte) (bool, error) {
			res, err := e.Client.PutSecret(ctx, project.ID, name, value)
			return err == nil && res != nil && res.Pushed, err
		},
		pushedText: func(pushed bool) string {
			if pushed {
				return "pushed to the running machine"
			}
			return "delivered at the next start"
		},
	}, nil
}

func newSecretsImportCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var opts SecretsImportOptions
	cmd := &cobra.Command{
		Use:   "import [FILE]",
		Short: "Set a secret for each NAME=VALUE in a .env file",
		Long: "Read FILE (./.env by default, - for stdin) in the dotenv format and set each\n" +
			"NAME=VALUE in it as a secret of the project, replacing one of the same name.\n" +
			"Names are checked before anything is sent; values are never printed. --dry-run\n" +
			"lists what would be set.\n\n" +
			"With --mcp, set the secrets your carried MCP servers need instead, from the\n" +
			"tokens in your laptop's Claude Code config, for this folder's project. It asks\n" +
			"once before replacing secrets the project already has; -y replaces them\n" +
			"without asking.\n\n" +
			"With --global, set them on your account instead, for every project; --mcp\n" +
			"then reads your user-scope servers alone, since a local-scope server is one\n" +
			"repository's.",
		Example: "  repose secrets import\n  repose secrets import .env.production\n  op inject -i .env.tpl | repose secrets import -\n  repose secrets import --mcp\n  repose secrets import --global ~/.config/shared.env",
		Args:    argsN(0, 1, "at most one FILE"),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.ProjectArg = g.project
			opts.File = ".env"
			if len(args) == 1 {
				if opts.MCP {
					return exitf(ExitUsage, "--mcp reads your laptop's Claude Code config, not a file; leave out %s.", args[0])
				}
				opts.File = args[0]
			}
			if opts.Yes && !opts.MCP {
				return exitf(ExitUsage, "--yes goes with --mcp; a file import replaces without asking.")
			}
			if opts.Global {
				if err := globalProjectConflict(g.project); err != nil {
					return err
				}
			}
			e, err := env()
			if err != nil {
				return err
			}
			if opts.MCP {
				return SecretsImportMCPCmd(cmd.Context(), e, opts)
			}
			return SecretsImportCmd(cmd.Context(), e, opts, os.Stdin)
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "list the names that would be set, and send nothing")
	cmd.Flags().BoolVar(&opts.MCP, "mcp", false, "set the secrets your carried MCP servers need from the tokens in your laptop's Claude Code config")
	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "with --mcp, replace secrets the project already has without asking")
	cmd.Flags().BoolVar(&opts.Global, "global", false, "set them on your account, for every project (with --mcp, from your user-scope servers)")
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
	target, err := resolveImportTarget(ctx, e, opts)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	metas, err := target.list()
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
		_, _ = fmt.Fprintf(e.Out, "Would set %d %s from %s: %s\nNothing sent (--dry-run).\n", len(entries), target.where, from, strings.Join(names, ", "))
		return nil
	}
	var set []string
	pushed := false
	for _, en := range entries {
		p, err := target.put(en.Name, []byte(en.Value))
		if err != nil {
			if len(set) > 0 {
				_, _ = fmt.Fprintf(e.ErrOut, "Set before the failure: %s. Running the import again sets them all.\n", strings.Join(set, ", "))
			}
			return stepFailed(fmt.Sprintf("set %s (line %d of %s)", en.Name, en.Line, from), err, "")
		}
		pushed = pushed || p
		set = append(set, label(en.Name))
	}
	_, _ = fmt.Fprintf(e.Out, "Set %d %s from %s (%s): %s\n", len(set), target.where, from, target.pushedText(pushed), strings.Join(set, ", "))
	return nil
}

// SecretsImportMCPCmd implements `repose secrets import --mcp` (I-556):
// it reads the laptop's Claude Code MCP servers again, as the carry does
// (user scope, and local scope for this folder's repository), and sets
// each secret the carry made from a literal value to that value. Values
// stay in memory and go through the same PUT as `secrets set`; only
// names are printed. The names are the carry's, not the user's, so a
// name the project already has is replaced only after one question
// (or --yes); a no sets the others.
func SecretsImportMCPCmd(ctx context.Context, e *Env, opts SecretsImportOptions) error {
	values := map[string]string{}
	// --global reads the user scope alone: a local-scope server is this
	// repository's, and its tokens belong to its project.
	repo := gitRepoRoot(e.Cwd)
	if opts.Global {
		repo = ""
	}
	if _, err := collectMCP(e.HomeDir, repo, nil, values); err != nil {
		return exitf(ExitGeneric, "Could not read your Claude Code MCP servers: %s.", strings.TrimSuffix(err.Error(), "."))
	}
	if len(values) == 0 {
		_, _ = fmt.Fprintln(e.Out, "Your laptop's Claude Code MCP servers hold no tokens to set.")
		return nil
	}
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	entries := make([]dotenvEntry, len(names))
	for i, n := range names {
		entries[i] = dotenvEntry{Name: n, Value: values[n]}
	}
	if err := validateMCPImport(entries); err != nil {
		return err
	}
	target, err := resolveImportTarget(ctx, e, opts)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	metas, err := target.list()
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
	const from = "your laptop's MCP servers"
	if opts.DryRun {
		l := make([]string, len(names))
		for i, n := range names {
			l[i] = label(n)
		}
		_, _ = fmt.Fprintf(e.Out, "Would set %d %s from %s: %s\nNothing sent (--dry-run).\n", len(names), target.where, from, strings.Join(l, ", "))
		return nil
	}
	var replace []string
	for _, n := range names {
		if existing[n] {
			replace = append(replace, n)
		}
	}
	if len(replace) > 0 && !opts.Yes {
		confirm := opts.Confirm
		if confirm == nil {
			confirm = func(prompt string) (bool, error) { return askYesNo(ctx, prompt, false, "replacing secrets") }
		}
		ok, err := confirm(fmt.Sprintf("%s already has %s. Replace with your laptop's values? [y/N] ", target.owner, strings.Join(replace, ", ")))
		if err != nil {
			return err
		}
		if !ok {
			kept := entries[:0]
			for _, en := range entries {
				if !existing[en.Name] {
					kept = append(kept, en)
				}
			}
			entries = kept
		}
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(e.Out, "Nothing imported.")
		return nil
	}
	var set []string
	pushed := false
	for _, en := range entries {
		p, err := target.put(en.Name, []byte(en.Value))
		if err != nil {
			if len(set) > 0 {
				_, _ = fmt.Fprintf(e.ErrOut, "Set before the failure: %s. Running the import again sets them all.\n", strings.Join(set, ", "))
			}
			return stepFailed("set "+en.Name, err, "")
		}
		pushed = pushed || p
		set = append(set, label(en.Name))
	}
	_, _ = fmt.Fprintf(e.Out, "Set %d %s from %s (%s): %s\n", len(set), target.where, from, target.pushedText(pushed), strings.Join(set, ", "))
	return nil
}

// validateMCPImport refuses a value over the api's limit before anything
// is sent. The names are the carry's own and always valid.
func validateMCPImport(entries []dotenvEntry) error {
	var bad []string
	for _, en := range entries {
		if len(en.Value) > secretMaxBytes {
			bad = append(bad, en.Name+"'s value is over 64 KB")
		}
	}
	if len(bad) > 0 {
		return exitf(ExitUsage, "Nothing imported:\n  %s", strings.Join(bad, "\n  "))
	}
	return nil
}
