package shell

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// A process that fails carries what it said on stderr. Every consumer
// of Proc.Stderr() in this repo copies it to io.Discard so the process
// does not block on a full pipe, which threw the diagnosis away with
// it: a failed cryptsetup or zstd reported `exit 1: ` and nothing
// else, and a lost guest memory image said nothing about why (I-922).
func TestStartedProcessKeepsItsStderrTail(t *testing.T) {
	p, err := Exec{}.Start(context.Background(), nil, "sh", "-c", "echo 'device-mapper: reload ioctl failed' >&2; exit 1")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, p.Stderr()) }() // as every caller does
	werr := p.Wait()
	if werr == nil {
		t.Fatal("exit 1 was not an error")
	}
	var ee *ExitError
	if !errors.As(werr, &ee) {
		t.Fatalf("err %T, want *ExitError", werr)
	}
	if !strings.Contains(ee.Error(), "device-mapper: reload ioctl failed") {
		t.Errorf("error %q lacks the stderr tail", ee.Error())
	}
	if ee.Result.ExitCode != 1 {
		t.Errorf("exit code %d", ee.Result.ExitCode)
	}
}
