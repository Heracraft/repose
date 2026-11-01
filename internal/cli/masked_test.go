package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadMaskedEchoesAStarPerCharacter(t *testing.T) {
	for _, tc := range []struct {
		name, in, want, echo string
		err                  error
	}{
		{"typed", "hunter2\r", "hunter2", "*******", nil},
		{"newline ends it too", "abc\n", "abc", "***", nil},
		{"pasted token", "sk-" + strings.Repeat("x", 40) + "\r", "sk-" + strings.Repeat("x", 40), strings.Repeat("*", 43), nil},
		{"multi-byte counts once", "pä🔑\r", "pä🔑", "***", nil},
		{"backspace", "abx\x7fc\r", "abc", "***\b \b*", nil},
		{"ctrl-h", "ab\x08c\r", "ac", "**\b \b*", nil},
		{"backspace a multi-byte character", "aé\x7f\r", "a", "**\b \b", nil},
		{"backspace on empty", "\x7fa\r", "a", "*", nil},
		{"ctrl-u clears", "abc\x15xy\r", "xy", "***\b \b\b \b\b \b**", nil},
		{"arrow keys dropped", "a\x1b[Ab\x1bOC\r", "ab", "**", nil},
		{"tab dropped", "a\tb\r", "ab", "**", nil},
		{"empty enter", "\r", "", "", nil},
		{"ctrl-c", "ab\x03", "", "**", errMaskedInterrupted},
		{"ctrl-d on empty", "\x04", "", "", io.EOF},
		{"ctrl-d mid-line ignored", "a\x04b\r", "ab", "**", nil},
		{"eof after text", "abc", "abc", "***", nil},
		{"eof on nothing", "", "", "", io.EOF},
		{"crlf is one line end", "abc\r\n", "abc", "***", nil},
		{"a pasted PEM is refused, not cut", "-----BEGIN KEY-----\rMIIE\r-----END KEY-----\r", "", strings.Repeat("*", 19), errMaskedMultiline},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var echo bytes.Buffer
			got, err := readMasked(strings.NewReader(tc.in), &echo)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if tc.err == nil && string(got) != tc.want {
				t.Errorf("value = %q, want %q", got, tc.want)
			}
			if echo.String() != tc.echo {
				t.Errorf("echo = %q, want %q", echo.String(), tc.echo)
			}
			if strings.Contains(echo.String(), tc.want) && tc.want != "" {
				t.Errorf("echo %q shows the value", echo.String())
			}
		})
	}
}
