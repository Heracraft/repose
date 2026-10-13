package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
)

// Account secrets (DECISIONS I-697): `repose secrets set|list|rm|import
// --global` act on the secrets of the account, which every project gets
// with its own set; a project's secret of the same name wins. The flag is
// config's: --global there acts on machine.nix, which every machine of
// the account gets.

// globalProjectConflict refuses --global beside a project: an account
// secret belongs to no one project.
func globalProjectConflict(project string) error {
	if project == "" {
		return nil
	}
	return cobraUsageError{fmt.Errorf("--global takes no project (%s): an account secret is for every project", project)}
}

// olderAPI turns the 404 an api from before I-697 answers on /me/secrets
// (its router has no such route, so no not_found envelope) into a line
// that says so, instead of "try again in a minute".
func olderAPI(err error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == 404 && apiErr.Code != "not_found" {
		return exitf(ExitGeneric, "This repose api does not have account secrets yet.")
	}
	return err
}

// SecretsSetGlobalCmd implements `repose secrets set --global NAME`.
func SecretsSetGlobalCmd(ctx context.Context, e *Env, name string, value []byte) error {
	if err := checkSecretName(name); err != nil {
		return err
	}
	res, err := e.Client.PutAccountSecret(ctx, name, value)
	if err != nil {
		return olderAPI(err)
	}
	_, _ = fmt.Fprintf(e.Out, "Set %s for every project; %s.\n", name, accountReach(res.Pushed))
	writeOverridden(e.Out, name, res.OverriddenBy)
	return nil
}

// accountReach says where a set landed: the running machines have it now,
// the stopped ones at their next start.
func accountReach(pushed int) string {
	switch pushed {
	case 0:
		return "each machine gets it at its next start"
	case 1:
		return "1 running machine has it now"
	}
	return fmt.Sprintf("%d running machines have it now", pushed)
}

// writeOverridden names the projects whose own secret of that name wins,
// which would otherwise read as having the account's value.
func writeOverridden(w io.Writer, name string, projects []string) {
	if len(projects) == 0 {
		return
	}
	verb, their := "keeps", "its"
	if len(projects) > 1 {
		verb, their = "keep", "their"
	}
	_, _ = fmt.Fprintf(w, "%s %s %s own %s.\n", joinNames(projects), verb, their, name)
}

// SecretsRmGlobalCmd implements `repose secrets rm --global NAME`.
func SecretsRmGlobalCmd(ctx context.Context, e *Env, name string) error {
	if err := e.Client.DeleteAccountSecret(ctx, name); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == "not_found" {
			return exitf(ExitGeneric, "Your account has no secret %s.", name)
		}
		return olderAPI(err)
	}
	_, _ = fmt.Fprintf(e.Out, "Removed %s from your account.\n", name)
	return nil
}

// SecretsListGlobalCmd implements `repose secrets list --global`.
func SecretsListGlobalCmd(ctx context.Context, e *Env) error {
	secrets, err := e.Client.ListAccountSecrets(ctx)
	if err != nil {
		return olderAPI(err)
	}
	if e.JSON {
		return writeJSONOut(e.Out, secrets)
	}
	writeAccountSecrets(e.Out, writerIsTerminal(e.Out), secrets, nil, "", true)
	return nil
}

// writeAccountSecrets prints the account's secrets: on a terminal under a
// heading, each one a project's own secret overrides marked with that
// project; piped, one "NAME\tDATE" line each, as a project's list. Alone
// (list --global) the heading says none when there are none; under a
// project's list an empty account prints nothing.
func writeAccountSecrets(w io.Writer, tty bool, secrets []SecretMeta, own map[string]bool, slug string, alone bool) {
	if tty {
		switch {
		case len(secrets) > 0:
			_, _ = fmt.Fprintln(w, "Stored by repose for every project:")
		case alone:
			_, _ = fmt.Fprintln(w, "Stored by repose for every project: none")
		}
	}
	indent := ""
	if tty {
		indent = "  "
	}
	for _, s := range secrets {
		mark := ""
		if tty && own[s.Name] {
			mark = fmt.Sprintf("\t(%s's own is used)", slug)
		}
		_, _ = fmt.Fprintf(w, "%s%s\t%s%s\n", indent, s.Name, tableTime(s.UpdatedAt), mark)
	}
}

// accountSecretsForList is the account's secrets for a project's list on
// a terminal; an api from before I-697 has none to show.
func accountSecretsForList(ctx context.Context, e *Env) ([]SecretMeta, error) {
	s, err := e.Client.ListAccountSecrets(ctx)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == 404 {
			return nil, nil
		}
		return nil, err
	}
	return s, nil
}

// accountSecretNamesForCompletion is the account's secret names for
// `secrets rm --global` (two seconds at most, a shell is waiting).
func accountSecretNamesForCompletion(env func() (*Env, error)) []string {
	e, err := env()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	secrets, err := e.Client.ListAccountSecrets(ctx)
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
