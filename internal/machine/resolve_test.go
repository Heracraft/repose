package machine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeSSHG answers for `ssh -G`: one reply per destination, keyed by the
// address the arguments end with. A destination with no reply is a
// configuration ssh could not read, which is what the real one exits
// non-zero for.
func fakeSSHG(t *testing.T, replies map[string]string) *[][]string {
	t.Helper()
	var calls [][]string
	old := sshG
	sshG = func(_ context.Context, args []string) ([]byte, error) {
		calls = append(calls, args)
		dest := args[len(args)-1]
		// A run with -F /dev/null is the defaults run: it reads no
		// configuration, so it answers with ssh's own identity files and
		// the destination as it stands.
		if len(args) > 1 && args[0] == "-F" {
			return []byte("user " + LocalUser() + "\nhostname " + dest + "\nport 22\n" +
				"identityfile ~/.ssh/id_rsa\nidentityfile ~/.ssh/id_ecdsa\nidentityfile ~/.ssh/id_ed25519\n"), nil
		}
		reply, ok := replies[dest]
		if !ok {
			return nil, errors.New("Bad configuration option: nonsense")
		}
		return []byte(reply), nil
	}
	t.Cleanup(func() { sshG = old })
	return &calls
}

// fakeResolver answers for this computer's resolver: the names in known
// resolve, everything else does not.
func fakeResolver(t *testing.T, known ...string) {
	t.Helper()
	old := lookupHost
	lookupHost = func(_ context.Context, host string) ([]string, error) {
		for _, k := range known {
			if k == host {
				return []string{"192.0.2.1"}, nil
			}
		}
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { lookupHost = old })
}

func TestResolveTarget(t *testing.T) {
	cases := []struct {
		name    string
		ssh     string // the --ssh value
		replies map[string]string
		known   []string // what this computer resolves
		want    Resolved
		says    string // the refusal, for a case that has one
	}{
		{
			name: "an address ssh takes as it stands",
			ssh:  "me@192.168.1.40",
			replies: map[string]string{
				"192.168.1.40": "user me\nhostname 192.168.1.40\nport 22\n" +
					"identityfile ~/.ssh/id_rsa\nidentityfile ~/.ssh/id_ed25519\n",
			},
			want: Resolved{Address: "192.168.1.40", Login: "me"},
		},
		{
			name: "a Host entry of the user's own",
			ssh:  "shed",
			replies: map[string]string{
				"shed": "user ada\nhostname 10.0.0.9\nport 2022\nidentityfile ~/.ssh/work\n",
			},
			known: []string{"10.0.0.9"},
			want:  Resolved{Address: "10.0.0.9", Port: 2022, Login: "ada", KeyPath: "~/.ssh/work"},
		},
		{
			name: "a Tailscale name, which is a name like any other",
			ssh:  "me@homebox.tail1234.ts.net",
			replies: map[string]string{
				"homebox.tail1234.ts.net": "user me\nhostname homebox.tail1234.ts.net\nport 22\n",
			},
			known: []string{"homebox.tail1234.ts.net"},
			want:  Resolved{Address: "homebox.tail1234.ts.net", Login: "me"},
		},
		{
			name: "a port the value spelled wins over the config's",
			ssh:  "me@shed:2299",
			replies: map[string]string{
				"shed": "user me\nhostname 10.0.0.9\nport 2299\n",
			},
			known: []string{"10.0.0.9"},
			want:  Resolved{Address: "10.0.0.9", Port: 2299, Login: "me"},
		},
		{
			name: "a box behind a jump host",
			ssh:  "shed",
			replies: map[string]string{
				"shed":    "user ada\nhostname 10.0.0.9\nport 22\nproxyjump bastion\n",
				"bastion": "user jump\nhostname bast.example\nport 2222\n",
			},
			known: []string{"bast.example"},
			want:  Resolved{Address: "10.0.0.9", Login: "ada", ProxyJump: "jump@bast.example:2222"},
		},
		{
			name: "a jump host reached through one of its own",
			ssh:  "shed",
			replies: map[string]string{
				"shed":    "user ada\nhostname 10.0.0.9\nport 22\nproxyjump bastion\n",
				"bastion": "user jump\nhostname bast.example\nport 22\nproxyjump outer\n",
				"outer":   "user me\nhostname outer.example\nport 22\n",
			},
			known: []string{"outer.example"},
			want: Resolved{
				Address: "10.0.0.9", Login: "ada",
				ProxyJump: "me@outer.example,jump@bast.example",
			},
		},
		{
			name: "a ProxyCommand is carried as it stands",
			ssh:  "tunnel",
			replies: map[string]string{
				"tunnel": "user me\nhostname tunnel.example\nport 22\n" +
					"proxycommand cloudflared access ssh --hostname %h\n",
			},
			want: Resolved{
				Address: "tunnel.example", Login: "me",
				ProxyCommand: "cloudflared access ssh --hostname %h",
			},
		},
		{
			name:    "an address ssh cannot read a configuration for",
			ssh:     "me@nonsense",
			replies: map[string]string{},
			says:    "ssh cannot work out how to reach nonsense",
		},
		{
			name: "a name this computer does not resolve",
			ssh:  "me@typo.example",
			replies: map[string]string{
				"typo.example": "user me\nhostname typo.example\nport 22\n",
			},
			says: "typo.example does not resolve on this computer",
		},
		{
			name: "a jump host this computer does not resolve",
			ssh:  "shed",
			replies: map[string]string{
				"shed":    "user ada\nhostname 10.0.0.9\nport 22\nproxyjump bastion\n",
				"bastion": "user jump\nhostname gone.example\nport 22\n",
			},
			says: "gone.example does not resolve on this computer",
		},
		{
			name: "a config that resolves to no host name at all",
			ssh:  "me@shed",
			replies: map[string]string{
				"shed": "user me\nport 22\n",
			},
			says: `ssh resolves shed to "", which is no host name or address`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeSSHG(t, c.replies)
			fakeResolver(t, c.known...)
			target, err := ParseSSHTarget(c.ssh, "ada")
			if err != nil {
				t.Fatalf("ParseSSHTarget(%q): %v", c.ssh, err)
			}
			got, err := ResolveTarget(context.Background(), target)
			if c.says != "" {
				if err == nil {
					t.Fatalf("ResolveTarget(%q) = %+v, want a refusal", c.ssh, got)
				}
				if !strings.Contains(err.Error(), c.says) {
					t.Errorf("%q does not say %q", err.Error(), c.says)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveTarget(%q): %v", c.ssh, err)
			}
			if got != c.want {
				t.Errorf("ResolveTarget(%q) = %+v, want %+v", c.ssh, got, c.want)
			}
		})
	}
}

// Only what the user spelled is passed to ssh: a login this laptop
// guessed would override the one their own `Host` entry names.
func TestResolveTargetPassesSSHOnlyWhatWasSpelled(t *testing.T) {
	for _, c := range []struct {
		ssh  string
		want string // the arguments before the destination
	}{
		{"shed", ""},
		{"me@shed", "-l me"},
		{"shed:2299", "-p 2299"},
		{"me@shed:2299", "-p 2299 -l me"},
	} {
		calls := fakeSSHG(t, map[string]string{"shed": "user ada\nhostname 10.0.0.9\nport 22\n"})
		fakeResolver(t, "10.0.0.9")
		target, err := ParseSSHTarget(c.ssh, "ada")
		if err != nil {
			t.Fatalf("ParseSSHTarget(%q): %v", c.ssh, err)
		}
		if _, err := ResolveTarget(context.Background(), target); err != nil {
			t.Fatalf("ResolveTarget(%q): %v", c.ssh, err)
		}
		if len(*calls) == 0 {
			t.Fatalf("ResolveTarget(%q) ran no ssh", c.ssh)
		}
		args := (*calls)[0]
		got := strings.Join(args[:len(args)-1], " ")
		if got != c.want || args[len(args)-1] != "shed" {
			t.Errorf("ssh -G %v, want %q then shed", args, c.want)
		}
	}
}

// The identity files ssh prints whether or not anything named one are
// not the user's choice, so they are not written into the block.
func TestResolveTargetKeepsOnlyTheKeyTheConfigNamed(t *testing.T) {
	fakeSSHG(t, map[string]string{
		"shed": "user ada\nhostname 10.0.0.9\nport 22\n" +
			"identityfile ~/.ssh/id_rsa\nidentityfile ~/.ssh/id_ecdsa\nidentityfile ~/.ssh/id_ed25519\n",
	})
	fakeResolver(t, "10.0.0.9")
	target, err := ParseSSHTarget("shed", "ada")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolveTarget(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if got.KeyPath != "" {
		t.Errorf("KeyPath = %q, want none: ssh prints those with no configuration at all", got.KeyPath)
	}
}

// Two `Host` entries that jump through each other end, rather than
// reading ssh's configuration until something gives.
func TestResolveTargetRefusesJumpHostsThatLoop(t *testing.T) {
	fakeSSHG(t, map[string]string{
		"shed": "user ada\nhostname 10.0.0.9\nport 22\nproxyjump a\n",
		"a":    "user ada\nhostname a.example\nport 22\nproxyjump b\n",
		"b":    "user ada\nhostname b.example\nport 22\nproxyjump a\n",
	})
	fakeResolver(t, "a.example", "b.example")
	target, err := ParseSSHTarget("shed", "ada")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ResolveTarget(context.Background(), target)
	if err == nil || !strings.Contains(err.Error(), "deep") {
		t.Fatalf("err = %v, want one naming how deep the jump hosts go", err)
	}
}
