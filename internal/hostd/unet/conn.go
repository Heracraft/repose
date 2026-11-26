//go:build unix

package unet

import (
	"bytes"
	"errors"
	"net"
	"os"
	"sync"
)

// vfkitMagic is the first datagram a VM's NIC socket sends after it
// connects (vfkit's protocol, pkg/vf/virtionet.go; gvproxy reads it the
// same way), and again after each re-connect.
var vfkitMagic = []byte("VFKT")

// DgramConn is a guest NIC's socket on the host side: a unixgram socket
// bound at a path the VM's launcher connects to (I-1668, the contract
// agreed with d2-runtime in design2-program.log 14:58Z). The peer is
// whoever last sent vfkitMagic; frames from anyone else are dropped, so a
// process that finds the path cannot inject frames into a guest's link
// unless it also replaces the launcher's socket, which needs the
// launcher's own directory.
type DgramConn struct {
	path string
	c    *net.UnixConn
	mu   sync.Mutex
	peer *net.UnixAddr
}

// ListenDgram binds path (removing a stale socket left by a previous
// hostd) with the buffers the launcher uses: 4 MiB to receive, 1 MiB to
// send. macOS's default unixgram datagram limit is 2 KiB, below one
// 1500-byte frame plus overhead at high rates.
func ListenDgram(path string) (*DgramConn, error) {
	_ = os.Remove(path) // a stale socket from a hostd that died; absent is fine
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return nil, err
	}
	_ = c.SetReadBuffer(4 << 20)  // best effort: the default still works, slower
	_ = c.SetWriteBuffer(1 << 20) // same
	return &DgramConn{path: path, c: c}, nil
}

// Ready reports whether a VM has connected.
func (d *DgramConn) Ready() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.peer != nil }

// Read returns the next frame from the peer.
func (d *DgramConn) Read(b []byte) (int, error) {
	for {
		n, from, err := d.c.ReadFromUnix(b)
		if err != nil {
			return 0, err
		}
		if from == nil {
			continue // an unbound sender cannot be answered: not a VM NIC
		}
		if n == len(vfkitMagic) && bytes.Equal(b[:n], vfkitMagic) {
			d.mu.Lock()
			d.peer = from
			d.mu.Unlock()
			continue
		}
		d.mu.Lock()
		ok := d.peer != nil && d.peer.Name == from.Name
		d.mu.Unlock()
		if ok {
			return n, nil
		}
	}
}

// errNoPeer is a write before any VM connected; the frame is dropped.
var errNoPeer = errors.New("unet: no VM connected")

// Write sends one frame to the peer.
func (d *DgramConn) Write(b []byte) (int, error) {
	d.mu.Lock()
	p := d.peer
	d.mu.Unlock()
	if p == nil {
		return 0, errNoPeer
	}
	return d.c.WriteToUnix(b, p)
}

// Close closes and unlinks the socket.
func (d *DgramConn) Close() error {
	err := d.c.Close()
	_ = os.Remove(d.path) // gone with the port
	return err
}
