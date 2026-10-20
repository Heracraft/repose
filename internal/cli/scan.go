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
	// Machine is the machine the result is about: which kind it is, and
	// what it answered (scanMachine).
	Machine scanMachine
}

// scanMachine is the machine a scan is about. A machine repose runs has
// the base's tool list, which is a promise; a machine the user owns has
// whatever its owner installed, which is nothing repose can assume, so
// the box's own `command -v` sweep is the only honest source there
// (DECISIONS I-812).
type scanMachine struct {
	// Owned marks a machine the user added with `repose machine`.
	Owned bool
	// Name is that machine's name, "" for a machine repose runs.
	Name string
	// Have is the commands the machine answered to, of those it was
	// asked about. Asked, not this map, says whether it answered: a box
	// with none of them answers an empty list.
	Have map[string]bool
	// Asked is whether the machine itself said what it has. False for a
	// machine repose runs, whose base is the answer, and for an owned one
	// that could not be reached, where Why says so.
	Asked bool
	// Why is the sentence `repose scan` prints for an owned machine it
	// could not ask: which failure it was, and whose list stood in.
	Why string
	// ask asks the machine; nil for a machine repose runs and in tests.
	ask scanAsk
}

// scanAsk asks one machine which of names it has on its PATH.
type scanAsk func(ctx context.Context, names []string) (map[string]bool, error)

// has reports whether the machine already has cmd, so nothing offers to
// install it.
func (m scanMachine) has(cmd string) bool {
	if everyBoxCommands[cmd] {
		return true
	}
	if m.Asked {
		return m.Have[cmd]
	}
	return hostedBaseCommands[cmd]
}

// The two reasons a command the scripts run needs no install because the
// machine has it: the base's promise, and the machine's own answer.
const (
	baseSkipReason  = "in the machine's base"
	onMachinePrefix = "already on "
)

func onMachineReason(name string) string { return onMachinePrefix + name }

// alreadyThere is whether a skip reason means the machine has the
// command, which the line naming what was not installed leaves out.
func alreadyThere(why string) bool {
	return why == baseSkipReason || strings.HasPrefix(why, onMachinePrefix)
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

// scanProject scans the checkout at root for a machine repose runs.
func scanProject(root string) *scanResult { return scanProjectOn(root, scanMachine{}) }

// scanProjectOn scans the checkout at root for one machine: a command
// mach has needs no install, and a machine that was not asked leaves the
// hosted base's list standing in.
func scanProjectOn(root string, mach scanMachine) *scanResult {
	s := &scanner{root: root, mach: mach, cand: map[string]*toolItem{}, skip: map[string]string{}, local: map[string]bool{}, pydeps: map[string]bool{}}
	s.run()
	return s.result()
}

type scanner struct {
	root string
	// mach is the machine the commands are decided against.
	mach scanMachine
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
				v.Note = s.noMajorNote()
			case s.res.Node == nil:
				v.Note = "nodejs_" + v.Major + " goes into the machine's nix profile when its node is another major"
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
			v.Note = "pnpm/npm on the machine are the base's"
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

// noMajorNote is what a node pin that names no one major means for this
// machine (">=18" and "^20 || ^22" name none). Nothing is installed
// either way; on a machine the user owns, whether the box has a node of
// its own is the difference between the project running and not, so the
// note says which it is rather than leaving the project with no runtime
// and nothing said.
func (s *scanner) noMajorNote() string {
	if !s.mach.Owned {
		return "pins no one major; the machine's node is kept"
	}
	note := "pins no one major, so none is installed"
	switch {
	case !s.mach.Asked:
		return note
	case s.mach.Have["node"]:
		return note + "; " + s.mach.Name + " has node"
	default:
		return note + "; " + s.mach.Name + " has no node"
	}
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
	if everyBoxCommands[cmd] {
		return baseSkipReason
	}
	// A machine that answered is the truth about itself; one that did not
	// leaves the hosted base's list. Either way a command the machine has
	// not got falls through to what the project provides itself.
	if s.mach.Asked {
		if s.mach.Have[cmd] {
			return onMachineReason(s.mach.Name)
		}
	} else if hostedBaseCommands[cmd] {
		return baseSkipReason
	}
	switch {
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
	s.res.Machine = s.mach
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

// ScanCmd prints what `repose run` would install on the machine for this
// laptop and the checkout at dir, and why. Nothing is installed. mach is
// the machine that checkout's project is on (scanMachineFor); one the
// user owns is asked what it has, because the base's list is a promise
// about a machine repose runs and about no other.
func ScanCmd(ctx context.Context, out io.Writer, homeDir, dir string, jsonOut bool, personal scanPersonal, mach scanMachine) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if root := gitRepoRoot(abs); root != "" && dir == "." {
		abs = root
	}
	te := toolEnv{Home: homeDir, GOOS: goosForScan, Getenv: os.Getenv, LookPath: lookPathFast}
	laptop := collectGlobalTools(te)
	mach = askMachine(ctx, mach, laptop, abs)
	global := dedupeTools(laptop, mach)
	sc := scanProjectOn(abs, mach)
	prec := precedenceFor(personal.Has, abs)
	if jsonOut {
		return writeJSONOut(out, scanJSON(global, sc, prec, personal))
	}
	printScan(out, abs, global, sc, prec, personal)
	return nil
}

// askMachine asks a machine the user owns which of the commands this
// laptop and this checkout name it already has: one sweep, in one ssh.
// The scan runs twice for it, since the first pass is what there is to
// ask about, and that pass costs a dozen small file reads and no
// process. A machine that does not answer comes back saying so, and the
// hosted base's list stands in.
func askMachine(ctx context.Context, mach scanMachine, laptop []toolItem, root string) scanMachine {
	if mach.ask == nil {
		return mach
	}
	first := scanProjectOn(root, scanMachine{Owned: true, Name: mach.Name, Asked: true})
	have, err := mach.ask(ctx, scanAskNames(laptop, first))
	if err != nil {
		mach.Why = scanNotAsked(mach.Name, silentWords(err))
		return mach
	}
	mach.Asked, mach.Have = true, have
	return mach
}

// scanNotAsked is the sentence for an owned machine the scan could not
// ask: what the silence was, and whose list the listing is instead.
func scanNotAsked(name, words string) string {
	return name + " did not answer (" + words + "), so the commands above are the ones a machine repose runs has."
}

// scanAskNames is every command the machine is asked about: the ones the
// laptop's tools provide, the ones the project's scripts run that the
// project does not provide itself, and node, which decides whether those
// scripts run at all. Sorted, so one checkout asks one question.
func scanAskNames(laptop []toolItem, sc *scanResult) []string {
	set := map[string]bool{"node": true}
	for _, items := range [][]toolItem{laptop, sc.Candidates} {
		for _, it := range items {
			for _, b := range it.Bins {
				if safeBin.MatchString(b) && !everyBoxCommands[b] {
					set[b] = true
				}
			}
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// machineToolsHave asks a machine which of names it has: one
// `command -v` for each in one ssh, and nothing else runs. The sweep is
// the box's own answer, which is the only thing a machine the user owns
// can be asked (the base's list is a promise about a machine repose
// runs). `repose-agent tools plan` answers the same question for the
// list the box already holds; the scan asks about commands it has not
// sent, and about the ones the base's list would otherwise have hidden.
func machineToolsHave(ctx context.Context, t sshTarget, slug string, names []string) (map[string]bool, error) {
	if len(names) == 0 {
		return map[string]bool{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, machineSpaceWait)
	defer cancel()
	out, err := runOnMachine(ctx, t, toolSweepScript(slug, names), nil)
	if err != nil {
		return nil, err
	}
	have := make(map[string]bool, len(names))
	for _, l := range nonEmptyLines(string(out)) {
		have[strings.TrimSpace(l)] = true
	}
	return have, nil
}

// toolSweepScript prints the names the machine has, one per line. The
// PATH it looks at is the project's own profile first — that is where a
// run installs what the project needs (cmd/repose-agent/tools.go) — then
// the directories a login shell adds that a plain `ssh box cmd` does
// not, then the box's own.
func toolSweepScript(slug string, names []string) string {
	var b strings.Builder
	b.WriteString(checkoutVar(slug, ""))
	b.WriteString(`PATH="$repose_co/.repose/profile/bin:$HOME/.nix-profile/bin:$HOME/.local/bin:$HOME/go/bin:$HOME/.cargo/bin:$PATH"` + "\n")
	b.WriteString("for c in")
	for _, n := range names {
		b.WriteString(" " + shQuote(n))
	}
	b.WriteString("; do command -v \"$c\" >/dev/null 2>&1 && printf '%s\\n' \"$c\"; done\ntrue\n")
	return b.String()
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
	m := sc.Machine
	return map[string]any{"laptop": conv(global), "project": conv(sc.Candidates), "node": major(sc.Node),
		"ruby": major(sc.Ruby), "java": major(sc.Java),
		// Which machine the two lists are about, and whether it answered
		// for itself: on a machine the user owns nothing else did.
		"machine": map[string]any{"name": m.Name, "owned": m.Owned, "asked": m.Asked},
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
		how := "nixpkgs, looked up on the machine"
		if c.Manager == "npm" {
			how = "nixpkgs, else npm " + c.Pkg
		}
		if sc.Machine.Asked {
			// The machine answered for itself, so the line says what it
			// said and not how a guest's installer would have found it.
			how = "not on " + sc.Machine.Name
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
			if alreadyThere(s.Why) {
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
	if m := sc.Machine; m.Owned {
		p("\n%s\n", ownedScanTail(n, m))
	} else {
		p("\n%d to check on the machine; each one it lacks is installed in the background after `repose run`.\n", n)
	}
	if tc != nil && !sc.Machine.Owned {
		var pins []string
		for _, r := range []struct{ tool, v string }{{"node", tc.Wanted.Node}, {"ruby", tc.Wanted.Ruby}, {"java", tc.Wanted.Java}} {
			if r.v != "" {
				pins = append(pins, r.tool+" "+r.v)
			}
		}
		if len(pins) > 0 {
			p("Made the machine's default the same way when it has another version: %s.\n", strings.Join(pins, ", "))
		}
	}
}

// ownedScanTail is the last line for a machine the user owns: what that
// box has not got. It promises no install: what puts a tool on such a
// box is the project's repose.nix, applied there by Nix (I-832), and a
// box that was not asked is told apart from one that has everything.
func ownedScanTail(n int, m scanMachine) string {
	switch {
	case !m.Asked && m.Why != "":
		return fmt.Sprintf("%d to check on %s. %s", n, m.Name, m.Why)
	case !m.Asked:
		return fmt.Sprintf("%d to check on %s.", n, m.Name)
	case n == 0:
		return fmt.Sprintf("%s has every command this project and your laptop name.", m.Name)
	default:
		return fmt.Sprintf("%s has not got %d of them.", m.Name, n)
	}
}

// scanMachineFor is the machine the checkout at dir is on: one the user
// owns when the project map records it there (SPINE.md Contract 2), else
// the machine repose runs. It reads two files on the laptop and connects
// to nothing — the ask it carries is what reaches the box.
func scanMachineFor(env func() (*Env, error), dir string) scanMachine {
	if env == nil {
		return scanMachine{}
	}
	e, err := env()
	if err != nil {
		return scanMachine{}
	}
	// The project is resolved from the directory the scan is about, which
	// `repose scan DIR` may name instead of the working directory.
	at := *e
	if abs, err := filepath.Abs(dir); err == nil {
		at.Cwd = abs
	}
	m, place, ok, err := spinePlace(&at, "", defaultResolveDeps())
	if err != nil || !ok {
		return scanMachine{}
	}
	configPath, err := machinesConfigPath()
	if err != nil {
		return scanMachine{Owned: true, Name: m.Name, Why: scanNotAsked(m.Name, "no ssh config")}
	}
	t := probeTarget(machineTarget(configPath, m))
	ask := func(ctx context.Context, names []string) (map[string]bool, error) {
		return machineToolsHave(ctx, t, place.Slug, names)
	}
	return scanMachine{Owned: true, Name: m.Name, ask: ask}
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
		Short: "List the tools repose run would install on the machine",
		Long: `List what the next repose run sends to the machine's tool installer, and why:
the tools this laptop installed globally (npm, pnpm, bun, go, cargo, uv, pipx),
the commands the checkout's scripts run that neither the machine's base nor the
project's own dependencies provide, and the node, ruby and java versions the
checkout pins, with the version the machine gets (the closest nixpkgs has when
it lacks the pinned one). Nothing is installed and nothing leaves the laptop.
DIR defaults to the current checkout.

A .nix file takes precedence over the scan: with a machine.nix on your account
the laptop's tools are skipped, and with a repose.nix at the checkout root the
scripts' commands are. The listing says which half was skipped and why.

On a machine you own the listing is what that box answered to one sweep of its
own PATH, since the base's list describes a machine repose runs and promises
nothing about yours. A box that does not answer is named, and so is the list
that stands in for it.`,
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
			return ScanCmd(cmd.Context(), cmd.OutOrStdout(), home, dir, jsonOut,
				scanPersonalFor(cmd.Context(), env, home), scanMachineFor(env, dir))
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	return cmd
}
