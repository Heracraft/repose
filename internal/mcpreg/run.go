package mcpreg

import (
	"os"
	"os/exec"
	"sort"
	"strings"
)

// LaunchError is why `repose-mcp run NAME` cannot start NAME, with the
// exit code it leaves with.
type LaunchError struct {
	Code int
	Msg  string
}

func (e *LaunchError) Error() string { return e.Msg }

// Prepare resolves `repose-mcp run NAME`: the carried stdio server NAME
// with every ${X} in its command, arguments and env filled from
// /run/repose/secrets, else the environment. It returns the program's path,
// its argv and its environment (the caller's, plus the server's env).
func Prepare(p Paths, name string) (path string, argv, env []string, err error) {
	reg, lerr := Load(p)
	if lerr != nil {
		return "", nil, nil, &LaunchError{1, "repose-mcp: " + lerr.Error()}
	}
	s, ok := reg.LaptopServer(name)
	if !ok {
		return "", nil, nil, &LaunchError{127, "repose-mcp: " + name + " is not in ~/.repose/mcp/laptop.json"}
	}
	if transport(s) != "stdio" {
		return "", nil, nil, &LaunchError{1, "repose-mcp: " + name + " is a remote server; agents reach it by its URL"}
	}
	raw := str(s, "command")
	cmd := Expand(raw, p.SecretsDir)
	path, xerr := exec.LookPath(cmd)
	if xerr != nil {
		// raw, not cmd: the expanded command may hold a secret's value.
		return "", nil, nil, &LaunchError{127, "repose-mcp: " + name + " needs " + raw + ", which the machine lacks"}
	}
	argv = []string{cmd}
	for _, a := range strs(s["args"]) {
		argv = append(argv, Expand(a, p.SecretsDir))
	}
	extra := strMap(s["env"])
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// A name the server sets replaces the inherited one: execve passes
	// duplicates through, and glibc getenv (so Node) reads the first.
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if _, over := extra[k]; over {
			continue
		}
		env = append(env, kv)
	}
	for _, k := range keys {
		env = append(env, k+"="+Expand(extra[k], p.SecretsDir))
	}
	return path, argv, env, nil
}
