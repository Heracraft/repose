package hooks

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// TestPresenceBothStates (DECISIONS I-1098): GET /presence answers
// "attached" while the owner is at the machine and "away" while they are
// not, and refuses on a guestd that was never given a presence source,
// which is what makes `repose-presence` exit 1 rather than guess.
func TestPresenceBothStates(t *testing.T) {
	s, _, c := newHookServer(t, nil)

	resp, err := c.Get("http://guestd/presence")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("without a presence source the route answered 200")
	}

	attached := false
	s.SetPresence(func(context.Context) bool { return attached })

	for _, tc := range []struct {
		at   bool
		want string
	}{{false, StateAway}, {true, StateAttached}} {
		attached = tc.at
		resp, err := c.Get("http://guestd/presence")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		var st PresenceState
		err = json.NewDecoder(resp.Body).Decode(&st)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.StatusCode != http.StatusOK || st.State != tc.want || st.Attached != tc.at {
			t.Fatalf("with a client %v: status %d state %q attached %v, want %q", tc.at, resp.StatusCode, st.State, st.Attached, tc.want)
		}
	}
}
