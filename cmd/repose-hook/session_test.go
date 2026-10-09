package main

import "testing"

func TestSessionIDOf(t *testing.T) {
	cases := []struct {
		agent, payload, want string
	}{
		{"claude", `{"hook_event_name":"Stop","session_id":"0199aa11-2222-7333-8444-555566667777"}`, "0199aa11-2222-7333-8444-555566667777"},
		{"codex", `{"type":"agent-turn-complete","thread-id":"0199b0c1-aaaa-7bbb-8ccc-dddddddddddd"}`, "0199b0c1-aaaa-7bbb-8ccc-dddddddddddd"},
		{"codex", `{"type":"agent-turn-complete"}`, ""},
		{"claude", `{"session_id":"x; rm -rf ~"}`, ""},
		{"opencode", `{"session_id":"abc"}`, ""},
		{"claude", `not json`, ""},
	}
	for _, c := range cases {
		if got := sessionIDOf(c.agent, []byte(c.payload)); got != c.want {
			t.Errorf("sessionIDOf(%s, %s) = %q, want %q", c.agent, c.payload, got, c.want)
		}
	}
}

func TestRecordSessionOnlyInTmux(t *testing.T) {
	var got [][2]string
	old := setSessionOption
	setSessionOption = func(pane, id string) { got = append(got, [2]string{pane, id}) }
	defer func() { setSessionOption = old }()

	t.Setenv("TMUX_PANE", "")
	recordSession("claude", []byte(`{"session_id":"s1"}`))
	if len(got) != 0 {
		t.Fatalf("set outside tmux: %v", got)
	}
	t.Setenv("TMUX_PANE", "%3")
	recordSession("claude", []byte(`{"session_id":"s1"}`))
	if len(got) != 1 || got[0] != [2]string{"%3", "s1"} {
		t.Fatalf("got %v, want one set of s1 on %%3", got)
	}
}
