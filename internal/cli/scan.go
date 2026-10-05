package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// The project scan (DECISIONS I-222): which commands the checkout's own
// scripts run that the guest may lack, and which node, ruby and java it pins. It
// reads a fixed set of small files at the root and in each workspace
// package (package.json and its scripts, Makefile, justfile, Procfile,
// .air.toml, compose files, version files); it never walks the tree, so a
// monorepo costs a handful of reads. The result is deterministic: the
// same checkout gives the same list, byte for byte.

// scanResult is what the scan found.
type scanResult struct {
	// Candidates are commands to install, From "project", Why set.
	Candidates []toolItem
	// Skipped are commands the scripts run that need nothing, with why.
	Skipped []scanSkip
	// Node is the node major the project pins, nil for none.
	Node *scanVersion
	// Ruby and Java are the ruby series and java major the guest makes
	// its default (DECISIONS I-265), nil for none.
	Ruby, Java *scanVersion
	// Versions are the other version files, for `repose scan`: the
	// toolchains that honour them fetch the version themselves.
	Versions []scanVersion
}

type scanSkip struct{ Name, Why string }

type scanVersion struct {
	Tool, Version, Source string
	// Major is the pinned node major, or the ruby series ("3.3") or java
	// major ("21") the guest installs; Note says what happens.
	Major, Note string
}

// npmOnly names commands nixpkgs does not package that npm has, with the
// package that provides them. The guest tries nixpkgs first anyway.
var npmOnly = map[string]string{
	"portless": "portless",
	"vercel":   "vercel",
	"serve":    "serve",
}

// depBins maps packages whose commands are not named after them, so a
// script's `tsc` counts as the project's own typescript.
var depBins = map[string][]string{
	"typescript": {"tsc", "tsserver"}, "@biomejs/biome": {"biome"}, "@playwright/test": {"playwright"},
	"@angular/cli": {"ng"}, "@nestjs/cli": {"nest"}, "@sveltejs/kit": {"svelte-kit"},
	"@tailwindcss/cli": {"tailwindcss"}, "@vue/cli-service": {"vue-cli-service"},
	"storybook": {"storybook", "sb"}, "@storybook/cli": {"storybook", "sb"}, "@changesets/cli": {"changeset"},
	"npm-run-all": {"npm-run-all", "run-p", "run-s"}, "npm-run-all2": {"npm-run-all", "run-p", "run-s"},
	"@swc/cli": {"swc"}, "@lingui/cli": {"lingui"}, "@graphql-codegen/cli": {"graphql-codegen", "gql-gen"},
	"@sentry/cli": {"sentry-cli"}, "@tauri-apps/cli": {"tauri"}, "@remix-run/dev": {"remix"},
	"nuxt": {"nuxi", "nuxt"}, "nuxi": {"nuxi", "nuxt"}, "@expo/cli": {"expo"}, "dotenv-cli": {"dotenv"},
	"cross-env": {"cross-env", "cross-env-shell"}, "concurrently": {"concurrently", "conc"},
	"ts-node": {"ts-node", "ts-node-esm"}, "@hey-api/openapi-ts": {"openapi-ts"}, "@redocly/cli": {"redocly"},
	"@commitlint/cli": {"commitlint"}, "@arethetypeswrong/cli": {"attw"}, "@microsoft/api-extractor": {"api-extractor"},
	"firebase-tools": {"firebase"}, "@vercel/ncc": {"ncc"}, "webpack-cli": {"webpack", "webpack-cli"},
	"@rsbuild/core": {"rsbuild"}, "@rspack/cli": {"rspack"}, "@marp-team/marp-cli": {"marp"},
	"start-server-and-test": {"start-server-and-test", "server-test"}, "@antfu/ni": {"ni", "nr", "nlx"},
	"@sveltejs/package": {"svelte-package"}, "@prisma/cli": {"prisma"}, "wrangler": {"wrangler", "wrangler2"}, "@astrojs/cli": {"astro"},
}

// scanProject scans the checkout at root.
func scanProject(root string) *scanResult {
	s := &scanner{root: root, cand: map[string]*toolItem{}, skip: map[string]string{}, local: map[string]bool{}, pydeps: map[string]bool{}}
	s.run()
	return s.result()
}

type scanner struct {
	root string
	// workspaces are the package dirs (relative, "." first) with their
	// manifests.
	ws []scanWorkspace
	// local are commands the project provides itself: workspace bins,
	// pyproject scripts.
	local map[string]bool
	// pydeps are pyproject.toml's dependencies (they land in .venv/bin).
	pydeps map[string]bool
	cand   map[string]*toolItem
	skip   map[string]string
	res    scanResult
}

type scanWorkspace struct {
	Rel  string
	Pkg  *scanPackage
	Deps map[string]bool
}

type scanPackage struct {
	Name           string            `json:"name"`
	Scripts        map[string]string `json:"scripts"`
	Engines        map[string]string `json:"engines"`
	PackageManager string            `json:"packageManager"`
	Workspaces     json.RawMessage   `json:"workspaces"`
	Bin            json.RawMessage   `json:"bin"`
	Volta          map[string]string `json:"volta"`
	Deps           map[string]string `json:"dependencies"`
	DevDeps        map[string]string `json:"devDependencies"`
	PeerDeps       map[string]string `json:"peerDependencies"`
	OptDeps        map[string]string `json:"optionalDependencies"`
}

func (s *scanner) read(rel string) []byte {
	b, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	return b
}

func (s *scanner) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(s.root, filepath.FromSlash(rel)))
	return err == nil
}

func (s *scanner) readPackage(dir string) *scanPackage {
	b := s.read(joinRel(dir, "package.json"))
	if b == nil {
		return nil
	}
	var p scanPackage
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	return &p
}

func joinRel(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return dir + "/" + name
}

func (s *scanner) run() {
	rootPkg := s.readPackage(".")
	s.ws = append(s.ws, scanWorkspace{Rel: ".", Pkg: rootPkg})
	for _, d := range s.workspaceDirs(rootPkg) {
		if p := s.readPackage(d); p != nil {
			s.ws = append(s.ws, scanWorkspace{Rel: d, Pkg: p})
		}
	}
	for i := range s.ws {
		w := &s.ws[i]
		w.Deps = map[string]bool{}
		if w.Pkg == nil {
			continue
		}
		for _, m := range []map[string]string{w.Pkg.Deps, w.Pkg.DevDeps, w.Pkg.PeerDeps, w.Pkg.OptDeps} {
			for d := range m {
				w.Deps[d] = true
			}
		}
		for _, b := range (nodePackage{Bin: w.Pkg.Bin}).bins(w.Pkg.Name) {
			s.local[b] = true
		}
	}
	s.pyprojectScripts()
	s.versions(rootPkg)
	for _, w := range s.ws {
		s.scanDir(w)
	}
}

// workspaceDirs expands pnpm-workspace.yaml's packages and package.json's
// workspaces into package directories, sorted.
func (s *scanner) workspaceDirs(rootPkg *scanPackage) []string {
	var pats []string
	if b := s.read("pnpm-workspace.yaml"); b != nil {
		pats = append(pats, yamlList(b, "packages")...)
	}
	if rootPkg != nil && len(rootPkg.Workspaces) > 0 {
		var list []string
		if json.Unmarshal(rootPkg.Workspaces, &list) != nil {
			var obj struct {
				Packages []string `json:"packages"`
			}
			_ = json.Unmarshal(rootPkg.Workspaces, &obj)
			list = obj.Packages
		}
		pats = append(pats, list...)
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range pats {
		if strings.HasPrefix(p, "!") {
			continue
		}
		p = strings.TrimSuffix(strings.TrimPrefix(p, "./"), "/")
		for _, d := range s.globDirs(p) {
			if d != "." && !seen[d] && !strings.Contains(d, "node_modules") {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	sort.Strings(out)
	return out
}

// globDirs expands one workspace pattern; "**" matches up to three
// levels, never inside node_modules or a dot directory.
func (s *scanner) globDirs(pat string) []string {
	if !strings.Contains(pat, "**") {
		ms, _ := filepath.Glob(filepath.Join(s.root, filepath.FromSlash(pat)))
		var out []string
		for _, m := range ms {
			if fi, err := os.Stat(m); err == nil && fi.IsDir() {
				if rel, err := filepath.Rel(s.root, m); err == nil {
					out = append(out, filepath.ToSlash(rel))
				}
			}
		}
		return out
	}
	prefix, _, _ := strings.Cut(pat, "**")
	var out []string
	cur := []string{strings.TrimSuffix(prefix, "/")}
	for depth := 0; depth < 3; depth++ {
		var next []string
		for _, d := range cur {
			ents, _ := os.ReadDir(filepath.Join(s.root, filepath.FromSlash(d)))
			for _, e := range ents {
				if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" {
					continue
				}
				rel := joinRel(d, e.Name())
				next = append(next, rel)
				if s.exists(joinRel(rel, "package.json")) {
					out = append(out, rel)
				}
			}
		}
		cur = next
	}
	return out
}

// yamlList reads a top-level `key:` block list from a YAML file, enough
// for pnpm-workspace.yaml.
func yamlList(b []byte, key string) []string {
	var out []string
	in := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		l := sc.Text()
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "-") {
			in = strings.TrimSpace(strings.TrimSuffix(t, ":")) == key && strings.HasSuffix(t, ":")
			continue
		}
		if in && strings.HasPrefix(t, "- ") {
			v := strings.TrimSpace(t[2:])
			if i := strings.Index(v, " #"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
			out = append(out, strings.Trim(v, `"'`))
		}
	}
	return out
}

// pyprojectScripts marks [project.scripts] as the project's own commands
// and its dependencies as provided (they land in .venv/bin).
func (s *scanner) pyprojectScripts() {
	b := s.read("pyproject.toml")
	if b == nil {
		return
	}
	section := ""
	inArray := false
	for _, l := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") && !inArray {
			section = strings.Trim(t, "[] ")
			continue
		}
		switch section {
		case "project.scripts", "tool.poetry.scripts":
			if k, _, ok := strings.Cut(t, "="); ok {
				s.local[strings.Trim(strings.TrimSpace(k), `"`)] = true
			}
		default:
			// The strings of any array: dependencies, optional
			// dependencies, dependency groups ("ruff>=0.5", "pytest").
			opens := strings.Contains(t, "= [") || strings.Contains(t, "=[")
			if !opens && !inArray {
				continue
			}
			for _, m := range pyDep.FindAllStringSubmatch(t, -1) {
				s.pydeps[strings.ToLower(m[1])] = true
			}
			if opens {
				inArray = !strings.Contains(t, "]")
			} else if strings.HasPrefix(t, "]") {
				inArray = false
			}
		}
	}
}

var pyDep = regexp.MustCompile(`"([A-Za-z0-9][A-Za-z0-9._-]*)\s*(?:\[[^\]]*\])?\s*(?:[<>=!~;@ ][^"]*)?"`)

// versions reads the version files at the root.
func (s *scanner) versions(rootPkg *scanPackage) {
	add := func(tool, version, source string) {
		version = strings.TrimSpace(version)
		if version == "" {
			return
		}
		v := scanVersion{Tool: tool, Version: version, Source: source}
		switch tool {
		case "node":
			v.Major = nodeMajor(version)
			switch {
			case v.Major == "":
				v.Note = "pins no one major; the guest's node is kept"
			case s.res.Node == nil:
				v.Note = "nodejs_" + v.Major + " goes into the guest's nix profile when its node is another major"
				n := v
				s.res.Node = &n
			default:
				v.Note = "the first pin (" + s.res.Node.Source + ") wins"
			}
		case "ruby", "java":
			s.runtimePin(&v)
		case "go":
			v.Note = "go fetches it itself (GOTOOLCHAIN)"
		case "rust":
			v.Note = "rustup installs it at the first cargo"
		case "python":
			v.Note = "uv fetches it itself"
		case "packageManager":
			v.Note = "pnpm/npm in the guest are the base's"
		}
		s.res.Versions = append(s.res.Versions, v)
	}
	if b := s.read(".nvmrc"); b != nil {
		add("node", firstLineOf(b), ".nvmrc")
	}
	if b := s.read(".node-version"); b != nil {
		add("node", firstLineOf(b), ".node-version")
	}
	if b := s.read(".tool-versions"); b != nil {
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if len(f) < 2 || strings.HasPrefix(f[0], "#") {
				continue
			}
			tool := map[string]string{"nodejs": "node", "node": "node", "golang": "go", "go": "go", "rust": "rust", "python": "python", "ruby": "ruby", "java": "java"}[f[0]]
			if tool != "" {
				add(tool, f[1], ".tool-versions")
			}
		}
	}
	if rootPkg != nil {
		if v := rootPkg.Volta["node"]; v != "" {
			add("node", v, "package.json volta.node")
		}
		if v := rootPkg.Engines["node"]; v != "" {
			add("node", v, "package.json engines.node")
		}
		if v := rootPkg.PackageManager; v != "" {
			add("packageManager", v, "package.json packageManager")
		}
	}
	if b := s.read("go.mod"); b != nil {
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if len(f) == 2 && (f[0] == "go" || f[0] == "toolchain") {
				add("go", f[1], "go.mod "+f[0])
			}
		}
	}
	if b := s.read("rust-toolchain.toml"); b != nil {
		if m := regexp.MustCompile(`(?m)^\s*channel\s*=\s*"([^"]+)"`).FindSubmatch(b); m != nil {
			add("rust", string(m[1]), "rust-toolchain.toml")
		}
	} else if b := s.read("rust-toolchain"); b != nil {
		add("rust", firstLineOf(b), "rust-toolchain")
	}
	if b := s.read(".python-version"); b != nil {
		add("python", firstLineOf(b), ".python-version")
	}
	s.runtimeFiles(add)
}

func firstLineOf(b []byte) string {
	l, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSpace(l)
}

// nodeMajor is the one major a node version or range pins: "22",
// "v22.3.0", "22.x", "^22.1", "~22", ">=22 <23", "22.1.0 - 22.9". A range
// open at the top (">=18"), an alias ("lts/*") or several majors pin none.
var nodeVer = regexp.MustCompile(`^v?(\d+)(\.[\dxX*]+)*$`)

func nodeMajor(v string) string {
	v = strings.TrimSpace(v)
	if m := nodeVer.FindStringSubmatch(v); m != nil {
		return m[1]
	}
	for _, p := range []string{"^", "~"} {
		if strings.HasPrefix(v, p) && !strings.ContainsAny(v, " |") {
			if m := nodeVer.FindStringSubmatch(v[1:]); m != nil {
				return m[1]
			}
		}
	}
	// ">=22 <23" and ">=22.0.0 <23.0.0"
	if m := regexp.MustCompile(`^>=\s*v?(\d+)(\.\d+)*\s+<\s*v?(\d+)(\.0)*$`).FindStringSubmatch(v); m != nil {
		lo, hi := atoiSafe(m[1]), atoiSafe(m[3])
		if hi == lo+1 {
			return m[1]
		}
	}
	// "22.1.0 - 22.9.0"
	if a, b, ok := strings.Cut(v, " - "); ok {
		ma, mb := nodeMajor(a), nodeMajor(b)
		if ma != "" && ma == mb {
			return ma
		}
	}
	return ""
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' || n > 1e6 {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// scanDir reads one workspace's scripts and runner files.
func (s *scanner) scanDir(w scanWorkspace) {
	dir := w.Rel
	if w.Pkg != nil {
		names := make([]string, 0, len(w.Pkg.Scripts))
		for n := range w.Pkg.Scripts {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			s.commands(w, w.Pkg.Scripts[n], joinRel(dir, "package.json")+" script "+n)
		}
	}
	for _, f := range []string{"Makefile", "makefile", "GNUmakefile"} {
		if b := s.read(joinRel(dir, f)); b != nil {
			s.note(w, "make", joinRel(dir, f))
			for _, r := range makeRecipes(b) {
				s.commands(w, r.line, joinRel(dir, f)+" "+r.target)
			}
			break
		}
	}
	for _, f := range []string{"justfile", "Justfile", ".justfile"} {
		if b := s.read(joinRel(dir, f)); b != nil {
			s.note(w, "just", joinRel(dir, f))
			for _, r := range justRecipes(b) {
				s.commands(w, r.line, joinRel(dir, f)+" "+r.target)
			}
			break
		}
	}
	if b := s.read(joinRel(dir, "Procfile")); b != nil {
		for _, l := range strings.Split(string(b), "\n") {
			if name, cmd, ok := strings.Cut(l, ":"); ok && !strings.HasPrefix(strings.TrimSpace(l), "#") {
				s.commands(w, cmd, joinRel(dir, "Procfile")+" "+strings.TrimSpace(name))
			}
		}
	}
	if s.exists(joinRel(dir, ".air.toml")) {
		s.note(w, "air", joinRel(dir, ".air.toml"))
	}
	for _, f := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		if s.exists(joinRel(dir, f)) {
			s.note(w, "docker", joinRel(dir, f))
			break
		}
	}
}

// commands adds every command a shell line runs.
func (s *scanner) commands(w scanWorkspace, line, why string) {
	for _, c := range shellCommands(line) {
		s.note(w, c, why)
	}
}

// note decides one command.
func (s *scanner) note(w scanWorkspace, cmd, why string) {
	if _, ok := s.cand[cmd]; ok {
		return
	}
	if _, ok := s.skip[cmd]; ok {
		return
	}
	if reason := s.provided(w, cmd); reason != "" {
		s.skip[cmd] = reason
		return
	}
	it := &toolItem{Name: cmd, Bins: []string{cmd}, From: "project", Why: why}
	if pkg, ok := npmOnly[cmd]; ok {
		it.Manager, it.Pkg = "npm", pkg
	}
	s.cand[cmd] = it
}

// provided says why cmd needs no install, "" when it may.
func (s *scanner) provided(w scanWorkspace, cmd string) string {
	switch {
	case baseCommands[cmd]:
		return "in the guest base"
	case s.res.Ruby != nil && rubyBins[cmd]:
		return "comes with " + runtimeAttr("ruby", s.res.Ruby.Major)
	case s.res.Java != nil && javaBins[cmd]:
		return "comes with " + runtimeAttr("java", s.res.Java.Major)
	case s.local[cmd]:
		return "the project's own command"
	case s.pydeps[strings.ToLower(cmd)]:
		return "a Python dependency"
	}
	for _, ws := range s.ws {
		if ws.Pkg != nil {
			// A script named like the command it runs ("stripe": "stripe
			// listen") does not make the command the project's.
			if body, ok := ws.Pkg.Scripts[cmd]; ok && !runsItself(body, cmd) {
				return "a script of the workspace"
			}
		}
	}
	for _, ws := range []scanWorkspace{w, s.ws[0]} {
		for d := range ws.Deps {
			if d == cmd {
				return "a dependency"
			}
			for _, b := range depBins[d] {
				if b == cmd {
					return "from the dependency " + d
				}
			}
		}
		if s.exists(joinRel(ws.Rel, "node_modules/.bin/"+cmd)) {
			return "in node_modules/.bin"
		}
	}
	if s.exists(".venv/bin/" + cmd) {
		return "in .venv/bin"
	}
	return ""
}

func (s *scanner) result() *scanResult {
	names := make([]string, 0, len(s.cand))
	for n := range s.cand {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s.res.Candidates = append(s.res.Candidates, *s.cand[n])
	}
	skips := make([]string, 0, len(s.skip))
	for n := range s.skip {
		skips = append(skips, n)
	}
	sort.Strings(skips)
	for _, n := range skips {
		s.res.Skipped = append(s.res.Skipped, scanSkip{Name: n, Why: s.skip[n]})
	}
	return &s.res
}

// recipe is one line of a Makefile or justfile recipe.
type recipe struct{ target, line string }

// makeRecipes returns the tab-indented recipe lines with their target,
// continuations joined, make's @-+ prefixes dropped.
func makeRecipes(b []byte) []recipe {
	var out []recipe
	target := ""
	lines := strings.Split(strings.ReplaceAll(string(b), "\\\n", " "), "\n")
	for _, l := range lines {
		if strings.HasPrefix(l, "\t") {
			if target == "" {
				continue
			}
			t := strings.TrimLeft(strings.TrimSpace(l), "@-+")
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			out = append(out, recipe{target, t})
			continue
		}
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		target = ""
		if name, rest, ok := strings.Cut(t, ":"); ok && !strings.HasPrefix(rest, "=") && !strings.ContainsAny(name, "=$") {
			target = strings.Fields(name + " x")[0]
			if strings.HasPrefix(target, ".") {
				target = "" // .PHONY and friends
			}
		}
	}
	return out
}

// justRecipes returns each recipe's body lines (a shebang recipe is
// another language and is skipped).
func justRecipes(b []byte) []recipe {
	var out []recipe
	target := ""
	shebang := false
	first := false
	for _, l := range strings.Split(string(b), "\n") {
		if l == "" {
			continue
		}
		if l[0] == ' ' || l[0] == '\t' {
			if target == "" {
				continue
			}
			t := strings.TrimSpace(l)
			if first && strings.HasPrefix(t, "#!") {
				shebang = true
			}
			first = false
			if shebang || t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			out = append(out, recipe{target, strings.TrimLeft(t, "@-")})
			continue
		}
		target, shebang, first = "", false, true
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "set ") || strings.HasPrefix(t, "import ") || strings.HasPrefix(t, "mod ") || strings.HasPrefix(t, "alias ") || strings.HasPrefix(t, "export ") {
			continue
		}
		if name, rest, ok := strings.Cut(t, ":"); ok && !strings.HasPrefix(rest, "=") {
			f := strings.Fields(strings.TrimLeft(name, "@"))
			if len(f) > 0 {
				target = f[0]
			}
		}
	}
	return out
}

// shellCommands returns the command names a shell line runs, in order:
// the first word of every simple command, after variable assignments and
// wrappers (env, cross-env, dotenv --, time, nohup, sudo, exec, pnpm
// exec, npm exec), and the quoted commands concurrently and npm-run-all
// are given. npx and pnpm dlx fetch what they run, so they name nothing.
// Paths, variables and templated words are not commands anyone installs.
func shellCommands(line string) []string {
	var out []string
	for _, simple := range splitSimple(line) {
		out = append(out, simpleCommand(simple)...)
	}
	return out
}

func simpleCommand(words []string) []string {
	i := 0
	for i < len(words) && isAssignment(words[i]) {
		i++
	}
	for i < len(words) {
		w := words[i]
		switch w {
		case "env", "cross-env", "cross-env-shell":
			i++
			for i < len(words) && (isAssignment(words[i]) || strings.HasPrefix(words[i], "-")) {
				i++
			}
			continue
		case "time", "nohup", "sudo", "exec", "command", "nice", "builtin":
			i++
			for i < len(words) && strings.HasPrefix(words[i], "-") {
				i++
			}
			continue
		case "dotenv":
			for i < len(words) && words[i] != "--" {
				i++
			}
			i++
			continue
		case "npx", "bunx", "pnpx":
			return nil
		}
		break
	}
	if i >= len(words) {
		return nil
	}
	w := words[i]
	var out []string
	if isCommandWord(w) {
		out = append(out, w)
	}
	rest := words[i+1:]
	switch w {
	case "pnpm", "npm", "yarn", "bun":
		if len(rest) >= 2 && rest[0] == "exec" {
			out = append(out, simpleCommand(rest[1:])...)
		}
		if len(rest) >= 1 && rest[0] == "dlx" {
			return out
		}
	case "concurrently", "conc":
		// Each argument is a command line; npm-run-all's are script names.
		for j := 0; j < len(rest); j++ {
			a := rest[j]
			if strings.HasPrefix(a, "-") {
				if concurrentlyValueFlags[a] {
					j++
				}
				continue
			}
			out = append(out, shellCommands(a)...)
		}
	}
	return out
}

// concurrentlyValueFlags take the next argument as their value.
var concurrentlyValueFlags = setOf("-n", "--names", "-c", "--prefix-colors", "-p", "--prefix", "-s", "--success",
	"-l", "--prefix-length", "-t", "--timestamp-format", "--restart-tries", "--restart-after", "--max-processes", "-m",
	"--default-input-target", "--name-separator", "--handle-input", "--pad-prefix")

// runsItself reports whether a script's body runs the command it is named
// after.
func runsItself(body, name string) bool {
	for _, c := range shellCommands(body) {
		if c == name {
			return true
		}
	}
	return false
}

func isAssignment(w string) bool {
	k, _, ok := strings.Cut(w, "=")
	return ok && k != "" && regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(k)
}

func isCommandWord(w string) bool {
	if !safeBin.MatchString(w) || strings.Contains(w, "{{") {
		return false
	}
	for _, ext := range []string{".js", ".mjs", ".cjs", ".ts", ".sh", ".py", ".json"} {
		if strings.HasSuffix(w, ext) {
			return false
		}
	}
	return true
}

// splitSimple splits a line into simple commands (on ; & && || | newlines
// and parentheses) of unquoted words. Quoted text stays one word with the
// quotes removed; $( ) and backticks are left inside words.
func splitSimple(line string) [][]string {
	var cmds [][]string
	var words []string
	var cur strings.Builder
	has := false
	flushWord := func() {
		if has {
			words = append(words, cur.String())
			cur.Reset()
			has = false
		}
	}
	flushCmd := func() {
		flushWord()
		if len(words) > 0 {
			cmds = append(cmds, words)
			words = nil
		}
	}
	r := []rune(line)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch c {
		case '\'', '"':
			j := i + 1
			for j < len(r) && r[j] != c {
				if c == '"' && r[j] == '\\' && j+1 < len(r) {
					j++
				}
				j++
			}
			if j > len(r) {
				j = len(r)
			}
			cur.WriteString(string(r[i+1 : min(j, len(r))]))
			has = true
			i = j
		case '\\':
			if i+1 < len(r) {
				cur.WriteRune(r[i+1])
				has = true
				i++
			}
		case '$', '{':
			// $( ), ${ }, make's $( ) and just's {{ }} stay inside the word,
			// which then names no command.
			open, closer := rune(0), rune(0)
			switch {
			case c == '$' && i+1 < len(r) && r[i+1] == '(':
				open, closer = '(', ')'
			case c == '$' && i+1 < len(r) && r[i+1] == '{':
				open, closer = '{', '}'
			case c == '{' && i+1 < len(r) && r[i+1] == '{':
				open, closer = '{', '}'
			}
			if open == 0 {
				if c == '{' {
					flushCmd()
					continue
				}
				cur.WriteRune(c)
				has = true
				continue
			}
			depth := 0
			j := i + 1
			for ; j < len(r); j++ {
				if r[j] == open {
					depth++
				} else if r[j] == closer {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			if c == '{' && j+1 < len(r) && r[j+1] == '}' {
				j++
			}
			cur.WriteString(string(r[i:min(j+1, len(r))]))
			has = true
			i = j
		case ' ', '\t':
			flushWord()
		case ';', '&', '|', '\n', '(', ')', '}':
			flushCmd()
		case '>', '<':
			// a redirection's target is not a command: skip it
			flushWord()
			j := i + 1
			for j < len(r) && (r[j] == '>' || r[j] == '&' || r[j] == ' ') {
				j++
			}
			for j < len(r) && r[j] != ' ' && r[j] != ';' && r[j] != '&' && r[j] != '|' {
				j++
			}
			i = j - 1
		case '#':
			if !has {
				flushCmd()
				return cmds
			}
			cur.WriteRune(c)
		default:
			cur.WriteRune(c)
			has = true
		}
	}
	flushCmd()
	return cmds
}

// ---- repose scan ----

// scanPersonal is what `repose scan` knows of the account's machine.nix
// (DECISIONS I-490): whether there is one, and how it knows.
type scanPersonal struct {
	Has bool
	// Why names the source: "your account has a machine.nix", or the
	// laptop's copy when the account could not be asked.
	Why string
}

// ScanCmd prints what `repose run` would install in a guest for this
// laptop and the checkout at dir, and why. Nothing is installed.
func ScanCmd(out io.Writer, homeDir, dir string, jsonOut bool, personal scanPersonal) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if root := gitRepoRoot(abs); root != "" && dir == "." {
		abs = root
	}
	te := toolEnv{Home: homeDir, GOOS: goosForScan, Getenv: os.Getenv, LookPath: lookPathFast}
	global := readGlobalTools(te)
	sc := scanProject(abs)
	prec := precedenceFor(personal.Has, abs)
	if jsonOut {
		return writeJSONOut(out, scanJSON(global, sc, prec, personal))
	}
	printScan(out, abs, global, sc, prec, personal)
	return nil
}

// skipReasons are the lines `repose scan` gives for each half it leaves
// to a .nix file.
func skipReasons(prec toolPrecedence, personal scanPersonal) (laptop, project string) {
	if prec.SkipGlobals {
		laptop = personal.Why + ", which describes your tools, so none of these is installed"
	}
	if prec.SkipScripts {
		project = "repose.nix at the checkout root describes this project's tools, so the commands its scripts run are not installed"
	}
	return laptop, project
}

// goosForScan is runtime.GOOS; a variable for tests.
var goosForScan = runtime.GOOS

func scanJSON(global []toolItem, sc *scanResult, prec toolPrecedence, personal scanPersonal) any {
	type item struct {
		Name    string   `json:"name"`
		Bins    []string `json:"bins"`
		Manager string   `json:"manager,omitempty"`
		Pkg     string   `json:"pkg,omitempty"`
		Version string   `json:"version,omitempty"`
		From    string   `json:"from"`
		Why     string   `json:"why"`
	}
	conv := func(xs []toolItem) []item {
		out := []item{}
		for _, x := range xs {
			out = append(out, item(x))
		}
		return out
	}
	major := func(v *scanVersion) string {
		if v == nil {
			return ""
		}
		return v.Major
	}
	laptopWhy, projectWhy := skipReasons(prec, personal)
	return map[string]any{"laptop": conv(global), "project": conv(sc.Candidates), "node": major(sc.Node),
		"ruby": major(sc.Ruby), "java": major(sc.Java),
		// What a .nix file takes over (I-490): "" when the half is
		// installed as listed.
		"skipped": map[string]string{"laptop": laptopWhy, "project": projectWhy}}
}

func printScan(out io.Writer, dir string, global []toolItem, sc *scanResult, prec toolPrecedence, personal scanPersonal) {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(out, format, a...) }
	laptopWhy, projectWhy := skipReasons(prec, personal)
	p("Your laptop's tools (%d):\n", len(global))
	if laptopWhy != "" {
		p("  skipped: %s\n", laptopWhy)
	}
	if len(global) == 0 {
		p("  none found\n")
	}
	for _, it := range global {
		v := it.Pkg
		if it.Version != "" {
			v += "@" + it.Version
		}
		p("  %-20s %-6s %s (commands: %s)\n", it.Name, it.Manager, v, strings.Join(it.Bins, ", "))
	}
	p("\nThis project (%s):\n", dir)
	if projectWhy != "" {
		p("  skipped: %s\n", projectWhy)
	}
	if len(sc.Candidates) == 0 {
		p("  no commands to install\n")
	}
	for _, c := range sc.Candidates {
		how := "nixpkgs, looked up in the guest"
		if c.Manager == "npm" {
			how = "nixpkgs, else npm " + c.Pkg
		}
		p("  %-20s %s; from %s\n", c.Name, how, c.Why)
	}
	for _, v := range sc.Versions {
		line := fmt.Sprintf("  %s %s (%s)", v.Tool, v.Version, v.Source)
		if v.Note != "" {
			line += ": " + v.Note
		}
		p("%s\n", line)
	}
	if len(sc.Skipped) > 0 {
		var parts []string
		for _, s := range sc.Skipped {
			if s.Why == "in the guest base" {
				continue
			}
			parts = append(parts, s.Name+" ("+s.Why+")")
		}
		if len(parts) > 0 {
			p("  not installed: %s\n", strings.Join(parts, ", "))
		}
	}
	carried, scanned := global, sc
	if prec.SkipGlobals {
		carried = nil
	}
	if prec.SkipScripts {
		c := *sc
		c.Candidates = nil
		scanned = &c
	}
	tc := newToolsCarry(carried, scanned)
	n := 0
	if tc != nil {
		n = len(tc.Wanted.Items)
	}
	p("\n%d to check in the guest; each one it lacks is installed in the background after `repose run`.\n", n)
	if tc != nil {
		var pins []string
		for _, r := range []struct{ tool, v string }{{"node", tc.Wanted.Node}, {"ruby", tc.Wanted.Ruby}, {"java", tc.Wanted.Java}} {
			if r.v != "" {
				pins = append(pins, r.tool+" "+r.v)
			}
		}
		if len(pins) > 0 {
			p("Made the guest's default the same way when it has another version: %s.\n", strings.Join(pins, ", "))
		}
	}
}

// scanPersonalFor asks the account whether it has a machine.nix, which
// takes the laptop's tools over (DECISIONS I-490). Logged out, or with
// the api out of reach, the laptop's copy stands in, and the line says so.
func scanPersonalFor(ctx context.Context, env func() (*Env, error), homeDir string) scanPersonal {
	local := func(reason string) scanPersonal {
		dir := os.Getenv(envXDGConfigHome)
		if dir == "" {
			dir = filepath.Join(homeDir, ".config")
		}
		b, err := os.ReadFile(filepath.Join(dir, "repose", machineNixFile))
		if err != nil || strings.TrimSpace(string(b)) == "" {
			return scanPersonal{}
		}
		return scanPersonal{Has: true, Why: "~/.config/repose/machine.nix exists (not checked against your account: " + reason + ")"}
	}
	if env == nil {
		return local("not logged in")
	}
	e, err := env()
	if err != nil || e.Client == nil {
		return local("not logged in")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	acct, err := e.Client.GetPersonal(ctx)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == 404 {
			return scanPersonal{}
		}
		return local("the api did not answer")
	}
	if strings.TrimSpace(acct.Fragment) == "" {
		return scanPersonal{}
	}
	return scanPersonal{Has: true, Why: "your account has a machine.nix"}
}

func newScanCmd(env func() (*Env, error)) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "scan [DIR]",
		Short: "Show which of your tools and which project commands `repose run` installs on a machine (dry run)",
		Long: `List what the next ` + "`repose run`" + ` sends to the guest's tool installer, and why:
the tools this laptop installed globally (npm, pnpm, bun, go, cargo, uv,
pipx), and the commands the checkout's scripts run that neither the guest
base nor the project's own dependencies provide, and the node, ruby and
java versions the checkout pins with the version the guest gets (the
closest nixpkgs has when it lacks the pinned one). Nothing is installed and
nothing leaves the laptop. DIR defaults to the current checkout.

A .nix file takes precedence over the scan: with a machine.nix on your
account (` + "`repose config --global`" + `) the laptop's tools are skipped, and with
a repose.nix at the checkout root the scripts' commands are. The listing
says which half was skipped and why.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return cobraUsageError{fmt.Errorf("repose scan takes at most one DIR, got %s", gotArgs(args))}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
				return exitf(ExitUsage, "%s is not a directory.", dir)
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			return ScanCmd(cmd.OutOrStdout(), home, dir, jsonOut, scanPersonalFor(cmd.Context(), env, home))
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	return cmd
}
