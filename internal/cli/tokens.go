package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// API tokens (DECISIONS I-1314..I-1320): `repose tokens create|ls|rm`
// mint, list and end the tokens an agent or a script calls the api
// with, and REPOSE_TOKEN makes this CLI one such caller.

// tokenScopes is the six scopes and one line each, the api's
// tokens.Means word for word; the CLI carries its own copy because it
// does not import the api.
var tokenScopes = []struct{ name, means string }{
	{"read", "every project's status, events, questions, triggers and settings"},
	{"projects", "create, start, stop, resize, snapshot, restore and remove projects"},
	{"config", "change machine.nix and a project's config"},
	{"secrets", "set and remove secrets (never read a value)"},
	{"triggers", "make, change, fire and remove triggers and webhook routes"},
	{"agents", "message an agent and answer its questions"},
}

// tokenScopeLines is the scope table as `repose tokens create --help`
// prints it under its Long text, one line each, so cobra's flag column
// does not wrap it.
func tokenScopeLines() string {
	var b strings.Builder
	for _, s := range tokenScopes {
		fmt.Fprintf(&b, "\n  %-9s %s", s.name, s.means)
	}
	return b.String()
}

var tokenNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func checkTokenName(name string) error {
	if !tokenNameRe.MatchString(name) {
		return exitf(ExitUsage, "NAME must match [a-z0-9][a-z0-9-]{0,31}, got %q", name)
	}
	return nil
}

// parseTokenScopes takes the --scope values, each a name or a
// comma-separated list, and answers them deduped in the table's order.
func parseTokenScopes(flags []string) ([]string, error) {
	seen := map[string]bool{}
	for _, f := range flags {
		for _, s := range strings.Split(f, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			known := false
			for _, k := range tokenScopes {
				if k.name == s {
					known = true
				}
			}
			if !known {
				var names []string
				for _, k := range tokenScopes {
					names = append(names, k.name)
				}
				return nil, cobraUsageError{fmt.Errorf("--scope %s: a scope is one of %s", s, strings.Join(names, ", "))}
			}
			seen[s] = true
		}
	}
	var out []string
	for _, k := range tokenScopes {
		if seen[k.name] {
			out = append(out, k.name)
		}
	}
	if len(out) == 0 {
		return nil, cobraUsageError{errors.New("--scope is required: at least one of read, projects, config, secrets, triggers, agents")}
	}
	return out, nil
}

// parseTokenExpiry is --expires: a number of days (`30`, `30d`), with
// 1 to 365 the api's bounds.
func parseTokenExpiry(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
	if err != nil || n < 1 || n > 365 {
		return 0, cobraUsageError{fmt.Errorf("--expires %s: a number of days, 1 to 365", s)}
	}
	return n, nil
}

// TokensCreateCmd implements `repose tokens create NAME`. The token is
// the whole of stdout, so it pipes; where it lands is said on stderr.
func TokensCreateCmd(ctx context.Context, e *Env, name string, scopeFlags []string, projectArg, expires string) error {
	if err := checkTokenName(name); err != nil {
		return err
	}
	scopes, err := parseTokenScopes(scopeFlags)
	if err != nil {
		return err
	}
	days, err := parseTokenExpiry(expires)
	if err != nil {
		return err
	}
	req := CreateTokenRequest{Name: name, Scopes: scopes, ExpiresInDays: days}
	var bound *Project
	// --project or $REPOSE_PROJECT, never the folder: a token is the
	// account's unless the user names a project.
	if projectArg = e.resolveArg(projectArg); projectArg != "" {
		p, err := requireProject(ctx, e, projectArg)
		if err != nil {
			return err
		}
		req.ProjectID = p.ID
		bound = p
	}
	t, err := e.Client.CreateToken(ctx, req)
	if err != nil {
		return tokenErr(err, name)
	}
	if e.JSON {
		return writeJSONOut(e.Out, t)
	}
	_, _ = fmt.Fprintln(e.Out, t.Token)
	// The one line that prevents a loss the user cannot undo: the token
	// is not shown again.
	reach := "all projects"
	if bound != nil {
		reach = bound.Slug + " only"
	}
	_, _ = fmt.Fprintf(e.ErrOut, "%s: %s, %s, until %s. Not shown again.\n", name, strings.Join(scopes, ", "), reach, t.ExpiresAt.Local().Format("2006-01-02"))
	return nil
}

// TokensListCmd implements `repose tokens` and `repose tokens ls`.
func TokensListCmd(ctx context.Context, e *Env) error {
	list, err := e.Client.ListTokens(ctx)
	if err != nil {
		return tokenErr(err, "")
	}
	if e.JSON {
		return writeJSONOut(e.Out, list)
	}
	if e.Quiet {
		for _, t := range list {
			if t.RevokedAt == nil {
				_, _ = fmt.Fprintln(e.Out, t.Name)
			}
		}
		return nil
	}
	if len(list) == 0 {
		_, _ = fmt.Fprintln(e.Out, "No API tokens.")
		return nil
	}
	sort.SliceStable(list, func(i, j int) bool {
		if (list[i].RevokedAt == nil) != (list[j].RevokedAt == nil) {
			return list[i].RevokedAt == nil
		}
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})
	tw := tabwriter.NewWriter(e.Out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tSCOPES\tPROJECT\tEXPIRES\tLAST USED")
	for _, t := range list {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", t.Name, strings.Join(t.Scopes, ","), tokenProject(t), tokenExpires(t), tokenUsed(t))
	}
	return tw.Flush()
}

func tokenProject(t APIToken) string {
	if t.Project == nil {
		return "all"
	}
	if t.Project.Slug != nil {
		return *t.Project.Slug
	}
	return t.Project.ID
}

// tokenExpires is the end as a date, or the state that ended it first.
func tokenExpires(t APIToken) string {
	switch {
	case t.RevokedAt != nil:
		return "revoked " + humanAge(*t.RevokedAt)
	case !t.ExpiresAt.After(time.Now()):
		return "expired " + humanAge(t.ExpiresAt)
	}
	return t.ExpiresAt.Local().Format("2006-01-02")
}

func tokenUsed(t APIToken) string {
	if t.LastUsedAt == nil {
		return "never"
	}
	return humanAge(*t.LastUsedAt)
}

// TokensRmCmd implements `repose tokens rm NAME...`.
func TokensRmCmd(ctx context.Context, e *Env, names []string) error {
	for _, name := range names {
		if err := checkTokenName(name); err != nil {
			return err
		}
	}
	for _, name := range names {
		if err := e.Client.DeleteToken(ctx, name); err != nil {
			return tokenErr(err, name)
		}
	}
	return nil
}

// tokenErr turns the api's answers on the token routes into the line
// the user reads: the api from before I-1314 (no route), a name that is
// taken or unknown, and a call made with a token, which these routes
// refuse.
func tokenErr(err error, name string) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch {
	case apiErr.Status == 404 && apiErr.Code != "not_found":
		return exitf(ExitGeneric, "This repose api does not have API tokens yet.")
	case apiErr.Code == "not_found" && name != "":
		return exitf(ExitGeneric, "You have no token named %s.", name)
	case apiErr.Code == "conflict" && name != "":
		return exitf(ExitGeneric, "You already have a token named %s; `repose tokens rm %s` first.", name, name)
	case apiErr.Code == "forbidden" && apiErr.Detail["reason"] == "token_never":
		return exitf(ExitGeneric, "repose tokens takes a login, not REPOSE_TOKEN.")
	}
	return err
}
