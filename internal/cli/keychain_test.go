package cli

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/parkkey"
)

// A fake `security`: the item is absent until add-generic-password puts
// it there, and a locked keychain answers exit 36 to everything.
type fakeSecurity struct {
	item   string
	locked bool
	calls  []string
}

func exitErr(code int, stderr string) error {
	cmd := exec.Command("sh", "-c", "echo '"+stderr+"' >&2; exit "+itoa(code))
	_, err := cmd.Output()
	return err
}

func itoa(n int) string { return strconv.Itoa(n) }

func (f *fakeSecurity) exec(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if name != "security" {
		return nil, errors.New("not security")
	}
	if f.locked {
		return nil, exitErr(36, "security: SecKeychainSearchCopyNext: User interaction is not allowed.")
	}
	switch args[0] {
	case "find-generic-password":
		if f.item == "" {
			return nil, exitErr(44, "security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.")
		}
		return []byte(f.item + "\n"), nil
	case "add-generic-password":
		for i, a := range args {
			if a == "-w" {
				f.item = args[i+1]
			}
		}
		return nil, nil
	}
	return nil, errors.New("unknown subcommand")
}

func TestKeychainKeeperMakesOneItemAndReadsItBack(t *testing.T) {
	f := &fakeSecurity{}
	old := macExec
	macExec = f.exec
	t.Cleanup(func() { macExec = old })
	var logged []string
	k := &keychainKeeper{log: func(format string, a ...any) { logged = append(logged, format) }}
	k1, err := k.WrappingKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(k1) != parkkey.WrappingKeyBytes || hex.EncodeToString(k1) != f.item {
		t.Fatalf("the key is not what the keychain holds")
	}
	k2, err := k.WrappingKey(context.Background())
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatalf("second read: %v, same=%v", err, bytes.Equal(k1, k2))
	}
	adds := 0
	for _, c := range f.calls {
		if strings.Contains(c, "add-generic-password") {
			adds++
			for _, want := range []string{"-s " + keychainService, "-a " + keychainAccount, "-l " + keychainLabel, "-j " + keychainComment} {
				if !strings.Contains(c, want) {
					t.Errorf("add lacks %q", want)
				}
			}
		}
	}
	if adds != 1 {
		t.Fatalf("%d adds, want 1: %v", adds, f.calls)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "keychain_item_created") {
		t.Fatalf("log: %v", logged)
	}
	for _, l := range logged {
		if strings.Contains(l, f.item) {
			t.Fatal("the key reached the log")
		}
	}
}

func TestKeychainKeeperReportsALockedKeychain(t *testing.T) {
	f := &fakeSecurity{locked: true}
	old := macExec
	macExec = f.exec
	t.Cleanup(func() { macExec = old })
	k := &keychainKeeper{log: func(string, ...any) {}}
	_, err := k.WrappingKey(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exit 36") || !strings.Contains(err.Error(), "User interaction is not allowed") {
		t.Fatalf("locked keychain: %v", err)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "add-generic-password") {
			t.Fatal("an add was tried against a locked keychain")
		}
	}
}

// What the keychain holds is what unwraps: a value that is not a key is
// an error, not a key of the wrong size.
func TestKeychainKeeperRefusesAForeignValue(t *testing.T) {
	f := &fakeSecurity{item: "not-hex"}
	old := macExec
	macExec = f.exec
	t.Cleanup(func() { macExec = old })
	k := &keychainKeeper{log: func(string, ...any) {}}
	if _, err := k.WrappingKey(context.Background()); err == nil || !strings.Contains(err.Error(), "does not hold a wrapping key") {
		t.Fatalf("foreign value: %v", err)
	}
}
