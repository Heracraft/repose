// Package ch renders the Cloud Hypervisor invocation for a guest and talks
// to its API socket for shutdown, pause, resume and info.
package ch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Spec is everything the argv needs; the guest package fills it from the
// guest record and the system closure.
type Spec struct {
	GuestDir     string // /var/lib/repose/guests/<id>
	Kernel       string // <closure>/kernel
	Initrd       string // <closure>/initrd
	Init         string // <closure>/init
	KernelParams string // contents of <closure>/kernel-params
	IP           string
	Gateway      string
	Netmask      string
	Tap          string
	MAC          string
	CID          uint32
	VolumeDev    string
	VCPUs        uint32
	MemMiB       uint64
	// DiskIOPS and DiskBytesPerSec are the disk's rate limiter, read and
	// write together, each a token bucket holding one second's worth
	// (DECISIONS I-450); zero leaves that one unlimited.
	DiskIOPS        uint64
	DiskBytesPerSec uint64
	StoreTag        string // ro-store
	// AuthTag is the user's Claude login share (DECISIONS I-278); empty
	// when the guest has none (no user id recorded), and then no second
	// --fs is rendered.
	AuthTag string // claude-auth
}

// Paths under the guest directory.
func APISocket(dir string) string     { return filepath.Join(dir, "ch.sock") }
func VsockSocket(dir string) string   { return filepath.Join(dir, "vsock.sock") }
func ConsoleSocket(dir string) string { return filepath.Join(dir, "console.sock") }

// VirtiofsSocket lives in a subdirectory virtiofsd owns, since the guest
// directory itself is writable only by hostd and the hypervisor's user.
func VirtiofsSocket(dir string) string { return filepath.Join(dir, "virtiofsd", "virtiofsd.sock") }

// AuthSocket is the login share's virtiofsd socket, in its own directory
// for the same reason (DECISIONS I-278).
func AuthSocket(dir string) string { return filepath.Join(dir, "virtiofsd-auth", "virtiofsd.sock") }

// Cmdline renders the kernel command line: the closure's init and params,
// the serial console, and the static address the guest's networkd reads.
func (s Spec) Cmdline() string {
	parts := []string{"init=" + s.Init}
	if p := strings.TrimSpace(s.KernelParams); p != "" {
		parts = append(parts, p)
	}
	parts = append(parts, "console=ttyS0",
		fmt.Sprintf("ip=%s::%s:%s::eth0:off", s.IP, s.Gateway, s.Netmask))
	return strings.Join(parts, " ")
}

// diskLimits renders the --disk rate limiter options: buckets refilled
// every second, so the size is the rate.
func (s Spec) diskLimits() string {
	var b strings.Builder
	if s.DiskBytesPerSec > 0 {
		fmt.Fprintf(&b, ",bw_size=%d,bw_refill_time=1000", s.DiskBytesPerSec)
	}
	if s.DiskIOPS > 0 {
		fmt.Fprintf(&b, ",ops_size=%d,ops_refill_time=1000", s.DiskIOPS)
	}
	return b.String()
}

// Args renders the cloud-hypervisor argv.
func (s Spec) Args() []string {
	args := []string{
		"cloud-hypervisor",
		"--api-socket", APISocket(s.GuestDir),
		"--kernel", s.Kernel,
		"--initramfs", s.Initrd,
		"--cmdline", s.Cmdline(),
		"--cpus", fmt.Sprintf("boot=%d", s.VCPUs),
		"--memory", fmt.Sprintf("size=%dM,shared=on", s.MemMiB),
		// image_type=raw: Cloud Hypervisor 53 refuses sector-0 writes on a
		// disk whose type it auto-detected, and ext4 keeps its superblock
		// there (DECISIONS I-63). direct=on: the volume is opened O_DIRECT,
		// so the guest's disk writes never sit in the host page cache. That
		// cache is charged to guest@<id>, whose MemoryMax is the guest's RAM
		// (all shmem, never reclaimable) plus a small overhead; buffered
		// writes filled the overhead with pages under writeback and the
		// kernel OOM-killed the hypervisor (DECISIONS I-230). The guest has
		// its own page cache; a second one on the host only doubled it.
		// The rate limiter is Cloud Hypervisor's own, applied before the
		// request reaches the volume, so it holds with direct=on and covers
		// nothing the host does to the volume itself (snapshots, restores).
		"--disk", "path=" + s.VolumeDev + ",image_type=raw,direct=on" + s.diskLimits(),
		"--net", fmt.Sprintf("tap=%s,mac=%s", s.Tap, s.MAC),
		"--fs", fmt.Sprintf("tag=%s,socket=%s", s.StoreTag, VirtiofsSocket(s.GuestDir)),
		"--vsock", fmt.Sprintf("cid=%d,socket=%s", s.CID, VsockSocket(s.GuestDir)),
		"--serial", "socket=" + ConsoleSocket(s.GuestDir),
		"--console", "off",
		// Cloud Hypervisor's default; written out so a build that flips the
		// default, or an operator reading ch.args, sees the filter is on.
		"--seccomp", "true",
	}
	if s.AuthTag != "" {
		args = append(args, "--fs", fmt.Sprintf("tag=%s,socket=%s", s.AuthTag, AuthSocket(s.GuestDir)))
	}
	return args
}

// Client is the API-socket side.
type Client interface {
	Shutdown(ctx context.Context, sock string) error
	Pause(ctx context.Context, sock string) error
	Resume(ctx context.Context, sock string) error
	Info(ctx context.Context, sock string) (json.RawMessage, error)
	// ResizeDisk tells the hypervisor a disk's backing device grew, so the
	// guest's virtio-blk reports the new capacity (vm.resize-disk).
	ResizeDisk(ctx context.Context, sock, id string, newSize uint64) error
	Version(ctx context.Context) (string, error)
}

// DiskID is the identifier Cloud Hypervisor gives the guest's one disk
// (the first --disk without an explicit id).
const DiskID = "_disk0"

// HTTP talks to the API socket over HTTP/unix.
type HTTP struct {
	Timeout time.Duration
	// VersionArgv is what prints the version, usually cloud-hypervisor --version.
	VersionFn func(ctx context.Context) (string, error)
}

func (h *HTTP) client(sock string) *http.Client {
	t := h.Timeout
	if t == 0 {
		t = 10 * time.Second
	}
	return &http.Client{Timeout: t, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
}

func (h *HTTP) put(ctx context.Context, sock, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://localhost/api/v1/"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client(sock).Do(req)
	if err != nil {
		return nil, fmt.Errorf("ch api %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()                // body drained below
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // error bodies are informational only
	if resp.StatusCode >= 300 {
		return body, fmt.Errorf("ch api %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (h *HTTP) get(ctx context.Context, sock, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/api/v1/"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client(sock).Do(req)
	if err != nil {
		return nil, fmt.Errorf("ch api %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }() // body read below
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return body, fmt.Errorf("ch api %s: status %d", path, resp.StatusCode)
	}
	return body, nil
}

func (h *HTTP) putJSON(ctx context.Context, sock, path string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://localhost/api/v1/"+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client(sock).Do(req)
	if err != nil {
		return fmt.Errorf("ch api %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()               // body drained below
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // error bodies are informational only
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ch api %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(out)))
	}
	return nil
}

// ResizeDisk implements Client. Without it an lvextend on the host is
// invisible to the guest: virtio-blk keeps the capacity it was created
// with and resize2fs has nothing to grow (DECISIONS I-66).
func (h *HTTP) ResizeDisk(ctx context.Context, sock, id string, newSize uint64) error {
	return h.putJSON(ctx, sock, "vm.resize-disk", map[string]any{"id": id, "desired_size": newSize})
}

// Shutdown ends the guest and then the hypervisor process. vm.shutdown
// alone tears the VM down but leaves the VMM waiting for a new one, so
// the unit stayed active and every fallback stop waited 15 s more before
// the kill (host-01, 2026-09-23 04:04:35-04:04:50; DECISIONS I-186).
func (h *HTTP) Shutdown(ctx context.Context, sock string) error {
	_, err := h.put(ctx, sock, "vm.shutdown")
	if _, verr := h.put(ctx, sock, "vmm.shutdown"); err == nil {
		err = verr
	}
	return err
}

func (h *HTTP) Pause(ctx context.Context, sock string) error {
	_, err := h.put(ctx, sock, "vm.pause")
	return err
}

func (h *HTTP) Resume(ctx context.Context, sock string) error {
	_, err := h.put(ctx, sock, "vm.resume")
	return err
}

func (h *HTTP) Info(ctx context.Context, sock string) (json.RawMessage, error) {
	b, err := h.get(ctx, sock, "vm.info")
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

func (h *HTTP) Version(ctx context.Context) (string, error) {
	if h.VersionFn == nil {
		return "", nil
	}
	return h.VersionFn(ctx)
}

// Fake records API calls.
type Fake struct {
	mu    sync.Mutex
	Calls []string
	Fail  map[string]error
	Ver   string
}

func (f *Fake) record(op, sock string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, op+" "+sock)
	return f.Fail[op]
}

func (f *Fake) Shutdown(_ context.Context, sock string) error { return f.record("shutdown", sock) }
func (f *Fake) Pause(_ context.Context, sock string) error    { return f.record("pause", sock) }
func (f *Fake) Resume(_ context.Context, sock string) error   { return f.record("resume", sock) }
func (f *Fake) ResizeDisk(_ context.Context, sock, _ string, _ uint64) error {
	return f.record("resize-disk", sock)
}
func (f *Fake) Info(_ context.Context, sock string) (json.RawMessage, error) {
	if err := f.record("info", sock); err != nil {
		return nil, err
	}
	return json.RawMessage(`{"state":"Running"}`), nil
}
func (f *Fake) Version(context.Context) (string, error) { return f.Ver, nil }
