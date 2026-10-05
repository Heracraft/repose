package cli

import (
	"bufio"
	"debug/buildinfo"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// The tools part of the carry (DECISIONS I-221, I-222): the command-line
// tools the laptop installed globally, and the commands the project's own
// scripts run (scan.go), go to the guest as one list; the base's
// repose-tools-install (nix/guest/base/tools-carry.nix) installs what the
// guest lacks, in the background, preferring a nixpkgs binary.
//
// The laptop side never runs a package manager: it reads their install
// directories (a directory listing and a few small files each, and
// debug/buildinfo for Go binaries), so it costs milliseconds and runs
// while the sync's probe ssh is in flight. What travels is names,
// managers, versions and Go module paths; never a laptop path, never a
// config value.

// toolItem is one entry of the list the guest installs from; the JSON is
// the contract with repose-tools-install (guest-conventions.md "Tools
// carry").
type toolItem struct {
	// Name is what the user sees ("typescript", "air").
	Name string `json:"name"`
	// Bins are the commands the tool provides; the first is the one
	// looked up in nixpkgs. The guest has the tool when any is on PATH.
	Bins []string `json:"bins"`
	// Manager is how the laptop installed it (npm, pnpm, bun, go, cargo,
	// uv, pipx), or "" for a command only a project script names: nixpkgs
	// or nothing.
	Manager string `json:"manager,omitempty"`
	// Pkg is the manager's name for it: the npm package, the Go package
	// path, the crate, the Python distribution.
	Pkg     string `json:"pkg,omitempty"`
	Version string `json:"version,omitempty"`
	// From is "laptop" or "project".
	From string `json:"from"`
	// Why says where it was found, for `repose scan` (not sent).
	Why string `json:"-"`
}

// toolsWanted is the file the guest installs from.
type toolsWanted struct {
	V     int        `json:"v"`
	Hash  string     `json:"hash"`
	Items []toolItem `json:"items"`
	// Node is the major version the project pins, "" for none.
	Node string `json:"node,omitempty"`
	// Ruby and Java are the ruby series and java major the project's pin
	// resolves to in the base's nixpkgs (scan_runtimes.go), "" for none.
	Ruby string `json:"ruby,omitempty"`
	Java string `json:"java,omitempty"`
}

// toolsCarry is what the tools part sends.
type toolsCarry struct {
	Wanted toolsWanted
	JSON   []byte
}

// safeName and safeVersion bound what may travel: a name or version
// outside them is dropped, so nothing in the list can be read as a shell
// word, an option or a path by the guest.
var (
	safeName    = regexp.MustCompile(`^[A-Za-z0-9@][A-Za-z0-9@/._+-]{0,199}$`)
	safeVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,99}$`)
	safeBin     = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._+-]{0,99}$`)
)

// baseCommands are the commands every guest has without a carry
// (nix/guest/base/tool-list.nix, agents.nix, coreutils and the shells),
// and the package managers themselves. The guest checks its PATH anyway;
// this list keeps them out of the list and out of `repose scan`.
var baseCommands = setOf(
	// base tool list
	"curl", "wget", "jq", "rg", "fd", "bat", "fzf", "tree", "unzip", "zip", "zstd", "htop",
	"git", "gh", "just", "node", "npm", "npx", "corepack", "pnpm", "pnpx", "python", "python3", "pip", "pip3",
	"uv", "uvx", "go", "gofmt", "rustup", "cargo", "rustc", "rustfmt", "clippy-driver", "cargo-clippy",
	"tmux", "ssh", "scp", "eza", "zoxide", "starship", "direnv", "nvim", "vim", "vi",
	"docker", "docker-compose", "dockerd", "nix", "nix-shell", "nix-env", "nix-build",
	// the C toolchain and everyday CLIs of I-218
	"cc", "gcc", "g++", "c++", "cpp", "ld", "ar", "as", "nm", "strip", "objdump", "readelf",
	"make", "gmake", "pkg-config", "cmake", "ctest", "lsof", "dig", "nslookup", "host", "sqlite3",
	"psql", "pg_dump", "pg_dumpall", "pg_restore", "pg_isready", "openssl", "gpg", "gpg2",
	"killall", "fuser", "nc", "strace", "rsync", "nix-locate",
	// the native gems' configs (compat.nix, I-265)
	"pg_config", "mysql_config", "mariadb_config",
	// agents and their helpers (agents.nix)
	"claude", "codex", "opencode", "gemini", "pi", "playwright-mcp", "chrome-devtools-mcp",
	// shell builtins and keywords
	"cd", "echo", "exit", "export", "set", "unset", "true", "false", "test", "[", "printf", "read",
	"source", ".", "eval", "exec", "if", "then", "else", "fi", "for", "do", "done", "while", "case", "esac",
	"trap", "wait", "shift", "return", "local", "alias", "type", "command", "builtin", "ulimit", "umask", ":",
	"sh", "bash", "zsh", "time", "nohup", "sudo", "env", "nice", "xargs",
	// coreutils and friends
	"rm", "cp", "mv", "mkdir", "rmdir", "ln", "ls", "cat", "touch", "chmod", "chown", "sleep", "kill",
	"grep", "egrep", "sed", "awk", "find", "tr", "head", "tail", "sort", "uniq", "wc", "tee", "date",
	"basename", "dirname", "pwd", "which", "tar", "gzip", "gunzip", "cut", "paste", "diff", "patch",
	"stat", "realpath", "readlink", "mktemp", "seq", "yes", "whoami", "id", "hostname", "uname", "df",
	"du", "ps", "pkill", "pgrep", "less", "more", "file", "install", "sha256sum", "md5sum", "base64",
	"open", "xdg-open",
)

func setOf(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// skippedGlobals are package-manager packages that are the managers
// themselves, never carried.
var skippedGlobals = setOf("npm", "corepack", "pnpm", "yarn", "bun", "uv", "pipx", "node")

// toolEnv is what the readers look at besides the file system: the
// laptop's environment and OS. A value so tests can give their own.
type toolEnv struct {
	Home   string
	GOOS   string
	Getenv func(string) string
	// LookPath finds node for npm's default prefix.
	LookPath func(string) (string, error)
}

// readGlobalTools lists the laptop's globally installed tools from every
// manager it knows, sorted by name, first occurrence of a command wins.
func readGlobalTools(te toolEnv) []toolItem {
	var all []toolItem
	all = append(all, readNpmGlobals(te)...)
	all = append(all, readPnpmGlobals(te)...)
	all = append(all, readBunGlobals(te)...)
	all = append(all, readGoBins(te)...)
	all = append(all, readCargoInstalls(te)...)
	all = append(all, readUvTools(te)...)
	all = append(all, readPipxVenvs(te)...)
	return dedupeTools(all)
}

// dedupeTools keeps the first item that claims a command and drops items
// every command of which is in the base or already claimed.
func dedupeTools(items []toolItem) []toolItem {
	claimed := map[string]bool{}
	var out []toolItem
	for _, it := range items {
		var bins []string
		for _, b := range it.Bins {
			if !safeBin.MatchString(b) || baseCommands[b] || claimed[b] {
				continue
			}
			bins = append(bins, b)
		}
		if len(bins) == 0 || !safeName.MatchString(it.Name) {
			continue
		}
		if it.Pkg != "" && !safeName.MatchString(it.Pkg) {
			continue
		}
		if it.Version != "" && !safeVersion.MatchString(it.Version) {
			it.Version = ""
		}
		for _, b := range bins {
			claimed[b] = true
		}
		it.Bins = bins
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (te toolEnv) getenv(k string) string {
	if te.Getenv == nil {
		return ""
	}
	return te.Getenv(k)
}

// expandHome turns a leading ~ or ${HOME}/$HOME into the home directory.
func (te toolEnv) expandHome(p string) string {
	switch {
	case p == "~":
		return te.Home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(te.Home, p[2:])
	case strings.HasPrefix(p, "${HOME}"):
		return te.Home + p[len("${HOME}"):]
	case strings.HasPrefix(p, "$HOME"):
		return te.Home + p[len("$HOME"):]
	}
	return p
}

// dataHome is $XDG_DATA_HOME or ~/.local/share.
func (te toolEnv) dataHome() string {
	if d := te.getenv("XDG_DATA_HOME"); d != "" {
		return d
	}
	return filepath.Join(te.Home, ".local", "share")
}

// ---- npm ----

// npmPrefix is npm's global prefix as npm would work it out, without
// running npm: $NPM_CONFIG_PREFIX, then `prefix=` in ~/.npmrc, then the
// default, which is the directory above the one node is in (on Windows,
// %APPDATA%\npm). Only the prefix line of ~/.npmrc is read; the file
// holds registry tokens.
func npmPrefixes(te toolEnv) []string {
	for _, k := range []string{"NPM_CONFIG_PREFIX", "npm_config_prefix"} {
		if v := te.getenv(k); v != "" {
			return []string{te.expandHome(v)}
		}
	}
	if f, err := os.Open(filepath.Join(te.Home, ".npmrc")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			k, v, ok := strings.Cut(sc.Text(), "=")
			if ok && strings.TrimSpace(k) == "prefix" {
				_ = f.Close()
				return []string{te.expandHome(strings.Trim(strings.TrimSpace(v), `"'`))}
			}
		}
		_ = f.Close()
	}
	if te.GOOS == "windows" {
		if a := te.getenv("APPDATA"); a != "" {
			return []string{filepath.Join(a, "npm")}
		}
		return nil
	}
	if te.LookPath == nil {
		return nil
	}
	node, err := te.LookPath("node")
	if err != nil {
		return nil
	}
	// The directory node was found in first (Homebrew's /opt/homebrew/bin
	// is a symlink into the Cellar, but npm's prefix is /opt/homebrew),
	// then where it resolves to (nvm, a tarball).
	out := []string{filepath.Dir(filepath.Dir(node))}
	if r, err := filepath.EvalSymlinks(node); err == nil {
		if p := filepath.Dir(filepath.Dir(r)); p != out[0] {
			out = append(out, p)
		}
	}
	return out
}

func npmModulesDir(te toolEnv, prefix string) string {
	if te.GOOS == "windows" {
		return filepath.Join(prefix, "node_modules")
	}
	return filepath.Join(prefix, "lib", "node_modules")
}

func readNpmGlobals(te toolEnv) []toolItem {
	for _, prefix := range npmPrefixes(te) {
		dir := npmModulesDir(te, prefix)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		var out []toolItem
		for _, name := range listNodeModules(dir) {
			if it, ok := nodePackageTool(filepath.Join(dir, filepath.FromSlash(name)), name, "npm", ""); ok {
				it.Why = "npm i -g"
				out = append(out, it)
			}
		}
		return out
	}
	return nil
}

// listNodeModules lists the package names in a node_modules directory,
// scoped ones as @scope/name. Symlinks (npm link, a laptop checkout) and
// dot entries are left out.
func listNodeModules(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if strings.HasPrefix(n, ".") || e.Type()&os.ModeSymlink != 0 || !e.IsDir() {
			continue
		}
		if strings.HasPrefix(n, "@") {
			sub, _ := os.ReadDir(filepath.Join(dir, n))
			for _, s := range sub {
				if s.IsDir() && s.Type()&os.ModeSymlink == 0 && !strings.HasPrefix(s.Name(), ".") {
					out = append(out, n+"/"+s.Name())
				}
			}
			continue
		}
		out = append(out, n)
	}
	return out
}

// nodePackage is the part of a package.json the readers need.
type nodePackage struct {
	Name    string          `json:"name"`
	Version string          `json:"version"`
	Bin     json.RawMessage `json:"bin"`
}

// bins is the package's commands: a string bin is named after the
// package (without its scope).
func (p nodePackage) bins(name string) []string {
	if len(p.Bin) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(p.Bin, &s) == nil {
		_, base, found := strings.Cut(name, "/")
		if !found {
			base = name
		}
		return []string{base}
	}
	var m map[string]string
	if json.Unmarshal(p.Bin, &m) != nil {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	// The command named like the package goes first: it is the one looked
	// up in nixpkgs.
	_, base, found := strings.Cut(name, "/")
	if !found {
		base = name
	}
	for i, b := range out {
		if b == base {
			out[0], out[i] = out[i], out[0]
			break
		}
	}
	return out
}

// nodePackageTool reads dir/package.json into an item; a package with no
// commands is not a tool.
func nodePackageTool(dir, name, manager, version string) (toolItem, bool) {
	if skippedGlobals[name] {
		return toolItem{}, false
	}
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return toolItem{}, false
	}
	var p nodePackage
	if json.Unmarshal(b, &p) != nil {
		return toolItem{}, false
	}
	bins := p.bins(name)
	if len(bins) == 0 {
		return toolItem{}, false
	}
	if version == "" {
		version = p.Version
	}
	return toolItem{Name: name, Bins: bins, Manager: manager, Pkg: name, Version: version, From: "laptop"}, true
}

// ---- pnpm and bun ----

// pnpmHome is $PNPM_HOME or pnpm's default per OS.
func pnpmHome(te toolEnv) string {
	if h := te.getenv("PNPM_HOME"); h != "" {
		return te.expandHome(h)
	}
	switch te.GOOS {
	case "darwin":
		return filepath.Join(te.Home, "Library", "pnpm")
	case "windows":
		if l := te.getenv("LOCALAPPDATA"); l != "" {
			return filepath.Join(l, "pnpm")
		}
		return ""
	}
	return filepath.Join(te.dataHome(), "pnpm")
}

// readPnpmGlobals reads $PNPM_HOME/global/<layout>/package.json, the
// manifest `pnpm add -g` keeps, and each dependency's own package.json.
func readPnpmGlobals(te toolEnv) []toolItem {
	h := pnpmHome(te)
	if h == "" {
		return nil
	}
	dirs, _ := filepath.Glob(filepath.Join(h, "global", "*"))
	sort.Sort(sort.Reverse(sort.StringSlice(dirs))) // the newest layout first
	for _, d := range dirs {
		if items := readManifestGlobals(d, "pnpm", "pnpm add -g"); items != nil {
			return items
		}
	}
	return nil
}

// readBunGlobals reads ~/.bun/install/global (or $BUN_INSTALL's).
func readBunGlobals(te toolEnv) []toolItem {
	root := te.getenv("BUN_INSTALL")
	if root == "" {
		root = filepath.Join(te.Home, ".bun")
	}
	return readManifestGlobals(filepath.Join(te.expandHome(root), "install", "global"), "bun", "bun add -g")
}

// readManifestGlobals reads a global install directory kept as a package
// (a package.json whose dependencies are the globals, a node_modules
// beside it).
func readManifestGlobals(dir, manager, why string) []toolItem {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil
	}
	var m struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	names := make([]string, 0, len(m.Dependencies))
	for n := range m.Dependencies {
		names = append(names, n)
	}
	sort.Strings(names)
	out := []toolItem{}
	for _, n := range names {
		it, ok := nodePackageTool(filepath.Join(dir, "node_modules", filepath.FromSlash(n)), n, manager, "")
		if !ok {
			continue
		}
		it.Why = why
		out = append(out, it)
	}
	return out
}

// ---- Go ----

// goBinDirs is where `go install` puts binaries: $GOBIN, else the first
// $GOPATH entry's bin, else ~/go/bin.
func goBinDir(te toolEnv) string {
	if b := te.getenv("GOBIN"); b != "" {
		return b
	}
	if p := te.getenv("GOPATH"); p != "" {
		first := filepath.SplitList(p)[0]
		return filepath.Join(first, "bin")
	}
	return filepath.Join(te.Home, "go", "bin")
}

// readGoBins reads each binary's build info: the package path and the
// main module's version are exactly what `go install path@version`
// takes. A binary built from a local checkout ("(devel)") names a laptop
// path in effect and is left out.
func readGoBins(te toolEnv) []toolItem {
	dir := goBinDir(te)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []toolItem
	for _, e := range ents {
		if !e.Type().IsRegular() {
			continue
		}
		bi, err := buildinfo.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil || bi.Path == "" || bi.Main.Version == "" || bi.Main.Version == "(devel)" {
			continue
		}
		bin := strings.TrimSuffix(e.Name(), ".exe")
		out = append(out, toolItem{Name: bin, Bins: []string{bin}, Manager: "go", Pkg: bi.Path, Version: bi.Main.Version, From: "laptop", Why: "go install"})
	}
	return out
}

// ---- cargo ----

func readCargoInstalls(te toolEnv) []toolItem {
	root := te.getenv("CARGO_HOME")
	if root == "" {
		root = filepath.Join(te.Home, ".cargo")
	}
	b, err := os.ReadFile(filepath.Join(root, ".crates2.json"))
	if err != nil {
		return nil
	}
	var m struct {
		Installs map[string]struct {
			Bins []string `json:"bins"`
		} `json:"installs"`
	}
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	var out []toolItem
	for key, v := range m.Installs {
		// "ripgrep 14.1.0 (registry+https://github.com/rust-lang/crates.io-index)":
		// only crates from a registry; a git or path install names
		// something only the laptop has.
		f := strings.Fields(key)
		if len(f) != 3 || !strings.HasPrefix(f[2], "(registry+") && !strings.HasPrefix(f[2], "(sparse+") {
			continue
		}
		var bins []string
		for _, b := range v.Bins {
			bins = append(bins, strings.TrimSuffix(b, ".exe"))
		}
		sort.Strings(bins)
		out = append(out, toolItem{Name: f[0], Bins: bins, Manager: "cargo", Pkg: f[0], Version: f[1], From: "laptop", Why: "cargo install"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ---- uv and pipx ----

func uvToolDir(te toolEnv) string {
	if d := te.getenv("UV_TOOL_DIR"); d != "" {
		return d
	}
	if te.GOOS == "windows" {
		if a := te.getenv("APPDATA"); a != "" {
			return filepath.Join(a, "uv", "data", "tools")
		}
	}
	return filepath.Join(te.dataHome(), "uv", "tools")
}

// uvEntry matches one entrypoint's name in uv-receipt.toml.
var uvEntry = regexp.MustCompile(`\{\s*name\s*=\s*"([^"]+)"\s*,\s*install-path`)

// readUvTools reads each tool's uv-receipt.toml (its commands) and the
// venv's dist-info directory (its version).
func readUvTools(te toolEnv) []toolItem {
	dir := uvToolDir(te)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []toolItem
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name(), "uv-receipt.toml"))
		if err != nil {
			continue
		}
		var bins []string
		for _, m := range uvEntry.FindAllSubmatch(b, -1) {
			bins = append(bins, strings.TrimSuffix(string(m[1]), ".exe"))
		}
		if len(bins) == 0 {
			continue
		}
		name := e.Name()
		out = append(out, toolItem{Name: name, Bins: primaryFirst(bins, name), Manager: "uv", Pkg: name, Version: distInfoVersion(filepath.Join(dir, name), name), From: "laptop", Why: "uv tool install"})
	}
	return out
}

// pipxHomes is $PIPX_HOME or pipx's defaults, newest first.
func pipxHomes(te toolEnv) []string {
	if h := te.getenv("PIPX_HOME"); h != "" {
		return []string{te.expandHome(h)}
	}
	homes := []string{filepath.Join(te.dataHome(), "pipx"), filepath.Join(te.Home, ".local", "pipx")}
	if te.GOOS == "darwin" {
		homes = append([]string{filepath.Join(te.Home, "Library", "Application Support", "pipx")}, homes...)
	}
	return homes
}

func readPipxVenvs(te toolEnv) []toolItem {
	for _, h := range pipxHomes(te) {
		ents, err := os.ReadDir(filepath.Join(h, "venvs"))
		if err != nil {
			continue
		}
		var out []toolItem
		for _, e := range ents {
			b, err := os.ReadFile(filepath.Join(h, "venvs", e.Name(), "pipx_metadata.json"))
			if err != nil {
				continue
			}
			var m struct {
				Main struct {
					Package string   `json:"package"`
					Version string   `json:"package_version"`
					Apps    []string `json:"apps"`
				} `json:"main_package"`
			}
			if json.Unmarshal(b, &m) != nil || m.Main.Package == "" || len(m.Main.Apps) == 0 {
				continue
			}
			var bins []string
			for _, a := range m.Main.Apps {
				bins = append(bins, strings.TrimSuffix(a, ".exe"))
			}
			out = append(out, toolItem{Name: m.Main.Package, Bins: primaryFirst(bins, m.Main.Package), Manager: "pipx", Pkg: m.Main.Package, Version: m.Main.Version, From: "laptop", Why: "pipx install"})
		}
		return out
	}
	return nil
}

// distInfoVersion finds <name>-<version>.dist-info in a venv's
// site-packages ("-" and "_" are the same in a distribution name).
func distInfoVersion(venv, name string) string {
	norm := strings.ToLower(strings.NewReplacer("-", "_", ".", "_").Replace(name))
	pats := []string{
		filepath.Join(venv, "lib", "python*", "site-packages", "*.dist-info"),
		filepath.Join(venv, "Lib", "site-packages", "*.dist-info"),
	}
	for _, p := range pats {
		ms, _ := filepath.Glob(p)
		for _, m := range ms {
			base := strings.TrimSuffix(filepath.Base(m), ".dist-info")
			i := strings.LastIndex(base, "-")
			if i <= 0 {
				continue
			}
			if strings.ToLower(strings.NewReplacer("-", "_", ".", "_").Replace(base[:i])) == norm {
				return base[i+1:]
			}
		}
	}
	return ""
}

// primaryFirst sorts bins with the one named name first.
func primaryFirst(bins []string, name string) []string {
	sort.Strings(bins)
	for i, b := range bins {
		if b == name {
			bins[0], bins[i] = bins[i], bins[0]
			break
		}
	}
	return bins
}

// ---- the part ----

// toolPrecedence is what a .nix file already describes, so the scan
// leaves it alone (DECISIONS I-490): with a machine.nix on the account
// (and the machine not opted out) the laptop's global tools are not
// carried; with a repose.nix at the checkout root the commands the
// project's scripts run are not. The runtime pins (.nvmrc and the like)
// are declarations, not guesses, and still travel.
type toolPrecedence struct {
	SkipGlobals bool
	SkipScripts bool
}

// precedenceFor reads the checkout half: a repose.nix at its root.
func precedenceFor(personal bool, repoDir string) toolPrecedence {
	p := toolPrecedence{SkipGlobals: personal}
	if repoDir != "" {
		if fi, err := os.Stat(filepath.Join(repoDir, "repose.nix")); err == nil && fi.Mode().IsRegular() {
			p.SkipScripts = true
		}
	}
	return p
}

// buildToolsCarry reads the laptop's globals and scans the checkout
// (repoDir may be "": globals only), each unless prec skips it. It
// returns nil when there is nothing to install.
func buildToolsCarry(homeDir, repoDir string, prec toolPrecedence) *toolsCarry {
	var items []toolItem
	if !prec.SkipGlobals {
		te := toolEnv{Home: homeDir, GOOS: runtime.GOOS, Getenv: os.Getenv, LookPath: lookPathFast}
		items = readGlobalTools(te)
	}
	var sc *scanResult
	if repoDir != "" {
		sc = scanProject(repoDir)
		if prec.SkipScripts {
			sc.Candidates = nil
		}
	}
	return newToolsCarry(items, sc)
}

// newToolsCarry merges the laptop's tools with the project's commands
// (the laptop's entry wins: it has a manager and a version) and hashes
// the list.
func newToolsCarry(items []toolItem, sc *scanResult) *toolsCarry {
	w := toolsWanted{V: 1, Items: items}
	if sc != nil {
		claimed := map[string]bool{}
		for _, it := range items {
			for _, b := range it.Bins {
				claimed[b] = true
			}
		}
		for _, c := range sc.Candidates {
			if claimed[c.Bins[0]] {
				continue
			}
			claimed[c.Bins[0]] = true
			w.Items = append(w.Items, c)
		}
		if sc.Node != nil {
			w.Node = sc.Node.Major
		}
		if sc.Ruby != nil {
			w.Ruby = sc.Ruby.Major
		}
		if sc.Java != nil {
			w.Java = sc.Java.Major
		}
	}
	if w.Items == nil {
		w.Items = []toolItem{}
	}
	if len(w.Items) == 0 && w.Node == "" && w.Ruby == "" && w.Java == "" {
		return nil
	}
	body, _ := json.Marshal(w)
	w.Hash = carryHash(body, toolsInstallScriptVersion)
	b, _ := json.Marshal(w)
	return &toolsCarry{Wanted: w, JSON: b}
}

// toolsInstallScriptVersion goes into the hash, so a change to what the
// guest does with the list sends it again once.
var toolsInstallScriptVersion = []byte("tools-1")

// toolsPart is the guest half: the list lands in ~/.repose, and the
// base's repose-tools-install plans (which commands are missing, a few
// ms, printed as #installing and written to $XDG_RUNTIME_DIR/
// repose-installing) and starts the background unit. On a base without
// the installer nothing happens and no marker is written, so the list is
// sent again after the base is upgraded. The marker is the installer's
// to write, when a pass over this list ends.
const toolsPart = `mkdir -p ~/.repose
cp "$1/tools/wanted.json" ~/.repose/tools-wanted.json.new
mv -f ~/.repose/tools-wanted.json.new ~/.repose/tools-wanted.json
if command -v repose-tools-install >/dev/null 2>&1; then
  repose-tools-install plan
fi
`

// toolsNoticesPart prints what the installer could not install since the
// last run as warnings, once: the file goes when it has been read.
const toolsNoticesPart = `f=~/.repose/tools-notices
if [ -s "$f" ]; then
  mv -f "$f" "$f.sent"
  sed 's/^/#warn /' "$f.sent"
  rm -f "$f.sent"
fi
`

// addToolsPart adds the list when its hash is not the guest's marker, and
// the notices part when the probe saw notices waiting.
func addToolsPart(p *guestPayload, tc *toolsCarry, opts carryOptions) ([]string, error) {
	var sent []string
	if opts.Markers != nil && opts.Markers["tools-notices"] != "" {
		if err := p.part("tool notices", toolsNoticesPart); err != nil {
			return nil, err
		}
		sent = append(sent, "tools-notices")
	}
	if tc == nil || opts.unchanged("tools", tc.Wanted.Hash) {
		return sent, nil
	}
	if err := p.file("tools/wanted.json", tc.JSON); err != nil {
		return nil, err
	}
	if err := p.part("tools", toolsPart); err != nil {
		return nil, err
	}
	return append(sent, "tools"), nil
}

// lookPathFast is exec.LookPath without the per-candidate checks Windows
// needs: a stat of each PATH entry, no process.
func lookPathFast(name string) (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		for _, n := range []string{name, name + ".exe"} {
			p := filepath.Join(dir, n)
			if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && (runtime.GOOS == "windows" || fi.Mode()&0o111 != 0) {
				return p, nil
			}
		}
	}
	return "", os.ErrNotExist
}
