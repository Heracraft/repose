package cli

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
	"testing"
	"time"
)

// The downloads and the join argument a Mac's install shares with design
// 1's (DECISIONS I-1380, I-1414): a 2 GB fetch that outlives the api's
// deadline, gives up on a stall, and continues from what it has.

func TestApplianceJoinBundle(t *testing.T) {
	arg := encodeTokenBundle("rjt_abc")
	b, err := decodeJoinBundle(arg)
	if err != nil || b.Token != "rjt_abc" || b.Version != 1 {
		t.Fatalf("round trip: %+v %v", b, err)
	}
	for _, bad := range []string{"", "not a bundle", "eyJ2IjoyLCJ0b2tlbiI6InQifQ", "eyJ2IjoxfQ"} {
		if _, err := decodeJoinBundle(bad); err == nil {
			t.Errorf("%q decoded", bad)
		}
	}
}

// ENG-117, measured on a real M4 on 2026-11-20: the first `repose host
// add` failed at 31 s with "the image: context deadline exceeded
// (Client.Timeout or context cancellation while reading body)", and the
// second succeeded in 37 s. The Env's client carries a 30 s Timeout for
// the api's calls, http.Client.Timeout covers reading the body, and the
// downloads took that client whole, so a 2 GB image ran under a
// 30-second whole-request deadline. These four hold the download's own
// client, what ends a dead connection instead, what a retry continues
// from, and what the customer reads.

// slowBody serves b one byte at a time, each after a pause: the shape of
// a long download against a short whole-request deadline.
func slowBody(b []byte, each time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.WriteHeader(http.StatusOK)
		for i := range b {
			time.Sleep(each)
			_, _ = w.Write(b[i : i+1])
			w.(http.Flusher).Flush()
		}
	}
}

func TestApplianceDownloadOutlivesTheApiTimeout(t *testing.T) {
	body := []byte("an appliance image, byte by byte")
	srv := httptest.NewServer(slowBody(body, 10*time.Millisecond))
	defer srv.Close()
	// The production Env: the api's 30 s client, through both wrappers,
	// which return the client they were given (timing.go, verbose.go).
	e := &Env{httpClient: withVerbose(withTiming(&http.Client{Timeout: 320 * time.Millisecond}), srv.URL)}
	c := e.applianceHTTP()
	if c.Transport == nil {
		t.Error("the download lost the Env's transport, so -v and --timing log nothing")
	}
	sum := sha256.Sum256(body)
	dest := filepath.Join(t.TempDir(), "image.raw.gz")
	if _, err := fetchTo(context.Background(), c, nil, srv.URL, dest, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("a download longer than the api's own timeout failed: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("the file on disk is not the download: %q %v", got, err)
	}
}

func TestApplianceDownloadGivesUpWhenItStalls(t *testing.T) {
	// No whole-request deadline means a connection that dies mid-body
	// would otherwise be held until TCP gives up, a quarter of an hour.
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "64")
		_, _ = w.Write([]byte("four"))
		w.(http.Flusher).Flush()
		<-hold
	}))
	defer func() { close(hold); srv.Close() }()
	defer func(d time.Duration) { applianceStallWindow = d }(applianceStallWindow)
	applianceStallWindow = 150 * time.Millisecond

	dest := filepath.Join(t.TempDir(), "image.raw.gz")
	done := make(chan error, 1)
	go func() {
		_, err := fetchTo(context.Background(), &http.Client{}, nil, srv.URL, dest, "")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a stalled download reported success")
		}
		if !strings.Contains(err.Error(), "no data") {
			t.Errorf("a stalled download does not say it stalled: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a stalled download was never given up; nothing ends a dead connection")
	}
}

func TestApplianceDownloadRetryContinuesFromDisk(t *testing.T) {
	body := []byte(strings.Repeat("repose appliance ", 64))
	const cut = 200
	var ranges []string
	first := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ranges = append(ranges, r.Header.Get("Range"))
		if first {
			// The connection dies part way, as a home connection does.
			first = false
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body[:cut])
			w.(http.Flusher).Flush()
			return
		}
		http.ServeContent(w, r, "image.raw.gz", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()

	sum := sha256.Sum256(body)
	dest := filepath.Join(t.TempDir(), "image.raw.gz")
	got, err := fetchTo(context.Background(), &http.Client{}, nil, srv.URL, dest, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("a download that died part way was not retried: %v", err)
	}
	if got != hex.EncodeToString(sum[:]) {
		t.Errorf("digest %s, want %s", got, hex.EncodeToString(sum[:]))
	}
	onDisk, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(onDisk, body) {
		t.Fatalf("the file on disk is not the download (%d of %d bytes): %v", len(onDisk), len(body), err)
	}
	if len(ranges) < 2 || ranges[1] != fmt.Sprintf("bytes=%d-", cut) {
		t.Errorf("the retry asked for %v; it should continue from byte %d, not fetch 2 GB again", ranges, cut)
	}
}

func TestApplianceStalePartIsNotADeadEnd(t *testing.T) {
	// An install the customer gave up on leaves a part file behind. If
	// the CLI is upgraded meanwhile, the image behind the URL is a
	// different one and continuing from that part cannot match the
	// checksum: the attempt after it starts from nothing rather than
	// refusing for good.
	body := []byte(strings.Repeat("the image this CLI wants ", 32))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "image.raw.gz", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "image.raw.gz")
	if err := os.WriteFile(dest+".part", []byte("an older image's first bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := hex.EncodeToString(func() []byte { s := sha256.Sum256(body); return s[:] }())
	got, err := fetchTo(context.Background(), &http.Client{}, nil, srv.URL, dest, sum)
	if err != nil {
		t.Fatalf("a part file from an older image is a dead end: %v", err)
	}
	if got != sum {
		t.Errorf("digest %s, want %s", got, sum)
	}
	onDisk, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(onDisk, body) {
		t.Fatalf("the file on disk is not the download: %v", err)
	}
}
