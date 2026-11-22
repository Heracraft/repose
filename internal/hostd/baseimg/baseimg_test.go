package baseimg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/protobuf/proto"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/metrics"
	"github.com/heracraft/repose/internal/hostd/state"
)

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// server serves three files per version and counts GETs per path. stallAt,
// when set for a path, makes the first full request for it send that many
// bytes and then hang; a Range request is served from the offset.
type server struct {
	t       *testing.T
	files   map[string][]byte
	gets    sync.Map // path -> *atomic.Int64
	ranges  sync.Map // path -> *atomic.Int64
	stallAt map[string]int
	stalled sync.Map // path -> bool
	hold    chan struct{}
	srv     *httptest.Server
}

func newServer(t *testing.T) *server {
	s := &server{t: t, files: map[string][]byte{}, stallAt: map[string]int{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *server) count(m *sync.Map, path string) *atomic.Int64 {
	v, _ := m.LoadOrStore(path, &atomic.Int64{})
	return v.(*atomic.Int64)
}

func (s *server) serve(w http.ResponseWriter, r *http.Request) {
	b, ok := s.files[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.count(&s.gets, r.URL.Path).Add(1)
	if s.hold != nil {
		<-s.hold
	}
	if rg := r.Header.Get("Range"); rg != "" {
		s.count(&s.ranges, r.URL.Path).Add(1)
		off, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
		if err != nil || off > len(b) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, len(b)-1, len(b)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(b[off:])
		return
	}
	if at, ok := s.stallAt[r.URL.Path]; ok {
		if _, done := s.stalled.LoadOrStore(r.URL.Path, true); !done {
			w.Header().Set("Content-Length", strconv.Itoa(len(b)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(b[:at])
			w.(http.Flusher).Flush()
			<-r.Context().Done() // the stall: nothing more until the client gives up
			return
		}
	}
	_, _ = w.Write(b)
}

// publish puts version's three files on the server and returns its BaseImage.
func (s *server) publish(version string, rootfs []byte) *hostdv1.BaseImage {
	k := []byte("kernel " + version)
	i := []byte("initramfs " + version)
	s.files["/"+version+"/vmlinuz"] = k
	s.files["/"+version+"/initramfs.img"] = i
	s.files["/"+version+"/rootfs.erofs"] = rootfs
	u := s.srv.URL + "/" + version
	return &hostdv1.BaseImage{Version: version, Url: u + "/rootfs.erofs", Sha256: sum(rootfs),
		KernelUrl: u + "/vmlinuz", KernelSha256: sum(k), InitramfsUrl: u + "/initramfs.img", InitramfsSha256: sum(i), KernelVersion: "6.8.0-generic"}
}

func newCache(t *testing.T) (*Cache, *state.DB, *metrics.M) {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := metrics.New()
	return &Cache{Dir: filepath.Join(t.TempDir(), "images"), Arch: "x86_64", Store: st, Metrics: m, StallWindow: 100 * time.Millisecond, RetryWait: 10 * time.Millisecond}, st, m
}

func spec(t *testing.T, b *hostdv1.BaseImage) Spec {
	t.Helper()
	s, err := FromProto(b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFetchGood(t *testing.T) {
	srv := newServer(t)
	c, st, m := newCache(t)
	rootfs := bytes.Repeat([]byte("erofs"), 4096)
	b, err := c.Ensure(context.Background(), spec(t, srv.publish("2026.11.22.1", rootfs)))
	if err != nil {
		t.Fatal(err)
	}
	if b.Dir != filepath.Join(c.Dir, "2026.11.22.1-x86_64") {
		t.Fatalf("dir %s", b.Dir)
	}
	got, err := os.ReadFile(b.Rootfs)
	if err != nil || !bytes.Equal(got, rootfs) {
		t.Fatalf("rootfs %d bytes, err %v", len(got), err)
	}
	for _, p := range []string{b.Kernel, b.Initrd} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
	// No temporary directory or part file is left beside the bundle.
	des, _ := os.ReadDir(c.Dir)
	if len(des) != 1 {
		t.Fatalf("images dir holds %d entries, want 1", len(des))
	}
	if !c.Has("2026.11.22.1") || c.Newest() != "2026.11.22.1" {
		t.Fatalf("has %v newest %q", c.Has("2026.11.22.1"), c.Newest())
	}
	r, err := st.GetBaseImage("2026.11.22.1-x86_64")
	if err != nil || r.Bytes == 0 {
		t.Fatalf("record %+v %v", r, err)
	}
	if v := testutil.ToFloat64(m.BaseImageVersions); v != 1 {
		t.Fatalf("versions gauge %v", v)
	}
	if v := testutil.ToFloat64(m.BaseImageBytes); v != float64(r.Bytes) {
		t.Fatalf("bytes gauge %v, record %d", v, r.Bytes)
	}
	// A second Ensure is served from the cache: no request.
	before := srv.count(&srv.gets, "/2026.11.22.1/rootfs.erofs").Load()
	if _, err := c.Ensure(context.Background(), spec(t, srv.publish("2026.11.22.1", rootfs))); err != nil {
		t.Fatal(err)
	}
	if after := srv.count(&srv.gets, "/2026.11.22.1/rootfs.erofs").Load(); after != before {
		t.Fatalf("cached bundle fetched again: %d -> %d", before, after)
	}
}

func TestChecksumMismatchRefusedAndRemoved(t *testing.T) {
	srv := newServer(t)
	c, st, m := newCache(t)
	bi := srv.publish("v-bad", []byte("the real rootfs"))
	bi.Sha256 = sum([]byte("another rootfs"))
	_, err := c.Ensure(context.Background(), spec(t, bi))
	if err == nil || !IsChecksum(err) {
		t.Fatalf("err %v, want a checksum mismatch", err)
	}
	if c.Has("v-bad") {
		t.Fatal("a bundle with a wrong file is in the cache")
	}
	des, _ := os.ReadDir(c.Dir)
	for _, d := range des {
		t.Errorf("left behind: %s", d.Name())
	}
	if _, err := st.GetBaseImage("v-bad-x86_64"); err == nil {
		t.Fatal("a record was written for a refused bundle")
	}
	if v := testutil.ToFloat64(m.BaseImageFetchErrors.WithLabelValues("checksum")); v != 1 {
		t.Fatalf("checksum errors %v", v)
	}
	// One attempt only: a fresh download that is the wrong file is not retried.
	if n := srv.count(&srv.gets, "/v-bad/rootfs.erofs").Load(); n != 1 {
		t.Fatalf("rootfs fetched %d times", n)
	}
}

func TestStallResumesWithRange(t *testing.T) {
	srv := newServer(t)
	c, _, m := newCache(t)
	rootfs := bytes.Repeat([]byte("0123456789"), 10000)
	bi := srv.publish("v-stall", rootfs)
	srv.stallAt["/v-stall/rootfs.erofs"] = 30000
	b, err := c.Ensure(context.Background(), spec(t, bi))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(b.Rootfs)
	if !bytes.Equal(got, rootfs) {
		t.Fatalf("rootfs is %d bytes, want %d", len(got), len(rootfs))
	}
	if n := srv.count(&srv.ranges, "/v-stall/rootfs.erofs").Load(); n != 1 {
		t.Fatalf("%d Range requests, want 1", n)
	}
	if v := testutil.ToFloat64(m.BaseImageFetchErrors.WithLabelValues("stall")); v != 0 {
		// A stall that the retry recovers from is not a failed fetch.
		t.Fatalf("stall errors %v", v)
	}
}

func TestConcurrentRequestsFetchOnce(t *testing.T) {
	srv := newServer(t)
	c, _, _ := newCache(t)
	srv.hold = make(chan struct{})
	bi := srv.publish("v-once", []byte("rootfs"))
	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = c.Ensure(context.Background(), spec(t, bi))
		}(i)
	}
	// Every caller is in Ensure before the server answers anything.
	deadline := time.Now().Add(2 * time.Second)
	for srv.count(&srv.gets, "/v-once/vmlinuz").Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !c.Fetching("v-once") {
		t.Fatal("no fetch in flight")
	}
	time.Sleep(50 * time.Millisecond)
	close(srv.hold)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	for _, f := range []string{"vmlinuz", "initramfs.img", "rootfs.erofs"} {
		if n := srv.count(&srv.gets, "/v-once/"+f).Load(); n != 1 {
			t.Fatalf("%s fetched %d times", f, n)
		}
	}
}

func TestGCKeepsReferencedAndNewest(t *testing.T) {
	srv := newServer(t)
	c, st, m := newCache(t)
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }
	refs := map[string]bool{}
	c.Refs = func() (map[string]bool, error) { return refs, nil }
	for i, v := range []string{"v1", "v2", "v3", "v4"} {
		now = now.Add(time.Duration(i+1) * time.Minute)
		if _, err := c.Ensure(context.Background(), spec(t, srv.publish(v, []byte("rootfs "+v)))); err != nil {
			t.Fatal(err)
		}
	}
	refs["v2"] = true // a stopped guest names v2
	// The first GC only marks v1 and v3 unreferenced.
	if gone, err := c.GC(context.Background()); err != nil || len(gone) != 0 {
		t.Fatalf("first gc removed %v, %v", gone, err)
	}
	now = now.Add(GCAfter - time.Minute)
	if gone, _ := c.GC(context.Background()); len(gone) != 0 {
		t.Fatalf("gc before the window removed %v", gone)
	}
	// v3 is named again before its day is up, and its mark is cleared.
	refs["v3"] = true
	if gone, _ := c.GC(context.Background()); len(gone) != 0 {
		t.Fatalf("gc removed %v", gone)
	}
	delete(refs, "v3")
	now = now.Add(2 * time.Minute)
	gone, err := c.GC(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(gone, ",") != "v1" {
		t.Fatalf("removed %v, want only v1 (v2 referenced, v4 newest, v3 re-marked)", gone)
	}
	if c.Has("v1") || !c.Has("v2") || !c.Has("v3") || !c.Has("v4") {
		t.Fatalf("has v1 %v v2 %v v3 %v v4 %v", c.Has("v1"), c.Has("v2"), c.Has("v3"), c.Has("v4"))
	}
	if _, err := st.GetBaseImage("v1-x86_64"); err == nil {
		t.Fatal("v1's record outlived its directory")
	}
	now = now.Add(GCAfter)
	gone, _ = c.GC(context.Background())
	if strings.Join(gone, ",") != "v3" {
		t.Fatalf("removed %v, want v3", gone)
	}
	// The newest is never removed, even with no guest naming it.
	if !c.Has("v4") || c.Newest() != "v4" {
		t.Fatal("the newest bundle went")
	}
	if v := testutil.ToFloat64(m.BaseImageVersions); v != 2 {
		t.Fatalf("versions gauge %v, want 2", v)
	}
}

func TestGCRefusesWithoutReferences(t *testing.T) {
	c, _, _ := newCache(t)
	c.Refs = func() (map[string]bool, error) { return nil, fmt.Errorf("bbolt closed") }
	if _, err := c.GC(context.Background()); err == nil {
		t.Fatal("gc ran without knowing which bundles guests name")
	}
}

func TestFromProtoRefuses(t *testing.T) {
	good := &hostdv1.BaseImage{Version: "2026.11.22.1", Url: "https://x/r", Sha256: strings.Repeat("a", 64),
		KernelUrl: "https://x/k", KernelSha256: strings.Repeat("b", 64), InitramfsUrl: "https://x/i", InitramfsSha256: strings.Repeat("c", 64)}
	if _, err := FromProto(good); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(b *hostdv1.BaseImage){
		"nil-ish version": func(b *hostdv1.BaseImage) { b.Version = "" },
		"traversal":       func(b *hostdv1.BaseImage) { b.Version = "../etc" },
		"dotdot":          func(b *hostdv1.BaseImage) { b.Version = ".." },
		"no kernel url":   func(b *hostdv1.BaseImage) { b.KernelUrl = "" },
		"short sha":       func(b *hostdv1.BaseImage) { b.InitramfsSha256 = "abc" },
		"ftp":             func(b *hostdv1.BaseImage) { b.Url = "ftp://x/r" },
	} {
		b := proto.Clone(good).(*hostdv1.BaseImage)
		mut(b)
		if _, err := FromProto(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := FromProto(nil); err == nil {
		t.Error("nil accepted")
	}
}
