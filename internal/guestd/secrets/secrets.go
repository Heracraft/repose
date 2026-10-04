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
	"encoding/json"
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
	env, err := h.writeEnv(list)
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

// genVar is the variable that records which generation of the secrets a
// process's environment was formed from. secrets.env sets it on its first
// line and secrets.refresh on its last; the BASH_ENV loader
// (nix/guest/base/bash-env.sh) compares it with the refresh file's first line
// (DECISIONS I-475). The name holds none of SECRET, KEY, TOKEN or
// PASS, so a sandbox that drops variables matching those (Codex's default
// shell_environment_policy) keeps it.
const genVar = "REPOSE_ENV_GEN"

// keepGens is how many generations secrets.refresh can bring up to date with
// its guarded lines. A process formed from an older one is treated as one
// that was never given a secret: it gets the names it lacks and keeps the
// values it has (I-475).
const keepGens = 16

// shellReserved are names guestd never exports, even as a secret, because
// they run the mechanism of I-475: BASH_ENV and ENV name the loader, and
// genVar is its marker. The secret file is still written (I-475).
var shellReserved = map[string]bool{"BASH_ENV": true, "ENV": true, genVar: true}

// exported reports whether a named secret becomes an environment variable.
func exported(name string) bool {
	return !shellReserved[name] && !strings.HasPrefix(name, "__repose_")
}

// generation is one state of the exported secrets: its id and the values it
// delivered.
type generation struct {
	Gen    string            `json:"gen"`
	Values map[string][]byte `json:"values"`
}

// secretsState is /run/repose/secrets.state: the last keepGens generations,
// oldest first, the current one last.
type secretsState struct {
	Gens []generation `json:"gens"`
}

// envFile is what writeEnv wrote, for pushTmux to mirror into the tmux
// server: the exported rows, the names to drop and the generation.
type envFile struct {
	rows   []envRow
	unsets []string
	gen    string
}

type envRow struct{ name, value string }

// writeEnv records the exported secrets as a generation and rewrites the two
// files shells read from it, in name order so they do not churn:
//
//   - secrets.env: `export REPOSE_ENV_GEN=<gen>` and an `export NAME='value'`
//     line per secret. The whole current set, for whoever sources it.
//   - secrets.refresh: what the BASH_ENV loader and /etc/profile.d/repose.sh
//     source when a process's REPOSE_ENV_GEN is not the current one. For
//     each name it compares the value the process holds with the value the
//     process's generation delivered, and replaces or unsets it only when
//     they match, so a value the process or its project's .envrc set on
//     purpose survives a later write (I-475).
//
// The generation stays the same while the exported set does.
func (h *Handler) writeEnv(list []*guestdv1.Secret) (envFile, error) {
	var out envFile
	current := map[string][]byte{}
	for _, s := range list {
		if _, ok := reserved[s.GetName()]; ok || !exported(s.GetName()) {
			continue
		}
		out.rows = append(out.rows, envRow{s.GetName(), string(s.GetValue())})
		current[s.GetName()] = s.GetValue()
	}
	sort.Slice(out.rows, func(i, j int) bool { return out.rows[i].name < out.rows[j].name })

	st := h.readState()
	if n := len(st.Gens); n > 0 && sameValues(st.Gens[n-1].Values, current) {
		out.gen = st.Gens[n-1].Gen
	} else {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return out, sysdep.Errf(sysdep.CodeInternal, "generate secrets generation: %w", err)
		}
		out.gen = hex.EncodeToString(b[:])
		st.Gens = append(st.Gens, generation{Gen: out.gen, Values: current})
		if len(st.Gens) > keepGens {
			st.Gens = append([]generation(nil), st.Gens[len(st.Gens)-keepGens:]...)
		}
	}
	stateJSON, err := json.Marshal(st)
	if err != nil {
		return out, sysdep.Errf(sysdep.CodeInternal, "encode secrets state: %w", err)
	}
	if err := sysdep.WriteFileAtomic(h.paths.SecretsState(), stateJSON, 0o600, 0, 0); err != nil {
		return out, sysdep.Errf(sysdep.CodeInternal, "write secrets state: %w", err)
	}

	known := map[string]bool{}
	for _, g := range st.Gens {
		for n := range g.Values {
			known[n] = true
		}
	}
	names := make([]string, 0, len(known))
	for n := range known {
		names = append(names, n)
		if _, ok := current[n]; !ok {
			out.unsets = append(out.unsets, n)
		}
	}
	sort.Strings(names)
	sort.Strings(out.unsets)

	var env bytes.Buffer
	fmt.Fprintf(&env, "export %s=%s\n", genVar, out.gen)
	env.WriteString("# Written by guestd: the current secrets; do not edit.\n")
	for _, r := range out.rows {
		fmt.Fprintf(&env, "export %s=%s\n", r.name, shellQuote(r.value))
	}

	if err := sysdep.WriteFileAtomic(h.paths.SecretsRefresh(), refreshScript(st, names, current, out.gen), 0o400, h.uid, h.gid); err != nil {
		return out, sysdep.Errf(sysdep.CodeInternal, "write secrets refresh file: %w", err)
	}
	if err := sysdep.WriteFileAtomic(h.paths.SecretsEnv(), env.Bytes(), 0o400, h.uid, h.gid); err != nil {
		return out, sysdep.Errf(sysdep.CodeInternal, "write secrets env file: %w", err)
	}
	return out, nil
}

// refreshScript is secrets.refresh. It is POSIX sh, runs builtins only, and
// sets nothing but the secrets, REPOSE_ENV_GEN and two scratch variables it
// unsets again:
//
//	# repose-env-gen <gen>
//	case ${REPOSE_ENV_GEN-} in g1) __repose_g=1 ;; ... *) __repose_g=0 ;; esac
//	case $__repose_g in 1|2) __repose_d=s'old' ;; 3) __repose_d=s'new' ;; *) __repose_d= ;; esac
//	[ "${NAME+s$NAME}" != "$__repose_d" ] || export NAME='new'
//	...
//	export REPOSE_ENV_GEN=<gen>
//
// __repose_d is what the process's generation delivered for NAME: "s" and
// the value, or empty when it delivered nothing (the name was not a secret
// then, or the generation is unknown). "${NAME+s$NAME}" is the same encoding
// of what the process holds. Only when the two match does the line export
// the new value, or unset a removed name.
func refreshScript(st secretsState, names []string, current map[string][]byte, gen string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# repose-env-gen %s\n", gen)
	b.WriteString("# Written by guestd. Sourced by /etc/repose/bash-env.sh when REPOSE_ENV_GEN differs from the line above; do not edit.\n")
	b.WriteString("case ${" + genVar + "-} in\n")
	for i, g := range st.Gens {
		fmt.Fprintf(&b, "%s) __repose_g=%d ;;\n", g.Gen, i+1)
	}
	b.WriteString("*) __repose_g=0 ;;\nesac\n")
	for _, n := range names {
		// The generations that delivered each value of n, by value.
		var order []string
		byValue := map[string][]string{}
		for i, g := range st.Gens {
			v, ok := g.Values[n]
			if !ok {
				continue
			}
			if _, seen := byValue[string(v)]; !seen {
				order = append(order, string(v))
			}
			byValue[string(v)] = append(byValue[string(v)], fmt.Sprint(i+1))
		}
		b.WriteString("case $__repose_g in")
		for _, v := range order {
			fmt.Fprintf(&b, " %s) __repose_d=s%s ;;", strings.Join(byValue[v], "|"), shellQuote(v))
		}
		b.WriteString(" *) __repose_d= ;; esac\n")
		fmt.Fprintf(&b, "[ \"${%s+s$%s}\" != \"$__repose_d\" ] || ", n, n)
		if v, ok := current[n]; ok {
			fmt.Fprintf(&b, "export %s=%s\n", n, shellQuote(string(v)))
		} else {
			fmt.Fprintf(&b, "unset %s\n", n)
		}
	}
	fmt.Fprintf(&b, "export %s=%s\nunset __repose_g __repose_d\n", genVar, gen)
	return b.Bytes()
}

// readState reads secrets.state. A missing or unreadable one (first write
// since boot, or a guest whose guestd predates I-475) is an empty history,
// which only costs a process formed before it the guarded replacement.
func (h *Handler) readState() secretsState {
	var st secretsState
	b, err := os.ReadFile(h.paths.SecretsState())
	if err != nil {
		return secretsState{}
	}
	if err := json.Unmarshal(b, &st); err != nil {
		h.log.Warn("secrets state unreadable, starting a new history", "event", "write_secrets_state")
		return secretsState{}
	}
	// Anything not shaped like what writeEnv writes is dropped rather than
	// written into a file every shell sources.
	gens := st.Gens[:0]
	for _, g := range st.Gens {
		if !genRe.MatchString(g.Gen) {
			continue
		}
		for n := range g.Values {
			if !nameRe.MatchString(n) || !exported(n) {
				delete(g.Values, n)
			}
		}
		gens = append(gens, g)
	}
	st.Gens = gens
	return st
}

func sameValues(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for n, v := range a {
		w, ok := b[n]
		if !ok || !bytes.Equal(v, w) {
			return false
		}
	}
	return true
}

var genRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// pushTmux mirrors the current set into dev's tmux server's global
// environment, so a new window starts with it even when its shell is not
// bash (DECISIONS I-475). No tmux server is the normal state before
// SetupProject, and any failure here is logged and ignored: the secrets are
// on the tmpfs and the BASH_ENV loader picks them up regardless.
//
// The commands go to `tmux source-file -` on stdin, never as arguments: an
// argument is readable by every user in /proc/<pid>/cmdline, and the tmux
// client refuses a command longer than 16 KiB (I-476). The generation is the
// last command, so a failure part way leaves tmux with an old generation and
// the loader still refreshes.
func (h *Handler) pushTmux(ctx context.Context, env envFile) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := h.run.Run(ctx, sysdep.RunSpec{
		Argv:      []string{"tmux", "source-file", "-"},
		Stdin:     tmuxScript(env),
		User:      "dev",
		Env:       sysdep.DevEnv(h.paths, "dev"),
		MaxOutput: 4 << 10,
	})
	switch {
	case err != nil:
		h.log.Warn("secrets not pushed to tmux", "event", "write_secrets_tmux", "error_code", sysdep.CodeOf(err))
	case res.ExitCode != 0:
		// stderr is not logged: tmux may echo a line back.
		if !strings.Contains(string(res.Stderr), "no server running") &&
			!strings.Contains(string(res.Stderr), "error connecting") {
			h.log.Warn("secrets not pushed to tmux", "event", "write_secrets_tmux", "exit_code", res.ExitCode)
		}
	}
}

// tmuxScript is the tmux configuration pushTmux sources: one
// set-environment line per secret, one -gu line per removed name, and the
// generation last.
func tmuxScript(env envFile) []byte {
	var b bytes.Buffer
	for _, r := range env.rows {
		fmt.Fprintf(&b, "set-environment -g %s %s\n", r.name, tmuxQuote(r.value))
	}
	for _, n := range env.unsets {
		fmt.Fprintf(&b, "set-environment -gu %s\n", n)
	}
	fmt.Fprintf(&b, "set-environment -g %s %s\n", genVar, env.gen)
	return b.Bytes()
}

// tmuxQuote writes v as one double-quoted tmux configuration token on one
// line. Inside double quotes tmux expands $ and a leading ~, and reads \ as
// an escape (cmd-parse.y), so \, " and $ get a backslash, and ~ and every
// byte outside printable ASCII, a newline included, become a three-digit
// octal escape. TestTmuxArgumentsAgainstARealTmux holds each case.
func tmuxQuote(v string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '"' || c == '\\' || c == '$':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c >= 0x20 && c < 0x7e: // 0x7e is ~, which tmux expands to $HOME
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "\\%03o", c)
		}
	}
	b.WriteByte('"')
	return b.String()
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
