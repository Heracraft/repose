package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Environment variables the CLI reads that a user may set. Every read of
// one goes through these names, and userEnvVars lists them all: each one is
// in the "Environment variables" table of apps/web/src/content/docs/cli.md,
// and docs_test.go fails when one is added here but not there, or when a
// REPOSE_* name is read anywhere in this package without being listed
// (DECISIONS I-242).
const (
	envProject         = "REPOSE_PROJECT"
	envAPIURL          = "REPOSE_API_URL"
	envTiming          = "REPOSE_TIMING"
	envNoSpinner       = "REPOSE_NO_SPINNER"
	envNoForward       = "REPOSE_NO_FORWARD"
	envNoFastPath      = "REPOSE_NO_FASTPATH"
	envNoBrowser       = "REPOSE_NO_BROWSER"
	envNoInputProxy    = "REPOSE_NO_INPUT_PROXY"    // "1": run and attach become ssh, no drops or Ctrl+V images (I-280)
	envNoClipboardPath = "REPOSE_NO_CLIPBOARD_PATH" // "1": no path on an image-only clipboard, so Cmd+V pastes nothing (I-341)
	// The =0 spellings of the two above, from before each switch was
	// spelled as a "no" set to 1 (I-621); read for a release.
	envInputProxy    = "REPOSE_INPUT_PROXY"
	envClipboardPath = "REPOSE_CLIPBOARD_PATH"
	envInGuest         = "REPOSE"                // "1" inside a repose guest: login never tries a browser there
	envXDGConfigHome   = "XDG_CONFIG_HOME"
	envClaudeConfigDir = "CLAUDE_CONFIG_DIR"
	envVisual          = "VISUAL"
	envEditor          = "EDITOR"
	envWaylandDisplay  = "WAYLAND_DISPLAY" // repose paste: read the Wayland clipboard (I-252)
	envDisplay         = "DISPLAY"         // repose paste: else the X11 one
	envReposeEditor    = "REPOSE_EDITOR"   // repose code: the editor when --editor is not given (I-282)
)

var userEnvVars = []string{
	envProject, envAPIURL, envTiming, envNoSpinner, envNoForward, envNoFastPath, envNoBrowser,
	envInGuest, envXDGConfigHome, envClaudeConfigDir, envVisual, envEditor,
	envWaylandDisplay, envDisplay, envNoInputProxy, envNoClipboardPath, envInputProxy, envClipboardPath,
	envReposeEditor,
}

// Env bundles what almost every command needs: config, the API client,
// and the laptop-local caches (docs/interfaces/cli-config.md).
type Env struct {
	// listed is the account's projects as connect's slow path read
	// them, for the laptop herdr's reconcile (I-510); nil until then.
	listed []Project
	// home caches inHome: the working directory is the home folder.
	home *bool

	Dir     string // ~/.config/repose
	Cfg     Config
	Client  *Client
	Cache   ProjectsCache
	Cwd     string
	HomeDir string
	Out     io.Writer
	ErrOut  io.Writer
	JSON    bool
	Verbose bool
	// Quiet is -q on a listing: only the names or ids, one per line, for
	// a pipe into xargs (DECISIONS I-276).
	Quiet bool
	// TTY is whether stderr is a terminal: a spinner there, plain phase
	// lines otherwise (I-154).
	TTY bool
	// Command is what the user ran ("repose attach"), for hints that
	// show the command again with a PROJECT argument; it ends in
	// " --project" for a command that takes PROJECT as that flag only
	// ("repose mcp forward --project").
	Command string

	active *progress // the command's progress display, so warnings do not tear its line

	// TargetFor builds the sshTarget for a project's slug; nil means
	// hostTarget (the real "<slug>.repose" alias). Tests point it at an
	// in-process fake guest instead.
	TargetFor func(slug string) sshTarget

	httpClient *http.Client

	// early is `run`'s probe started before the api answered (I-223), or
	// the one started the moment its guest's start finished (I-237).
	early *earlyProbe
	// guestUp, when set, is called by ensureRunningFrom the moment an op that
	// started the guest has finished, before anything else is read.
	guestUp func(p *Project)
	// personalOn is set by run when the account has a machine.nix and the
	// machine has not opted out: the tool scan leaves the laptop's
	// global tools to it (DECISIONS I-490).
	personalOn bool
}

func (e *Env) target(slug string) sshTarget {
	if e.TargetFor != nil {
		return e.TargetFor(slug)
	}
	return hostTarget(slug)
}

// newEnv builds an Env from the on-disk config and credentials. apiURLFlag
// and projectFlag are the --api-url/--project overrides ("" means unset).
func newEnv(apiURLFlag string, jsonOut, verbose bool) (*Env, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	cfg, err := loadConfig(dir)
	if err != nil {
		return nil, err
	}
	if v := os.Getenv(envAPIURL); v != "" {
		cfg.APIURL = v
	}
	if apiURLFlag != "" {
		cfg.APIURL = apiURLFlag
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cache, err := loadProjectsCache(dir)
	if err != nil {
		return nil, err
	}

	httpClient := withTiming(&http.Client{Timeout: 30 * time.Second})
	var tokens TokenSource
	if creds, ok, err := loadCredentials(dir); err == nil && ok && (creds.RefreshToken != "" || creds.AccessToken != "") {
		tokens = newOIDCTokenSource(dir, httpClient, creds)
	} else {
		tokens = notLoggedInSource{}
	}

	return &Env{
		Dir: dir, Cfg: cfg, Cache: cache, Cwd: cwd, HomeDir: home,
		Client: newClient(cfg.APIURL, tokens), Out: os.Stdout, ErrOut: os.Stderr,
		JSON: jsonOut, Verbose: verbose, httpClient: httpClient,
		TTY: isTerminal(os.Stderr),
	}, nil
}

// newProgress is the command's progress display on stderr (I-154). A
// --json command gets none: its stdout is a document, and a pipe reading
// it does not want phase lines on the terminal either.
func (e *Env) newProgress() *progress {
	if e.JSON {
		return nil
	}
	e.active = newProgress(e.ErrOut, e.TTY)
	return e.active
}

type notLoggedInSource struct{}

func (notLoggedInSource) AccessToken(context.Context, bool) (string, error) {
	return "", errors.New("not logged in")
}

// resolveArg resolves --project/$REPOSE_PROJECT plus cwd, per §5.3.
func (e *Env) resolveArg(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return os.Getenv(envProject)
}

func (e *Env) saveCache() error { return saveProjectsCache(e.Dir, e.Cache) }

// exitCodeFor maps any error this package returns to a process exit code
// and prints the right message, per docs/interfaces/cli-config.md's table.
// It is the single place main.go's error handling goes through.
func exitCodeFor(err error, stderr io.Writer) int {
	if err == nil {
		return ExitOK
	}
	var ee *exitError
	if errors.As(err, &ee) {
		if ee.msg != "" {
			_, _ = fmt.Fprintln(stderr, ee.msg)
		}
		return ee.code
	}
	var notLoggedIn *notLoggedInError
	if errors.As(err, &notLoggedIn) {
		// A refresh Logto refused (invalid_grant: the refresh token expired
		// or was revoked) is an expired login, not a missing one; a refresh
		// that never got an answer is a network problem, and "Not logged
		// in" would send the user to log in again for nothing.
		msg := notLoggedIn.Error()
		switch {
		case strings.Contains(msg, "invalid_grant"):
			_, _ = fmt.Fprintln(stderr, "Your login has expired. Run `repose login`.")
		case strings.HasPrefix(msg, "refreshing session"):
			_, _ = fmt.Fprintf(stderr, "Could not refresh your login: %v. Check your connection, or run `repose login`.\n", errors.Unwrap(notLoggedIn.cause))
		default:
			_, _ = fmt.Fprintln(stderr, "Not logged in. Run `repose login`.")
		}
		return ExitNotLoggedIn
	}
	var unreachable *unreachableError
	if errors.As(err, &unreachable) {
		_, _ = fmt.Fprintf(stderr, "Cannot reach the api: %v\n", unreachable.cause)
		return ExitGeneric
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "unauthenticated":
			_, _ = fmt.Fprintln(stderr, "Not logged in. Run `repose login`.")
			return ExitNotLoggedIn
		case "payment_required":
			_, _ = fmt.Fprintln(stderr, paymentRequiredMessage(apiErr))
			return ExitPaymentRequired
		case "capacity":
			_, _ = fmt.Fprintln(stderr, "No capacity right now; try again in a few minutes. (We have been alerted.)")
			return ExitCapacity
		case "waitlisted":
			// A first project while the fleet is near full (DECISIONS
			// I-269): the user is on the waitlist and gets an email.
			_, _ = fmt.Fprintln(stderr, waitlistedMessage(apiErr))
			return ExitCapacity
		case "invalid":
			// One wording for the project cap whichever command met it
			// (I-569); fork says the same before it snapshots.
			if have, limit, n, ok := projectLimitOf(apiErr); ok {
				_, _ = fmt.Fprintln(stderr, projectLimitMessage(have, limit, n))
				return ExitGeneric
			}
			_, _ = fmt.Fprintf(stderr, "%s: %s\n", apiErr.Code, apiErr.Message)
			return ExitGeneric
		case "rate_limited":
			// Only after the client waited out rateLimitBudget (I-187).
			_, _ = fmt.Fprintln(stderr, "The api is refusing this account's requests for now: too many in the last minute (a dashboard tab or another repose command may be polling). Try again in a minute; `repose status` shows where things stand.")
			return ExitGeneric
		default:
			_, _ = fmt.Fprintf(stderr, "%s: %s\n", apiErr.Code, apiErr.Message)
			return ExitGeneric
		}
	}
	msg := err.Error()
	if msg != "" {
		msg = strings.ToUpper(msg[:1]) + msg[1:]
	}
	_, _ = fmt.Fprintln(stderr, msg)
	return ExitGeneric
}
