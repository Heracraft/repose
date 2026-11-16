package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// `repose github setup` (DECISIONS I-1064): the machines get one GitHub
// token the user made for the repositories they chose, in place of the
// laptop's gh login, which reaches every repository they can.
//
// Why this shape. gh reads GH_TOKEN before ~/.config/gh/hosts.yml, and
// `gh auth git-credential` — the credential helper the guest's
// /etc/gitconfig names (I-526) — answers with the environment's token even
// when the machine has no gh login at all, so one named secret is the
// whole mechanism: git and gh on the machine use that token and nothing
// else. Nothing new holds a GitHub credential; the token has the homes a
// named secret has (Postgres ciphertext, the guest's tmpfs) and no other.
//
// A fine-grained token is made by hand on GitHub: there is no API that
// mints one, and a GitHub App is out (I-204, nothing on GitHub may name
// the platform). What repose can do is take the user straight to the form
// with everything but the repository list filled in: GitHub documents
// name, description, target_name, expires_in and one query parameter per
// permission. Repository selection has no parameter, so the user picks the
// repositories themselves, which is also the one choice that is theirs.
//
// Widening is another paste, not an edit. GitHub does not document editing
// a token's repository list, and the form is reported to default back to
// "All repositories", so repose never tells anyone to edit one: `repose
// github setup` again, or the agent's own `repose-ask --secret GH_TOKEN`,
// stores a new token over the old.

// githubTokenSecret is the name gh reads before its config file, so the
// token wins over any login left on the machine.
const githubTokenSecret = "GH_TOKEN"

// githubTokenFormURL is GitHub's fine-grained token form.
const githubTokenFormURL = "https://github.com/settings/personal-access-tokens/new"

// githubTokenPerms is what a machine needs to clone, push and open pull
// requests: Contents and Pull requests, write. `write` includes `read`,
// and Metadata read comes with any repository permission. Nothing else:
// not Administration (which deletes repositories and changes their
// visibility), not Repository creation, not Secrets, not Workflows.
var githubTokenPerms = map[string]string{"contents": "write", "pull_requests": "write"}

// githubTokenExpiry is the form's `expires_in`, in days: GitHub's maximum,
// so the token is renewed as rarely as the user's own policy allows. A
// token that never expires is not offered.
const githubTokenExpiry = "366"

// githubTokenName is the token's display name, the one thing on GitHub
// that says what these tokens are for. It names no platform: a token
// whose owner is an organisation is listed by name on that
// organisation's token review screen, which its owners read, and I-204
// holds there too. The form lets the user rename it.
const githubTokenName = "coding machines"

// githubTokenURL is the form, prefilled. An owner is the resource owner
// (`target_name`): the user's own account when empty. A parameter GitHub
// does not know is ignored, and the form validates what it does know, so
// a prefill can only ever save the user typing.
func githubTokenURL(owner string) string {
	v := url.Values{}
	v.Set("name", githubTokenName)
	if owner != "" {
		v.Set("target_name", owner)
	}
	v.Set("expires_in", githubTokenExpiry)
	for perm, level := range githubTokenPerms {
		v.Set(perm, level)
	}
	return githubTokenFormURL + "?" + v.Encode()
}

// githubOwnerOf is the GitHub account or organisation of a normalised
// remote ("github.com/owner/repo"), or "" for another host. The remote is
// already lowercased (I-73); GitHub's slugs are case-insensitive.
func githubOwnerOf(remote string) string {
	rest, ok := strings.CutPrefix(remote, "github.com/")
	if !ok {
		return ""
	}
	owner, _, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	return owner
}

// githubSetupOpts is `repose github setup`'s input. Token is the value
// when it came from a pipe; nil asks for it on the terminal, after naming
// the form.
type githubSetupOpts struct {
	Project string
	Owner   string
	Token   []byte
}

// GithubSetupCmd implements `repose github setup`: it names the prefilled
// form, takes the token, stores it as GH_TOKEN (the account's, or one
// project's), and leaves the laptop's gh login at home, since a broad
// token beside a narrow one protects nothing.
func GithubSetupCmd(ctx context.Context, e *Env, o githubSetupOpts) error {
	// One resolution for both halves. $REPOSE_PROJECT means the same as
	// --project everywhere else, and the secret's scope and the login's
	// have to agree: an account token with one project's row turned off
	// leaves every other project sending the broad login, which is the
	// failure this command exists to end.
	project := e.resolveArg(o.Project)
	scope, err := e.loginsScope(ctx, project)
	if err != nil {
		return err
	}
	token := o.Token
	if token == nil {
		// Before the user makes a token: a login that cannot store it
		// wastes a token they have already typed. The project path has
		// asked the api already, through loginsScope.
		if project == "" {
			if _, err := e.Client.ListAccountSecrets(ctx); err != nil {
				return olderAPI(err)
			}
		}
		owner := o.Owner
		if owner == "" {
			owner = githubOwnerOf(gitRemoteOrigin(e.Cwd))
		}
		link := githubTokenURL(owner)
		_, _ = fmt.Fprintf(e.Out, "Make a token at\n%s\n", link)
		_, _ = fmt.Fprintln(e.Out, "Pick the repositories; it needs Contents and Pull requests write.")
		if laptopOpensURLs() {
			_ = openBrowser(link)
		}
		_, _ = fmt.Fprint(e.ErrOut, "Paste the token: ")
		if token, err = readHiddenLine(); err != nil {
			return err
		}
	}
	// A paste often carries a space or a newline; a token holds neither.
	token = bytes.TrimSpace(token)
	if len(token) == 0 {
		return exitf(ExitUsage, "No token; nothing was stored.")
	}
	if !bytes.HasPrefix(token, []byte("github_pat_")) {
		e.warn("That is not a fine-grained token, so it is not limited to the repositories you picked.")
	}
	if project == "" {
		if err := SecretsSetGlobalCmd(ctx, e, githubTokenSecret, token); err != nil {
			return err
		}
	} else if err := SecretsSetCmd(ctx, e, project, githubTokenSecret, token); err != nil {
		return err
	}
	if err := e.leaveGhLogin(scope); err != nil {
		return fmt.Errorf("%w\n%s is stored; the gh login is still copied until that list says otherwise", err, githubTokenSecret)
	}
	return nil
}

// leaveGhLogin turns the gh row off for the scope `github setup` stored
// the token for, and says so. A scope that already leaves it says
// nothing: there is nothing to tell.
func (e *Env) leaveGhLogin(scope loginsScope) error {
	skip, _ := scope.current(e.Cfg)
	if skip["gh"] {
		return nil
	}
	skip["gh"] = true
	var off []string
	for _, n := range loginNames() {
		if skip[n] {
			off = append(off, n)
		}
	}
	if err := writeLoginsTable(e.Dir, scope, off, false); err != nil {
		return err
	}
	where := "every project"
	if scope.Slug != "" {
		where = scope.Slug
	}
	_, _ = fmt.Fprintf(e.Out, "Your gh login stays on your laptop, for %s.\n", where)
	e.warnProjectsStillCopyingGh(scope)
	return nil
}

// warnProjectsStillCopyingGh names the projects with a list of their own
// that still copies the gh login. A list of one's own replaces the shared
// one (I-422), so the table this command wrote does not reach them, and
// their machines keep a hosts.yml an agent can read whatever GH_TOKEN
// says.
func (e *Env) warnProjectsStillCopyingGh(scope loginsScope) {
	if scope.Slug != "" {
		return
	}
	var still []string
	for slug, p := range e.Cfg.Projects {
		if p.Logins.Skip == nil {
			continue
		}
		if skip, _, _ := e.Cfg.loginSkip(slug); !skip["gh"] {
			still = append(still, slug)
		}
	}
	if len(still) == 0 {
		return
	}
	sort.Strings(still)
	verb, own, copies := "has", "a list of its own", "copies"
	if len(still) > 1 {
		verb, own, copies = "have", "lists of their own", "copy"
	}
	e.warn("%s %s %s that still %s the gh login; run this with --project to cover it.", joinNames(still), verb, own, copies)
}
