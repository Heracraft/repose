// Package baseimg is hostd's cache of erofs base bundles (DECISIONS
// I-1458, I-1478): the kernel, initramfs and rootfs.erofs an erofs guest
// boots, one directory per base version and architecture under the
// images directory (docs/interfaces/host-conventions.md, "Base images on
// the host").
//
// A bundle is fetched file by file into a temporary directory beside the
// cache, each file checked against its own SHA-256 while it streams in,
// continued with a Range request after a stall or a broken connection
// (the pattern of DECISIONS I-1414), and renamed into place whole, so a
// directory under its final name is always a complete, verified bundle.
// A second request for a version that is being fetched waits for the
// first. The cache's records live in hostd's bbolt (state.BaseImage);
// GC removes a bundle no guest names, that is not the newest fetched,
// once it has been unreferenced for GCAfter.
package baseimg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/metrics"
	"github.com/heracraft/repose/internal/hostd/state"
)

// The bundle's files, by the names PR 2's build gives them
// (images/guest/build.sh) and the names they keep in the cache.
const (
	KernelFile = "vmlinuz"
	InitrdFile = "initramfs.img"
	RootfsFile = "rootfs.erofs"
)

// DefaultDir is the images directory on a host (hostd --images-dir).
const DefaultDir = "/var/lib/repose/images"

// GCAfter is how long a bundle stays after the last guest naming it
// went: a project that is restored or started again within a day finds
// its base still here.
const GCAfter = 24 * time.Hour

// Defaults for the fetch, the appliance download's numbers (I-1414): a
// stall is a whole window with no byte, and every attempt after the
// first continues from what is on disk.
const (
	DefaultStallWindow = 60 * time.Second
	DefaultTries       = 3
)

var versionRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ValidVersion reports whether v is a base_versions label, which is also
// a safe directory name (it cannot hold a slash or be "..").
func ValidVersion(v string) bool { return versionRE.MatchString(v) && v != "." && v != ".." }

// Source is one file of a bundle: where it is and its lower-case hex
// SHA-256.
type Source struct {
	URL    string
	SHA256 string
}

// Spec is a BaseImage as the api sent it, checked.
type Spec struct {
	Version       string
	KernelVersion string
	Kernel        Source
	Initrd        Source
	Rootfs        Source
}

// ErrInvalid wraps every refusal of a BaseImage's fields.
var ErrInvalid = errors.New("invalid base image")

// FromProto checks a BaseImage: a valid version, and three files each
// with a URL and a 64-hex-digit SHA-256.
func FromProto(b *hostdv1.BaseImage) (Spec, error) {
	if b == nil {
		return Spec{}, fmt.Errorf("%w: none given", ErrInvalid)
	}
	s := Spec{
		Version: b.GetVersion(), KernelVersion: b.GetKernelVersion(),
		Kernel: Source{URL: b.GetKernelUrl(), SHA256: strings.ToLower(b.GetKernelSha256())},
		Initrd: Source{URL: b.GetInitramfsUrl(), SHA256: strings.ToLower(b.GetInitramfsSha256())},
		Rootfs: Source{URL: b.GetUrl(), SHA256: strings.ToLower(b.GetSha256())},
	}
	if !ValidVersion(s.Version) {
		return Spec{}, fmt.Errorf("%w: version %q must match [A-Za-z0-9._-]{1,64}", ErrInvalid, s.Version)
	}
	for _, f := range []struct {
		name string
		src  Source
	}{{"url", s.Rootfs}, {"kernel_url", s.Kernel}, {"initramfs_url", s.Initrd}} {
		if f.src.URL == "" {
			return Spec{}, fmt.Errorf("%w: %s required", ErrInvalid, f.name)
		}
		if !strings.HasPrefix(f.src.URL, "https://") && !strings.HasPrefix(f.src.URL, "http://") && !strings.HasPrefix(f.src.URL, "file://") {
			return Spec{}, fmt.Errorf("%w: %s must be an https, http or file URL", ErrInvalid, f.name)
		}
		if b, err := hex.DecodeString(f.src.SHA256); err != nil || len(b) != sha256.Size {
			return Spec{}, fmt.Errorf("%w: the sha256 of %s must be 64 hex digits", ErrInvalid, f.name)
		}
	}
	return s, nil
}

// Bundle is a cached bundle's paths.
type Bundle struct {
	Version string
	Dir     string
	Kernel  string
	Initrd  string
	Rootfs  string
}

// Store is where the cache's records live: hostd's bbolt.
type Store interface {
	GetBaseImage(key string) (*state.BaseImage, error)
	PutBaseImage(r *state.BaseImage) error
	DeleteBaseImage(key string) error
	ListBaseImages() ([]*state.BaseImage, error)
}

// Cache is the images directory.
type Cache struct {
	// Dir is the images directory; Arch the host's ("x86_64",
	// "aarch64"), which names each bundle's directory beside its
	// version.
	Dir  string
	Arch string
	// Store keeps the records; Refs returns the base versions guests on
	// this host name (running or not), which GC never removes.
	Store Store
	Refs  func() (map[string]bool, error)
	// HTTP fetches; nil is a client with no whole-request deadline, since
	// a rootfs is a gigabyte and the stall window is what bounds it.
	HTTP *http.Client
	// StallWindow and Tries tune the fetch; zero takes the defaults.
	StallWindow time.Duration
	Tries       int
	// RetryWait is the pause between attempts; zero is 2 s.
	RetryWait time.Duration
	Metrics   *metrics.M
	Log       *slog.Logger
	Now       func() time.Time

	mu       sync.Mutex
	inflight map[string]*call
}

type call struct {
	done chan struct{}
	b    Bundle
	err  error
}

func (c *Cache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Cache) log() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Key is a bundle's record key and directory name.
func (c *Cache) Key(version string) string { return version + "-" + c.Arch }

// BundleDir is a version's directory in the cache.
func (c *Cache) BundleDir(version string) string { return filepath.Join(c.Dir, c.Key(version)) }

func (c *Cache) tmpDir(version string) string {
	return filepath.Join(c.Dir, ".tmp-"+c.Key(version))
}

// Paths is a version's bundle, whether or not it is cached.
func (c *Cache) Paths(version string) Bundle {
	d := c.BundleDir(version)
	return Bundle{Version: version, Dir: d, Kernel: filepath.Join(d, KernelFile), Initrd: filepath.Join(d, InitrdFile), Rootfs: filepath.Join(d, RootfsFile)}
}

// Has reports whether version's bundle is in the cache: its directory
// holds all three files. A directory under its final name only ever
// comes from a fetch that verified every file.
func (c *Cache) Has(version string) bool {
	if !ValidVersion(version) {
		return false
	}
	b := c.Paths(version)
	for _, p := range []string{b.Kernel, b.Initrd, b.Rootfs} {
		if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
			return false
		}
	}
	return true
}

// Ensure returns spec's bundle, fetching it when the cache does not hold
// it. A fetch of the same version already running is waited for, not
// repeated.
func (c *Cache) Ensure(ctx context.Context, spec Spec) (Bundle, error) {
	if !ValidVersion(spec.Version) {
		return Bundle{}, fmt.Errorf("%w: version %q", ErrInvalid, spec.Version)
	}
	if c.Has(spec.Version) {
		c.noteCached(spec.Version)
		return c.Paths(spec.Version), nil
	}
	key := c.Key(spec.Version)
	c.mu.Lock()
	if c.inflight == nil {
		c.inflight = map[string]*call{}
	}
	if cl, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-cl.done:
			return cl.b, cl.err
		case <-ctx.Done():
			return Bundle{}, ctx.Err()
		}
	}
	cl := &call{done: make(chan struct{})}
	c.inflight[key] = cl
	c.mu.Unlock()

	cl.b, cl.err = c.fetch(ctx, spec)
	c.mu.Lock()
	delete(c.inflight, key)
	c.mu.Unlock()
	close(cl.done)
	return cl.b, cl.err
}

// Fetching reports whether a fetch of version is running now.
func (c *Cache) Fetching(version string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.inflight[c.Key(version)]
	return ok
}

// noteCached makes sure a bundle found on disk has a record, as one
// fetched by a hostd whose record write failed, or a cache carried over
// from a rebuilt state.db, would not.
func (c *Cache) noteCached(version string) {
	if c.Store == nil {
		return
	}
	key := c.Key(version)
	if _, err := c.Store.GetBaseImage(key); err == nil {
		return
	}
	r := &state.BaseImage{Key: key, Version: version, Arch: c.Arch, Bytes: dirBytes(c.BundleDir(version)), FetchedAt: c.now().UTC()}
	if err := c.Store.PutBaseImage(r); err != nil {
		c.log().Warn("base image record not written", "event", "base_image", "base_version", version, "err", err.Error())
	}
	c.publish()
}

// checksumError is a whole file that is not the one the api named.
type checksumError struct {
	name, got, want string
	resumed         bool
}

func (e *checksumError) Error() string {
	return fmt.Sprintf("%s: checksum %s, not %s", e.name, e.got, e.want)
}

// IsChecksum reports whether err is a file whose digest differed, the
// failure docs/interfaces/grpc-hostd.md names "base image checksum
// mismatch".
func IsChecksum(err error) bool {
	var ce *checksumError
	return errors.As(err, &ce)
}

type statusError struct {
	status int
}

func (e *statusError) Error() string { return fmt.Sprintf("the server answered %d", e.status) }

type stallError struct {
	after  int64
	window time.Duration
}

func (e *stallError) Error() string {
	return fmt.Sprintf("no data for %s after %d bytes", e.window, e.after)
}

type diskError struct{ err error }

func (e *diskError) Error() string { return "image cache: " + e.err.Error() }
func (e *diskError) Unwrap() error { return e.err }

func reason(err error) string {
	var ce *checksumError
	var se *statusError
	var st *stallError
	var de *diskError
	switch {
	case errors.As(err, &ce):
		return "checksum"
	case errors.As(err, &se):
		return "status"
	case errors.As(err, &st):
		return "stall"
	case errors.As(err, &de):
		return "disk"
	}
	return "network"
}

func (c *Cache) fetch(ctx context.Context, spec Spec) (Bundle, error) {
	start := c.now()
	log := c.log().With("base_version", spec.Version, "arch", c.Arch)
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return Bundle{}, &diskError{err}
	}
	tmp := c.tmpDir(spec.Version)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return Bundle{}, &diskError{err}
	}
	log.Info("fetching base image", "event", "base_image_fetch")
	var total int64
	for _, f := range []struct {
		name string
		src  Source
	}{{KernelFile, spec.Kernel}, {InitrdFile, spec.Initrd}, {RootfsFile, spec.Rootfs}} {
		dest := filepath.Join(tmp, f.name)
		if st, err := os.Stat(dest); err == nil && st.Mode().IsRegular() {
			// An earlier fetch of this version finished this file and
			// verified it before naming it; only the others are left.
			total += st.Size()
			continue
		}
		n, err := c.fetchTo(ctx, f.src.URL, dest, f.src.SHA256)
		if err != nil {
			if m := c.Metrics; m != nil {
				m.BaseImageFetchErrors.WithLabelValues(reason(err)).Inc()
			}
			if IsChecksum(err) {
				// Nothing of a bundle with a file that is not the one
				// named is kept: the other files may be of the same
				// wrong release.
				_ = os.RemoveAll(tmp) // the error being returned is the mismatch; a leftover is swept by GC
			}
			log.Error("base image fetch failed", "event", "base_image_fetch", "file", f.name, "reason", reason(err), "err", err.Error())
			return Bundle{}, fmt.Errorf("base image %s, %s: %w", spec.Version, f.name, err)
		}
		total += n
	}
	final := c.BundleDir(spec.Version)
	if c.Has(spec.Version) {
		// Another hostd process cannot hold the cache (bbolt's lock), so
		// this is a bundle that appeared some other way: keep it.
		_ = os.RemoveAll(tmp) // the bundle in place is the one used
	} else {
		_ = os.RemoveAll(final) // a partial directory under the final name cannot come from a fetch; it is replaced
		if err := os.Rename(tmp, final); err != nil {
			return Bundle{}, &diskError{err}
		}
		syncDir(c.Dir)
	}
	if c.Store != nil {
		r := &state.BaseImage{Key: c.Key(spec.Version), Version: spec.Version, Arch: c.Arch, Bytes: uint64(total), FetchedAt: c.now().UTC()}
		if err := c.Store.PutBaseImage(r); err != nil {
			log.Warn("base image record not written", "event", "base_image_fetch", "err", err.Error())
		}
	}
	dur := c.now().Sub(start)
	if m := c.Metrics; m != nil {
		m.BaseImageFetchDuration.Observe(dur.Seconds())
	}
	c.publish()
	log.Info("base image fetched", "event", "base_image_fetch", "bytes", total, "duration_ms", dur.Milliseconds())
	return c.Paths(spec.Version), nil
}

func (c *Cache) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{}
}

func (c *Cache) window() time.Duration {
	if c.StallWindow > 0 {
		return c.StallWindow
	}
	return DefaultStallWindow
}

// retryable is whether another attempt is worth making: a stall, a
// network failure or a busy server. A refusal or a missing file is not,
// and a checksum mismatch only when the attempt continued a part file
// (the part may have been of another release).
func retryable(err error) bool {
	var se *statusError
	if errors.As(err, &se) {
		return se.status == http.StatusTooManyRequests || se.status >= 500
	}
	var ce *checksumError
	if errors.As(err, &ce) {
		return ce.resumed
	}
	var de *diskError
	return !errors.As(err, &de)
}

// fetchTo downloads url into dest, checking its SHA-256, in up to Tries
// attempts that each continue from dest.part.
func (c *Cache) fetchTo(ctx context.Context, url, dest, want string) (int64, error) {
	tries := c.Tries
	if tries <= 0 {
		tries = DefaultTries
	}
	wait := c.RetryWait
	if wait <= 0 {
		wait = 2 * time.Second
	}
	var err error
	var n int64
	for try := 1; ; try++ {
		n, err = c.fetchOnce(ctx, url, dest, want)
		if err == nil || try >= tries || ctx.Err() != nil || !retryable(err) {
			return n, err
		}
		c.log().Warn("base image fetch retrying", "event", "base_image_fetch", "file", filepath.Base(dest), "try", try+1, "reason", reason(err))
		select {
		case <-ctx.Done():
			return n, err
		case <-time.After(wait):
		}
	}
}

// progressReader counts what was read, for the stall watchdog.
type progressReader struct {
	r io.Reader
	n atomic.Int64
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n.Add(int64(n))
	return n, err
}

func watch(p *progressReader, window time.Duration, cancel context.CancelFunc, fired *atomic.Bool, done <-chan struct{}) {
	t := time.NewTicker(window)
	defer t.Stop()
	last := p.n.Load()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			now := p.n.Load()
			if now == last {
				fired.Store(true)
				cancel()
				return
			}
			last = now
		}
	}
}

// openRange is the body at url from byte off and the offset it really
// starts at: off when the server honoured the range, 0 when it sent the
// whole file.
func (c *Cache) openRange(ctx context.Context, url string, off int64) (io.ReadCloser, int64, error) {
	if path, ok := strings.CutPrefix(url, "file://"); ok {
		f, err := os.Open(path)
		if err != nil {
			return nil, 0, err
		}
		if off > 0 {
			if _, err := f.Seek(off, io.SeekStart); err != nil {
				_ = f.Close() // returning the seek error
				return nil, 0, err
			}
		}
		return f, off, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if off > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, 0, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp.Body, 0, nil
	case http.StatusPartialContent:
		return resp.Body, off, nil
	}
	_ = resp.Body.Close() // a refusal's body is not read
	return nil, 0, &statusError{status: resp.StatusCode}
}

func hashFile(h hash.Hash, path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }() // read only
	return io.Copy(h, f)
}

// fetchOnce is one attempt at url into dest.part, continuing from what an
// earlier attempt left there; the part is renamed onto dest only once its
// digest is want's.
func (c *Cache) fetchOnce(ctx context.Context, url, dest, want string) (int64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	part := dest + ".part"
	h := sha256.New()
	var off int64
	if st, err := os.Stat(part); err == nil && st.Size() > 0 {
		if n, err := hashFile(h, part); err == nil {
			off = n
		} else {
			h.Reset()
		}
	}
	pr := &progressReader{}
	var fired atomic.Bool
	done := make(chan struct{})
	window := c.window()
	go watch(pr, window, cancel, &fired, done)
	defer close(done)
	named := func(err error, had int64) error {
		if fired.Load() {
			return &stallError{after: had + pr.n.Load(), window: window}
		}
		return err
	}
	body, start, err := c.openRange(ctx, url, off)
	if err != nil {
		return 0, named(err, off)
	}
	defer func() { _ = body.Close() }() // read to the end or abandoned with the attempt
	if start != off {
		h.Reset()
		off = 0
	}
	flags := os.O_CREATE | os.O_WRONLY
	if off == 0 {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return 0, &diskError{err}
	}
	pr.r = body
	_, cerr := io.Copy(io.MultiWriter(f, h), pr)
	if cerr == nil {
		if err := f.Sync(); err != nil {
			cerr = &diskError{err}
		}
	}
	if err := f.Close(); cerr == nil && err != nil {
		cerr = &diskError{err}
	}
	if cerr != nil {
		// What arrived stays in the part: the next attempt continues.
		return 0, named(cerr, off)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		_ = os.Remove(part) // a wrong file is not continued
		return 0, &checksumError{name: filepath.Base(dest), got: got, want: want, resumed: off > 0}
	}
	st, err := os.Stat(part)
	if err != nil {
		return 0, &diskError{err}
	}
	if err := os.Rename(part, dest); err != nil {
		return 0, &diskError{err}
	}
	return st.Size(), nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()  // best effort: the rename is what matters, and it is atomic either way
		_ = d.Close() // read only
	}
}

func dirBytes(dir string) uint64 {
	var n uint64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			n += uint64(fi.Size())
		}
		return nil
	})
	return n
}

// Newest is the version of the newest fetched bundle in the cache for
// this host's arch, "" when there is none.
func (c *Cache) Newest() string {
	if c.Store == nil {
		return ""
	}
	rs, err := c.Store.ListBaseImages()
	if err != nil {
		return ""
	}
	var best *state.BaseImage
	for _, r := range rs {
		if r.Arch != c.Arch || !c.Has(r.Version) {
			continue
		}
		if best == nil || r.FetchedAt.After(best.FetchedAt) {
			best = r
		}
	}
	if best == nil {
		return ""
	}
	return best.Version
}

// Versions lists the cached versions for this arch, oldest fetched first.
func (c *Cache) Versions() []string {
	if c.Store == nil {
		return nil
	}
	rs, err := c.Store.ListBaseImages()
	if err != nil {
		return nil
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].FetchedAt.Before(rs[j].FetchedAt) })
	var out []string
	for _, r := range rs {
		if r.Arch == c.Arch && c.Has(r.Version) {
			out = append(out, r.Version)
		}
	}
	return out
}

// publish sets the two cache gauges from the records.
func (c *Cache) publish() {
	if c.Metrics == nil || c.Store == nil {
		return
	}
	rs, err := c.Store.ListBaseImages()
	if err != nil {
		return
	}
	var bytes uint64
	for _, r := range rs {
		bytes += r.Bytes
	}
	c.Metrics.BaseImageBytes.Set(float64(bytes))
	c.Metrics.BaseImageVersions.Set(float64(len(rs)))
}

// GC removes the bundles no guest names that are not the newest fetched
// and have been unreferenced for GCAfter, and the temporary directories
// of fetches that are not running and are older than GCAfter. It marks
// a bundle unreferenced the first time it finds one, and clears the mark
// of one a guest names again. It returns the versions it removed.
func (c *Cache) GC(ctx context.Context) ([]string, error) {
	if c.Store == nil {
		return nil, nil
	}
	refs := map[string]bool{}
	if c.Refs != nil {
		r, err := c.Refs()
		if err != nil {
			// Without the references nothing can be called unreferenced.
			return nil, fmt.Errorf("base image gc: guests unreadable: %w", err)
		}
		refs = r
	}
	rs, err := c.Store.ListBaseImages()
	if err != nil {
		return nil, err
	}
	newest := map[string]*state.BaseImage{} // per arch
	for _, r := range rs {
		if n := newest[r.Arch]; n == nil || r.FetchedAt.After(n.FetchedAt) {
			newest[r.Arch] = r
		}
	}
	now := c.now().UTC()
	var removed []string
	for _, r := range rs {
		if ctx.Err() != nil {
			break
		}
		keep := refs[r.Version] || newest[r.Arch] == r || c.Fetching(r.Version)
		if keep {
			if !r.UnreferencedSince.IsZero() {
				r.UnreferencedSince = time.Time{}
				_ = c.Store.PutBaseImage(r) // the mark is re-derived at the next GC
			}
			continue
		}
		if r.UnreferencedSince.IsZero() {
			r.UnreferencedSince = now
			_ = c.Store.PutBaseImage(r) // a failed write only delays the removal by a GC
			continue
		}
		if now.Sub(r.UnreferencedSince) < GCAfter {
			continue
		}
		dir := filepath.Join(c.Dir, r.Key)
		if err := os.RemoveAll(dir); err != nil {
			c.log().Warn("base image not removed", "event", "base_image_gc", "base_version", r.Version, "err", err.Error())
			continue
		}
		if err := c.Store.DeleteBaseImage(r.Key); err != nil {
			c.log().Warn("base image record not removed", "event", "base_image_gc", "base_version", r.Version, "err", err.Error())
		}
		removed = append(removed, r.Version)
		c.log().Info("base image removed", "event", "base_image_gc", "base_version", r.Version, "arch", r.Arch)
	}
	c.sweepTmp(now)
	c.publish()
	return removed, nil
}

// sweepTmp removes temporary fetch directories nothing is using.
func (c *Cache) sweepTmp(now time.Time) {
	des, err := os.ReadDir(c.Dir)
	if err != nil {
		return
	}
	for _, d := range des {
		name := d.Name()
		if !d.IsDir() || !strings.HasPrefix(name, ".tmp-") {
			continue
		}
		key := strings.TrimPrefix(name, ".tmp-")
		ver := strings.TrimSuffix(key, "-"+c.Arch)
		if c.Fetching(ver) {
			continue
		}
		fi, err := d.Info()
		if err != nil || now.Sub(fi.ModTime()) < GCAfter {
			continue
		}
		_ = os.RemoveAll(filepath.Join(c.Dir, name)) // a leftover; the next fetch of it starts over
	}
}
