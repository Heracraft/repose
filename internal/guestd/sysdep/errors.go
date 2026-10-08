package sysdep

import (
	"errors"
	"fmt"
)

// Error codes guestd puts in a Response.error. They are a subset of the set in
// docs/interfaces/grpc-hostd.md; guestd never adds one that is not listed
// there, because hostd maps them straight through to the api.
const (
	CodeInvalidArgument = "invalid_argument"
	CodeNotFound        = "not_found"
	CodeInternal        = "internal"
	// CodeStorePathHidden: a Switch whose closure, or a path of it, an
	// overlayfs whiteout in the guest's own store hides (DECISIONS I-589).
	CodeStorePathHidden = "store_path_hidden"
)

// CodedError carries one of those codes alongside the message hostd surfaces.
type CodedError struct {
	Code string
	Err  error
}

func (e *CodedError) Error() string { return e.Err.Error() }
func (e *CodedError) Unwrap() error { return e.Err }

// Errf builds a CodedError.
func Errf(code, format string, args ...any) error {
	return &CodedError{Code: code, Err: fmt.Errorf(format, args...)}
}

// Invalid is the common case: the request cannot be served as written.
func Invalid(format string, args ...any) error { return Errf(CodeInvalidArgument, format, args...) }

// NotFound is for a path or unit the request names that does not exist.
func NotFound(format string, args ...any) error { return Errf(CodeNotFound, format, args...) }

// CodeOf returns the code of err, or internal when it carries none.
func CodeOf(err error) string {
	var ce *CodedError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return CodeInternal
}
