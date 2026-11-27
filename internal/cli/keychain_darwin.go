//go:build darwin

package cli

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

const keychainService = "repose"

func keychainAvailable() bool { return true }

// The three are vars so the package's tests use an in-memory keychain
// (keychain_darwin_test.go) and never the Mac's own: a CI runner's login
// keychain is locked, and `security` waited 38 minutes on one there.

var keychainGet = func(account string) (string, bool) {
	if account == "" {
		return "", false
	}
	out, err := exec.Command("security", "find-generic-password", "-s", keychainService, "-a", account, "-w").Output()
	if err != nil {
		return "", false
	}
	return string(bytes.TrimSpace(out)), true
}

var keychainSet = func(account, value string) error {
	_ = keychainDelete(account) // add-generic-password does not overwrite by default
	// The value on stdin, never in argv, where ps shows it (ENG-275).
	cmd := exec.Command("security", keychainAddArgs()...)
	cmd.Stdin = strings.NewReader(keychainAddStdin(keychainService, account, value))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}
	// `security -i` can exit 0 when the command inside it failed, so the
	// write is read back.
	if got, ok := keychainGet(account); !ok || got != value {
		return errors.New("security: the login was not saved in the Keychain: " + string(bytes.TrimSpace(out)))
	}
	return nil
}

var keychainDelete = func(account string) error {
	return exec.Command("security", "delete-generic-password", "-s", keychainService, "-a", account).Run()
}
