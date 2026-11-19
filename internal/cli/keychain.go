package cli

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/heracraft/repose/internal/parkkey"
)

// The wrapping key of the images a park writes on this Mac lives in the
// user's keychain as one generic-password item (DECISIONS I-1386,
// I-1394): service `run.repose.appliance`, account `park-wrapping-key`,
// made the first time the appliance asks and never anywhere else. It is
// read and written through macOS's own `security` tool, because the CLI's
// darwin build has no cgo and so no Security.framework; the keychain's
// own rule then applies, which is that an item `security` added is read
// by `security` again without a prompt while the keychain is unlocked,
// and by nothing while it is locked. The item holds the wrapping key only:
// never an image key, never a secret of a tenant's, never a token. What
// a user sees in Keychain Access is the label below.
const (
	keychainService = "run.repose.appliance"
	keychainAccount = "park-wrapping-key"
	keychainLabel   = "repose: key for the machines kept across a restart"
	keychainComment = "Wraps the keys of the memory images repose keeps on this Mac while it restarts (https://repose.run/docs/own-hardware). Not a secret of yours; delete it and the machines start cold after the next restart."
	// keychainNotFound is `security`'s exit status for errSecItemNotFound.
	keychainNotFound = 44
)

// keychainKeeper is the parkkey.Keeper over `security`.
type keychainKeeper struct {
	log parkkey.Logger

	mu sync.Mutex
}

// WrappingKey implements parkkey.Keeper: the item's value, made on the
// first call. Serialised, so two requests at the first park make one
// item.
func (k *keychainKeeper) WrappingKey(context.Context) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	key, err := k.read()
	if err == nil {
		return key, nil
	}
	var nf notFound
	if !errors.As(err, &nf) {
		return nil, err
	}
	fresh, err := parkkey.NewWrappingKey()
	if err != nil {
		return nil, err
	}
	if _, err := macExec("security", "add-generic-password",
		"-s", keychainService, "-a", keychainAccount, "-l", keychainLabel, "-j", keychainComment,
		"-w", hex.EncodeToString(fresh)); err != nil {
		return nil, securityErr("add-generic-password", err)
	}
	k.log("park-key: event=keychain_item_created service=%s account=%s", keychainService, keychainAccount)
	// Read it back rather than trust the add: what the keychain holds is
	// what unwraps, and a second launcher racing this one may have won.
	return k.read()
}

type notFound struct{ error }

func (k *keychainKeeper) read() ([]byte, error) {
	out, err := macExec("security", "find-generic-password", "-s", keychainService, "-a", keychainAccount, "-w")
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == keychainNotFound {
			return nil, notFound{err}
		}
		return nil, securityErr("find-generic-password", err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(out)))
	if err != nil || len(key) != parkkey.WrappingKeyBytes {
		return nil, fmt.Errorf("the keychain item %s/%s does not hold a wrapping key", keychainService, keychainAccount)
	}
	return key, nil
}

// securityErr names the subcommand, the exit status and `security`'s
// own line, which says why ("User interaction is not allowed" for a
// locked keychain, "could not be found" for no keychain) and never the
// value.
func securityErr(sub string, err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		msg := strings.TrimSpace(string(ee.Stderr))
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		return fmt.Errorf("security %s: exit %d: %s", sub, ee.ExitCode(), msg)
	}
	return fmt.Errorf("security %s: %w", sub, err)
}
