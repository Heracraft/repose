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

// MissingSecretsLine is what `repose-mcp run` prints when NAME references
// secrets the machine lacks.
func MissingSecretsLine(name string, need []string) string {
	if len(need) == 1 {
		return "repose-mcp: " + name + " needs the secret " + need[0] + "; set it with `repose secrets set " + need[0] + "` on your laptop, then restart the agent."
	}
	return "repose-mcp: " + name + " needs the secrets " + strings.Join(need, ", ") + "; set each with `repose secrets set NAME` on your laptop, then restart the agent."
}

// Prepare resolves `repose-mcp run NAME`; PrepareIn with checkout ""
// does the same.
func Prepare(p Paths, name string) (path string, argv, env []string, err error) {
	return PrepareIn(p, name, "")
}

// PrepareIn resolves `repose-mcp run NAME [CHECKOUT]`: the carried stdio
// server NAME (checkout's own first, when given)
// with every ${X} in its command, arguments and env filled from
// /run/repose/secrets, else the environment. A ${X} without a default that
// neither holds stops it with exit 1. It returns the program's path,
// its argv and its environment (the caller's, plus the server's env).
func PrepareIn(p Paths, name, checkout string) (path string, argv, env []string, err error) {
	reg := Load(p)
	s, ok := reg.LaptopServerIn(name, checkout)
	if !ok {
		for _, pr := range reg.Problems {
			if strings.HasPrefix(pr, "~/.repose/mcp/laptop.json") {
				return "", nil, nil, &LaunchError{1, "repose-mcp: " + pr}
			}
		}
		return "", nil, nil, &LaunchError{127, "repose-mcp: " + name + " is not in ~/.repose/mcp/laptop.json"}
	}
	if transport(s) != "stdio" {
		return "", nil, nil, &LaunchError{1, "repose-mcp: " + name + " is a remote server; agents reach it by its URL"}
	}
	if need := Needs(s, p.SecretsDir); len(need) > 0 {
		// Started anyway, the server would send the literal ${NAME} to its
		// service as a token and fail with that service's auth error.
		return "", nil, nil, &LaunchError{1, MissingSecretsLine(name, need)}
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
