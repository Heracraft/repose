package cli

import "testing"

// The guest's PAM environment says truecolor on every ssh session, so tmux
// gave every client 24-bit colour; the attach unsets it unless the
// laptop's terminal said so itself (I-515).
func TestAttachColour(t *testing.T) {
	for in, want := range map[string]string{
		"truecolor": "",
		"24bit":     "",
		"TrueColor": "",
		"":          "unset COLORTERM; ",
		"256":       "unset COLORTERM; ",
	} {
		if got := attachColour(in); got != want {
			t.Errorf("attachColour(%q) = %q, want %q", in, got, want)
		}
	}
}
