// Package vzfake is a stand-in for the Mac's VM launcher, cmd/repose-vm
// (design 2, DECISIONS I-1662), for hostd's tests on any OS. Each VM is a
// unix socket at the path hostd's --api-socket names, answering the same
// subset of Cloud Hypervisor's HTTP API with the same bodies and status
// codes as the launcher, so hostd's real client (ch.HTTP) talks to it.
//
// It refuses what Virtualization.framework refuses, because a fake that
// says yes where VZ says no turns a test into a phantom:
//
//   - a restore while the user's session is locked: the save file's
//     Secure Enclave key is usable only in the unlocked session, and the
//     launcher exits 3 with "permission denied" (I-1662, n=5 on the lab
//     Mac);
//   - a save by a launcher running as root: the VM dies (I-1662);
//   - a resize of a running VM's disk: VZ cannot resize an attached disk
//     (I-1666);
//   - a pause of a VM that is not running, a resume of one that is not
//     paused, and a save of one that is not paused.
//
// A VM's process ending is reported through Host.OnExit, which a test
// wires to its unit table, as launchd sees the launcher exit.
package vzfake

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The launcher's own words, so a test can match what hostd passes on.
const (
	ErrResizeDisk = "resize of a running VM's disk is not supported on this host"
	ErrNoSave     = "no repose-vm.json in the restore directory"
	ErrLocked     = "permission denied" // VZ's restore error under a locked session
)

// VM states as vm.info reports them (main.swift stateName).
const (
	Running  = "Running"
	Paused   = "Paused"
	Shutdown = "Shutdown"
)

// Exit codes of the launcher (main.swift).
const (
	ExitOK      = 0
	ExitError   = 1 // the VM stopped with an error (a save as root)
	ExitRestore = 3 // the restore failed (locked session, no save file)
)

// Host is the Mac every fake VM runs on.
type Host struct {
	// OnExit is called, without the lock, when a VM's process ends: sock
	// is its API socket, code the launcher's exit code. It runs on its own
	// goroutine, after the start that ended it has returned, as launchd
	// sees a job exit after its bootstrap.
	OnExit func(sock string, code int)

	mu      sync.Mutex
	locked  bool
	asRoot  bool
	vms     map[string]*vm
	calls   []string
	refused []string
	closed  bool
	wg      sync.WaitGroup
}

type vm struct {
	sock  string
	state string
	srv   *http.Server
	ln    net.Listener
	argv  []string
}

// New returns a Mac with an unlocked session and a launcher run as the
// user.
func New() *Host { return &Host{vms: map[string]*vm{}} }

// SetLocked locks or unlocks the user's session.
func (h *Host) SetLocked(v bool) {
	h.mu.Lock()
	h.locked = v
	h.mu.Unlock()
}

// SetAsRoot makes every launcher started from now on run as root.
func (h *Host) SetAsRoot(v bool) {
	h.mu.Lock()
	h.asRoot = v
	h.mu.Unlock()
}

// Calls is every API call and start, in order: "start SOCK",
// "restore SOCK", "PUT vm.pause SOCK", ...
func (h *Host) Calls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

// Count is how many calls start with prefix ("PUT vm.resize-disk").
func (h *Host) Count(prefix string) int {
	n := 0
	for _, c := range h.Calls() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// State is the vm.info state of the VM at sock, or "" when no process
// serves it.
func (h *Host) State(sock string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if v := h.vms[sock]; v != nil {
		return v.state
	}
	return ""
}

func (h *Host) record(s string) {
	h.mu.Lock()
	h.calls = append(h.calls, s)
	h.mu.Unlock()
}

// Start is the launcher's process starting with argv, hostd's rendering
// of a boot or of a --restore. It returns an error only for an argv the
// launcher could not parse; a restore that fails ends the process, which
// OnExit reports, as on the Mac.
func (h *Host) Start(argv []string) error {
	sock, restore := "", ""
	for i := 0; i+1 < len(argv); i++ {
		switch argv[i] {
		case "--api-socket":
			sock = argv[i+1]
		case "--restore":
			for _, kv := range strings.Split(argv[i+1], ",") {
				if v, ok := strings.CutPrefix(kv, "source_url="); ok {
					u, err := url.Parse(v)
					if err != nil || u.Scheme != "file" {
						return fmt.Errorf("vzfake: --restore source_url %q is not a file URL", v)
					}
					restore = u.Path
				}
			}
		}
	}
	if sock == "" {
		return errors.New("vzfake: --api-socket is required")
	}
	h.mu.Lock()
	if old := h.vms[sock]; old != nil {
		h.mu.Unlock()
		return fmt.Errorf("vzfake: a launcher already serves %s", sock)
	}
	locked := h.locked
	h.mu.Unlock()
	if restore != "" {
		h.record("restore " + sock)
		if _, err := os.Stat(filepath.Join(restore, "repose-vm.json")); err != nil {
			h.exitLater(sock, ExitRestore)
			return nil
		}
		if _, err := os.Stat(filepath.Join(restore, "vz.state")); err != nil || locked {
			h.exitLater(sock, ExitRestore)
			return nil
		}
	} else {
		h.record("start " + sock)
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return fmt.Errorf("vzfake: bind %s: %w", sock, err)
	}
	v := &vm{sock: sock, state: Running, ln: ln, argv: append([]string(nil), argv...)}
	if restore != "" {
		v.state = Paused // CH, and so the launcher, comes up paused after --restore
	}
	v.srv = &http.Server{Handler: h.handler(v)}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		_ = ln.Close()
		return errors.New("vzfake: the host is closed")
	}
	h.vms[sock] = v
	h.wg.Add(1)
	h.mu.Unlock()
	go func() {
		defer h.wg.Done()
		_ = v.srv.Serve(ln)
	}()
	return nil
}

// Kill ends the VM at sock as SIGKILL does: no save, exit 137 to launchd.
func (h *Host) Kill(sock string) { h.end(sock, 137) }

// end stops serving sock and reports the exit.
func (h *Host) end(sock string, code int) {
	h.mu.Lock()
	v := h.vms[sock]
	delete(h.vms, sock)
	if v != nil {
		v.state = Shutdown
	}
	h.mu.Unlock()
	if v == nil {
		return
	}
	h.goAsync(func() {
		_ = v.srv.Close()
		_ = os.Remove(sock)
		if h.OnExit != nil {
			h.OnExit(sock, code)
		}
	})
}

func (h *Host) exitLater(sock string, code int) {
	h.goAsync(func() {
		if h.OnExit != nil {
			h.OnExit(sock, code)
		}
	})
}

// goAsync runs f on its own goroutine, which Close waits for; after Close
// it runs f inline, so nothing is added to a group being waited on.
func (h *Host) goAsync(f func()) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		f()
		return
	}
	h.wg.Add(1)
	h.mu.Unlock()
	go func() {
		defer h.wg.Done()
		f()
	}()
}

// Close ends every VM, without reporting the exits, and waits for every
// goroutine of the fake. A test closes it after the manager it serves.
func (h *Host) Close() {
	h.mu.Lock()
	h.closed = true
	vms := h.vms
	h.vms = map[string]*vm{}
	h.mu.Unlock()
	for sock, v := range vms {
		_ = v.srv.Close()
		_ = os.Remove(sock)
	}
	h.wg.Wait()
}

type errBody struct {
	Error string `json:"error"`
}

// Refused is every API call the launcher answered with an error, as
// "VERB: message".
func (h *Host) Refused() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.refused...)
}

func (h *Host) fail(w http.ResponseWriter, verb string, code int, msg string) {
	h.mu.Lock()
	h.refused = append(h.refused, verb+": "+msg)
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(errBody{msg})
}

func (h *Host) handler(v *vm) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verb := strings.TrimPrefix(r.URL.Path, "/api/v1/")
		h.record(r.Method + " " + verb + " " + v.sock)
		h.mu.Lock()
		asRoot := h.asRoot
		h.mu.Unlock()
		switch r.Method + " " + verb {
		case "GET vm.info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"state":%q,"config":{"cpus":{"boot_vcpus":1},"memory":{"size":1073741824}},"runtime":"vz"}`, h.stateOf(v))
		case "GET vmm.ping":
			_, _ = io.WriteString(w, `{"version":"repose-vm"}`)
		case "PUT vm.pause":
			if s := h.stateOf(v); s != Running {
				h.fail(w, verb, 500, "cannot pause in state "+s)
				return
			}
			h.setState(v, Paused)
			w.WriteHeader(204)
		case "PUT vm.resume":
			if s := h.stateOf(v); s != Paused {
				h.fail(w, verb, 500, "cannot resume in state "+s)
				return
			}
			h.setState(v, Running)
			w.WriteHeader(204)
		case "PUT vm.shutdown":
			h.setState(v, Shutdown)
			w.WriteHeader(204)
		case "PUT vmm.shutdown":
			w.WriteHeader(204)
			h.end(v.sock, ExitOK)
		case "PUT vm.snapshot":
			var body struct {
				DestinationURL string `json:"destination_url"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			u, err := url.Parse(body.DestinationURL)
			if err != nil || u.Scheme != "file" {
				h.fail(w, verb, 400, "destination_url must be a file URL")
				return
			}
			if s := h.stateOf(v); s != Paused {
				h.fail(w, verb, 500, "the VM must be paused to snapshot, it is "+s)
				return
			}
			if asRoot {
				// The VM dies under a save made as root (I-1662): the
				// connection ends with no answer and so does the process.
				h.end(v.sock, ExitError)
				if hj, ok := w.(http.Hijacker); ok {
					if c, _, err := hj.Hijack(); err == nil {
						_ = c.Close()
					}
				}
				return
			}
			if err := save(u.Path, v.argv); err != nil {
				h.fail(w, verb, 500, err.Error())
				return
			}
			w.WriteHeader(204)
		case "PUT vm.resize":
			w.WriteHeader(204) // the balloon; VZ's traditional balloon takes any target
		case "PUT vm.resize-disk":
			h.fail(w, verb, 500, ErrResizeDisk)
		default:
			h.fail(w, verb, 404, "not supported")
		}
	})
}

func (h *Host) stateOf(v *vm) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return v.state
}

func (h *Host) setState(v *vm, s string) {
	h.mu.Lock()
	v.state = s
	h.mu.Unlock()
}

// save writes what the launcher writes: the boot argv beside VZ's state
// file.
func save(dir string, argv []string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	j, err := json.Marshal(map[string]any{"args": argv})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "repose-vm.json"), j, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vz.state"), []byte("vz"), 0o600); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	return nil
}
