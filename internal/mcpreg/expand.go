package mcpreg

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// refRe matches ${NAME} and ${NAME:-default}, the references Claude Code
// expands in an MCP config.
var refRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

// Expand replaces each ${NAME} in s with the secret NAME from secretsDir,
// else the environment variable NAME. ${NAME:-default} gives default when
// both are unset or empty. A ${NAME} with neither stays as written; callers
// that start a server check Needs first.
func Expand(s, secretsDir string) string {
	return refRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := refRe.FindStringSubmatch(m)
		v, ok := lookup(sub[1], secretsDir)
		switch {
		case ok && v != "":
			return v
		case sub[2] != "":
			return sub[3]
		case ok:
			return ""
		}
		return m
	})
}

// lookup reads the secret file first: it holds the value the user set
// last, while the environment holds the one the agent started with.
func lookup(name, secretsDir string) (string, bool) {
	if secretsDir != "" {
		if b, err := os.ReadFile(filepath.Join(secretsDir, name)); err == nil {
			return string(b), true
		}
	}
	return os.LookupEnv(name)
}

// Refs are the names s references, each once, with whether every
// reference to it carries a default.
func Refs(s string) map[string]bool {
	out := map[string]bool{}
	for _, sub := range refRe.FindAllStringSubmatch(s, -1) {
		hasDefault := sub[2] != ""
		if prev, ok := out[sub[1]]; ok {
			out[sub[1]] = prev && hasDefault
		} else {
			out[sub[1]] = hasDefault
		}
	}
	return out
}

// environmentNames are references a machine always fills, never "a secret
// the machine lacks".
func environmentName(n string) bool {
	switch n {
	case "HOME", "USER", "PWD", "TMPDIR", "PATH", "SHELL", "LANG":
		return true
	}
	return strings.HasPrefix(n, "XDG_")
}

// serverStrings are every string of a server that may hold a reference.
func serverStrings(s Server) []string {
	out := []string{str(s, "command"), str(s, "url")}
	out = append(out, strs(s["args"])...)
	for _, v := range strMap(s["env"]) {
		out = append(out, v)
	}
	for _, v := range strMap(s["headers"]) {
		out = append(out, v)
	}
	return out
}

// Needs are the secrets a server references, without a default, that
// neither secretsDir nor the environment holds.
func Needs(s Server, secretsDir string) []string {
	need := map[string]bool{}
	for _, v := range serverStrings(s) {
		for n, hasDefault := range Refs(v) {
			if hasDefault || environmentName(n) {
				continue
			}
			if val, ok := lookup(n, secretsDir); !ok || val == "" {
				need[n] = true
			}
		}
	}
	return sortedStrings(need)
}
