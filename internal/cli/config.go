package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/heracraft/repose/internal/multiplexer"
)

const defaultAPIURL = "https://api.repose.herakraft.co/v1"
const defaultLogtoIssuer = "https://accounts.herakraft.co" // the owner's Logto (DECISIONS I-84); /oidc is appended
// defaultLogtoClientID is the App ID Logto assigned to the `repose-cli`
// Native application. Logto identifies applications by this opaque id, not
// by name; "repose-cli" as client_id answered oidc.invalid_client at the
// M2 gate (DECISIONS I-99). Public, like the dashboard's PUBLIC_LOGTO_APP_ID.
const defaultLogtoClientID = "jccig5bb3i4d78bq4farv"
const apiResource = "https://api.repose.herakraft.co"

// Config is config.toml (docs/interfaces/cli-config.md).
type Config struct {
	APIURL string `toml:"api_url"`
	// "gateway" was here, read and never used; an old file that sets it
	// still loads, since unknown keys are ignored (DECISIONS I-242).
	// DefaultSize is the size of new projects (I-621). DefaultClass is
	// its old name, read for a release; default_size wins over it.
	DefaultSize  string `toml:"default_size"`
	DefaultClass string `toml:"default_class"`
	DefaultAgent string `toml:"default_agent"`
	// Editor is what `repose code` opens when neither --editor nor
	// $REPOSE_EDITOR names one: code, cursor or zed (I-622).
	Editor string `toml:"editor"`
	// DefaultMultiplexer is the multiplexer of projects `run` creates,
	// tmux or herdr; empty lets the CLI pick (DECISIONS I-502).
	DefaultMultiplexer string `toml:"default_multiplexer"`
	// SyncExclude is sync.exclude. TOML spells that as a [sync] table
	// with an exclude key (Sync, merged in by loadConfig); the quoted
	// top-level key "sync.exclude" is what this tag matched before and
	// still works (DECISIONS I-241).
	SyncExclude []string `toml:"sync.exclude"`
	Sync        struct {
		Exclude []string `toml:"exclude"`
	} `toml:"sync"`
	LogtoIssuer string `toml:"logto_issuer"`
	// LogtoClientID overrides the built-in App ID for a different Logto
	// (staging, a fork). Public.
	LogtoClientID string `toml:"logto_client_id"`
	// Logins is the [logins] table: which of the laptop's logins `run`
	// leaves on the laptop (logins.go, DECISIONS I-422).
	Logins LoginsConfig `toml:"logins"`
	// MCP is the [mcp] table: forward lists the laptop's MCP servers
	// forwarded whenever you're attached (`repose mcp forward`, DECISIONS
	// I-557).
	MCP MCPConfig `toml:"mcp"`
	// Projects holds per-project tables, keyed by the project's name:
	// [projects.NAME.logins] replaces [logins] for that project, and
	// [projects.NAME.mcp] forward adds to [mcp] forward.
	Projects map[string]ProjectConfig `toml:"projects"`
}

// MCPConfig is an [mcp] table.
type MCPConfig struct {
	Forward []string `toml:"forward"`
}

// LoginsConfig is a [logins] table. Skip is nil when the table does not
// set it, and empty when it says to copy everything: a project's
// `skip = []` overrides a global list.
type LoginsConfig struct {
	Skip *[]string `toml:"skip"`
}

// ProjectConfig is one [projects.NAME] table.
type ProjectConfig struct {
	Logins LoginsConfig `toml:"logins"`
	MCP    MCPConfig    `toml:"mcp"`
}

// mcpForward is what the session helper forwards for the project named
// slug: [mcp] forward, then [projects.NAME.mcp] forward, each name once.
func (c Config) mcpForward(slug string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range append(append([]string{}, c.MCP.Forward...), c.Projects[slug].MCP.Forward...) {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func defaultConfig() Config {
	return Config{
		APIURL:        defaultAPIURL,
		DefaultClass:  "large",
		DefaultAgent:  "claude",
		LogtoIssuer:   defaultLogtoIssuer,
		LogtoClientID: defaultLogtoClientID,
	}
}

func configPath(dir string) string { return filepath.Join(dir, "config.toml") }

func loadConfig(dir string) (Config, error) {
	cfg := defaultConfig()
	b, err := os.ReadFile(configPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if _, err := toml.Decode(string(b), &cfg); err != nil {
		return cfg, err
	}
	if cfg.APIURL == "" {
		cfg.APIURL = defaultAPIURL
	}
	cfg.SyncExclude = append(cfg.SyncExclude, cfg.Sync.Exclude...)
	if cfg.DefaultSize != "" {
		cfg.DefaultClass = cfg.DefaultSize
	}
	if cfg.LogtoIssuer == "" {
		cfg.LogtoIssuer = defaultLogtoIssuer
	}
	if cfg.LogtoClientID == "" {
		cfg.LogtoClientID = defaultLogtoClientID
	}
	if cfg.DefaultMultiplexer != "" && !multiplexer.Valid(cfg.DefaultMultiplexer) {
		return cfg, fmt.Errorf("%s: default_multiplexer is %q; it takes %s", configPath(dir), cfg.DefaultMultiplexer, strings.Join(multiplexer.Names, " or "))
	}
	return cfg, nil
}

// Credentials is credentials.json. On macOS the refresh token lives in the
// keychain and this file holds only the issuer; RefreshToken is empty on
// disk there (07-cli.md §9: "cat credentials.json on macOS shows no
// refresh token").
type Credentials struct {
	RefreshToken string    `json:"refresh_token,omitempty"`
	AccessToken  string    `json:"access_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	LogtoIssuer  string    `json:"logto_issuer"`
	// LogtoClientID is the client_id the refresh token was issued to, so a
	// refresh needs no config. Empty in files written before v0.1.1: the
	// token source falls back to the built-in id.
	LogtoClientID string `json:"logto_client_id,omitempty"`
}

// clientIDOrDefault returns the credentials' client id, or the built-in one
// for credentials written before the field existed.
func (c Credentials) clientIDOrDefault() string {
	if c.LogtoClientID != "" {
		return c.LogtoClientID
	}
	return defaultLogtoClientID
}

func credentialsPath(dir string) string { return filepath.Join(dir, "credentials.json") }

func loadCredentials(dir string) (Credentials, bool, error) {
	b, err := os.ReadFile(credentialsPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return Credentials{}, false, nil
		}
		return Credentials{}, false, err
	}
	var c Credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return Credentials{}, false, err
	}
	if c.RefreshToken == "" {
		if tok, ok := keychainGet(c.LogtoIssuer); ok {
			c.RefreshToken = tok
		}
	}
	return c, true, nil
}

func saveCredentials(dir string, c Credentials) error {
	onDisk := c
	if keychainAvailable() {
		if err := keychainSet(c.LogtoIssuer, c.RefreshToken); err != nil {
			return err
		}
		onDisk.RefreshToken = ""
	}
	b, err := json.MarshalIndent(onDisk, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(credentialsPath(dir), b, 0o600)
}

func deleteCredentials(dir string) error {
	if keychainAvailable() {
		if c, ok, _ := loadCredentials(dir); ok {
			_ = keychainDelete(c.LogtoIssuer)
		}
	}
	err := os.Remove(credentialsPath(dir))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ProjectsCache is projects.json: a local cache of (user, remote) -> project
// and directory -> project for projects named on run, regenerable from GET
// /projects.
type ProjectsCache struct {
	ByRemote map[string]CachedProject `json:"-"` // top-level keys, merged into MarshalJSON
	ByDir    map[string]string        `json:"by_dir"`
	// Checkouts maps a laptop folder `repose run --on` added to a machine
	// to that machine and the checkout's name there (DECISIONS I-480).
	// Such a folder is never in ByDir: a CLI from before I-480 does not
	// read this key, finds nothing for the folder, and makes it a machine
	// of its own instead of syncing it over the machine's checkout.
	Checkouts map[string]CachedCheckout `json:"checkouts,omitempty"`

	// base is the file as this process loaded it. A save applies only what
	// this process changed since (added, updated, removed) to the file as it
	// is on disk then, under a lock, so concurrent `repose run`s in
	// different directories merge instead of the last writer erasing the
	// others' entries.
	base *ProjectsCache
}

// CachedCheckout is one entry of ProjectsCache.Checkouts.
type CachedCheckout struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"checkout"`
}

// CachedProject is one entry of ProjectsCache.ByRemote.
type CachedProject struct {
	ProjectID string `json:"project_id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
}

func newProjectsCache() ProjectsCache {
	return ProjectsCache{ByRemote: map[string]CachedProject{}, ByDir: map[string]string{}, Checkouts: map[string]CachedCheckout{}}
}

func projectsPath(dir string) string { return filepath.Join(dir, "projects.json") }

func (c ProjectsCache) MarshalJSON() ([]byte, error) {
	m := map[string]any{"by_dir": c.ByDir}
	if len(c.Checkouts) > 0 {
		m["checkouts"] = c.Checkouts
	}
	for k, v := range c.ByRemote {
		m[k] = v
	}
	return json.Marshal(m)
}

func (c *ProjectsCache) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*c = newProjectsCache()
	for k, v := range raw {
		if k == "by_dir" {
			if err := json.Unmarshal(v, &c.ByDir); err != nil {
				return err
			}
			continue
		}
		if k == "checkouts" {
			// A CLI from before I-480 reads this key as a remote and writes
			// it back as one ({"project_id": "", ...}); that, or anything
			// else unreadable, is no checkouts rather than an unreadable
			// cache.
			if err := json.Unmarshal(v, &c.Checkouts); err != nil || c.Checkouts == nil {
				c.Checkouts = map[string]CachedCheckout{}
			}
			continue
		}
		var p CachedProject
		if err := json.Unmarshal(v, &p); err != nil {
			return err
		}
		c.ByRemote[k] = p
	}
	return nil
}

func loadProjectsCache(dir string) (ProjectsCache, error) {
	b, err := os.ReadFile(projectsPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return newProjectsCache(), nil
		}
		return newProjectsCache(), err
	}
	c := newProjectsCache()
	if err := json.Unmarshal(b, &c); err != nil {
		return newProjectsCache(), err
	}
	snap := c.clone()
	c.base = &snap
	return c, nil
}

func (c ProjectsCache) clone() ProjectsCache {
	out := newProjectsCache()
	for k, v := range c.ByRemote {
		out.ByRemote[k] = v
	}
	for k, v := range c.ByDir {
		out.ByDir[k] = v
	}
	for k, v := range c.Checkouts {
		out.Checkouts[k] = v
	}
	return out
}

// mergeInto applies c's changes relative to its base onto disk.
func (c ProjectsCache) mergeInto(disk ProjectsCache) ProjectsCache {
	base := newProjectsCache()
	if c.base != nil {
		base = *c.base
	}
	for k, v := range c.ByRemote {
		if old, ok := base.ByRemote[k]; !ok || old != v {
			disk.ByRemote[k] = v
		}
	}
	for k := range base.ByRemote {
		if _, ok := c.ByRemote[k]; !ok {
			delete(disk.ByRemote, k)
		}
	}
	for k, v := range c.ByDir {
		if old, ok := base.ByDir[k]; !ok || old != v {
			disk.ByDir[k] = v
		}
	}
	for k := range base.ByDir {
		if _, ok := c.ByDir[k]; !ok {
			delete(disk.ByDir, k)
		}
	}
	for k, v := range c.Checkouts {
		if old, ok := base.Checkouts[k]; !ok || old != v {
			disk.Checkouts[k] = v
		}
	}
	for k := range base.Checkouts {
		if _, ok := c.Checkouts[k]; !ok {
			delete(disk.Checkouts, k)
		}
	}
	return disk
}

func saveProjectsCache(dir string, c ProjectsCache) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	unlock, err := lockFile(projectsPath(dir) + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	disk, err := loadProjectsCache(dir)
	if err != nil {
		// An unreadable file is replaced by this process's view, as before.
		disk = newProjectsCache()
	}
	merged := c.mergeInto(disk)
	b, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(projectsPath(dir), b, 0o600)
}
