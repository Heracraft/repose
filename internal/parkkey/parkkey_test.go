package parkkey

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func serve(t *testing.T, k Keeper) (*Client, *[]string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "k.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var lines []string
	go Serve(ctx, l, k, func(f string, a ...any) { lines = append(lines, strings.TrimSpace(fmt.Sprintf(f, a...))) })
	return NewClient(UnixDialer(sock)), &lines
}

// A key wrapped by the launcher unwraps to the same key, what crosses is
// not the key, and the record's key id names the launcher's key.
func TestWrapUnwrapThroughTheLauncher(t *testing.T) {
	c, _ := serve(t, &MemoryKeeper{})
	if err := c.Ready(); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 64)
	w, id, err := c.Wrap(key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(w, key) || len(w) != 12+64+16 {
		t.Fatalf("wrapped key of %d bytes carries the key", len(w))
	}
	if !strings.HasPrefix(id, "keychain-") || len(id) != len("keychain-")+16 {
		t.Fatalf("key id %q", id)
	}
	got, err := c.Unwrap(context.Background(), w, id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, key) {
		t.Fatal("unwrapped key differs")
	}
	w2, _, _ := c.Wrap(key)
	if bytes.Equal(w, w2) {
		t.Fatal("two wraps of one key are identical: no nonce")
	}
}

// A record wrapped under a key the Mac no longer has (the Keychain item
// deleted and made again) is refused by name, not opened to garbage; a
// wrapped key with a flipped byte does not open either.
func TestUnwrapRefusesAnotherKey(t *testing.T) {
	k := &MemoryKeeper{}
	c, lines := serve(t, k)
	key := bytes.Repeat([]byte{9}, 64)
	w, id, err := c.Wrap(key)
	if err != nil {
		t.Fatal(err)
	}
	w[20] ^= 1
	if _, err := c.Unwrap(context.Background(), w, id); err == nil || !strings.Contains(err.Error(), "does not open") {
		t.Fatalf("a tampered wrapped key unwrapped: %v", err)
	}
	w[20] ^= 1
	k.Replace()
	_, err = c.Unwrap(context.Background(), w, id)
	if err == nil || !strings.Contains(err.Error(), ErrWrongKey.Error()) {
		t.Fatalf("a record from a replaced key unwrapped: %v", err)
	}
	found := false
	for _, l := range *lines {
		if strings.Contains(l, "event=unwrap_failed key_id="+id) {
			found = true
		}
		if strings.Contains(l, string(key)) {
			t.Fatal("a key reached the log")
		}
	}
	if !found {
		t.Fatalf("no unwrap_failed line: %v", *lines)
	}
}

// A keychain that does not answer (locked, or no login yet) makes Ready,
// Wrap and Unwrap fail with the launcher's reason, so the park skips the
// guests with `no_key` rather than writing images nobody can read.
func TestALockedKeychainIsAPlainError(t *testing.T) {
	c, _ := serve(t, &MemoryKeeper{Err: errors.New("security: exit 36: User interaction is not allowed")})
	err := c.Ready()
	if err == nil || !strings.Contains(err.Error(), "wrapping key unavailable") || !strings.Contains(err.Error(), "exit 36") {
		t.Fatalf("Ready: %v", err)
	}
	if _, _, err := c.Wrap([]byte("k")); err == nil {
		t.Fatal("Wrap succeeded with no key")
	}
}

// No launcher at the socket: the error names it and the call does not
// hang past its timeout.
func TestNoLauncher(t *testing.T) {
	c := NewClient(UnixDialer(filepath.Join(t.TempDir(), "none.sock")))
	c.RequestTimeout = 200 * time.Millisecond
	start := time.Now()
	err := c.Ready()
	if err == nil || !strings.Contains(err.Error(), "launcher") {
		t.Fatalf("Ready: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("Ready hung")
	}
}

// Wrong versions, bad JSON and unknown ops are answered, never hung on.
func TestBadRequests(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "k.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, l, &MemoryKeeper{}, nil)
	for _, in := range []string{"{\n", "{\"v\":2,\"op\":\"ready\"}\n", "{\"v\":1,\"op\":\"steal\"}\n", strings.Repeat("x", 5000) + "\n"} {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = conn.Write([]byte(in))
		buf := make([]byte, 4096)
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		n, _ := conn.Read(buf)
		_ = conn.Close()
		if !strings.Contains(string(buf[:n]), `"ok":false`) {
			t.Fatalf("%q answered %q", in[:min(len(in), 30)], buf[:n])
		}
	}
}

// The port is a constant on both sides of the VM boundary: here, and
// in nix/hosts/mac.nix's park.launcherPort, which hostd is started with.
func TestMacNixAgreesOnThePort(t *testing.T) {
	b, err := os.ReadFile("../../nix/hosts/mac.nix")
	if err != nil {
		t.Skip(err)
	}
	want := fmt.Sprintf("launcherPort = lib.mkDefault %d;", Port)
	if !strings.Contains(string(b), want) {
		t.Fatalf("nix/hosts/mac.nix does not set %q", want)
	}
}
