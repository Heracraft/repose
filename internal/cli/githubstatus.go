package cli

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"text/tabwriter"
	"time"

	"golang.org/x/sync/errgroup"
)

// `repose github status` (DECISIONS I-1344), which secrets.md deferred:
// what the machines actually reach GitHub with. Three answers, and the
// user has until now had to read two places to work out which they had —
// `repose secrets choose` for the login and `repose secrets list` for the
// token, in each scope.

// githubReach is what one scope's machines use for GitHub.
type githubReach int

const (
	// githubNothing: no login copied and no token. An HTTPS push then
	// meets git's username prompt (features/secrets.md, Kind 1).
	githubNothing githubReach = iota
	githubLogin               // this laptop's gh login, copied at each run
	githubToken               // the GH_TOKEN secret
)

func (r githubReach) String() string {
	switch r {
	case githubToken:
		return "GH_TOKEN"
	case githubLogin:
		return "your laptop's gh login"
	}
	return "nothing"
}

// githubRow is one line of the table: the account's default, or a project
// whose answer differs from it.
type githubRow struct {
	Scope string      `json:"project"`
	Reach githubReach `json:"-"`
	What  string      `json:"reaches_github_with"`
	// Expires is what GitHub said about this scope's token, nil when it
	// said nothing or there is no token. Only the account's is held
	// (I-1342).
	Expires *time.Time `json:"expires_at"`
}

// GithubStatusCmd implements `repose github status`.
func GithubStatusCmd(ctx context.Context, e *Env) error {
	rows, err := e.githubRows(ctx)
	if err != nil {
		return err
	}
	if e.JSON {
		return writeJSONOut(e.Out, rows)
	}
	tw := tabwriter.NewWriter(e.Out, 0, 0, 2, ' ', 0)
	// PROJECT, not SCOPE: on anything GitHub, "scope" is `repo` and
	// `read:org`, and these rows are projects (council, 2026-11-18).
	_, _ = fmt.Fprintln(tw, "PROJECT\tREACHES GITHUB WITH\tEXPIRES")
	for _, r := range rows {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Scope, r.What, githubExpiresCell(r.Expires, time.Now()))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, r := range rows {
		if l := ghExpiryLine(r.Expires, time.Now()); l != "" {
			e.warn("%s", l)
		}
	}
	return nil
}

// githubExpiresCell is the EXPIRES column: the date and how long is left,
// or "-" when nothing is known. A token GitHub said nothing about is the
// same cell as no token, because what the user can act on is the same.
func githubExpiresCell(t *time.Time, now time.Time) string {
	if t == nil {
		return "-"
	}
	d := t.Sub(now)
	if d <= 0 {
		return t.Local().Format("2006-01-02") + " (expired)"
	}
	return fmt.Sprintf("%s (in %s)", t.Local().Format("2006-01-02"), humanDuration(d))
}

// githubRows is the account's row, then one per project whose answer
// differs from it. A project that follows the account says nothing: the
// account's row already said it, and twenty rows repeating one fact are
// twenty lines nobody reads (I-485).
func (e *Env) githubRows(ctx context.Context) ([]githubRow, error) {
	account, err := e.Client.ListAccountSecrets(ctx)
	if err != nil {
		return nil, olderAPI(err)
	}
	me, err := e.Client.GetMe(ctx)
	if err != nil {
		return nil, err
	}
	e.rememberAccount(me)
	var expires *time.Time
	if me.GitHub != nil {
		expires = me.GitHub.TokenExpiresAt
	}
	accountToken := hasSecretNamed(account, githubTokenSecret)
	base := githubRow{Scope: "every project", Reach: e.reachFor("", accountToken, me)}
	if base.Reach == githubToken {
		base.Expires = expires
	}
	base.What = base.Reach.String()
	rows := []githubRow{base}

	projects, err := e.Client.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	// A project's own GH_TOKEN overrides the account's (I-697), and a
	// project's own logins list replaces the shared one (I-422), so each
	// project is asked. The reads go out together, bounded, and one that
	// fails leaves that project out rather than failing the command: the
	// account's row is the answer that matters most.
	var mu sync.Mutex
	own := map[string]bool{}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for _, p := range projects {
		p := p
		g.Go(func() error {
			ss, err := e.Client.ListSecrets(gctx, p.ID)
			if err != nil {
				return nil
			}
			mu.Lock()
			own[p.Slug] = hasSecretNamed(ss, githubTokenSecret)
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()
	var slugs []string
	for _, p := range projects {
		slugs = append(slugs, p.Slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		r := githubRow{Scope: slug, Reach: e.reachFor(slug, own[slug] || accountToken, me)}
		if r.Reach == base.Reach {
			continue
		}
		if r.Reach == githubToken && !own[slug] {
			r.Expires = expires
		}
		r.What = r.Reach.String()
		rows = append(rows, r)
	}
	return rows, nil
}

// reachFor is what one scope's machines use: the token when one reaches
// them, else the copied login when this laptop has one and nothing turns
// the copy off, else nothing.
func (e *Env) reachFor(slug string, token bool, me *Me) githubReach {
	if token {
		return githubToken
	}
	skip, _, _ := e.Cfg.loginSkip(slug)
	if skip["gh"] || !e.hasGhLogin() || !me.ghLoginCopy() {
		return githubNothing
	}
	return githubLogin
}

func hasSecretNamed(ss []SecretMeta, name string) bool {
	for _, s := range ss {
		if s.Name == name {
			return true
		}
	}
	return false
}
