package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

var secretNameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// reservedSecretNames mirrors docs/interfaces/api.md's "Secrets" table:
// the guest's sshd material, delivered by hostd, never through this path.
var reservedSecretNames = map[string]bool{
	"ssh_host_ed25519_key":          true,
	"ssh_host_ed25519_key-cert.pub": true,
	"user_ca.pub":                   true,
}

// shellSecretNames mirrors docs/interfaces/api.md: the variables the machine
// uses to keep each command's secrets current (DECISIONS I-475).
var shellSecretNames = map[string]bool{"BASH_ENV": true, "ENV": true, "REPOSE_ENV_GEN": true}

// checkSecretName refuses a NAME before any value is asked for. NAME=VALUE
// is refused with why: a value on the command line stays in the shell's
// history (I-620).
func checkSecretName(name string) error {
	if n, _, ok := strings.Cut(name, "="); ok && secretNameRe.MatchString(n) {
		return exitf(ExitUsage, "secrets set takes NAME alone, so the value stays out of your shell history: type it at the prompt, pipe it in, or pass --from-env or --from-file PATH.")
	}
	if !secretNameRe.MatchString(name) {
		return exitf(ExitUsage, "NAME must match [A-Z][A-Z0-9_]{0,63}, got %q", name)
	}
	if reservedSecretNames[name] {
		return exitf(ExitUsage, "%s is reserved for the machine's sshd keys", name)
	}
	if shellSecretNames[name] {
		return exitf(ExitUsage, "%s is reserved: the machine uses it to keep each command's secrets current", name)
	}
	return nil
}

// readPipedSecret is a secret's value from standard input: all of it,
// less one trailing newline (and a carriage return before it).
func readPipedSecret(r io.Reader, name string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxSecretBytes+1))
	if err != nil {
		return nil, exitf(ExitUsage, "Could not read %s's value from standard input: %v", name, err)
	}
	b = bytes.TrimSuffix(b, []byte("\n"))
	b = bytes.TrimSuffix(b, []byte("\r"))
	if len(b) == 0 {
		return nil, exitf(ExitUsage, "No value for %s on standard input.", name)
	}
	return b, nil
}

// maxSecretBytes bounds a piped value; the api refuses a larger one
// anyway, and a pipe that never ends must not fill memory.
const maxSecretBytes = 64 << 10

// SecretsSetCmd implements `repose secrets set NAME` (07-cli.md §5.10).
func SecretsSetCmd(ctx context.Context, e *Env, projectArg, name string, value []byte) error {
	if err := checkSecretName(name); err != nil {
		return err
	}
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	res, err := e.Client.PutSecret(ctx, project.ID, name, value)
	if err != nil {
		return err
	}
	if res != nil && res.Pushed {
		_, _ = fmt.Fprintf(e.Out, "Set %s on %s; the running machine has it now.\n", name, project.Slug)
	} else {
		_, _ = fmt.Fprintf(e.Out, "Set %s on %s; the machine gets it at its next start.\n", name, project.Slug)
	}
	return nil
}

// SecretsListCmd implements `repose secrets list`.
func SecretsListCmd(ctx context.Context, e *Env, projectArg string) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	secrets, err := e.Client.ListSecrets(ctx, project.ID)
	if err != nil {
		return err
	}
	if e.JSON {
		return writeJSONOut(e.Out, secrets)
	}
	// Piped, it stays one "NAME\tDATE" line per secret for scripts. On a
	// terminal it also shows what this laptop copies at each run, the
	// other way a secret reaches the machine (DECISIONS I-422).
	tty := writerIsTerminal(e.Out)
	if tty {
		if len(secrets) == 0 {
			_, _ = fmt.Fprintf(e.Out, "Stored by repose for %s: none\n", project.Slug)
		} else {
			_, _ = fmt.Fprintf(e.Out, "Stored by repose for %s:\n", project.Slug)
		}
	}
	for _, s := range secrets {
		indent := ""
		if tty {
			indent = "  "
		}
		_, _ = fmt.Fprintf(e.Out, "%s%s\t%s\n", indent, s.Name, tableTime(s.UpdatedAt))
	}
	if tty {
		skip, _, _ := e.Cfg.loginSkip(project.Slug)
		_, _ = fmt.Fprintln(e.Out, "Copied from this laptop at each repose run, onto the machine's disk (so into its snapshots and forks):")
		writeLoginRows(e.Out, skip, e.loginsFound())
	}
	return nil
}

// SecretsRmCmd implements `repose secrets rm NAME`.
func SecretsRmCmd(ctx context.Context, e *Env, projectArg, name string) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	if err := e.Client.DeleteSecret(ctx, project.ID, name); err != nil {
		// The api's generic not_found named no secret and no project;
		// the line reads as `mcp rm`'s (I-633).
		var apiErr *APIError
		if errors.As(err, &apiErr) && (apiErr.Code == "not_found" || apiErr.Status == 404) {
			return exitf(ExitGeneric, "%s has no secret %s.", project.Slug, name)
		}
		return err
	}
	_, _ = fmt.Fprintf(e.Out, "Removed %s from %s.\n", name, project.Slug)
	return nil
}

// secretNamesForCompletion is the project's secret names for `secrets rm`
// (two seconds at most, a shell is waiting).
func secretNamesForCompletion(env func() (*Env, error), projectArg string) []string {
	e, err := env()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return nil
	}
	secrets, err := e.Client.ListSecrets(ctx, project.ID)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(secrets))
	for _, s := range secrets {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return out
}
