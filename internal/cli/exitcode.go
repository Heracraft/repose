package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// Exit codes, docs/interfaces/cli-config.md "Exit codes".
const (
	ExitOK              = 0
	ExitGeneric         = 1
	ExitUsage           = 2
	ExitNotLoggedIn     = 3
	ExitProjectNotFound = 4
	ExitGuestNotRunning = 5
	ExitDirtyRemoteTree = 6
	ExitPaymentRequired = 7
	ExitCapacity        = 8
	ExitBuildFailed     = 10
	ExitInterrupted     = 130 // Ctrl-C, as a shell reports SIGINT
)

// exitError carries a message already printed (or to be printed) and the
// exit code main should return. Commands return this instead of calling
// os.Exit directly so tests can assert on it.
type exitError struct {
	code  int
	msg   string // empty when the command already printed its own message
	cause error  // the underlying error, for errors.As in tests; never printed on its own
}

func (e *exitError) Unwrap() error { return e.cause }

func (e *exitError) Error() string {
	if e.msg == "" {
		return "exit"
	}
	return e.msg
}

func exitf(code int, format string, args ...any) *exitError {
	return &exitError{code: code, msg: fmt.Sprintf(format, args...)}
}

// silent exits with code and no further message (the command already
// printed one, e.g. streamed build log output).
func silent(code int) *exitError { return &exitError{code: code} }

// errNoSuchProject is the one wording for a name that is none of the
// account's projects (DECISIONS I-623).
func errNoSuchProject(name string) *exitError {
	return exitf(ExitProjectNotFound, "%s", noSuchProjectMessage(name))
}

func noSuchProjectMessage(name string) string {
	return fmt.Sprintf("No repose project is called %s. `repose ls` lists yours.", name)
}

// fileReadError is a laptop file the command was given that it could not
// read, as one sentence with no Go wrapping (exit 2: the path is the
// mistake).
func fileReadError(path string, err error) *exitError {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return exitf(ExitUsage, "%s does not exist.", path)
	case errors.Is(err, fs.ErrPermission):
		return exitf(ExitUsage, "%s is not readable by you.", path)
	}
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		msg = msg[i+2:]
	}
	return exitf(ExitUsage, "Could not read %s: %s.", path, msg)
}
