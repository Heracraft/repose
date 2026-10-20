package machine

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"regexp"
	"strconv"
	"strings"
)

// Target is a machine's address as ssh takes it, which is what `repose
// machine add --ssh` spells: [user@]host[:port].
type Target struct {
	Login   string
	Address string // no brackets: an IPv6 address is stored bare
	Port    int    // 0 when the value named none, which means 22
}

// Target is the machine's address as --ssh spelled it.
func (m Machine) Target() Target {
	return Target{Login: m.Login, Address: m.Address, Port: m.Port}
}

// String is the --ssh form of t, with an IPv6 address back in brackets.
// It is the ADDRESS column of `repose machine ls`.
func (t Target) String() string {
	addr := t.Address
	if strings.Contains(addr, ":") {
		addr = "[" + addr + "]"
	}
	if t.Port != 0 {
		addr += ":" + strconv.Itoa(t.Port)
	}
	if t.Login == "" {
		return addr
	}
	return t.Login + "@" + addr
}

// A login and an address go into the generated ssh config as the whole of
// a `User` and a `HostName` line, so both are held to characters that
// cannot end a line or start a directive. The address keeps ':' for an
// IPv6 address and '%' for its zone id.
var (
	loginRe   = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	addressRe = regexp.MustCompile(`^[A-Za-z0-9._:%-]+$`)
)

// ParseSSHTarget reads a --ssh value: [user@]host[:port]. defaultLogin is
// the login a value with no user means; the CLI passes LocalUser. An IPv6
// address comes in brackets, as ssh and every URL spell it:
// me@[2001:db8::1]:2222.
func ParseSSHTarget(s, defaultLogin string) (Target, error) {
	usage := errors.New("--ssh takes [user@]host[:port]")
	s = strings.TrimSpace(s)
	if s == "" {
		return Target{}, usage
	}
	t := Target{Login: defaultLogin}
	// ssh splits a target at the last '@', and so does this; the login
	// rules below then refuse a second one.
	if at := strings.LastIndex(s, "@"); at >= 0 {
		t.Login, s = s[:at], s[at+1:]
		if t.Login == "" {
			return Target{}, errors.New("--ssh has nothing before the @")
		}
	}
	if t.Login == "" {
		return Target{}, errors.New("--ssh named no login and $USER is not set; spell it as user@host")
	}
	if !loginRe.MatchString(t.Login) {
		return Target{}, fmt.Errorf("%q is not an ssh login", t.Login)
	}

	port, hasPort := "", false
	switch {
	case strings.HasPrefix(s, "["):
		end := strings.Index(s, "]")
		if end < 0 {
			return Target{}, errors.New("--ssh has an opening bracket and no closing one")
		}
		t.Address, s = s[1:end], s[end+1:]
		switch {
		case s == "":
		case strings.HasPrefix(s, ":"):
			port, hasPort = s[1:], true
		default:
			return Target{}, usage
		}
	case strings.Count(s, ":") > 1:
		return Target{}, errors.New("--ssh wants an IPv6 address in brackets: [2001:db8::1]:2222")
	default:
		if host, p, ok := strings.Cut(s, ":"); ok {
			s, port, hasPort = host, p, true
		}
		t.Address = s
	}
	if t.Address == "" {
		return Target{}, usage
	}
	if !addressRe.MatchString(t.Address) {
		return Target{}, errors.New("--ssh's host is not a host name or an address")
	}
	if hasPort {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return Target{}, fmt.Errorf("--ssh's port %q is not a number from 1 to 65535", port)
		}
		t.Port = n
	}
	return t, nil
}

// LocalUser is the login a --ssh value with no user means: $USER, else the
// name in this laptop's passwd entry.
func LocalUser() string {
	if u := strings.TrimSpace(os.Getenv("USER")); u != "" {
		return u
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}
