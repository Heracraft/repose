// Package system implements the Switch request of
// docs/interfaces/vsock-guestd.md: activate a system closure the host built
// and put in the shared store, without rebooting unless the kernel or initrd
// changed.
package system

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// OutputCap is the cap on the output returned to hostd
// (docs/interfaces/vsock-guestd.md: 32 KB).
const OutputCap = 32 << 10

// DefaultTimeout bounds a switch (docs/workstreams/04-guestd.md: 10 minutes).
const DefaultTimeout = 10 * time.Minute

// Handler runs switches.
type Handler struct {
	paths   sysdep.Paths
	run     sysdep.Runner
	timeout time.Duration
	log     *slog.Logger
	// reboot is the command used to reboot after a forced boot-activation.
	// Named so the unit test can observe it without rebooting the test host.
	rebootArgv []string
	// viewMu serialises the store view's roots (view.go): guestd's start
	// and a registration may both update them.
	viewMu sync.Mutex
}

// New builds the handler.
func New(p sysdep.Paths, run sysdep.Runner, timeout time.Duration, log *slog.Logger) *Handler {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Handler{
		paths:      p,
		run:        run,
		timeout:    timeout,
		log:        log,
		rebootArgv: []string{"systemctl", "reboot"},
	}
}

// Switch activates closure. It returns needs_reboot without doing anything
// when the kernel or initrd differ and force is false, because rebooting a
// guest under the user's nose loses the agent that was running in it.
func (h *Handler) Switch(ctx context.Context, closure string, force bool, registration []byte) (*guestdv1.SwitchResult, error) {
	if err := h.checkHidden(closure, registration); err != nil {
		return nil, err
	}
	real, err := h.resolveClosure(closure)
	if err != nil {
		return nil, err
	}
	if len(registration) > 0 {
		if err := h.RegisterPaths(ctx, registration); err != nil {
			return nil, err
		}
	}

	changed, err := h.kernelChanged(real)
	if err != nil {
		return nil, err
	}
	if changed && !force {
		h.log.Info("switch needs a reboot; nothing applied",
			"event", "switch", "result", "needs_reboot")
		return &guestdv1.SwitchResult{NeedsReboot: true}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	// The profile is set first so that a reboot, whether this switch asks for
	// one or the guest is restarted later, lands on the new system.
	if err := h.setProfile(ctx, closure); err != nil {
		return nil, err
	}

	action := "switch"
	if changed {
		action = "boot"
	}
	// The activation runs as a transient unit of its own, not a child of
	// guestd: a stop of guestd for any reason then cannot kill a switch
	// half way (I-143). --wait keeps the call synchronous; the output goes
	// to a file rather than a pipe back to guestd, because a pipe whose
	// reader has gone (guestd stopped by an older base's activation) ends
	// the activation with SIGPIPE before its start step (I-148, m3-held
	// 2026-09-21 04:38Z); --collect drops the unit whatever its exit.
	logPath := h.paths.SwitchLog()
	_ = os.Remove(logPath)
	res, runErr := h.run.Run(ctx, sysdep.RunSpec{
		Argv: []string{"systemd-run", "--wait", "--collect", "--quiet",
			"--unit", "repose-switch-" + strconv.FormatInt(time.Now().UnixNano(), 36),
			"--setenv=PATH=" + sysdep.GuestPATH,
			"-p", "StandardOutput=append:" + logPath, "-p", "StandardError=append:" + logPath,
			filepath.Join(real, "bin", "switch-to-configuration"), action},
		MaxOutput: OutputCap,
		Env:       sysdep.DevEnv(h.paths, "root"),
	})
	logged, _ := os.ReadFile(logPath)
	output := combine(append(logged, res.Stdout...), res.Stderr, OutputCap)
	if runErr != nil {
		h.log.Error("switch-to-configuration did not run",
			"event", "switch", "result", "error", "action", action)
		return &guestdv1.SwitchResult{Output: output},
			sysdep.Errf(sysdep.CodeInternal, "switch-to-configuration %s: %w", action, runErr)
	}
	if res.ExitCode != 0 {
		// The old system stays active; hostd surfaces this as an ApplyConfig
		// failure with the output, which is why output is returned either way.
		h.log.Warn("switch-to-configuration failed; previous system still active",
			"event", "switch", "result", "failed", "exit_code", res.ExitCode, "action", action)
		return &guestdv1.SwitchResult{Output: output},
			sysdep.Errf(sysdep.CodeInternal, "switch-to-configuration %s exited %d", action, res.ExitCode)
	}

	if !changed {
		h.log.Info("system switched", "event", "switch", "result", "ok")
		h.restartSelfIfChanged(ctx, real)
		return &guestdv1.SwitchResult{Output: output}, nil
	}

	h.log.Info("system activated for boot; rebooting",
		"event", "switch", "result", "rebooting")
	if _, err := h.run.Run(ctx, sysdep.RunSpec{Argv: h.rebootArgv, MaxOutput: 4 << 10}); err != nil {
		return &guestdv1.SwitchResult{Output: output},
			sysdep.Errf(sysdep.CodeInternal, "reboot after boot activation: %w", err)
	}
	return &guestdv1.SwitchResult{Rebooted: true, Output: output}, nil
}

// resolveClosure checks that the closure is a store path and that the store
// actually holds it. A missing path means the host garbage-collected it, which
// is the store_path_missing case.
func (h *Handler) resolveClosure(closure string) (string, error) {
	if closure == "" {
		return "", sysdep.Invalid("switch: system_closure is empty")
	}
	if !strings.HasPrefix(closure, "/nix/store/") || strings.Contains(closure, "..") {
		return "", sysdep.Invalid("switch: system_closure is not a /nix/store path")
	}
	real := filepath.Join(h.paths.Root, closure)
	if _, err := os.Stat(real); err != nil {
		if os.IsNotExist(err) {
			return "", sysdep.NotFound("switch: %s is not in the store share", closure)
		}
		return "", sysdep.Errf(sysdep.CodeInternal, "switch: stat closure: %w", err)
	}
	return real, nil
}

// kernelChanged compares the closure's kernel and initrd with the running
// system's. Either differing means a reboot.
func (h *Handler) kernelChanged(real string) (bool, error) {
	cur := h.paths.CurrentSystem()
	for _, name := range []string{"kernel", "initrd"} {
		want, err := resolve(filepath.Join(real, name))
		if err != nil {
			return false, sysdep.Errf(sysdep.CodeInternal, "switch: read new %s: %w", name, err)
		}
		have, err := resolve(filepath.Join(cur, name))
		if err != nil {
			// No running system to compare with (first activation): treat it
			// as unchanged so the switch proceeds rather than demanding a
			// reboot nobody can grant.
			h.log.Debug("no running system to compare against", "event", "switch", "part", name)
			continue
		}
		if want != have {
			return true, nil
		}
	}
	return false, nil
}

// resolve follows a symlink if there is one, and otherwise returns the path,
// so a closure that stores the kernel as a plain file compares too.
func resolve(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	target, err := os.Readlink(path)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(target) {
		return target, nil
	}
	return filepath.Join(filepath.Dir(path), target), nil
}

// RegisterPaths loads a `nix-store --dump-db` listing into the guest's nix
// database and leaves the stamp guest units wait for. Paths that arrive
// through the shared store are on disk but unknown to the database until
// this runs; `nix-env --set`, home-manager's activation and any user
// `nix` command that touches them fail without it (DECISIONS I-67).
// Loading is idempotent: a listing that is already registered changes
// nothing. Then every path the shared store serves is rooted (I-588).
func (h *Handler) RegisterPaths(ctx context.Context, registration []byte) error {
	h.viewMu.Lock()
	defer h.viewMu.Unlock()
	v, overlay := h.readView()
	// What the database says of the listed paths and of the ones the
	// shared store serves, asked once for both: the skip below needs the
	// first, the roots the second.
	var invalid, known map[string]bool
	if overlay {
		ask := registeredPaths(registration)
		if names, err := v.lowerNames(); err == nil {
			listed := map[string]bool{}
			for _, p := range ask {
				listed[p] = true
			}
			for name := range names {
				if p := storeDir + "/" + name; !listed[p] {
					ask = append(ask, p)
				}
			}
		}
		if got, err := h.invalidPaths(ctx, ask); err == nil {
			invalid, known = got, map[string]bool{}
			for _, p := range ask {
				known[p] = true
			}
		}
	}

	if err := h.loadRegistration(ctx, registration, overlay, invalid, known); err != nil {
		return err
	}
	if overlay {
		if invalid != nil {
			// The load made the listed paths valid.
			for _, p := range registeredPaths(registration) {
				delete(invalid, p)
			}
		}
		h.syncView(ctx, v, invalid, known)
	}
	return nil
}

// loadRegistration is RegisterPaths' load and stamp.
func (h *Handler) loadRegistration(ctx context.Context, registration []byte, overlay bool, invalid, known map[string]bool) error {
	sum := sha256.Sum256(registration)
	hash := hex.EncodeToString(sum[:])
	if b, err := os.ReadFile(h.paths.PathsLoaded()); err == nil && strings.TrimSpace(string(b)) == hash && !h.lostRegistration(registration, overlay, invalid, known) {
		if _, err := os.Stat(h.paths.NixDB()); err == nil {
			// The same registration was loaded on an earlier boot of this
			// volume and the database is there: the load would change
			// nothing, and it costs most of a second of the start (I-225).
			if err := os.WriteFile(h.paths.PathsRegistered(), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
				return sysdep.Errf(sysdep.CodeInternal, "register paths: stamp: %w", err)
			}
			h.log.Info("store paths already registered", "event", "register_paths", "bytes", len(registration), "skipped", true)
			return nil
		}
	}
	res, err := h.run.Run(ctx, sysdep.RunSpec{
		Argv:      []string{"nix-store", "--load-db"},
		Stdin:     registration,
		MaxOutput: 8 << 10,
		Env:       sysdep.DevEnv(h.paths, "root"),
	})
	if err != nil {
		return sysdep.Errf(sysdep.CodeInternal, "register paths: %w", err)
	}
	if res.ExitCode != 0 {
		return sysdep.Errf(sysdep.CodeInternal, "register paths: nix-store --load-db exited %d", res.ExitCode)
	}
	if err := os.WriteFile(h.paths.PathsRegistered(), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return sysdep.Errf(sysdep.CodeInternal, "register paths: stamp: %w", err)
	}
	// Recorded after the load and the stamp: a load that failed is
	// never skipped next time.
	if err := os.MkdirAll(filepath.Dir(h.paths.PathsLoaded()), 0o755); err == nil {
		_ = os.WriteFile(h.paths.PathsLoaded(), []byte(hash+"\n"), 0o644)
	}
	h.log.Info("store paths registered", "event", "register_paths", "bytes", len(registration))
	return nil
}

// lostRegistration reports whether a path this listing registers is no
// longer valid in the database although it was loaded before: a garbage
// collection inside the guest deleted it (the files are back once a start
// removed its whiteout, I-587). The listing is then loaded again rather
// than skipped (I-588). Without an overlay or an answer from nix it
// reports false, the I-225 behaviour.
func (h *Handler) lostRegistration(registration []byte, overlay bool, invalid, known map[string]bool) bool {
	if !overlay || invalid == nil {
		return false
	}
	for _, p := range registeredPaths(registration) {
		if known[p] && invalid[p] {
			return true
		}
	}
	return false
}

// setProfile points /nix/var/nix/profiles/system at the new closure.
func (h *Handler) setProfile(ctx context.Context, closure string) error {
	res, err := h.run.Run(ctx, sysdep.RunSpec{
		Argv:      []string{"nix-env", "--profile", h.paths.SystemProfile(), "--set", closure},
		MaxOutput: 8 << 10,
		Env:       sysdep.DevEnv(h.paths, "root"),
	})
	if err != nil {
		return sysdep.Errf(sysdep.CodeInternal, "set system profile: %w", err)
	}
	if res.ExitCode != 0 {
		return sysdep.Errf(sysdep.CodeInternal, "set system profile: nix-env exited %d", res.ExitCode)
	}
	return nil
}

// combine appends stderr to stdout and keeps the tail within cap, because the
// end of a failed activation is the part that says why.
func combine(stdout, stderr []byte, limit int) []byte {
	out := make([]byte, 0, len(stdout)+len(stderr)+1)
	out = append(out, stdout...)
	if len(stderr) > 0 {
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, stderr...)
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// restartSelfIfChanged schedules a restart of guestd when the system just
// switched to carries another guestd binary. The unit's activation no
// longer restarts guestd (restartIfChanged = false, I-143), so this is
// where the new binary takes over: a transient timer unit, not a child of
// guestd, runs `systemctl restart guestd` 3 s from now, after the Switch
// result has reached hostd; hostd sees guestd_lost then guestd_regained.
func (h *Handler) restartSelfIfChanged(ctx context.Context, real string) {
	unit, err := os.ReadFile(filepath.Join(real, "etc", "systemd", "system", "guestd.service"))
	if err != nil {
		return // no guestd unit in that system: nothing to restart into
	}
	next := execStart(string(unit))
	self, _ := os.Executable()
	if self, err = filepath.EvalSymlinks(self); err != nil || next == "" || next == self {
		return
	}
	_, err = h.run.Run(ctx, sysdep.RunSpec{
		Argv: []string{"systemd-run", "--collect", "--quiet", "--unit", "repose-guestd-restart",
			"--on-active=3", "systemctl", "restart", "guestd.service"},
		MaxOutput: 4 << 10,
	})
	if err != nil {
		h.log.Warn("guestd restart not scheduled; the new guestd runs at the next boot",
			"event", "switch", "result", "restart_not_scheduled")
		return
	}
	h.log.Info("guestd restart scheduled for the new binary", "event", "switch", "result", "restart_scheduled")
}

// execStart returns the program of a unit file's ExecStart line.
func execStart(unit string) string {
	for _, line := range strings.Split(unit, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart="); ok {
			rest = strings.TrimLeft(rest, "-@:+!")
			if f := strings.Fields(rest); len(f) > 0 {
				return f[0]
			}
		}
	}
	return ""
}
