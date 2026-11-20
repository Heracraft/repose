package parkkey

import (
	"context"
	"fmt"
	"net"

	"github.com/mdlayher/vsock"
)

// HostCID is the vsock address of the hypervisor's host, which under
// vfkit is the launcher's Mac.
const HostCID = 2

// VsockDialer dials port on the host's CID: what hostd uses inside the
// appliance (the VM's virtio-vsock transport, `vmw_vsock_virtio_transport`
// in nix/hosts/mac.nix). The connect is bounded by ctx through a
// non-blocking socket polled until it is writable.
func VsockDialer(cid, port uint32) Dialer {
	return func(ctx context.Context) (net.Conn, error) {
		// vsock.Dial, not a hand-rolled socket: Go's net package has no
		// AF_VSOCK, so wrapping the raw descriptor with net.FileConn fails
		// in getsockname with "address family not supported by protocol",
		// which is what the first park on a Mac appliance hit (the connect
		// succeeded; the wrap did not). The same library already dials
		// guests in internal/vsockrpc. It takes no context, so the
		// context bounds the dial by closing the conn when it is done and
		// is carried onto the conn as a deadline, which is what the
		// caller's timeout was for.
		type result struct {
			c   net.Conn
			err error
		}
		ch := make(chan result, 1)
		go func() {
			c, err := vsock.Dial(cid, port, nil)
			ch <- result{c, err}
		}()
		select {
		case <-ctx.Done():
			go func() {
				if r := <-ch; r.c != nil {
					_ = r.c.Close()
				}
			}()
			return nil, fmt.Errorf("vsock connect cid=%d port=%d: %w", cid, port, ctx.Err())
		case r := <-ch:
			if r.err != nil {
				return nil, fmt.Errorf("vsock connect cid=%d port=%d: %w", cid, port, r.err)
			}
			if dl, ok := ctx.Deadline(); ok {
				_ = r.c.SetDeadline(dl)
			}
			return r.c, nil
		}
	}
}
