package machine

import "testing"

func TestParseSSHTarget(t *testing.T) {
	const me = "ada" // what LocalUser would have returned
	cases := []struct {
		in    string
		want  Target
		bad   bool
		print string // String of the parsed target
	}{
		{in: "me@192.168.1.40", want: Target{Login: "me", Address: "192.168.1.40"}, print: "me@192.168.1.40"},
		{in: "192.168.1.40", want: Target{Login: me, Address: "192.168.1.40"}, print: "ada@192.168.1.40"},
		{in: "  me@box.example  ", want: Target{Login: "me", Address: "box.example"}, print: "me@box.example"},
		{in: "me@box.example:2222", want: Target{Login: "me", Address: "box.example", Port: 2222}, print: "me@box.example:2222"},
		{in: "box.example:2222", want: Target{Login: me, Address: "box.example", Port: 2222}, print: "ada@box.example:2222"},
		{in: "box.example:22", want: Target{Login: me, Address: "box.example", Port: 22}, print: "ada@box.example:22"},
		{in: "me@[2001:db8::1]:2222", want: Target{Login: "me", Address: "2001:db8::1", Port: 2222}, print: "me@[2001:db8::1]:2222"},
		{in: "[2001:db8::1]", want: Target{Login: me, Address: "2001:db8::1"}, print: "ada@[2001:db8::1]"},
		{in: "me@[fe80::1%eth0]", want: Target{Login: "me", Address: "fe80::1%eth0"}, print: "me@[fe80::1%eth0]"},
		{in: "me_1@box-2.example", want: Target{Login: "me_1", Address: "box-2.example"}, print: "me_1@box-2.example"},

		{in: "", bad: true},
		{in: "   ", bad: true},
		{in: "@box.example", bad: true},         // no login
		{in: "me@", bad: true},                  // no host
		{in: "me@box.example:", bad: true},      // a colon and no port
		{in: "me@box.example:0", bad: true},     // out of range
		{in: "me@box.example:65536", bad: true}, // out of range
		{in: "me@box.example:ssh", bad: true},   // not a number
		{in: "me@box.example:-1", bad: true},    // not a port
		{in: "2001:db8::1", bad: true},          // IPv6 wants brackets
		{in: "me@[2001:db8::1", bad: true},      // no closing bracket
		{in: "me@[2001:db8::1]x", bad: true},    // rubbish after the bracket
		{in: "me@[2001:db8::1]:", bad: true},    // a colon and no port
		{in: "my box@host", bad: true},          // a space ends the User line
		{in: "me@ho st", bad: true},             // a space ends the HostName line
		{in: "me@host\nHost evil", bad: true},   // no second directive
		{in: "me@host #comment", bad: true},     // no comment
		{in: "first@second@host", bad: true},    // one login
		{in: "me@host/../../etc", bad: true},    // not a host
		{in: "me@host:2222:3333", bad: true},    // IPv6 wants brackets
	}
	for _, c := range cases {
		got, err := ParseSSHTarget(c.in, me)
		if c.bad {
			if err == nil {
				t.Errorf("ParseSSHTarget(%q) = %+v, want an error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSSHTarget(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseSSHTarget(%q) = %+v, want %+v", c.in, got, c.want)
		}
		if s := got.String(); s != c.print {
			t.Errorf("ParseSSHTarget(%q).String() = %q, want %q", c.in, s, c.print)
		}
	}
}

// A value with no user and no $USER to fall back on is refused rather
// than written into the config as an empty User line.
func TestParseSSHTargetNoDefaultLogin(t *testing.T) {
	if _, err := ParseSSHTarget("box.example", ""); err == nil {
		t.Error("ParseSSHTarget with no login and no default: want an error")
	}
}

func TestMachineTarget(t *testing.T) {
	m := Machine{Name: "homebox", Address: "192.168.1.40", Port: 2222, Login: "me"}
	if got := m.Target().String(); got != "me@192.168.1.40:2222" {
		t.Errorf("Target().String() = %q", got)
	}
	m.Port = 0
	if got := m.Target().String(); got != "me@192.168.1.40" {
		t.Errorf("Target().String() = %q", got)
	}
}
