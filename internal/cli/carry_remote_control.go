package cli

import "fmt"

// The Remote Control switch travels with the carry (DECISIONS I-1136).
// config.toml's `remote_control`, global or under [projects.NAME], is
// what the laptop says; the guest keeps the answer in
// ~/.repose/remote-control as the word `on` or `off`, which
// repose-remote-control and repose-agent-setup read. Nothing else about
// Remote Control crosses: the machine talks to Anthropic itself, and
// repose proxies none of it.

// remoteControlPart writes the switch and applies it at once: the window
// opens, or the one repose opened closes. repose-remote-control exits 0
// on every path and on a base that has no such command the part is a
// no-op, so an older machine keeps working.
func remoteControlPart(on bool) string {
	v := "off"
	if on {
		v = "on"
	}
	return fmt.Sprintf(`mkdir -p ~/.repose && printf '%%s\n' %s > ~/.repose/remote-control.new && mv -f ~/.repose/remote-control.new ~/.repose/remote-control
command -v repose-remote-control >/dev/null 2>&1 && repose-remote-control >/dev/null 2>&1 || true
`, shQuote(v))
}
