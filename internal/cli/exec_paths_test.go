package cli

import (
	"strings"
	"testing"
)

// I-1048: a word that is ~ or starts with ~/ is the machine's home, as
// --workdir and `repose cp` already read it; every other word still
// reaches the command unchanged.
func TestExecWordReadsTheMachinesHome(t *testing.T) {
	for _, tc := range []struct{ word, want string }{
		{"~", `"$HOME"`},
		{"~/job/out", `"$HOME"/'job/out'`},
		{"~/", `"$HOME"/''`},
		{"/home/dev/job", `'/home/dev/job'`},
		{"$HOME", `'$HOME'`},
		{"a~b", `'a~b'`},
		{"backup~", `'backup~'`},
		{"~other/x", `'~other/x'`},
	} {
		if got := execWord(tc.word); got != tc.want {
			t.Errorf("execWord(%q) = %s, want %s", tc.word, got, tc.want)
		}
	}
	if s := execScript("todo-app", "", []string{"ls", "~/job/out"}); !strings.HasSuffix(s, `exec 'ls' "$HOME"/'job/out'`) {
		t.Errorf("the script does not expand the word on the machine:\n%s", s)
	}
}

// I-1048: the laptop's shell expands an unquoted ~ before repose sees it,
// so the command carries a path that cannot exist on the machine. The
// first such word is named, with the home it came from.
func TestLaptopHomePathFindsTheExpandedTilde(t *testing.T) {
	t.Setenv("HOME", "/home/agustus")
	for _, tc := range []struct {
		argv       []string
		word, home string
	}{
		{[]string{"ls", "/home/agustus/job/out"}, "/home/agustus/job/out", "/home/agustus"},
		{[]string{"ls", "/home/agustus"}, "/home/agustus", "/home/agustus"},
		{[]string{"ls", "/home/dev/job"}, "", ""},
		{[]string{"ls", "~/job/out"}, "", ""},
		{[]string{"ls", "/home/agustusx/job"}, "", ""},
	} {
		word, home := laptopHomePath(tc.argv)
		if word != tc.word || home != tc.home {
			t.Errorf("laptopHomePath(%v) = %q, %q, want %q, %q", tc.argv, word, home, tc.word, tc.home)
		}
	}
	// A laptop whose home is the machine's has nothing to say.
	t.Setenv("HOME", guestHome)
	if word, _ := laptopHomePath([]string{"ls", guestHome + "/job"}); word != "" {
		t.Errorf("flagged the machine's own home: %q", word)
	}
}
