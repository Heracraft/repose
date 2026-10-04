package console

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	tl := New(filepath.Join(dir, "console.sock"), filepath.Join(dir, "console.log"))
	tl.Rotate = 100
	tl.Keep = 3
	chunk := bytes.Repeat([]byte("x"), 60)
	for i := 0; i < 8; i++ {
		if _, err := tl.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	_ = tl.Close()
	for _, name := range []string{"console.log", "console.log.1", "console.log.2", "console.log.3"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s missing after rotation", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "console.log.4")); err == nil {
		t.Fatal("more than Keep old files")
	}
}

func TestRunCopiesSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "console.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	tl := New(sock, filepath.Join(dir, "console.log"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tl.Run(ctx) }()
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Write([]byte("[    0.000000] Linux version 6.12\n"))
	_ = c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(filepath.Join(dir, "console.log"))
		if bytes.Contains(b, []byte("Linux version")) {
			cancel()
			<-done
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	t.Fatal("console line never written")
}

// TestCloseWithUnreadBytesResetsThePeer records the kernel behaviour behind
// I-186: a unix stream socket closed with bytes still in its receive queue
// hands its peer ECONNRESET instead of EOF. Cloud Hypervisor 53's serial
// thread exits on that error without detaching the socket, after which the
// guest's console output stalls for good.
func TestCloseWithUnreadBytesResetsThePeer(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	client, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	server, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Write([]byte("guest console output\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	_ = client.Close() // what capture did on a stop: close without reading what had arrived
	_, err = server.Read(make([]byte, 64))
	if !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("read after the peer closed with unread bytes: %v, want ECONNRESET", err)
	}
}

// TestRunDrainsBeforeClosing: capture that is told to stop while the guest
// is mid-burst reads everything the hypervisor sent before it closes, so
// the hypervisor's side sees EOF, never ECONNRESET (I-186).
func TestRunDrainsBeforeClosing(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "console.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	tl := New(sock, filepath.Join(dir, "console.log"))
	tl.Rate = 0 // every byte reaches the log, so the count below proves the drain
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tl.Run(ctx) }()
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	// 4 MB is far more than a unix socket buffers, so the writes are still
	// going on when capture is told to stop.
	line := bytes.Repeat([]byte("x"), 1023)
	line = append(line, '\n')
	wrote := make(chan error, 1)
	go func() {
		for i := 0; i < 4096; i++ {
			if _, err := c.Write(line); err != nil {
				wrote <- err
				return
			}
		}
		wrote <- nil
	}()
	for {
		if st, err := os.Stat(filepath.Join(dir, "console.log")); err == nil && st.Size() > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-wrote; err != nil {
		t.Fatalf("the hypervisor's writes failed while capture stopped: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	n, err := c.Read(make([]byte, 64))
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("hypervisor side after capture ended: n=%d err=%v, want EOF", n, err)
	}
	st, _ := os.Stat(filepath.Join(dir, "console.log"))
	if st.Size() != 4096*1024 {
		t.Fatalf("console.log has %d bytes, want every byte written (%d)", st.Size(), 4096*1024)
	}
}

// A guest printing without pause gets Burst bytes, then Rate a second; the
// rest is dropped with one line saying how much (DECISIONS I-452).
func TestRateLimit(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "console.log")
	tl := New(filepath.Join(dir, "console.sock"), log)
	now := time.Unix(1_700_000_000, 0)
	tl.Now = func() time.Time { return now }
	tl.Rate, tl.Burst = 100, 1000
	chunk := bytes.Repeat([]byte("x"), 100)
	for i := 0; i < 30; i++ { // 3000 bytes at once: 1000 fit
		if n, err := tl.Write(chunk); err != nil || n != len(chunk) {
			t.Fatalf("write %d: n=%d err=%v; over the limit must still report written", i, n, err)
		}
	}
	now = now.Add(time.Second) // 100 bytes of tokens
	if _, err := tl.Write([]byte("after\n")); err != nil {
		t.Fatal(err)
	}
	_ = tl.Close()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := string(bytes.Repeat([]byte("x"), 1000)) + "\n[repose: 2000 bytes of console output dropped, over the limit of 100 bytes a second]\nafter\n"
	if string(b) != want {
		t.Fatalf("log %q", b)
	}
}
