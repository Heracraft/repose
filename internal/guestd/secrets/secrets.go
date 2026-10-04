// Package secrets implements the WriteSecrets request of
// docs/interfaces/vsock-guestd.md: named secret values onto the tmpfs at
// /run/repose/secrets, the shell env file beside them, and the three reserved
// sshd names of DECISIONS I-10.
//
// Nothing in this package logs a secret name together with a value, and
// nothing logs a value at all. Counts and error codes are what comes out.
package secrets

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// MaxValueBytes is the per-secret cap (docs/workstreams/04-guestd.md).
const MaxValueBytes = 64 << 10

// MaxNameBytes bounds a secret name so a name cannot be used as a payload.
const MaxNameBytes = 128

// Reserved names carry the guest's sshd material rather than a tenant secret.
// DECISIONS I-10: hostd delivers them through WriteSecrets and they land in
// /run/repose, not in the secrets directory, and never in secrets.env.
const (
	ReservedHostKey  = "ssh_host_ed25519_key"
	ReservedHostCert = "ssh_host_ed25519_key-cert.pub"
	ReservedUserCA   = "user_ca.pub"
)

type reservedSpec struct {
	file string
	mode os.FileMode
}

var reserved = map[string]reservedSpec{
	ReservedHostKey:  {file: ReservedHostKey, mode: 0o600},
	ReservedHostCert: {file: ReservedHostCert, mode: 0o644},
	ReservedUserCA:   {file: ReservedUserCA, mode: 0o644},
}

// nameRe is the shape of a named secret: it becomes a shell variable, so it
// must be a shell variable's name.
var nameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Handler writes secrets.
type Handler struct {
	paths sysdep.Paths
	run   sysdep.Runner
	log   *slog.Logger
	uid   int
	gid   int

	reloadRetry time.Duration // 0: 250 ms
}

// New builds the handler. uid and gid own the secret files; a real guest
// passes dev's.
func New(p sysdep.Paths, run sysdep.Runner, log *slog.Logger) *Handler {
	uid, gid := sysdep.DevIdentity()
	return &Handler{paths: p, run: run, log: log, uid: uid, gid: gid}
}

// Write replaces the guest's named secrets with list. The list is the whole
// set: a name that was there and is not in the list is removed, which is what
// makes `repose secrets rm` reach the guest. Reserved names are never removed
// this way, because they arrive on the create path, not the secrets path.
//
// Validation happens before any write, so a bad batch changes nothing.
func (h *Handler) Write(ctx context.Context, list []*guestdv1.Secret) error {
	seen := make(map[string]bool, len(list))
	for _, s := range list {
		name := s.GetName()
		switch {
		case name == "":
			return sysdep.Invalid("write secrets: a secret has no name")
		case len(name) > MaxNameBytes:
			return sysdep.Invalid("write secrets: a secret name is longer than %d bytes", MaxNameBytes)
		case seen[name]:
			return sysdep.Invalid("write secrets: %s appears twice", name)
		}
		if _, ok := reserved[name]; !ok {
			if !nameRe.MatchString(name) {
				return sysdep.Invalid("write secrets: %s is not a valid environment variable name", name)
			}
			if bytes.IndexByte(s.GetValue(), 0) >= 0 {
				return sysdep.Invalid("write secrets: the value of %s contains a NUL byte", name)
			}
		}
		if len(s.GetValue()) > MaxValueBytes {
			return sysdep.Invalid("write secrets: the value of %s is larger than %d bytes", name, MaxValueBytes)
		}
		seen[name] = true
	}

	if err := h.ensureDirs(); err != nil {
		return err
	}

	named := 0
	sshMaterial := false
	for _, s := range list {
		if spec, ok := reserved[s.GetName()]; ok {
			path := filepath.Join(h.paths.RunDir(), spec.file)
			if err := sysdep.WriteFileAtomic(path, s.GetValue(), spec.mode, 0, 0); err != nil {
				return sysdep.Errf(sysdep.CodeInternal, "write sshd material: %w", err)
			}
			sshMaterial = true
			continue
		}
		path := filepath.Join(h.paths.SecretsDir(), s.GetName())
		if err := sysdep.WriteFileAtomic(path, s.GetValue(), 0o400, h.uid, h.gid); err != nil {
			return sysdep.Errf(sysdep.CodeInternal, "write secret: %w", err)
		}
		named++
	}

	removedNames, err := h.removeStale(seen)
	if err != nil {
		return err
	}
	removed := len(removedNames)
	env, err := h.writeEnv(list, removedNames)
	if err != nil {
		return err
	}
	h.pushTmux(ctx, env)
	if sshMaterial {
		if err := h.reloadSSHD(ctx); err != nil {
			return err
		}
	}

	h.log.Info("secrets written",
		"event", "write_secrets", "count", named, "removed", removed, "ssh_material", sshMaterial)
	return nil
}

func (h *Handler) ensureDirs() error {
	if err := os.MkdirAll(h.paths.RunDir(), 0o755); err != nil {
		return sysdep.Errf(sysdep.CodeInternal, "create run directory: %w", err)
	}
	if err := os.MkdirAll(h.paths.SecretsDir(), 0o700); err != nil {
		return sysdep.Errf(sysdep.CodeInternal, "create secrets directory: %w", err)
	}
	if err := os.Chown(h.paths.SecretsDir(), h.uid, h.gid); err != nil && !os.IsPermission(err) {
		return sysdep.Errf(sysdep.CodeInternal, "chown secrets directory: %w", err)
	}
	return nil
}

// removeStale deletes secret files whose names are no longer in the set and
// returns those names.
func (h *Handler) removeStale(keep map[string]bool) ([]string, error) {
	entries, err := os.ReadDir(h.paths.SecretsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, sysdep.Errf(sysdep.CodeInternal, "list secrets directory: %w", err)
	}
	var removed []string
	for _, e := range entries {
		if e.IsDir() || keep[e.Name()] || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if err := os.Remove(filepath.Join(h.paths.SecretsDir(), e.Name())); err != nil {
			return removed, sysdep.Errf(sysdep.CodeInternal, "remove withdrawn secret: %w", err)
		}
		removed = append(removed, e.Name())
	}
	return removed, nil
}

// genVar is the variable secrets.env sets on its first line. The BASH_ENV
// loader (nix/guest/base/bash-env.sh) compares it with the environment it
// inherited and sources the file only when they differ, so a command whose
// parent already holds the current secrets keeps any value its parent set
// for it on purpose (DECISIONS I-475).
const genVar = "REPOSE_SECRETS_GEN"

// envFile is what writeEnv wrote: the exported rows, the names it unsets and
// the generation, which pushTmux mirrors into the tmux server.
type envFile struct {
	rows   []envRow
	unsets []string
	gen    string
}

type envRow struct{ name, value string }

// writeEnv rewrites /run/repose/secrets.env from the non-reserved secrets, in
// name order so the file does not churn. A name written since boot and absent
// now gets an `unset NAME` line, so a process that inherited it (an agent
// started before `repose secrets rm`) loses it in its next command. Those
// names are kept in secrets.names, written before secrets.env, so a guestd
// restart keeps unsetting them; removed are the names removeStale just took
// away, which covers a guest whose names file predates I-475.
//
// The first line is `export REPOSE_SECRETS_GEN=<hex>`. It changes when the
// rest of the file changes and only then.
func (h *Handler) writeEnv(list []*guestdv1.Secret, removed []string) (envFile, error) {
	var out envFile
	current := make(map[string]bool, len(list))
	for _, s := range list {
		if _, ok := reserved[s.GetName()]; ok {
			continue
		}
		out.rows = append(out.rows, envRow{s.GetName(), string(s.GetValue())})
		current[s.GetName()] = true
	}
	sort.Slice(out.rows, func(i, j int) bool { return out.rows[i].name < out.rows[j].name })

	known, err := h.readNames()
	if err != nil {
		return out, err
	}
	for _, n := range removed {
		known[n] = true
	}
	for n := range current {
		known[n] = true
	}
	all := make([]string, 0, len(known))
	for n := range known {
		all = append(all, n)
		if !current[n] {
			out.unsets = append(out.unsets, n)
		}
	}
	sort.Strings(all)
	sort.Strings(out.unsets)
	if err := sysdep.WriteFileAtomic(h.paths.SecretsNames(), []byte(strings.Join(all, "\n")+"\n"), 0o600, 0, 0); err != nil {
		return out, sysdep.Errf(sysdep.CodeInternal, "write secret names: %w", err)
	}

	var body bytes.Buffer
	body.WriteString("# Written by guestd. Sourced by every bash through BASH_ENV and by login shells; do not edit.\n")
	for _, r := range out.rows {
		fmt.Fprintf(&body, "export %s=%s\n", r.name, shellQuote(r.value))
	}
	for _, n := range out.unsets {
		fmt.Fprintf(&body, "unset %s\n", n)
	}

	out.gen = h.previousGen(body.Bytes())
	if out.gen == "" {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return out, sysdep.Errf(sysdep.CodeInternal, "generate secrets generation: %w", err)
		}
		out.gen = hex.EncodeToString(b[:])
	}
	file := append([]byte("export "+genVar+"="+out.gen+"\n"), body.Bytes()...)
	if err := sysdep.WriteFileAtomic(h.paths.SecretsEnv(), file, 0o400, h.uid, h.gid); err != nil {
		return out, sysdep.Errf(sysdep.CodeInternal, "write secrets env file: %w", err)
	}
	return out, nil
}

// readNames reads secrets.names; a missing file is an empty set (first write
// since boot).
func (h *Handler) readNames() (map[string]bool, error) {
	names := map[string]bool{}
	b, err := os.ReadFile(h.paths.SecretsNames())
	if errors.Is(err, os.ErrNotExist) {
		return names, nil
	}
	if err != nil {
		return nil, sysdep.Errf(sysdep.CodeInternal, "read secret names: %w", err)
	}
	for _, n := range strings.Split(string(b), "\n") {
		// A line that is not a name is dropped rather than written into a
		// file every shell sources.
		if nameRe.MatchString(n) {
			names[n] = true
		}
	}
	return names, nil
}

// previousGen returns the generation of the secrets.env on disk when the rest
// of it equals body, and "" otherwise.
func (h *Handler) previousGen(body []byte) string {
	old, err := os.ReadFile(h.paths.SecretsEnv())
	if err != nil {
		return ""
	}
	first, rest, ok := bytes.Cut(old, []byte("\n"))
	if !ok || !bytes.Equal(rest, body) {
		return ""
	}
	gen, ok := strings.CutPrefix(string(first), "export "+genVar+"=")
	if !ok || !genRe.MatchString(gen) {
		return ""
	}
	return gen
}

var genRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// pushTmux mirrors the file into dev's tmux server's global environment, so a
// new window starts with the current secrets even when its shell is not bash
// (DECISIONS I-475). No tmux server is the normal state before SetupProject,
// and any failure here is logged and ignored: the secrets are on the tmpfs and
// the BASH_ENV loader picks them up regardless.
//
// The values travel as tmux arguments. tmux reads an argument ending in ';' as
// the end of a command, and "\;" at the end as a literal ';' (cmd-parse.y,
// cmd_parse_from_arguments), so such a value gets a backslash before its last
// byte. The generation goes in the last command, after every value, so a
// failure part way leaves tmux with an old generation and the loader still
// reloads.
func (h *Handler) pushTmux(ctx context.Context, env envFile) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, args := range tmuxBatches(env) {
		res, err := h.run.Run(ctx, sysdep.RunSpec{
			Argv:      append([]string{"tmux"}, args...),
			User:      "dev",
			Env:       sysdep.DevEnv(h.paths, "dev"),
			MaxOutput: 4 << 10,
		})
		switch {
		case err != nil:
			h.log.Warn("secrets not pushed to tmux", "event", "write_secrets_tmux", "error_code", sysdep.CodeOf(err))
			return
		case res.ExitCode != 0:
			// stderr is not logged: tmux may echo an argument back.
			if !strings.Contains(string(res.Stderr), "no server running") &&
				!strings.Contains(string(res.Stderr), "error connecting") {
				h.log.Warn("secrets not pushed to tmux", "event", "write_secrets_tmux", "exit_code", res.ExitCode)
			}
			return
		}
	}
}

// tmuxBatches turns env into tmux argument lists of about 256 KiB each, under
// the kernel's limit for one exec, commands joined by ";". The generation is
// the last command of the last batch.
func tmuxBatches(env envFile) [][]string {
	var cmds [][]string
	for _, r := range env.rows {
		cmds = append(cmds, []string{"set-environment", "-g", r.name, tmuxArg(r.value)})
	}
	for _, n := range env.unsets {
		cmds = append(cmds, []string{"set-environment", "-gu", n})
	}
	cmds = append(cmds, []string{"set-environment", "-g", genVar, env.gen})

	const batchBytes = 256 << 10
	var batches [][]string
	var cur []string
	size := 0
	for _, c := range cmds {
		n := 0
		for _, a := range c {
			n += len(a) + 1
		}
		if len(cur) > 0 && size+n > batchBytes {
			batches = append(batches, cur)
			cur, size = nil, 0
		}
		if len(cur) > 0 {
			cur = append(cur, ";")
		}
		cur = append(cur, c...)
		size += n
	}
	return append(batches, cur)
}

// tmuxArg keeps a value ending in ';' from being read as a command separator.
func tmuxArg(v string) string {
	if strings.HasSuffix(v, ";") {
		return v[:len(v)-1] + `\;`
	}
	return v
}

// shellQuote single-quotes a value, escaping an embedded quote as '\” so a
// value containing quotes or newlines survives being sourced.
func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// reloadSSHD makes sshd use the host key just written. Since the boot
// got faster (I-231) guestd answers while sshd's own start job is still
// queued, and `reload-or-restart` then fails against it ("systemctl
// exited 1", the start came back guest_unresponsive); it is retried until
// the job settles, within the same 20 seconds.
func (h *Handler) reloadSSHD(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		res, err := h.run.Run(ctx, sysdep.RunSpec{
			Argv:      []string{"systemctl", "reload-or-restart", "sshd.service"},
			MaxOutput: 4 << 10,
			Env:       sysdep.DevEnv(h.paths, "root"),
		})
		if err == nil && res.ExitCode == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			if err != nil {
				return sysdep.Errf(sysdep.CodeInternal, "reload sshd after writing host key: %w", err)
			}
			return sysdep.Errf(sysdep.CodeInternal, "reload sshd after writing host key: systemctl exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
		case <-time.After(h.retryEvery()):
		}
	}
}

// retryEvery is the pause between sshd reload attempts; tests shorten it.
func (h *Handler) retryEvery() time.Duration {
	if h.reloadRetry > 0 {
		return h.reloadRetry
	}
	return 250 * time.Millisecond
}

// IsReserved reports whether name is one of the sshd material names.
func IsReserved(name string) bool {
	_, ok := reserved[name]
	return ok
}
