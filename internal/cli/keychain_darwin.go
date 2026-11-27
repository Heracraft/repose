//go:build darwin

package cli

import (
	"bytes"
	"os/exec"
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
	return exec.Command("security", "add-generic-password", "-s", keychainService, "-a", account, "-w", value, "-U").Run()
}

var keychainDelete = func(account string) error {
	return exec.Command("security", "delete-generic-password", "-s", keychainService, "-a", account).Run()
}
