package eventverbs

import "testing"

func TestVerb(t *testing.T) {
	for kind, want := range map[string]string{"completed": "finished", "personal_failed": "machine.nix did not apply", "snapshot.created": "snapshot created", "host_moved": "host moved"} {
		if got := Verb(kind); got != want {
			t.Errorf("%s: %q, want %q", kind, got, want)
		}
	}
}
