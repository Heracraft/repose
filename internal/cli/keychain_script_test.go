package cli

import (
	"strings"
	"testing"
)

// TestKeychainSetKeepsTheTokenOffTheCommandLine is ENG-275: the token
// goes to `security -i` on stdin, never in its argv, which ps shows to
// every local user while it runs.
func TestKeychainSetKeepsTheTokenOffTheCommandLine(t *testing.T) {
	const token = "rt_secret-VALUE.123"
	for _, a := range keychainAddArgs() {
		if strings.Contains(a, token) {
			t.Fatalf("argv carries the token: %q", keychainAddArgs())
		}
	}
	got := keychainAddStdin("repose", `dev@example.com`, token)
	if want := `add-generic-password -U -s "repose" -a "dev@example.com" -w "rt_secret-VALUE.123"` + "\n"; got != want {
		t.Fatalf("stdin %q, want %q", got, want)
	}
	if got := keychainQuote(`a"b\c`); got != `"a\"b\\c"` {
		t.Fatalf("quote %q", got)
	}
}
