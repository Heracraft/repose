package cli

import (
	"context"
	"fmt"
	"regexp"
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

// SecretsSetCmd implements `repose secrets set NAME` (07-cli.md §5.10).
func SecretsSetCmd(ctx context.Context, e *Env, projectArg, name string, value []byte) error {
	if !secretNameRe.MatchString(name) {
		return exitf(ExitUsage, "NAME must match [A-Z][A-Z0-9_]{0,63}")
	}
	if reservedSecretNames[name] {
		return exitf(ExitUsage, "%s is reserved for the guest's sshd material", name)
	}
	if shellSecretNames[name] {
		return exitf(ExitUsage, "%s is reserved: the machine uses it to keep each command's secrets current", name)
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
		_, _ = fmt.Fprintf(e.Out, "Set %s (pushed to running guest)\n", name)
	} else {
		_, _ = fmt.Fprintf(e.Out, "Set %s (will be delivered at next start)\n", name)
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
		_, _ = fmt.Fprintf(e.Out, "%s%s\t%s\n", indent, s.Name, s.UpdatedAt.Format("2006-01-02 15:04"))
	}
	if tty {
		skip, _, _ := e.Cfg.loginSkip(project.Slug)
		_, _ = fmt.Fprintln(e.Out, "Copied from this laptop at each repose run:")
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
		return err
	}
	_, _ = fmt.Fprintf(e.Out, "Removed %s\n", name)
	return nil
}
