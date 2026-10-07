package mcpreg

import (
	"regexp"
	"strings"
)

// Launcher is the command agents run for a server repose starts itself.
const Launcher = "repose-mcp"

// codexStartupTimeout is startup_timeout_sec on carried Codex stdio
// entries: Codex's default is too short for an `npx -y` server's first
// download.
const codexStartupTimeout = 60

// Rendered is what sync wants in one agent's config.
type Rendered struct {
	Agent string
	// User is name -> the value as the agent's file holds it.
	User map[string]any
	// Projects is checkout path -> name -> value; Claude Code only.
	Projects map[string]map[string]any
	// From is name -> source, for User and Projects alike.
	From map[string]string
	// Skipped are servers of the registry this agent does not get.
	Skipped []Skip
	// Retired is name -> values earlier bases wrote, which sync replaces.
	Retired map[string][]any
	// Legacy is name -> the native shape an earlier base wrote for a
	// carried server that now starts through the launcher or is left out
	// (DECISIONS I-555, amended): sync replaces it as its own, and status
	// counts it as the laptop's. ProjectLegacy is the same per checkout.
	Legacy        map[string][]any
	ProjectLegacy map[string]map[string][]any
}

// replaceable are the values sync owns under each user-scope name besides
// rendered.json's: retired platform values and legacy carried shapes.
func (r *Rendered) replaceable() map[string][]any {
	if len(r.Legacy) == 0 {
		return r.Retired
	}
	out := make(map[string][]any, len(r.Retired)+len(r.Legacy))
	for k, v := range r.Retired {
		out[k] = append(out[k], v...)
	}
	for k, v := range r.Legacy {
		out[k] = append(out[k], v...)
	}
	return out
}

// Render is the registry as agent's config entries.
func (r *Registry) Render(agent string) *Rendered {
	out := &Rendered{Agent: agent, User: map[string]any{}, From: map[string]string{}, Retired: map[string][]any{}, Legacy: map[string][]any{}}
	ls, taken := r.list(agent == "claude")
	out.Skipped = append(out.Skipped, taken...)
	for _, l := range ls {
		if l.From == FromRepose && agent != "claude" && agent != "codex" {
			// Gemini, opencode and pi get the platform servers from their
			// own system layers (DECISIONS I-553).
			continue
		}
		v, reason := renderOne(agent, l, r.SecretsDir, false)
		if l.From == FromLaptop {
			// What a base before the launcher rule wrote, so sync knows it
			// as its own on a machine that has it.
			if old, oreason := renderOne(agent, l, r.SecretsDir, true); oreason == "" && (reason != "" || !equal(old, v)) {
				if l.Project != "" && agent == "claude" {
					if out.ProjectLegacy == nil {
						out.ProjectLegacy = map[string]map[string][]any{}
					}
					if out.ProjectLegacy[l.Project] == nil {
						out.ProjectLegacy[l.Project] = map[string][]any{}
					}
					out.ProjectLegacy[l.Project][l.Name] = append(out.ProjectLegacy[l.Project][l.Name], old)
				} else {
					out.Legacy[l.Name] = append(out.Legacy[l.Name], old)
				}
			}
		}
		if reason != "" {
			out.Skipped = append(out.Skipped, Skip{l.Name, reason})
			continue
		}
		if l.Project != "" && agent == "claude" {
			if out.Projects == nil {
				out.Projects = map[string]map[string]any{}
			}
			if out.Projects[l.Project] == nil {
				out.Projects[l.Project] = map[string]any{}
			}
			out.Projects[l.Project][l.Name] = v
		} else {
			out.User[l.Name] = v
		}
		if _, ok := out.From[l.Name]; !ok {
			out.From[l.Name] = l.From
		}
	}
	for name, vals := range r.Platform.Retired {
		for _, s := range vals {
			v, reason := renderOne(agent, logical{Name: name, From: FromRepose, Server: s}, "", false)
			if reason == "" {
				out.Retired[name] = append(out.Retired[name], v)
			}
		}
	}
	return out
}

// renderOne is l as agent's config entry, or why agent does not get it.
// A carried stdio server with a reference the agent would fill itself
// starts through `repose-mcp run`, which reads the secret at each start
// and refuses when it is missing; a carried remote server with a missing
// secret is left out. Native renders what a base before that rule wrote.
func renderOne(agent string, l logical, secretsDir string, native bool) (any, string) {
	if l.From == FromForward {
		return renderForward(agent, l.Name), ""
	}
	s := l.Server
	if s == nil {
		return nil, "an empty entry"
	}
	t := transport(s)
	switch t {
	case "stdio", "http", "sse", "ws":
	default:
		return nil, "a server type repose does not know"
	}
	if t == "stdio" && str(s, "command") == "" {
		return nil, "no command"
	}
	if t != "stdio" && str(s, "url") == "" {
		return nil, "no URL"
	}
	if l.From == FromLaptop && !native {
		if t == "stdio" && agent != "codex" && needsLauncher(s) {
			return renderLauncher(agent, l), ""
		}
		if t != "stdio" {
			// The agent would send the literal ${NAME} to the service.
			if need := Needs(s, secretsDir); len(need) > 0 {
				return nil, NeedsReason(need)
			}
		}
	}
	switch agent {
	case "claude":
		return copyServer(s), ""
	case "codex":
		return renderCodex(l)
	case "gemini":
		return renderGemini(l)
	case "opencode":
		return renderOpencode(l)
	case "pi":
		return renderPi(l)
	}
	return nil, "an agent repose does not know"
}

func renderForward(agent, name string) any {
	switch agent {
	case "claude":
		return map[string]any{"type": "stdio", "command": Launcher, "args": []any{name}}
	case "opencode":
		return map[string]any{"type": "local", "command": []any{Launcher, name}}
	}
	return map[string]any{"command": Launcher, "args": []any{name}}
}

// renderLauncher is agent's entry for a carried stdio server that starts
// through `repose-mcp run NAME`. Claude Code's project entries add their
// checkout, since two checkouts may each have a server of that name.
func renderLauncher(agent string, l logical) any {
	switch agent {
	case "claude":
		args := []any{"run", l.Name}
		if l.Project != "" {
			args = append(args, l.Project)
		}
		return map[string]any{"type": "stdio", "command": Launcher, "args": args}
	case "opencode":
		return map[string]any{"type": "local", "command": []any{Launcher, "run", l.Name}}
	}
	return launch(l.Name)
}

// NeedsReason is the skip reason of a server whose secrets are missing.
func NeedsReason(need []string) string {
	if len(need) == 1 {
		return "needs the secret " + need[0]
	}
	return "needs the secrets " + strings.Join(need, ", ")
}

func copyServer(s Server) map[string]any {
	out := make(map[string]any, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

func anyStrings(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func anyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// needsLauncher: a carried stdio server references something only
// `repose-mcp run` fills right: a reference with a default, or one to
// anything but a name the machine always sets (environmentName), in the
// command, an argument or an env value. An agent that fills ${NAME}
// itself starts the server with the literal text when the secret is
// missing, and reads its environment only once, at its own start.
func needsLauncher(s Server) bool {
	vals := append([]string{str(s, "command")}, strs(s["args"])...)
	for _, v := range strMap(s["env"]) {
		vals = append(vals, v)
	}
	for _, v := range vals {
		for _, sub := range refRe.FindAllStringSubmatch(v, -1) {
			if sub[2] != "" || !environmentName(sub[1]) {
				return true
			}
		}
	}
	return false
}

// agentCannotExpand: Gemini CLI, opencode and pi cannot expand a
// reference in this stdio server themselves (one in the command or the
// arguments, or a default value). It was their launcher rule before
// needsLauncher, and still decides their native render.
func agentCannotExpand(s Server) bool {
	if strings.Contains(str(s, "command"), "${") {
		return true
	}
	for _, a := range strs(s["args"]) {
		if strings.Contains(a, "${") {
			return true
		}
	}
	for _, v := range strMap(s["env"]) {
		if hasDefaultRef(v) {
			return true
		}
	}
	return false
}

func hasDefaultRef(ss ...string) bool {
	for _, s := range ss {
		for _, sub := range refRe.FindAllStringSubmatch(s, -1) {
			if sub[2] != "" {
				return true
			}
		}
	}
	return false
}

func remoteStrings(s Server) []string {
	out := []string{str(s, "url")}
	for _, v := range strMap(s["headers"]) {
		out = append(out, v)
	}
	return out
}

func launch(name string) map[string]any {
	return map[string]any{"command": Launcher, "args": []any{"run", name}}
}

var (
	bearerRe = regexp.MustCompile(`^Bearer \$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)
	wholeRe  = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)
)

// renderCodex: Codex passes a fixed environment allowlist to stdio servers
// and expands nothing, so every carried stdio server starts through
// `repose-mcp run`; platform servers are command and args only, as
// /etc/repose/mcp.json has them.
func renderCodex(l logical) (any, string) {
	s := l.Server
	switch transport(s) {
	case "stdio":
		if l.From == FromRepose {
			return map[string]any{"command": str(s, "command"), "args": anyStrings(strs(s["args"]))}, ""
		}
		v := launch(l.Name)
		v["startup_timeout_sec"] = codexStartupTimeout
		return v, ""
	case "sse":
		return nil, "an SSE server, which Codex does not take"
	case "ws":
		return nil, "a WebSocket server, which Codex does not take"
	}
	url := str(s, "url")
	if strings.Contains(url, "${") {
		return nil, "a secret in its URL, which Codex cannot fill"
	}
	v := map[string]any{"url": url}
	plain, env := map[string]any{}, map[string]any{}
	for _, h := range sortedKeys(strMap(s["headers"])) {
		val := strMap(s["headers"])[h]
		if m := bearerRe.FindStringSubmatch(val); m != nil && strings.EqualFold(h, "authorization") {
			v["bearer_token_env_var"] = m[1]
		} else if m := wholeRe.FindStringSubmatch(val); m != nil {
			env[h] = m[1]
		} else if !strings.Contains(val, "${") {
			plain[h] = val
		} else {
			return nil, "a header Codex cannot fill"
		}
	}
	if len(plain) > 0 {
		v["http_headers"] = plain
	}
	if len(env) > 0 {
		v["env_http_headers"] = env
	}
	return v, ""
}

// renderGemini: Gemini expands $X and ${X} in env and headers, so a
// carried server keeps its own shape (DECISIONS I-555).
func renderGemini(l logical) (any, string) {
	s := l.Server
	switch t := transport(s); t {
	case "stdio":
		if agentCannotExpand(s) {
			return launch(l.Name), ""
		}
		v := map[string]any{"command": str(s, "command"), "args": anyStrings(strs(s["args"]))}
		if env := strMap(s["env"]); len(env) > 0 {
			v["env"] = anyMap(env)
		}
		return v, ""
	case "ws":
		return nil, "a WebSocket server, which Gemini CLI does not take"
	default:
		if hasDefaultRef(remoteStrings(s)...) {
			return nil, "a ${NAME:-default} reference Gemini CLI cannot fill"
		}
		v := map[string]any{"type": t, "url": str(s, "url")}
		if h := strMap(s["headers"]); len(h) > 0 {
			v["headers"] = anyMap(h)
		}
		return v, ""
	}
}

// renderPi: pi 1.0 takes the mcp.json entry shape and expands ${X} in env
// and headers.
func renderPi(l logical) (any, string) {
	s := l.Server
	switch transport(s) {
	case "stdio":
		if agentCannotExpand(s) {
			return launch(l.Name), ""
		}
		v := map[string]any{"command": str(s, "command"), "args": anyStrings(strs(s["args"]))}
		if env := strMap(s["env"]); len(env) > 0 {
			v["env"] = anyMap(env)
		}
		return v, ""
	case "ws":
		return nil, "a WebSocket server, which pi does not take"
	default:
		if hasDefaultRef(remoteStrings(s)...) {
			return nil, "a ${NAME:-default} reference pi cannot fill"
		}
		v := map[string]any{"url": str(s, "url")}
		if h := strMap(s["headers"]); len(h) > 0 {
			v["headers"] = anyMap(h)
		}
		return v, ""
	}
}

// opencodeRef turns ${X} into opencode's {env:X}.
func opencodeRef(s string) string {
	return refRe.ReplaceAllString(s, "{env:$1}")
}

// renderOpencode: opencode passes its environment to stdio servers and
// expands {env:X} in environment and headers.
func renderOpencode(l logical) (any, string) {
	s := l.Server
	switch transport(s) {
	case "stdio":
		if agentCannotExpand(s) {
			return map[string]any{"type": "local", "command": []any{Launcher, "run", l.Name}}, ""
		}
		cmd := append([]string{str(s, "command")}, strs(s["args"])...)
		v := map[string]any{"type": "local", "command": anyStrings(cmd)}
		if env := strMap(s["env"]); len(env) > 0 {
			e := map[string]any{}
			for k, val := range env {
				e[k] = opencodeRef(val)
			}
			v["environment"] = e
		}
		return v, ""
	case "ws":
		return nil, "a WebSocket server, which opencode does not take"
	default:
		if hasDefaultRef(remoteStrings(s)...) {
			return nil, "a ${NAME:-default} reference opencode cannot fill"
		}
		v := map[string]any{"type": "remote", "url": opencodeRef(str(s, "url"))}
		if h := strMap(s["headers"]); len(h) > 0 {
			hh := map[string]any{}
			for k, val := range h {
				hh[k] = opencodeRef(val)
			}
			v["headers"] = hh
		}
		return v, ""
	}
}
