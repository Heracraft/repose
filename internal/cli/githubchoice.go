package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"
)

// The GitHub choice (DECISIONS I-1338). Until now a laptop with a gh
// login copied it into every machine unless the user found `repose
// secrets choose` or `repose github setup` (I-422, I-1064), so the
// broadest credential a developer owns was the default and the narrow one
// was the thing you had to go looking for. The first `repose run` on a
// laptop that has a gh login and has chosen nothing asks, once.
//
// The answer lives where I-422 put it: `[logins] skip` in the laptop's
// config.toml. An explicit list is the record that a choice was made, so
// "copy it" writes an empty one rather than leaving the table out, and
// `repose secrets choose --reset` means "ask me again".

// githubChoice is what the user pressed.
type githubChoice byte

const (
	githubChoiceNone  githubChoice = 0   // Enter, q, Esc, EOF: ask again next time
	githubChoiceCopy  githubChoice = 'c' // copy the gh login, as before
	githubChoiceToken githubChoice = 't' // a token for the repositories they pick
	githubChoiceNo    githubChoice = 'n' // neither
)

// githubChoiceLines is the question and its three answers. It names what
// each answer costs, since that is the whole of the decision.
// Every line fits the 62 columns the docs' code blocks hold, so what the
// public page shows is what the terminal prints (apps/web docs.test.ts).
const githubChoiceLines = "This laptop has a GitHub CLI login.\n" +
	"What should your machines use for GitHub?\n" +
	"  c  copy this laptop's gh login: every repo, kept on disk\n" +
	"  t  a token for the repos you pick, made on github.com\n" +
	"  n  neither: nothing on the machine can push to GitHub\n" +
	"Choose c, t or n, or Enter to copy it now and be asked again: "

// githubChoiceMenu prints the question on w and reads one key from r,
// which the caller has put in raw mode. Every line ends CR LF, as the
// overlap menu's do (I-1100): raw mode turns off the terminal's own
// CR-for-LF. The key is echoed. Enter, q, Esc and the input ending leave
// the choice unmade; Ctrl-C is errPromptInterrupted.
func githubChoiceMenu(r io.Reader, w io.Writer) (githubChoice, error) {
	_, _ = io.WriteString(w, strings.ReplaceAll(githubChoiceLines, "\n", "\r\n"))
	br := bufio.NewReader(r)
	for {
		c, err := br.ReadByte()
		if err != nil {
			_, _ = io.WriteString(w, "\r\n")
			return githubChoiceNone, nil
		}
		switch c {
		case 'c', 'C', 't', 'T', 'n', 'N':
			_, _ = fmt.Fprintf(w, "%c\r\n", c)
			return githubChoice(c | 0x20), nil
		case 0x1b:
			// An arrow, Insert or F-key sends ESC and the rest of its
			// sequence in one read: skip it whole (ENG-192). An Esc with
			// nothing after it is the key itself.
			if br.Buffered() > 0 && skipEscapeSequence(br) {
				continue
			}
			_, _ = io.WriteString(w, "\r\n")
			return githubChoiceNone, nil
		case '\r', '\n', 'q', 'Q':
			_, _ = io.WriteString(w, "\r\n")
			return githubChoiceNone, nil
		case 0x03:
			_, _ = io.WriteString(w, "\r\n")
			return githubChoiceNone, errPromptInterrupted
		}
	}
}

// askGithubChoiceTTY runs the menu on the real terminal; tests replace it.
var askGithubChoiceTTY = func(w io.Writer) (githubChoice, error) {
	fd := int(os.Stdin.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		// No raw mode, no one-key menu: nothing is asked and the run
		// copies as it always has.
		return githubChoiceNone, nil
	}
	ch, err := githubChoiceMenu(os.Stdin, w)
	_ = term.Restore(fd, state)
	return ch, err
}

// canAskGithubChoice is whether there is a terminal to ask on: a real one
// on both stdin and stderr, as the overlap menu asks (I-1100). Tests
// replace it.
var canAskGithubChoice = func() bool { return canPrompt(os.Stdin) && isatty(os.Stderr) }

// askGithubChoice is the first-run question (I-1338), asked before any
// login is copied and only when there is something to ask about: a
// laptop with a gh login, an account that has not turned the copy off,
// no choice in config.toml, and a terminal. It never fails a run: a
// question that cannot be asked or answered leaves everything as it was.
func (e *Env) askGithubChoice(ctx context.Context) error {
	if e.JSON || !e.ghChoiceWanted() || !canAskGithubChoice() {
		return nil
	}
	// An account that has turned the copy off has answered for every
	// laptop (I-1340): offering `c` here would print "copied" for a login
	// that is not, which is the wrong line for a credential.
	if !e.accountGhLoginCopy(ctx) {
		return nil
	}
	ch, err := askGithubChoiceTTY(e.ErrOut)
	if err != nil {
		return err
	}
	switch ch {
	case githubChoiceCopy:
		// An empty list is the record that the question was answered;
		// nothing about what is copied changes. saveLoginSkip's own line
		// is about every login, and this answer was about one, so the
		// table is written here and the line is this one.
		if err = writeLoginsTable(e.Dir, loginsScope{}, nil, false); err == nil {
			_, _ = fmt.Fprintln(e.Out, "Saved: this laptop's gh login is copied, for every project.")
		}
	case githubChoiceNo:
		err = e.saveLoginSkip(loginsScope{}, []string{"gh"})
	case githubChoiceToken:
		// setup stores the token and turns the gh row off itself, for
		// the account and never for one project: the choice is the
		// laptop's, and $REPOSE_PROJECT must not narrow it.
		if err = GithubSetupCmd(ctx, e, githubSetupOpts{Global: true}); err != nil {
			// A paste nobody made, or a server that refused it, is not a
			// reason to end the run: nothing was changed, and the
			// question comes back next time.
			if errors.Is(err, errPromptInterrupted) {
				return err
			}
			e.warn("%s", oneLine(err.Error()))
			return nil
		}
	default:
		return nil
	}
	if err != nil {
		return err
	}
	// This run is the one the answer was given on, so it is the one that
	// must obey it: the config in memory was read before the question.
	// Without this the run that heard "no" still copied the login, which
	// is what the question was asked to stop (seen live, 2026-11-18).
	if cfg, cerr := loadConfig(e.Dir); cerr == nil {
		e.Cfg = cfg
	}
	return nil
}

// ghChoiceWanted is whether the first-run question has anything to ask:
// this laptop has a gh login, and no list in config.toml has recorded a
// choice for it. It reads no network.
func (e *Env) ghChoiceWanted() bool {
	if _, own := (loginsScope{}).current(e.Cfg); own {
		return false
	}
	// A project with a list of its own has chosen for itself; the
	// question is the laptop's, so only the shared list silences it.
	return e.hasGhLogin()
}

// hasGhLogin is whether this laptop has a GitHub CLI login to copy.
func (e *Env) hasGhLogin() bool {
	for _, r := range credRows {
		if r.Label != "gh" {
			continue
		}
		_, err := os.Stat(filepath.Join(e.HomeDir, r.laptopRel()))
		return err == nil
	}
	return false
}

// --- the account's answer, cached ------------------------------------

// accountFileName is where the laptop keeps what the account last said
// about GitHub (cli-config.md): the switch a dashboard flipped and the
// token's expiry, so a run that makes no api call of its own still
// honours them.
const accountFileName = "account.json"

// accountTTL is how long the cached answer is used before a command that
// would copy the gh login reads the account again. Short enough that a
// switch flipped in the dashboard reaches every laptop within the hour,
// long enough that a run in a loop costs no api call.
const accountTTL = time.Hour

// accountCache is accountFileName.
type accountCache struct {
	// API is the server this answer came from, so a laptop pointed at
	// staging never applies the production account's switch, and the
	// other way round.
	API string `json:"api,omitempty"`
	// Account is the user id it was read for; `repose login` and `repose
	// logout` remove this file, so a laptop that changed accounts has no
	// cache to apply. An id, and no credential.
	Account string `json:"account,omitempty"`
	// WarnedAt is when this laptop last printed the expiry warning on a
	// run (I-1342), so a token's last week is seven lines and not one
	// per run.
	WarnedAt time.Time `json:"warned_at"`
	// GhLoginCopy is the account's switch (I-1340); a cache that has
	// never been written says nothing, and nothing means yes.
	GhLoginCopy *bool `json:"gh_login_copy,omitempty"`
	// GhTokenExpiresAt is what GitHub said about the account's GH_TOKEN.
	GhTokenExpiresAt *time.Time `json:"gh_token_expires_at,omitempty"`
	ReadAt           time.Time  `json:"read_at"`
}

// fresh reports whether the cache is this server's and young enough to
// use. A ReadAt in the future is a clock that moved, not a fresh read.
func (a accountCache) fresh(apiURL string, now time.Time) bool {
	if a.GhLoginCopy == nil || a.API != apiURL {
		return false
	}
	d := now.Sub(a.ReadAt)
	return d >= 0 && d < accountTTL
}

func accountCachePath(dir string) string { return filepath.Join(dir, accountFileName) }

// loadAccountCache reads it; a missing or damaged file is an empty one.
func loadAccountCache(dir string) accountCache {
	var a accountCache
	if dir == "" {
		return a
	}
	if b, err := os.ReadFile(accountCachePath(dir)); err == nil {
		_ = json.Unmarshal(b, &a)
	}
	return a
}

// rememberAccount writes what GET /me said, best effort: a failed write
// costs one api call next time.
func (e *Env) rememberAccount(me *Me) {
	if me == nil || e.Dir == "" {
		return
	}
	prev := loadAccountCache(e.Dir)
	a := accountCache{ReadAt: time.Now(), API: e.Cfg.APIURL, Account: me.ID, WarnedAt: prev.WarnedAt}
	if prev.Account != me.ID || prev.API != e.Cfg.APIURL {
		a.WarnedAt = time.Time{} // another account's warning is not this one's
	}
	copyOK := me.ghLoginCopy()
	a.GhLoginCopy = &copyOK
	if me.GitHub != nil {
		a.GhTokenExpiresAt = me.GitHub.TokenExpiresAt
	}
	if b, err := json.Marshal(a); err == nil {
		_ = writeFileAtomic(accountCachePath(e.Dir), b, 0o600)
	}
}

// accountGhLoginCopy is whether the account lets this laptop copy its gh
// login, read from the cache and refreshed when the cache is older than
// accountTTL. Every failure answers yes: the switch subtracts from what
// the laptop would do, and a laptop that cannot reach the api must not
// start behaving differently because of it.
func (e *Env) accountGhLoginCopy(ctx context.Context) bool {
	a := loadAccountCache(e.Dir)
	if a.fresh(e.Cfg.APIURL, time.Now()) {
		return *a.GhLoginCopy
	}
	if e.Client == nil {
		return true // no account to ask: copy, as before
	}
	me, err := e.Client.GetMe(ctx)
	if err != nil {
		if a.GhLoginCopy != nil && a.API == e.Cfg.APIURL {
			return *a.GhLoginCopy // the last answer, however old
		}
		return true
	}
	e.rememberAccount(me)
	return me.ghLoginCopy()
}

// forgetAccount removes the cached answer, which `login` and `logout`
// do: the next account's answer is not this one's (I-1340).
func forgetAccount(dir string) {
	if dir != "" {
		_ = os.Remove(accountCachePath(dir))
	}
}

// ghExpiryWarnOnce is the expiry warning for a run: the line when this
// laptop has not printed it today, "" otherwise. A token's last week is
// seven lines, not one per run of a day's work.
func (e *Env) ghExpiryWarnOnce(now time.Time) string {
	a := loadAccountCache(e.Dir)
	l := ghExpiryLine(a.GhTokenExpiresAt, now)
	if l == "" || now.Sub(a.WarnedAt) < 24*time.Hour {
		return ""
	}
	a.WarnedAt = now
	if b, err := json.Marshal(a); err == nil {
		_ = writeFileAtomic(accountCachePath(e.Dir), b, 0o600)
	}
	return l
}

// --- what GitHub says about a token ----------------------------------

// githubExpiryHeader is the header GitHub answers with a token's
// expiration. It is documented for classic tokens; for a fine-grained
// one it is read and used only when it parses to a time ahead of now
// (I-1342), so a GitHub that says nothing useful costs nothing.
const githubExpiryHeader = "github-authentication-token-expiration"

// githubAPIRoot needs no scope at all, so a fine-grained token with only
// Contents and Pull requests still gets an answer.
const githubAPIRoot = "https://api.github.com/"

// githubExpiryClient is the HTTP client the expiry read uses; a test
// replaces it. The call is the laptop's own, to GitHub, with the token
// the user has just pasted: the api never sees either.
// A redirect is refused rather than followed: Go keeps the Authorization
// header across a redirect to the same parent domain, and the token has
// no business anywhere but api.github.com.
var githubExpiryClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// tokenExpiry asks GitHub when this token expires. It returns nil for
// anything it cannot read, which is never an error the user hears about:
// the expiry is a warning's input, not a credential's validity.
func tokenExpiry(ctx context.Context, token []byte) *time.Time {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIRoot, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+string(token))
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := githubExpiryClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<10))
	return parseGithubExpiry(res.Header.Get(githubExpiryHeader), time.Now())
}

// parseGithubExpiry reads the header's time. GitHub writes it as
// "2026-11-25 10:00:00 UTC" or with a numeric zone; anything else, and
// anything not ahead of now, is no answer.
func parseGithubExpiry(v string, now time.Time) *time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05 MST", "2006-01-02 15:04:05 -0700", time.RFC3339} {
		t, err := time.Parse(layout, v)
		if err != nil || !t.After(now) {
			continue
		}
		u := t.UTC()
		return &u
	}
	return nil
}

// ghExpiryWarnWithin is how long before a token's expiry the warning
// starts: a week, which is time to make another before an agent working
// overnight meets a 401.
const ghExpiryWarnWithin = 7 * 24 * time.Hour

// ghExpiryLine is the one-line warning for a token near its end, "" when
// there is nothing to warn about. An expiry already past says so too:
// the token is dead and the agent's next push is the proof.
//
// It names no command (I-484): the line prints under `repose github`,
// where `setup` is the next thing in the same help, and the fact is the
// whole of what the user cannot work out for themselves.
func ghExpiryLine(expires *time.Time, now time.Time) string {
	if expires == nil {
		return ""
	}
	left := expires.Sub(now)
	switch {
	case left <= 0:
		return "Your GitHub token expired " + humanDuration(-left) + " ago."
	case left <= ghExpiryWarnWithin:
		return "Your GitHub token expires in " + humanDuration(left) + "."
	}
	return ""
}

// skipEscapeSequence reads the rest of an escape sequence after its ESC:
// CSI (`[`, parameter and intermediate bytes, then one final byte in
// 0x40-0x7e) or SS3 (`O` and one byte). It reports false, reading
// nothing, when the next byte starts neither, so a bare Esc stays Esc.
func skipEscapeSequence(br *bufio.Reader) bool {
	next, err := br.Peek(1)
	if err != nil {
		return false
	}
	switch next[0] {
	case 'O':
		_, _ = br.ReadByte()
		_, _ = br.ReadByte()
		return true
	case '[':
		_, _ = br.ReadByte()
		for {
			c, err := br.ReadByte()
			if err != nil || (c >= 0x40 && c <= 0x7e) {
				return true
			}
		}
	}
	return false
}
