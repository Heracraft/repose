//go:build !linux

package parkkey

import (
	"context"
	"errors"
	"net"
)

// HostCID is the vsock address of the hypervisor's host.
const HostCID = 2

// VsockDialer needs AF_VSOCK, which only a Linux guest of the launcher
// has. On a Mac running hostd natively (design 2) VZ encrypts the park's
// save file itself and no key is asked for.
func VsockDialer(cid, port uint32) Dialer {
	return func(context.Context) (net.Conn, error) {
		return nil, errors.New("parkkey: vsock needs Linux")
	}
}
