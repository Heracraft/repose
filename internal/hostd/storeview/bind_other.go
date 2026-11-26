//go:build !linux

package storeview

import "errors"

// Bind needs Linux mount namespaces; a host without them (a Mac, design 2)
// runs no Nix guest, so nothing calls it there.
func Bind(pid int, paths []string) error {
	return errors.New("storeview: bind needs Linux mount namespaces")
}

// tmpfsMagic is Linux's tmpfs f_type; nothing on another host matches it.
const tmpfsMagic = 0x01021994
