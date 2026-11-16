package cli

import (
	"strings"
	"testing"
)

// I-276: a mistyped command, top level or under a group, is a usage error
// that names the command meant; the old names (I-273) lead to the new.
func TestUnknownCommandSuggests(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string // "" is "not an unknown command"
	}{
		{[]string{"projetcs"}, "Did you mean `repose ls`?"},
		{[]string{"prjects"}, "Did you mean `repose ls`?"},
		{[]string{"list"}, "Did you mean `repose ls`?"},
		{[]string{"lst"}, "Did you mean `repose ls`?"},
		{[]string{"destory"}, "`repose rm`"},
		{[]string{"delete", "izma"}, "Did you mean `repose rm`?"},
		{[]string{"atach"}, "Did you mean `repose attach`?"},
		{[]string{"--project", "izma", "stauts"}, "`repose status`"},
		{[]string{"secrets", "lsit"}, "Did you mean `repose secrets list`?"},
		{[]string{"secrets", "--project", "izma", "imprt"}, "Did you mean `repose secrets import`?"},
		{[]string{"snapshots", "lst"}, "Did you mean `repose snapshots list`?"},
		{[]string{"config", "remov", "air"}, "Did you mean `repose config remove`?"},
		{[]string{"zzzz"}, "`repose --help` shows its usage."},
		// Real commands, aliases, help and completion are not unknown.
		{[]string{"ls"}, ""},
		{[]string{"projects", "--destroyed"}, ""},
		{[]string{"destroy", "izma"}, ""},
		{[]string{"secrets", "ls"}, ""},
		{[]string{"snapshots", "ls", "-q"}, ""},
		{[]string{"secrets"}, ""},
		{[]string{"help", "ls"}, ""},
		{[]string{"exec", "izma", "--", "npm", "test"}, ""},
		{[]string{"__complete", "sec"}, ""},
		{[]string{"--version"}, ""},
		{nil, ""},
	} {
		got := unknownCommand(newRootCmd("test"), tc.args, nil)
		if tc.want == "" {
			if got != "" {
				t.Errorf("%v: flagged as unknown:\n%s", tc.args, got)
			}
			continue
		}
		if !strings.HasPrefix(got, "unknown command ") || !strings.Contains(got, tc.want) {
			t.Errorf("%v: got\n%s\nwant it to contain %q", tc.args, got, tc.want)
		}
	}
	// A group names its own --help.
	if got := unknownCommand(newRootCmd("test"), []string{"secrets", "qqq"}, nil); !strings.Contains(got, "`repose secrets --help` shows its usage.") {
		t.Errorf("group hint missing:\n%s", got)
	}
}

func TestEditDistanceCountsASwapAsOne(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		d    int
	}{{"projetcs", "projects", 1}, {"lsit", "list", 1}, {"ls", "ls", 0}, {"", "rm", 2}, {"lst", "ssh", 2}} {
		if got := editDistance(tc.a, tc.b); got != tc.d {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.d)
		}
	}
}

// I-1046: `repose PROJECT COMMAND ...` is the two words the other way
// round, not a mistyped command. The answer names the project and, when
// the next word is a command, the line to type instead.
func TestProjectBeforeTheCommand(t *testing.T) {
	isProject := func(w string) bool { return w == "job" }
	const head = "job is a project, not a command.\n"
	const tail = "\n`repose --help` shows its usage."
	for _, tc := range []struct{ args, want string }{
		{"job exec rm out/resume.pdf", head + "Did you mean `repose exec job rm out/resume.pdf`?" + tail},
		{"job status", head + "Did you mean `repose status job`?" + tail},
		{"job stop", head + "Did you mean `repose stop job`?" + tail},
		// A word that is no command of repose's is one for the machine.
		{"job cat out/x", head + "Did you mean `repose exec job cat out/x`?" + tail},
		{"job zzzz", head + "Did you mean `repose exec job zzzz`?" + tail},
		// ls, rm and cp are the shell's words here, not repose's: the line
		// must never offer `repose rm job`, which destroys the project.
		{"job rm out/resume.pdf", head + "Did you mean `repose exec job rm out/resume.pdf`?" + tail},
		{"job ls out", head + "Did you mean `repose exec job ls out`?" + tail},
		{"job cp a b", head + "Did you mean `repose exec job cp a b`?" + tail},
		// A ~ the reader quoted is quoted again, or their shell eats it.
		{"job ls ~/job/out", head + "Did you mean `repose exec job ls '~/job/out'`?" + tail},
		// Nothing after the project: what the word is, and nothing invented.
		{"job", "job is a project, not a command." + tail},
	} {
		if got := unknownCommand(newRootCmd("dev"), strings.Fields(tc.args), isProject); got != tc.want {
			t.Errorf("repose %s:\n got %q\nwant %q", tc.args, got, tc.want)
		}
	}
	// A word that is no project of the account's keeps the old answer, and
	// a typo of a command is read as a typo without a lookup.
	if got := unknownCommand(newRootCmd("dev"), []string{"other", "exec", "ls"}, isProject); !strings.Contains(got, `unknown command "other"`) {
		t.Errorf("repose other exec ls: %q", got)
	}
	if got := unknownCommand(newRootCmd("dev"), []string{"exce", "ls"}, func(string) bool { t.Error("asked about a typo"); return true }); !strings.Contains(got, "repose exec") {
		t.Errorf("repose exce ls: %q", got)
	}
}
