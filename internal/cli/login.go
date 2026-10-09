package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// loginOptions configures runLogin so tests can stub the browser and the
// clock; the real command builds this from cobra flags and the OS.
type loginOptions struct {
	// Browser asks for the authorization-code-with-PKCE loopback flow. Off
	// by default since v0.1.2 (DECISIONS I-101): the Logto application is a
	// Native app with device flow and no registered redirect URIs, so the
	// loopback flow ends in oidc.invalid_redirect_uri.
	Browser   bool
	NoBrowser bool
	Display   string // GOOS "" means "no DISPLAY env"; darwin/windows ignore it
	GOOS      string
	GuestEnv  bool // REPOSE=1
	Stdout    *os.File
	// Open opens a URL in a browser; nil means openBrowser. Tests replace
	// it with a stub that hits the loopback callback directly instead of
	// launching a real browser.
	Open func(string) error
}

func runLogin(ctx context.Context, dir string, cfg Config, httpClient *http.Client, opts loginOptions) error {
	doc, err := discover(ctx, httpClient, dir, cfg.LogtoIssuer)
	if err != nil {
		return loginFailed(err)
	}

	open := opts.Open
	if open == nil {
		open = openBrowser
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	canOpen := browserAvailable(opts.NoBrowser, opts.GuestEnv, opts.Display, opts.GOOS)
	var tr *tokenResponse
	if opts.Browser && canOpen {
		tr, err = loginPKCE(ctx, httpClient, doc, cfg.LogtoClientID, open)
	} else {
		// The device code is printed, and opened in this computer's
		// browser when it has one (DECISIONS I-627): the page is the same
		// one the link leads to, so a failed open costs nothing.
		tr, err = loginDeviceCode(ctx, httpClient, doc, cfg.LogtoClientID, func(da deviceAuthResponse) {
			_, _ = fmt.Fprintln(stdout, deviceInstructions(da))
			if canOpen {
				u := da.VerificationURIComplete
				if u == "" {
					u = da.VerificationURI
				}
				_ = open(u)
			}
		})
	}
	if err != nil {
		return loginFailed(err)
	}

	creds := Credentials{
		RefreshToken:  tr.RefreshToken,
		AccessToken:   tr.AccessToken,
		ExpiresAt:     time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
		LogtoIssuer:   cfg.LogtoIssuer,
		LogtoClientID: cfg.LogtoClientID,
	}
	if err := saveCredentials(dir, creds); err != nil {
		return err
	}

	client := newClient(cfg.APIURL, staticToken(tr.AccessToken))
	client.HTTP = httpClient
	me, err := client.GetMe(ctx)
	if err != nil {
		return exitf(ExitGeneric, "Logged in, but could not fetch your account: %v", err)
	}
	_, _ = fmt.Fprintf(stdout, "Logged in as %s (%s)\n", me.Handle, me.Email)
	if me.Billing.Status == "none" || (me.Billing.Status == "" && !me.Billing.HasCard) {
		_, _ = fmt.Fprintf(stdout, "No plan yet: %s\n", billingURL)
	}
	if !opts.GuestEnv {
		setUpPlainSSH()
	}
	return nil
}

// loginFailed is a failed login flow: the login server out of reach is a
// network problem; a refusal (the code expired, the user declined) is
// the login's.
func loginFailed(err error) error {
	var lu *loginUnreachableError
	if errors.As(err, &lu) {
		return exitf(ExitGeneric, "%s", lu.Error())
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return exitf(ExitNotLoggedIn, "Login failed: %v.", strings.TrimSuffix(err.Error(), "."))
}

// loginStatus is `repose login --status` (DECISIONS I-627): the account
// this laptop is logged in as, read from the api so a revoked or expired
// login shows as one, and nothing else. Exit 3 when there is none.
func loginStatus(ctx context.Context, e *Env) error {
	if _, ok := e.Client.Tokens.(notLoggedInSource); ok {
		return exitf(ExitNotLoggedIn, "Not logged in.")
	}
	me, err := e.Client.GetMe(ctx)
	if err != nil {
		return err
	}
	plan := "no plan"
	if me.Billing.Plan != nil && *me.Billing.Plan != "" {
		plan = planTitle(*me.Billing.Plan) + " plan"
		if me.Billing.Status != "" && me.Billing.Status != "active" && me.Billing.Status != "exempt" {
			plan += " (" + strings.ReplaceAll(me.Billing.Status, "_", " ") + ")"
		}
	}
	_, _ = fmt.Fprintf(e.Out, "%s (%s) on %s, %s\n", me.Handle, me.Email, hostOf(e.Cfg.APIURL), plan)
	return nil
}

func planTitle(p string) string {
	if p == "" {
		return p
	}
	return strings.ToUpper(p[:1]) + p[1:]
}

// hostOf is a URL's host, or the URL when it has none.
func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

// setUpPlainSSH writes ~/.ssh/repose/config and the Include line at login,
// so `ssh <project>.repose` works for every project straight after it,
// before any `repose run` (I-281): the first ssh writes the certificate
// and the Host block. A failure is a warning; run and attach try again.
func setUpPlainSSH() {
	sd, err := sshDir()
	if err == nil {
		err = writeSSHEntry(sd)
	}
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "warning: could not write ~/.ssh/repose/config (%v); `repose run` tries again.\n", err)
		return
	}
	usc, err := userSSHConfig()
	if err != nil {
		return
	}
	if err := ensureIncludeLine(usc); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "warning: %s\n", includeProblemText(usc, err))
	}
}

func runLogout(ctx context.Context, dir string, cfg Config, httpClient *http.Client, purge bool, out io.Writer) error {
	creds, ok, err := loadCredentials(dir)
	if err != nil {
		return err
	}
	// Revoking the SSH certificates is what makes a logout after a lost
	// laptop mean something, so its result is said (DECISIONS I-627).
	// Logto's own refresh-token revocation is not called (not part of the
	// discovery document this CLI reads); an un-revoked refresh token
	// expires on its own.
	var revokeErr error
	if ok && (creds.AccessToken != "" || creds.RefreshToken != "") {
		client := newClient(cfg.APIURL, newOIDCTokenSource(dir, httpClient, creds))
		client.HTTP = httpClient
		revokeErr = client.RevokeCertsAll(ctx)
	}
	if err := deleteCredentials(dir); err != nil {
		return err
	}
	sd, err := sshDir()
	if err != nil {
		return err
	}
	_ = os.Remove(sd + "/id_ed25519-cert.pub")
	if purge {
		if err := purgeCLIFiles(dir, sd); err != nil {
			return err
		}
	}
	switch {
	case !ok:
		_, _ = fmt.Fprintln(out, "Not logged in.")
	case revokeErr != nil:
		return exitf(ExitGeneric, "Logged out on this laptop, but the SSH certificates were not revoked: %s They stop working within 24 hours.", revokeReason(revokeErr))
	default:
		_, _ = fmt.Fprintln(out, "Logged out. Your SSH certificates are revoked; connections they opened, on any device, close within 30 seconds.")
	}
	return nil
}

// revokeReason is why a revoke failed, as a sentence. A "try again"
// is dropped: with the login gone there is nothing to try again with.
func revokeReason(err error) string {
	var b strings.Builder
	_ = exitCodeFor(err, &b)
	s := strings.TrimSpace(b.String())
	if i := strings.Index(s, " Try again"); i > 0 {
		s = s[:i]
	}
	return s
}
