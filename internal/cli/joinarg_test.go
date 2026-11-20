package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ENG-42: the install argument is base64url JSON carrying a live
// WireGuard private key (I-1380). Passed as a flag value it sits in the
// argv of `repose host add --join ...` for the whole install, 97 s on
// the AWS Mac, where any process on that Mac can read it from `ps`. The
// key dies at Register, so the window is the install, and the window is
// the whole of it.
//
// `repose secrets set` already answers this for a secret's value: it
// reads a pipe, takes --from-file, and refuses NAME=VALUE "since the
// value would stay in your shell history". The join argument takes the
// same three ways in.

func TestJoinArgumentOffTheCommandLine(t *testing.T) {
	bundle := encodeTokenBundle("rjt_a_live_key")

	t.Run("a dash reads standard input", func(t *testing.T) {
		got, err := readJoinArgument("-", "", strings.NewReader(bundle+"\n"))
		if err != nil || got != bundle {
			t.Fatalf("--join -: %q %v", got, err)
		}
	})

	t.Run("a file is read and never echoed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "join")
		if err := os.WriteFile(path, []byte(bundle+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := readJoinArgument("", path, nil)
		if err != nil || got != bundle {
			t.Fatalf("--join-file: %q %v", got, err)
		}
	})

	t.Run("the flag value still works, for the lab", func(t *testing.T) {
		got, err := readJoinArgument(bundle, "", nil)
		if err != nil || got != bundle {
			t.Fatalf("--join VALUE: %q %v", got, err)
		}
	})

	t.Run("both at once is a usage error", func(t *testing.T) {
		if _, err := readJoinArgument(bundle, "/tmp/x", nil); err == nil {
			t.Fatal("--join with --join-file should be refused")
		}
	})

	t.Run("an empty standard input says so", func(t *testing.T) {
		if _, err := readJoinArgument("-", "", strings.NewReader("")); err == nil {
			t.Fatal("an empty pipe should be refused")
		}
	})
}

// The one line the customer is told to run after a failed download must
// not put the key in argv either: it is the sentence most likely to be
// copied, and it was `repose host add --join <the key>`.
func TestFetchFailureTellsThemTheSafeForm(t *testing.T) {
	msg := applianceFetchFailed("The appliance image", errTestFetch{}, encodeTokenBundle("rjt_a_live_key"), "mac-mini")
	if strings.Contains(msg, "--join ey") || strings.Contains(msg, "--join rjt_") {
		t.Fatalf("the failure line puts the argument in argv:\n%s", msg)
	}
	if !strings.Contains(msg, "--join-file") && !strings.Contains(msg, "--join -") {
		t.Fatalf("the failure line offers no way in that keeps the key off argv:\n%s", msg)
	}
}

type errTestFetch struct{}

func (errTestFetch) Error() string { return "connection reset" }
