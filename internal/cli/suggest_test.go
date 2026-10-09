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
