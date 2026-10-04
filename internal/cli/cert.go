package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// certReuseMargin is how much validity a certificate on disk must have
// left to be used for a new connection. The gateway ends a connection when
// its certificate expires (I-436), so this is also the shortest life a
// connection opened by the CLI can have: half the 24-hour lifetime.
const certReuseMargin = 12 * time.Hour

// The CLI's own key pair, ~/.ssh/repose/id_ed25519 (DECISIONS I-149). It
// is generated here, has no passphrase, and is used for nothing but the
// repose certificate, so an ssh to a guest never prompts and the user's
// own keys (~/.ssh/id_*) are never read, written or offered. v0.1.4 and
// earlier certified ~/.ssh/id_ed25519 instead; a certificate for that key
// is detected in ensureCert and re-issued for this one.
const reposeKeyName = "id_ed25519"

func reposeKeyPath() (string, error) {
	sd, err := sshDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(sd, reposeKeyName), nil
}

// ensureReposeKey makes sure the CLI's key pair exists, generating it in
// process (no ssh-keygen needed) with mode 0600 for the private half. It
// returns the public key.
func ensureReposeKey() (ssh.PublicKey, error) {
	priv, err := reposeKeyPath()
	if err != nil {
		return nil, err
	}
	pubPath := priv + ".pub"
	if b, err := os.ReadFile(priv); err == nil {
		signer, err := ssh.ParsePrivateKey(b)
		if err != nil {
			return nil, fmt.Errorf("%s is not a usable key (%v); delete it and run the command again to make a new one", priv, err)
		}
		// Tighten a mode someone loosened: ssh refuses a private key
		// others can read, which would surface as a confusing prompt.
		if info, err := os.Stat(priv); err == nil && info.Mode().Perm()&0o077 != 0 {
			_ = os.Chmod(priv, 0o600)
		}
		if _, err := os.Stat(pubPath); err != nil {
			_ = writeFileAtomic(pubPath, ssh.MarshalAuthorizedKey(signer.PublicKey()), 0o644)
		}
		return signer.PublicKey(), nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	pk, sk, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(sk, "repose")
	if err != nil {
		return nil, err
	}
	pub, err := ssh.NewPublicKey(pk)
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(priv, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	line := bytes.TrimSpace(ssh.MarshalAuthorizedKey(pub))
	if err := writeFileAtomic(pubPath, append(line, []byte(" repose\n")...), 0o644); err != nil {
		return nil, err
	}
	return pub, nil
}

// parseCertFile reads an OpenSSH certificate written by a previous
// ensureCert. A missing or unparsable file is not an error: it just means
// there is nothing to reuse.
func parseCertFile(path string) *ssh.Certificate {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(b)
	if err != nil {
		return nil
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return nil
	}
	return cert
}

// certUsableFor reports whether cert is valid for at least margin more and
// carries every id in want as a principal.
func certUsableFor(cert *ssh.Certificate, want []string, now time.Time, margin time.Duration) bool {
	if cert == nil {
		return false
	}
	if time.Unix(int64(cert.ValidBefore), 0).Sub(now) <= margin {
		return false
	}
	have := map[string]bool{}
	for _, p := range cert.ValidPrincipals {
		have[p] = true
	}
	for _, id := range want {
		if !have[id] {
			return false
		}
	}
	return true
}

// certIsFor reports whether cert certifies key: a certificate for the
// user's own ~/.ssh/id_ed25519 (what v0.1.4 wrote) is not.
func certIsFor(cert *ssh.Certificate, key ssh.PublicKey) bool {
	return cert != nil && key != nil && bytes.Equal(cert.Key.Marshal(), key.Marshal())
}

// certParams is what ensureCert needs about the account to write the SSH
// config and known_hosts.
type certParams struct {
	Handle   string
	Projects []Project // every project the user has; the cert covers all of them
	// Force re-issues even when the certificate on disk looks usable: the
	// gateway refused it (revoked, or the CA rotated).
	Force bool
	// CheckAlias runs `ssh -G <slug>.repose` after writing the files to
	// prove the Include line works (I-151). Tests that point ssh at a
	// fake guest leave it off.
	CheckAlias bool
}

// certResult is what ensureCert hands back to the command.
type certResult struct {
	Path string
	// AliasProblem is non-empty when `<slug>.repose` does not resolve to
	// the gateway through ~/.ssh/config: what is wrong and exactly how
	// to fix it. The CLI's own connections still work (they fall back to
	// -F), so it is a warning, not an error.
	AliasProblem string
}

// ensureCert implements 07-cli.md §5.4: reuse a valid certificate for the
// CLI's own key, else issue a new one covering every project the user
// has, write it and the known_hosts and ssh config files, and register
// the Include line.
func ensureCert(ctx context.Context, client *Client, params certParams, now func() time.Time) (*certResult, error) {
	if now == nil {
		now = time.Now
	}
	sd, err := sshDir()
	if err != nil {
		return nil, err
	}
	certPath := filepath.Join(sd, reposeKeyName+"-cert.pub")

	ids := make([]string, 0, len(params.Projects))
	for _, p := range params.Projects {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)

	pub, err := ensureReposeKey()
	if err != nil {
		return nil, err
	}
	pubLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))

	existing := parseCertFile(certPath)
	if !certIsFor(existing, pub) {
		existing = nil // a certificate for another key (v0.1.4's ~/.ssh/id_ed25519) is re-issued
	}
	_, knownErr := os.Stat(filepath.Join(sd, "known_hosts"))
	if !params.Force && knownErr == nil && certUsableFor(existing, ids, now(), certReuseMargin) {
		problem, err := writeSSHFiles(sd, params, "")
		if err != nil {
			return nil, err
		}
		return &certResult{Path: certPath, AliasProblem: problem}, nil
	}

	resp, err := client.IssueCert(ctx, pubLine, ids)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == "rate_limited" && knownErr == nil {
			if existing != nil && time.Unix(int64(existing.ValidBefore), 0).After(now()) {
				_, _ = fmt.Fprintf(os.Stderr, "warning: certificate rate limited; reusing the one on disk (%s left)\n", time.Unix(int64(existing.ValidBefore), 0).Sub(now()).Round(time.Minute))
				problem, err := writeSSHFiles(sd, params, "")
				if err != nil {
					return nil, err
				}
				return &certResult{Path: certPath, AliasProblem: problem}, nil
			}
		}
		return nil, stepFailed("get an SSH certificate", err, "")
	}

	if err := writeFileAtomic(certPath, []byte(strings.TrimSpace(resp.Certificate)+"\n"), 0o600); err != nil {
		return nil, err
	}
	problem, err := writeSSHFiles(sd, params, resp.Gateway.HostCAPub)
	if err != nil {
		return nil, err
	}
	stopMasters(ctx, sd, params.Projects)
	return &certResult{Path: certPath, AliasProblem: problem}, nil
}

// stopMasters asks each multiplexing master still running with the old
// certificate to stop taking new sessions (`ssh -O stop`). The gateway
// ends a connection when the certificate it logged in with expires
// (I-436), so a command sent over an old master could be cut long before
// the new certificate's 12 hours. Sessions already on the old master keep
// running; the next ssh starts a new master with the new certificate.
// Nothing runs when no control socket exists, the common case.
func stopMasters(ctx context.Context, sd string, projects []Project) {
	if goos() == "windows" {
		return
	}
	if socks, _ := filepath.Glob(filepath.Join(sd, "cm-*")); len(socks) == 0 { // a bad pattern is impossible; no match is no master
		return
	}
	for _, p := range projects {
		muxStop(ctx, sd, p.Slug+".repose")
	}
}

// muxStop runs `ssh -O stop` for one alias, best effort; tests replace it.
var muxStop = func(ctx context.Context, sd, alias string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-F", filepath.Join(sd, "config"), "-O", "stop", alias)
	// The config's Match line would run ssh-prepare again; this is a local
	// control request, so mark it prepared.
	cmd.Env = append(os.Environ(), envSSHPrepared+"=1")
	cmd.WaitDelay = time.Second
	_ = cmd.Run() // no master for this alias is the common case; ssh says so on stderr, which is discarded
}

// writeSSHFiles writes known_hosts (when the host CA is known), the Host
// blocks (~/.ssh/repose/hosts), ~/.ssh/repose/config, and the Include line; then, when asked, proves the
// alias resolves. Only a failure to write ~/.ssh/repose/ is an error; a
// ~/.ssh/config the CLI cannot or should not edit becomes the returned
// problem text.
func writeSSHFiles(sshDirPath string, params certParams, hostCAPub string) (string, error) {
	if hostCAPub != "" {
		known := fmt.Sprintf("@cert-authority %s,10.64.* %s\n", gatewayHost, strings.TrimSpace(hostCAPub))
		if err := writeFileAtomic(filepath.Join(sshDirPath, "known_hosts"), []byte(known), 0o600); err != nil {
			return "", err
		}
	}
	cfg := renderSSHConfig(params.Projects, params.Handle, sshPathPrefix(), goos() != "windows")
	if err := writeFileAtomic(filepath.Join(sshDirPath, sshHostsName), []byte(cfg), 0o600); err != nil {
		return "", err
	}
	if err := writeSSHEntry(sshDirPath); err != nil {
		return "", err
	}
	usc, err := userSSHConfig()
	if err != nil {
		return "", err
	}
	includeErr := ensureIncludeLine(usc)
	if !params.CheckAlias || len(params.Projects) == 0 {
		if includeErr != nil {
			return includeProblemText(usc, includeErr), nil
		}
		return "", nil
	}
	p := params.Projects[0]
	if ok, got := aliasResolves(p.Slug, params.Handle); !ok {
		if includeErr != nil {
			return includeProblemText(usc, includeErr), nil
		}
		return fmt.Sprintf("`ssh %s.repose` does not reach repose from a plain terminal: ssh resolves it to %s. ~/.ssh/config must include ~/.ssh/repose/config before its first Host or Match line; put this line at the very top of ~/.ssh/config:\n    %s", p.Slug, got, includeLine), nil
	}
	return "", nil
}

func includeProblemText(path string, err error) string {
	var ro *includeUnwritableError
	if errors.As(err, &ro) && ro.target != "" {
		return fmt.Sprintf("~/.ssh/config is a link to %s, which repose does not edit, so `ssh <project>.repose` will not work from a plain terminal. Add this line at the top of the file it is generated from (home-manager: `programs.ssh.includes = [ \"~/.ssh/repose/config\" ];`):\n    %s", ro.target, includeLine)
	}
	return fmt.Sprintf("Could not add the Include line to %s (%v), so `ssh <project>.repose` will not work from a plain terminal. Add this line at its very top:\n    %s", path, err, includeLine)
}

// aliasResolves runs `ssh -G <slug>.repose` and checks the effective
// HostName and User (I-151). ok is true when ssh is missing entirely:
// there is nothing to check against, and every connection will fail
// with its own clear error. got describes what ssh resolved otherwise.
func aliasResolves(slug, handle string) (ok bool, got string) {
	out, err := exec.Command("ssh", "-G", slug+".repose").Output()
	if err != nil {
		return true, ""
	}
	var host, user string
	for _, l := range strings.Split(string(out), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(l), " ")
		switch strings.ToLower(k) {
		case "hostname":
			host = v
		case "user":
			user = v
		}
	}
	if host == gatewayHost && user == slug+"."+handle {
		return true, ""
	}
	return false, fmt.Sprintf("host %q, user %q", host, user)
}

const gatewayHost = "ssh.repose.herakraft.co"

// renderSSHConfig builds ~/.ssh/repose/hosts: one Host block per project,
// in slug order for a stable diff (docs/interfaces/ssh-gateway.md "CLI
// side"). ~/.ssh/repose/config includes it after the Match line that
// keeps it current (I-281). prefix spells the home directory
// (sshPathPrefix). multiplex adds the ControlMaster lines (not on Windows,
// whose OpenSSH has no multiplexing).
func renderSSHConfig(projects []Project, handle, prefix string, multiplex bool) string {
	sorted := append([]Project(nil), projects...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Slug < sorted[j].Slug })

	var b strings.Builder
	b.WriteString("# Generated by repose; edits here are overwritten. ~/.ssh/repose/config includes this file.\n")
	for _, p := range sorted {
		_, _ = fmt.Fprintf(&b, "Host %s.repose\n", p.Slug)
		// The id the certificate must carry for this host: `repose
		// ssh-prepare` checks it without asking the api (I-281).
		_, _ = fmt.Fprintf(&b, "  # project %s\n", p.ID)
		_, _ = fmt.Fprintf(&b, "  HostName %s\n", gatewayHost)
		_, _ = fmt.Fprintf(&b, "  User %s.%s\n", p.Slug, handle)
		// The CLI's own passphrase-less key and its certificate, and only
		// those: with an agent loaded, ssh otherwise offers the agent's
		// plain keys first and the gateway answers "certificate required"
		// (M2 gate, I-108), and a passphrase-protected ~/.ssh/id_ed25519
		// prompted on every connection (owner's session, I-149).
		_, _ = fmt.Fprintf(&b, "  IdentityFile %s\n", sshPath(prefix, ".ssh/repose/id_ed25519"))
		_, _ = fmt.Fprintf(&b, "  CertificateFile %s\n", sshPath(prefix, ".ssh/repose/id_ed25519-cert.pub"))
		b.WriteString("  IdentitiesOnly yes\n")
		_, _ = fmt.Fprintf(&b, "  UserKnownHostsFile %s\n", sshPath(prefix, ".ssh/repose/known_hosts"))
		// No agent forwarding (I-247): anything running in the guest
		// could sign with the laptop's keys while attached. Pushes to
		// GitHub go over HTTPS with the carried gh login instead.
		b.WriteString("  ForwardAgent no\n")
		b.WriteString("  ServerAliveInterval 30\n")
		if multiplex {
			// One handshake per `repose run`: every later ssh rides this
			// connection (I-149).
			b.WriteString("  ControlMaster auto\n")
			_, _ = fmt.Fprintf(&b, "  ControlPath %s\n", sshPath(prefix, ".ssh/repose/cm-%C"))
			b.WriteString("  ControlPersist 10m\n")
		}
	}
	return b.String()
}

// refreshSSHAccess ends every command that creates a project (restore by
// name, a snapshot restored as a new project): the certificate's
// principals are project ids and the config has one Host block per
// project, so a new project needs both rewritten before a plain
// `ssh <slug>.repose` is let in (I-188). ensureCert reuses the certificate
// on disk when it already covers every project. A failure is a warning,
// not the command's failure: the project exists, and the next command
// that connects issues the certificate again.
func refreshSSHAccess(ctx context.Context, e *Env, slug string) {
	closeMaster(ctx, e, slug) // a master left from before a destroy leads to the old guest
	me, err := e.Client.GetMe(ctx)
	if err == nil {
		var projects []Project
		projects, err = e.Client.ListProjects(ctx)
		if err == nil {
			var cr *certResult
			cr, err = ensureCert(ctx, e.Client, certParams{Handle: me.Handle, Projects: projects, CheckAlias: e.TargetFor == nil}, nil)
			if err == nil && cr.AliasProblem != "" {
				e.warn("warning: %s", cr.AliasProblem)
			}
		}
	}
	if err != nil {
		e.warn("Could not renew your SSH certificate for %s (%v); `repose attach %s` renews it.", slug, err, slug)
	}
}
