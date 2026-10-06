// Package mcpreg is the guest's MCP registry (DECISIONS I-555): one list of
// MCP servers per machine, kept in ~/.repose/mcp/, rendered into each
// agent's own config by `repose-mcp sync AGENT`.
//
// Sources, in the order a name is claimed:
//
//	machine   /etc/repose/mcp.json, the platform servers (I-246)
//	forward   ~/.repose/mcp/forward/NAME.json, written by repose-mcp hold
//	laptop    ~/.repose/mcp/laptop.json "user", written by the CLI's carry
//	laptop    ~/.repose/mcp/laptop.json "projects", Claude Code local scope
//
// A name already claimed is skipped for the later source. An entry in an
// agent's file belongs to repose only while its value equals what repose
// wrote (rendered.json), what it would write now, or a value an earlier
// base wrote (repose_retired); anything else under that name is the
// user's and stays. The layout is docs/interfaces/guest-conventions.md
// "MCP registry".
package mcpreg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Agents are the five agents sync renders for, in display order.
var Agents = []string{"claude", "codex", "gemini", "opencode", "pi"}

// Where a server comes from, as `repose-mcp status --json` reports it.
const (
	FromMachine = "machine" // the platform, /etc/repose/mcp.json
	FromLaptop  = "laptop"  // carried from the laptop's Claude Code
	FromForward = "forward" // runs on the laptop, through repose-mcp NAME
	FromProject = "project" // a checkout's .mcp.json
	FromYours   = "yours"   // added on the machine, in an agent's own config
)

// nameRe is a server name repose renders: what every agent accepts as a
// key and the carry's classifier allows.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidName reports whether name can be rendered.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// Reserved are repose-mcp's own command words; a server of that name could
// not be started as `repose-mcp NAME`.
var Reserved = map[string]bool{"sync": true, "run": true, "status": true, "hold": true, "help": true}

// Server is a Claude Code `mcpServers` value: {type?, command, args, env}
// or {type: http|sse|ws, url, headers, oauth}.
type Server = map[string]any

// Skip is a laptop server the carry left behind, with its short reason.
type Skip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Laptop is ~/.repose/mcp/laptop.json, written by the carry.
type Laptop struct {
	Version  int                          `json:"version"`
	User     map[string]Server            `json:"user,omitempty"`
	Projects map[string]map[string]Server `json:"projects,omitempty"`
	Skipped  []Skip                       `json:"skipped,omitempty"`
	Secrets  map[string][]string          `json:"secrets,omitempty"`
}

// Forward is ~/.repose/mcp/forward/NAME.json, written by repose-mcp hold.
type Forward struct {
	Version         int               `json:"version"`
	Name            string            `json:"name"`
	ProtocolVersion string            `json:"protocolVersion,omitempty"`
	Initialize      json.RawMessage   `json:"initialize,omitempty"`
	Tools           []json.RawMessage `json:"tools,omitempty"`
	Updated         string            `json:"updated,omitempty"`
}

// Platform is /etc/repose/mcp.json.
type Platform struct {
	MCPServers map[string]Server   `json:"mcpServers"`
	Retired    map[string][]Server `json:"repose_retired,omitempty"`
}

// Registry is everything sync renders.
type Registry struct {
	Platform Platform
	Laptop   Laptop
	Forward  map[string]*Forward
}

// Paths are the files the registry reads and writes. Tests point them at a
// temporary directory.
type Paths struct {
	Home       string // the agent user's home
	Platform   string // /etc/repose/mcp.json
	SecretsDir string // /run/repose/secrets
	SocketDir  string // /run/repose/mcp, where a forward's socket lives
	Etc        string // root of the agents' system layers, "/" in a guest
}

// DefaultPaths are the guest's paths for home. REPOSE_SECRETS_DIR moves the
// secrets directory, for tests.
func DefaultPaths(home string) Paths {
	p := Paths{
		Home:       home,
		Platform:   "/etc/repose/mcp.json",
		SecretsDir: "/run/repose/secrets",
		SocketDir:  "/run/repose/mcp",
		Etc:        "/",
	}
	if d := os.Getenv("REPOSE_SECRETS_DIR"); d != "" {
		p.SecretsDir = d
	}
	return p
}

// Dir is ~/.repose/mcp.
func (p Paths) Dir() string { return filepath.Join(p.Home, ".repose", "mcp") }

func (p Paths) laptopFile() string   { return filepath.Join(p.Dir(), "laptop.json") }
func (p Paths) forwardDir() string   { return filepath.Join(p.Dir(), "forward") }
func (p Paths) renderedFile() string { return filepath.Join(p.Dir(), "rendered.json") }
func (p Paths) agentFile(agent string) string {
	return filepath.Join(p.Dir(), "agents", agent+".json")
}
func (p Paths) etc(rel string) string { return filepath.Join(p.Etc, rel) }

// Load reads the registry. Missing files are empty sources; a file that
// does not parse is an error, so sync changes nothing rather than remove
// every entry that file's servers had.
func Load(p Paths) (*Registry, error) {
	r := &Registry{Forward: map[string]*Forward{}}
	if err := readJSON(p.Platform, &r.Platform); err != nil {
		return nil, fmt.Errorf("%s: %w", p.Platform, err)
	}
	if err := readJSON(p.laptopFile(), &r.Laptop); err != nil {
		return nil, fmt.Errorf("~/.repose/mcp/laptop.json: %w", err)
	}
	ents, err := os.ReadDir(p.forwardDir())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("~/.repose/mcp/forward: %w", err)
	}
	for _, e := range ents {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() || !ValidName(name) {
			continue
		}
		var f Forward
		if err := readJSON(filepath.Join(p.forwardDir(), e.Name()), &f); err != nil {
			return nil, fmt.Errorf("~/.repose/mcp/forward/%s: %w", e.Name(), err)
		}
		r.Forward[name] = &f
	}
	return r, nil
}

// readJSON decodes path into v; a missing or empty file leaves v as it is.
func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}

// logical is one server in the merged list, before any agent's rendering.
type logical struct {
	Name    string
	From    string
	Project string // checkout path, for a laptop local-scope server
	Server  Server
}

// list is the merged list, each name once, in claim order: machine,
// forward, laptop user, laptop projects (paths sorted). With perProject
// (Claude Code's local scope), a project server is kept per checkout
// unless the machine or a forward holds its name; otherwise the user scope
// and then the first checkout in path order keep a name.
// taken lists the names skipped because an earlier source holds them.
func (r *Registry) list(perProject bool) (out []logical, taken []Skip) {
	seen := map[string]bool{}
	add := func(l logical) {
		if !ValidName(l.Name) || Reserved[l.Name] {
			taken = append(taken, Skip{l.Name, "a name repose cannot give a server"})
			return
		}
		if seen[l.Name] {
			taken = append(taken, Skip{l.Name, "another server has this name"})
			return
		}
		seen[l.Name] = true
		out = append(out, l)
	}
	for _, n := range sortedKeys(r.Platform.MCPServers) {
		add(logical{Name: n, From: FromMachine, Server: r.Platform.MCPServers[n]})
	}
	for _, n := range sortedKeys(r.Forward) {
		add(logical{Name: n, From: FromForward})
	}
	// Claude Code's local scope overrides its user scope, so with
	// perProject a project server only gives way to the machine's and the
	// forwarded ones, as on the laptop.
	base := map[string]bool{}
	if perProject {
		for k := range seen {
			base[k] = true
		}
	}
	for _, n := range sortedKeys(r.Laptop.User) {
		add(logical{Name: n, From: FromLaptop, Server: r.Laptop.User[n]})
	}
	if !perProject {
		for k := range seen {
			base[k] = true
		}
	}
	projSeen := map[string]bool{}
	for _, path := range sortedKeys(r.Laptop.Projects) {
		for _, n := range sortedKeys(r.Laptop.Projects[path]) {
			l := logical{Name: n, From: FromLaptop, Project: path, Server: r.Laptop.Projects[path][n]}
			if !ValidName(n) || Reserved[n] {
				taken = append(taken, Skip{n, "a name repose cannot give a server"})
				continue
			}
			if base[n] || (!perProject && projSeen[n]) {
				taken = append(taken, Skip{n, "another server has this name"})
				continue
			}
			projSeen[n] = true
			seen[n] = true
			out = append(out, l)
		}
	}
	return out, taken
}

// LaptopServer is the carried server `repose-mcp run NAME` starts: the user
// scope's, else the first checkout's in path order, the same choice sync
// makes for agents without a per-folder scope.
func (r *Registry) LaptopServer(name string) (Server, bool) {
	ls, _ := r.list(false)
	for _, l := range ls {
		if l.Name == name && l.From == FromLaptop {
			return l.Server, true
		}
	}
	return nil, false
}

// Checkouts are the checkout and the other checkouts of the machine
// (docs/interfaces/guest-conventions.md "The checkout", "Other
// checkouts"), as real paths, the checkout first.
func Checkouts(home string) []string {
	var names []string
	if b, err := os.ReadFile(filepath.Join(home, ".repose", "checkout")); err == nil {
		names = append(names, strings.TrimSpace(string(b)))
	} else {
		var pj struct {
			Slug string `json:"slug"`
		}
		if readJSON(filepath.Join(home, ".repose", "project.json"), &pj) == nil && pj.Slug != "" {
			names = append(names, pj.Slug)
		}
	}
	if b, err := os.ReadFile(filepath.Join(home, ".repose", "checkouts")); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			names = append(names, strings.TrimSpace(l))
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" || strings.ContainsRune(n, '/') || strings.HasPrefix(n, ".") {
			continue
		}
		d, err := filepath.EvalSymlinks(filepath.Join(home, n))
		if err != nil || seen[d] {
			continue
		}
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// transport is a server's kind: stdio, http, sse, ws, or what its type says.
func transport(s Server) string {
	if t, _ := s["type"].(string); t != "" {
		return t
	}
	if _, ok := s["url"]; ok {
		return "http"
	}
	return "stdio"
}

func str(s Server, k string) string {
	v, _ := s[k].(string)
	return v
}

func strs(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			} else {
				out = append(out, fmt.Sprint(e))
			}
		}
		return out
	}
	return nil
}

func strMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, e := range m {
		if s, ok := e.(string); ok {
			out[k] = s
		} else {
			out[k] = fmt.Sprint(e)
		}
	}
	return out
}

func sortedStrings(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
