package main

import (
	"errors"
	"io"
	"net"
	"os"
	"syscall"
	"time"
)

// sdNotify sends one state line to systemd's notification socket
// (sd_notify(3)). Without NOTIFY_SOCKET (not under systemd, tests) it does
// nothing. Go's net package reads a leading '@' as an abstract socket, as
// systemd means it.
func sdNotify(state string) error {
	conn, err := notifyConn()
	if conn == nil || err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_, err = conn.Write([]byte(state))
	return err
}

// sdBarrier waits until systemd has handled every notification sent before
// it (sd_notify_barrier(3)): it sends BARRIER=1 with the write end of a
// pipe and waits for systemd to close its copy. A MAINPID= that systemd has
// not read yet when the old main process exits would be read as the
// service dying.
func sdBarrier(timeout time.Duration) error {
	sock := os.Getenv("NOTIFY_SOCKET")
	if sock == "" {
		return nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	// A raw unconnected datagram socket: Go's UnixConn refuses a message
	// with ancillary data on a connected one.
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		_ = w.Close()
		return err
	}
	err = syscall.Sendmsg(fd, []byte("BARRIER=1"), syscall.UnixRights(int(w.Fd())), &syscall.SockaddrUnix{Name: sock}, 0)
	_ = syscall.Close(fd)
	_ = w.Close()
	if err != nil {
		return err
	}
	if err := r.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected data on the barrier pipe")
		}
		return err
	}
	return nil
}

func notifyConn() (*net.UnixConn, error) {
	sock := os.Getenv("NOTIFY_SOCKET")
	if sock == "" {
		return nil, nil
	}
	return net.DialUnix("unixgram", nil, &net.UnixAddr{Name: sock, Net: "unixgram"})
}
