package machine

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// A machine's address is whatever ssh takes, and that includes a name
// only the user's own ~/.ssh/config knows: `Host shed` with a HostName,
// a Port, a User, an IdentityFile and a ProxyJump under it. The generated
// blocks are read with `-F` (internal/cli machineTarget), which replaces
// the user's config rather than adding to it, so an alias written into a
// block as it was typed would reach nothing.
//
// So ssh itself resolves the alias on the laptop when the machine is
// added, and what it resolves to is what the registry keeps: the host
// name, the port, the login, the key and the jump hosts. A later edit to
// that `Host` entry does not follow, which is what the docs promise
// (apps/web/src/content/docs/own-machine.md).

// Resolved is what ssh makes of an address.
type Resolved struct {
	Address   string // the HostName ssh connects to
	Port      int    // 0 when it is 22
	Login     string // the User ssh logs in as
	KeyPath   string // the IdentityFile the user's config named, "" when it named none
	ProxyJump string // the hops, in order, each as [user@]host[:port], "" for none
	// ProxyCommand is the command ssh reaches the box through, as the
	// user's config spells it, when their config uses one instead of a
	// ProxyJump. Empty for a box ssh connects to itself.
	ProxyCommand string
}

// Target is the resolved address as ssh reaches it.
func (r Resolved) Target() Target {
	return Target{Login: r.Login, Address: r.Address, Port: r.Port}
}

// maxHops is how deep a chain of jump hosts is followed. More than this
// is a `Host` entry that jumps through itself, which would otherwise read
// ssh's config forever.
const maxHops = 8

// ResolveTarget asks ssh what it makes of t and checks that the laptop
// can reach it. It runs `ssh -G`, which reads the configuration and
// connects to nothing.
func ResolveTarget(ctx context.Context, t Target) (Resolved, error) {
	r, jump, err := resolveOne(ctx, t)
	if err != nil {
		return Resolved{}, err
	}
	hops, err := resolveHops(ctx, jump, 0)
	if err != nil {
		return Resolved{}, err
	}
	spelled := make([]string, 0, len(hops))
	for _, h := range hops {
		spelled = append(spelled, h.String())
	}
	r.ProxyJump = strings.Join(spelled, ",")
	// The name this computer has to resolve is the first hop's when there
	// are hops: the far side of the chain resolves the rest.
	reach := r.Address
	if len(hops) > 0 {
		reach = hops[0].Address
	}
	// A ProxyCommand resolves the box itself, so there is nothing here to
	// look up.
	if r.ProxyCommand == "" {
		if err := reachable(ctx, reach); err != nil {
			return Resolved{}, err
		}
	}
	return r, nil
}

// resolveOne is one `ssh -G`: the resolved address, and the ProxyJump
// value as the user's config spells it.
func resolveOne(ctx context.Context, t Target) (Resolved, string, error) {
	args := make([]string, 0, 5)
	if t.Port != 0 {
		args = append(args, "-p", strconv.Itoa(t.Port))
	}
	if t.LoginGiven {
		args = append(args, "-l", t.Login)
	}
	args = append(args, t.Address)

	out, err := sshG(ctx, args)
	if err != nil {
		return Resolved{}, "", fmt.Errorf("ssh cannot work out how to reach %s: %v", t.Address, err)
	}
	fields, keys := sshGFields(out)
	// What ssh would have used with no configuration at all, so the
	// IdentityFile below is the user's own choice and not the default list
	// ssh prints whether or not anything named a key.
	bare, err := sshG(ctx, append([]string{"-F", os.DevNull}, args...))
	if err != nil {
		return Resolved{}, "", fmt.Errorf("ssh cannot work out how to reach %s: %v", t.Address, err)
	}
	_, bareKeys := sshGFields(bare)
	defaultKeys := make(map[string]bool, len(bareKeys))
	for _, k := range bareKeys {
		defaultKeys[k] = true
	}

	r := Resolved{Address: fields["hostname"], Login: fields["user"]}
	if r.Address == "" || !addressRe.MatchString(r.Address) {
		return Resolved{}, "", fmt.Errorf("ssh resolves %s to %q, which is no host name or address", t.Address, r.Address)
	}
	if r.Login == "" || !loginRe.MatchString(r.Login) {
		return Resolved{}, "", fmt.Errorf("ssh logs in to %s as %q, which is no ssh login", t.Address, r.Login)
	}
	if p, err := strconv.Atoi(fields["port"]); err == nil && p != DefaultPort {
		if p < 1 || p > 65535 {
			return Resolved{}, "", fmt.Errorf("ssh reaches %s on port %d, which is no port", t.Address, p)
		}
		r.Port = p
	}
	for _, k := range keys {
		if defaultKeys[k] {
			continue
		}
		if strings.ContainsAny(k, "\r\n") {
			return Resolved{}, "", fmt.Errorf("the key ssh names for %s is no path repose can write into an ssh config", t.Address)
		}
		r.KeyPath = k
		break
	}
	jump := fields["proxyjump"]
	if jump == "none" {
		jump = ""
	}
	// A ProxyCommand is carried as it stands: ssh runs it with the same
	// %h, %p and %r it would have run it with here, and the command's own
	// ssh, if it has one, reads the user's config as before. ProxyJump,
	// when both are set, is the one ssh uses.
	if cmd := fields["proxycommand"]; jump == "" && cmd != "" && cmd != "none" {
		if strings.ContainsAny(cmd, "\r\n") {
			return Resolved{}, "", fmt.Errorf("the ProxyCommand ssh reaches %s through is no line repose can write into an ssh config", t.Address)
		}
		r.ProxyCommand = cmd
	}
	return r, jump, nil
}

// resolveHops is the chain of jump hosts an address is reached through,
// in the order ssh connects to them, each resolved the same way the
// address itself is. A hop reached through hops of its own contributes
// those first, which is the chain ssh builds from the user's config: the
// generated block carries no aliases, so each hop is spelled out.
func resolveHops(ctx context.Context, spec string, depth int) ([]Target, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil
	}
	if depth >= maxHops {
		return nil, fmt.Errorf("the jump hosts to %s are more than %d deep", spec, maxHops)
	}
	var hops []Target
	for _, word := range strings.Split(spec, ",") {
		word = strings.TrimSpace(word)
		if word == "" {
			return nil, fmt.Errorf("%q is no jump host", spec)
		}
		t, err := parseTarget(word, fmt.Sprintf("the jump host %q", word))
		if err != nil {
			return nil, err
		}
		r, jump, err := resolveOne(ctx, t)
		if err != nil {
			return nil, err
		}
		if r.ProxyCommand != "" {
			return nil, fmt.Errorf("the jump host %s is itself reached through a ProxyCommand, which cannot go in a chain of jumps", word)
		}
		own, err := resolveHops(ctx, jump, depth+1)
		if err != nil {
			return nil, err
		}
		hops = append(hops, own...)
		hops = append(hops, r.Target())
	}
	return hops, nil
}

// reachable is whether this computer can turn host into an address. A
// name that resolves nowhere is refused when the machine is added, where
// it is one sentence, rather than during the install, where it is ssh's.
// An address needs no resolving, and a box behind a jump host is resolved
// by the far side of it.
func reachable(ctx context.Context, host string) error {
	// A link-local address carries a zone id, which is not part of the
	// address itself: fe80::1%eth0.
	bare := host
	if h, _, found := strings.Cut(bare, "%"); found {
		bare = h
	}
	if bare == "" || net.ParseIP(bare) != nil {
		return nil
	}
	if _, err := lookupHost(ctx, bare); err != nil {
		return fmt.Errorf("%s does not resolve on this computer", host)
	}
	return nil
}

// sshGFields reads `ssh -G` output: the first value for each keyword,
// since that is the one ssh uses, and the identity files in the order
// they were printed, since a config names more than one and ssh offers
// them in that order.
func sshGFields(out []byte) (map[string]string, []string) {
	fields := map[string]string{}
	var keys []string
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		k, v = strings.ToLower(k), strings.TrimSpace(v)
		if k == "identityfile" {
			keys = append(keys, v)
			continue
		}
		if _, seen := fields[k]; !seen {
			fields[k] = v
		}
	}
	return fields, keys
}

// sshG runs `ssh -G`, which prints the configuration ssh would connect
// with and connects to nothing. A variable so a test answers for ssh.
var sshG = func(ctx context.Context, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ssh", append([]string{"-G"}, args...)...)
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		if line := firstLine(errs.String()); line != "" {
			return nil, fmt.Errorf("%s", line)
		}
		return nil, err
	}
	return out.Bytes(), nil
}

// lookupHost is this computer's resolver. A variable so a test answers
// for it.
var lookupHost = func(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

// firstLine is the first line with anything on it, which is where ssh
// puts what it could not read.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}
