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

// Restore rebuilds secrets.refresh from the secret files when it is missing,
// and mirrors the set into tmux. A guest whose secrets an older guestd wrote
// has secrets.env and /run/repose/secrets/ but no refresh file, so after a
// live switch onto a base with the BASH_ENV loader the loader found nothing,
// and the tmux server, restarted by the switch, had no secrets for new
// windows until the next write (DECISIONS I-475). guestd calls it once at
// start, before serving. It does nothing when the refresh file exists or
// there are no secret files, so a restart repeats nothing. The history
// starts empty: a process holding a value from before keeps it, as the
// loader does for any value it did not deliver.
func (h *Handler) Restore(ctx context.Context) error {
	if _, err := os.Stat(h.paths.SecretsRefresh()); err == nil || !os.IsNotExist(err) {
		return nil
	}
	entries, err := os.ReadDir(h.paths.SecretsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return sysdep.Errf(sysdep.CodeInternal, "list secrets directory: %w", err)
	}
	var list []*guestdv1.Secret
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || strings.HasPrefix(name, ".") || len(name) > MaxNameBytes || !nameRe.MatchString(name) {
			continue
		}
		if _, ok := reserved[name]; ok {
			continue
		}
		v, err := os.ReadFile(filepath.Join(h.paths.SecretsDir(), name))
		if err != nil || len(v) > MaxValueBytes || bytes.IndexByte(v, 0) >= 0 {
			continue
		}
		list = append(list, &guestdv1.Secret{Name: name, Value: v})
	}
	if len(list) == 0 {
		return nil
	}
	env, err := h.writeEnv(list)
	if err != nil {
		return err
	}
	h.pushTmux(ctx, env)
	h.log.Info("secrets refresh rebuilt", "event", "write_secrets", "count", len(list))
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

// genVar is the variable that records which set of secrets a process's
// environment was formed from. secrets.env sets it on its first line and
// secrets.refresh on its last; the BASH_ENV loader
// (nix/guest/base/bash-env.sh) compares it with the refresh file's first line
// and skips the file when they match (DECISIONS I-475). It decides nothing
// else: the refresh compares values, not generations.
const genVar = "REPOSE_ENV_GEN"

// keepValues is how many earlier values of one name secrets.refresh
// recognises. A process holding a value of that name older than these keeps
// it, like any value guestd did not deliver. The bound is per name, so writes
// to other names never age a name's values out (I-475).
const keepValues = 16

// shellReserved are names guestd never exports, even as a secret, because
// they run the mechanism of I-475: BASH_ENV and ENV name the loader, and
// genVar is its marker. The secret file is still written (I-475).
var shellReserved = map[string]bool{"BASH_ENV": true, "ENV": true, genVar: true}

// exported reports whether a named secret becomes an environment variable.
func exported(name string) bool {
	return !shellReserved[name] && !strings.HasPrefix(name, "__repose_")
}

// secretsState is /run/repose/secrets.state: the current generation and its
// values, and for each name the values guestd exported earlier and no longer
// does, distinct, oldest first, at most keepValues. A removed name keeps its
// earlier values, so the refresh can take it out of a process that still
// holds one.
type secretsState struct {
	Gen     string              `json:"gen"`
	Current map[string][]byte   `json:"current"`
	Earlier map[string][][]byte `json:"earlier"`
}

// envFile is what writeEnv wrote, for pushTmux to mirror into the tmux
// server: the exported rows, the names to drop and the generation.
type envFile struct {
	rows   []envRow
	unsets []string
	gen    string
}

type envRow struct{ name, value string }

// writeEnv records the exported secrets and rewrites the two files shells
// read from them, in name order so they do not churn:
//
//   - secrets.env: `export REPOSE_ENV_GEN=<gen>` and an `export NAME='value'`
//     line per secret. The whole current set, for whoever sources it.
//   - secrets.refresh: what the BASH_ENV loader and /etc/profile.d/repose.sh
//     source when a process's REPOSE_ENV_GEN is not the current one. For
//     each name it sets the current value where the process lacks the name
//     or holds an earlier value guestd exported, and unsets a removed name
//     where the process holds one of its earlier values. Any other value is
//     the process's own (an .envrc, an export, `NAME=x cmd`) and stays
//     (I-475).
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
	if st.Gen != "" && sameValues(st.Current, current) {
		out.gen = st.Gen
	} else {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return out, sysdep.Errf(sysdep.CodeInternal, "generate secrets generation: %w", err)
		}
		out.gen = hex.EncodeToString(b[:])
		// A value that stops being current becomes an earlier one; a value
		// that is current again is not earlier any more.
		for n, v := range st.Current {
			if w, ok := current[n]; !ok || !bytes.Equal(v, w) {
				st.Earlier[n] = appendEarlier(st.Earlier[n], v)
			}
		}
		for n, v := range current {
			st.Earlier[n] = withoutValue(st.Earlier[n], v)
			if len(st.Earlier[n]) == 0 {
				delete(st.Earlier, n)
			}
		}
		st.Gen, st.Current = out.gen, current
	}
	stateJSON, err := json.Marshal(st)
	if err != nil {
		return out, sysdep.Errf(sysdep.CodeInternal, "encode secrets state: %w", err)
	}
	if err := sysdep.WriteFileAtomic(h.paths.SecretsState(), stateJSON, 0o600, 0, 0); err != nil {
		return out, sysdep.Errf(sysdep.CodeInternal, "write secrets state: %w", err)
	}
	for n := range st.Earlier {
		if _, ok := current[n]; !ok {
			out.unsets = append(out.unsets, n)
		}
	}
	sort.Strings(out.unsets)

	var env bytes.Buffer
	fmt.Fprintf(&env, "export %s=%s\n", genVar, out.gen)
	env.WriteString("# Written by guestd: the current secrets; do not edit.\n")
	for _, r := range out.rows {
		fmt.Fprintf(&env, "export %s=%s\n", r.name, shellQuote(r.value))
	}

	if err := sysdep.WriteFileAtomic(h.paths.SecretsRefresh(), refreshScript(st), 0o400, h.uid, h.gid); err != nil {
		return out, sysdep.Errf(sysdep.CodeInternal, "write secrets refresh file: %w", err)
	}
	if err := sysdep.WriteFileAtomic(h.paths.SecretsEnv(), env.Bytes(), 0o400, h.uid, h.gid); err != nil {
		return out, sysdep.Errf(sysdep.CodeInternal, "write secrets env file: %w", err)
	}
	return out, nil
}

// appendEarlier adds v as the newest earlier value, once, keeping the last
// keepValues.
func appendEarlier(vs [][]byte, v []byte) [][]byte {
	vs = append(withoutValue(vs, v), v)
	if len(vs) > keepValues {
		vs = append([][]byte(nil), vs[len(vs)-keepValues:]...)
	}
	return vs
}

func withoutValue(vs [][]byte, v []byte) [][]byte {
	out := vs[:0:0]
	for _, w := range vs {
		if !bytes.Equal(w, v) {
			out = append(out, w)
		}
	}
	return out
}

// refreshScript is secrets.refresh. It is POSIX sh, runs builtins only, and
// sets nothing but the secrets and REPOSE_ENV_GEN:
//
//	# repose-env-gen <gen>
//	case ${NAME+s$NAME} in ''|s'old1'|s'old2') export NAME='new' ;; esac
//	case ${GONE+s$GONE} in s'old') unset GONE ;; esac
//	export REPOSE_ENV_GEN=<gen>
//
// "${NAME+s$NAME}" is empty when the process lacks NAME and "s" and the
// value when it holds one, so the patterns are "lacks it" and "holds a value
// guestd exported before". Anything else is the process's own value.
func refreshScript(st secretsState) []byte {
	names := make([]string, 0, len(st.Current)+len(st.Earlier))
	for n := range st.Current {
		names = append(names, n)
	}
	for n := range st.Earlier {
		if _, ok := st.Current[n]; !ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)

	var b bytes.Buffer
	fmt.Fprintf(&b, "# repose-env-gen %s\n", st.Gen)
	b.WriteString("# Written by guestd. Sourced by /etc/repose/bash-env.sh when REPOSE_ENV_GEN differs from the line above; do not edit.\n")
	for _, n := range names {
		var pats []string
		v, current := st.Current[n]
		if current {
			pats = append(pats, "''")
		}
		for _, e := range st.Earlier[n] {
			pats = append(pats, "s"+shellQuote(string(e)))
		}
		fmt.Fprintf(&b, "case ${%s+s$%s} in %s) ", n, n, strings.Join(pats, "|"))
		if current {
			fmt.Fprintf(&b, "export %s=%s", n, shellQuote(string(v)))
		} else {
			fmt.Fprintf(&b, "unset %s", n)
		}
		b.WriteString(" ;; esac\n")
	}
	fmt.Fprintf(&b, "export %s=%s\n", genVar, st.Gen)
	return b.Bytes()
}

// readState reads secrets.state. A missing or unreadable one (first write
// since boot, or a guest whose guestd predates I-475) is an empty history,
// which only costs a process holding a value from before it the
// replacement: it keeps that value.
func (h *Handler) readState() secretsState {
	st := secretsState{Current: map[string][]byte{}, Earlier: map[string][][]byte{}}
	b, err := os.ReadFile(h.paths.SecretsState())
	if err != nil {
		return st
	}
	var raw secretsState
	if err := json.Unmarshal(b, &raw); err != nil {
		h.log.Warn("secrets state unreadable, starting a new history", "event", "write_secrets_state")
		return st
	}
	// Anything not shaped like what writeEnv writes is dropped rather than
	// written into a file every shell sources.
	ok := func(n string) bool { return nameRe.MatchString(n) && exported(n) }
	if genRe.MatchString(raw.Gen) {
		st.Gen = raw.Gen
	}
	for n, v := range raw.Current {
		if ok(n) {
			st.Current[n] = v
		}
	}
	for n, vs := range raw.Earlier {
		if ok(n) && len(vs) > 0 {
			if len(vs) > keepValues {
				vs = vs[len(vs)-keepValues:]
			}
			st.Earlier[n] = vs
		}
	}
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
