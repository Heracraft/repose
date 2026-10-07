package mcpreg

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestExpand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "TOKEN"), []byte("from-file"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "EMPTY"), nil, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOKEN", "from-env")
	t.Setenv("ONLY_ENV", "env-value")
	os.Unsetenv("UNSET_X")
	cases := []struct{ in, want string }{
		{"${TOKEN}", "from-file"},
		{"Bearer ${TOKEN}", "Bearer from-file"},
		{"${ONLY_ENV}", "env-value"},
		{"${UNSET_X}", "${UNSET_X}"},
		{"${UNSET_X:-fallback}", "fallback"},
		{"${UNSET_X:-}", ""},
		{"${TOKEN:-fallback}", "from-file"},
		{"${EMPTY:-fallback}", "fallback"},
		{"${EMPTY}", ""},
		{"a ${TOKEN} b ${ONLY_ENV}", "a from-file b env-value"},
		{"$TOKEN", "$TOKEN"},
		{"plain", "plain"},
	}
	for _, c := range cases {
		if got := Expand(c.in, dir); got != c.want {
			t.Errorf("Expand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNeeds(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "HAVE"), []byte("x"), 0o400); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("LACK_A")
	os.Unsetenv("LACK_B")
	s := Server{
		"command": "npx",
		"args":    []any{"--x", "${LACK_A}", "${HOME}/y"},
		"env":     map[string]any{"A": "${HAVE}", "B": "${LACK_B}", "C": "${OPT:-1}", "D": "${XDG_CONFIG_HOME}"},
	}
	if got := Needs(s, dir); !reflect.DeepEqual(got, []string{"LACK_A", "LACK_B"}) {
		t.Errorf("Needs = %v", got)
	}
}
