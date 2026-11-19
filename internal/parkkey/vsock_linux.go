package parkkey

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
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
		fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
		if err != nil {
			return nil, fmt.Errorf("vsock socket: %w", err)
		}
		sa := &unix.SockaddrVM{CID: cid, Port: port}
		err = unix.Connect(fd, sa)
		if err != nil && err != unix.EINPROGRESS {
			_ = unix.Close(fd)
			return nil, fmt.Errorf("vsock connect cid=%d port=%d: %w", cid, port, err)
		}
		if err == unix.EINPROGRESS {
			if err := awaitConnect(ctx, fd); err != nil {
				_ = unix.Close(fd)
				return nil, fmt.Errorf("vsock connect cid=%d port=%d: %w", cid, port, err)
			}
		}
		// Blocking from here: net.FileConn puts the descriptor in the
		// runtime poller, which wants it that way.
		if err := unix.SetNonblock(fd, false); err != nil {
			_ = unix.Close(fd)
			return nil, err
		}
		f := os.NewFile(uintptr(fd), "vsock")
		c, err := net.FileConn(f)
		_ = f.Close() // FileConn dup'ed it
		if err != nil {
			return nil, err
		}
		return c, nil
	}
}

func awaitConnect(ctx context.Context, fd int) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		wait := 100 * time.Millisecond
		if dl, ok := ctx.Deadline(); ok {
			if left := time.Until(dl); left < wait {
				wait = left
			}
		}
		pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
		n, err := unix.Poll(pfd, int(wait/time.Millisecond))
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		soerr, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
		if err != nil {
			return err
		}
		if soerr != 0 {
			return unix.Errno(soerr)
		}
		return nil
	}
}
