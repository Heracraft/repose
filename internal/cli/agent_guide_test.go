package cli

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The machine guide agents in the guest read (DECISIONS I-243) is
// images/guest/agent-guide.md, the Ubuntu base's (I-1492), which
// images/guest/scripts/configure.sh renders into /etc/claude-code/CLAUDE.md
// and the other agents' instruction files. These tests keep it in step
// with the user docs and the guest: every section of the machine, agents
// and limits pages is summarised by a guide line (or listed below with why
// not), every reference names a real page and heading, and every command
// the guide tells an agent to run is on the machine. agent-guide.commands
// is that command list, for a guest test to run `command -v` on.

const (
	guidePath         = "../../images/guest/agent-guide.md"
	guideCommandsPath = "testdata/agent-guide.commands"
	docsDir           = "../../apps/web/src/content/docs"
	// guideCopyPath is PR 2's guide at f99e9726 (ws/nonix-guest-image),
	// read only while images/guest is not on this branch; delete it once
	// it is.
	guideCopyPath = "testdata/agent-guide.md"
	// imageDir holds what the guest is built from; a guestCommands source
	// under it is checked only when it is here.
	imageDir = "../../images/guest"
)

var updateGuideCommands = flag.Bool("update-agent-guide", false, "rewrite internal/cli/testdata/agent-guide.commands")

// guidePages are the pages whose every H2 and H3 the guide must reference:
// what the machine has, what agents get, what is not allowed.
var guidePages = []string{"machine", "agents", "limits"}

// guideSkippedSections are headings of guidePages the guide leaves out on
// purpose, as "page#anchor" with why. A new heading must be summarised in
// the guide or added here.
var guideSkippedSections = map[string]string{
	"agents#permissions":                            "the user's own permission settings, not something the machine offers",
	"agents#what-agents-are-told-about-the-machine": "describes this guide",
	"agents#agent-states":                           "what the user sees of the agent; the guide's turn line covers what the agent does",
	"agents#send-it-a-message":                      "a `repose say` arrives in the agent's own input as the user's next prompt (I-1144); there is nothing for the agent to do about it, and a line narrating it would be help nobody asked for",
	"agents#continue-a-conversation-on-the-machine": "`repose run --session` reads files on the laptop; an agent on the machine cannot bring one and would only find the conversation already there (I-1328)",
	"agents#opencode-version":                       "which opencode the platform pins, and the copy a reader installed under version 1; nothing an agent does (I-1050)",
	"machine#older-machines":                        "machines from before the Ubuntu base; this guide is the Ubuntu base's, and a NixOS machine has its own",
	"limits#projects":                               "account limits on the number of projects, not the machine",
	"limits#when-repose-is-full":                    "the seats waitlist gates choosing a plan (I-290); an agent on a machine is past it",
	"limits#ssh-connections":                        "the gateway limits connections from laptops into machines; an agent on the machine makes none",
}

// guestCommands is every command the guide may tell an agent to run, with
// where the guest gets it: "file:word" means the word must appear in that
// file (the Dockerfile's package lists, the agents' install script),
// "system:why" is a command the Ubuntu 24.04 base has.
var guestCommands = map[string]string{
	"sudo":    "images/guest/rootfs/etc/sudoers.d/repose:dev",
	"sh":      "system:Ubuntu's /bin/sh (dash)",
	"bash":    "system:Ubuntu's bash (the login shell)",
	"[":       "system:a bash builtin (and coreutils)",
	"grep":    "system:Ubuntu's base (grep)",
	"df":      "system:Ubuntu's base (coreutils)",
	"nproc":   "system:Ubuntu's base (coreutils); the guide's cores line (I-1326)",
	"dmesg":   "system:Ubuntu's base (util-linux)",
	"ss":      "system:Ubuntu's base (iproute2); auto-forward reads it too",
	"fstrim":  "system:Ubuntu's base (util-linux); fstrim.timer runs it daily (I-585)",
	"python3": "system:Ubuntu 24.04's Python 3.12",
	"nohup":   "system:Ubuntu's base (coreutils)",
	"pkill":   "system:Ubuntu's base (procps)",
	// Scheduled jobs are user timers (I-518).
	"systemctl": "system:systemd, Ubuntu's init",
	"curl":      "images/guest/Dockerfile:curl",
	"jq":        "images/guest/Dockerfile:jq",
	"git":       "images/guest/Dockerfile:git",
	"gh":        "images/guest/Dockerfile:gh",
	"npm":       "images/guest/Dockerfile:nodejs",
	"npx":       "images/guest/Dockerfile:nodejs",
	"go":        "images/guest/Dockerfile:go",
	"uv":        "images/guest/Dockerfile:uv",
	"rustup":    "images/guest/Dockerfile:rustup",
	"docker":    "images/guest/Dockerfile:docker-ce",
	// The quick tunnel for an owner who is away (I-1246).
	"cloudflared": "images/guest/Dockerfile:cloudflared",
	"claude":      "images/guest/scripts/install-agents.sh:claude",
	"codex":       "images/guest/scripts/install-agents.sh:codex",
	"opencode":    "images/guest/scripts/install-agents.sh:opencode",
	"gemini":      "images/guest/scripts/install-agents.sh:gemini",
	"pi":          "images/guest/scripts/install-agents.sh:pi",
}

// notCommands are backticked words in the guide that are names, not
// commands: MCP servers, menu entries, the user, a .envrc line.
var notCommands = map[string]string{
	"dev":                  "the user",
	"localhost":            "an address",
	"0.0.0.0":              "an address",
	"host.docker.internal": "a host name",
	"playwright":           "an MCP server name (the command is playwright-mcp)",
	"chrome-devtools":      "an MCP server name",
	"redis":                "a menu entry",
	"mysql":                "a menu entry",
	"attached":             "a word repose-presence prints",
	"away":                 "a word repose-presence prints",
	"env_vars":             "a Codex config key",
	"bearer_token_env_var": "a Codex config key",
}

type guideLine struct {
	n     int
	text  string // without comments
	refs  []string
	needs []string
}

var (
	guideRefRE   = regexp.MustCompile(`<!--\s*(/docs/[^\s>]*)\s*-->`)
	guideNeedsRE = regexp.MustCompile(`<!--\s*needs:\s*([A-Za-z0-9._-]+)\s*-->`)
	guideCmtRE   = regexp.MustCompile(`\s*<!--.*?-->`)
)

// readGuide returns the guide's lines as agent-guide.nix renders them
// (comments dropped), with each line's references and needs, skipping the
// leading maintainers' comment. Headings and blank lines are left out.
func readGuide(t *testing.T) []guideLine {
	t.Helper()
	b, err := os.ReadFile(guidePath)
	if errors.Is(err, os.ErrNotExist) {
		t.Logf("%s is not on this branch; reading the copy in %s", guidePath, guideCopyPath)
		b, err = os.ReadFile(guideCopyPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []guideLine
	inComment := false
	for i, raw := range strings.Split(string(b), "\n") {
		if inComment {
			inComment = !strings.Contains(raw, "-->")
			continue
		}
		if strings.HasPrefix(raw, "<!--") && !strings.Contains(raw, "-->") {
			inComment = true
			continue
		}
		text := strings.TrimSpace(guideCmtRE.ReplaceAllString(raw, ""))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		l := guideLine{n: i + 1, text: text}
		for _, m := range guideRefRE.FindAllStringSubmatch(raw, -1) {
			l.refs = append(l.refs, m[1])
		}
		for _, m := range guideNeedsRE.FindAllStringSubmatch(raw, -1) {
			l.needs = append(l.needs, m[1])
		}
		out = append(out, l)
	}
	return out
}

// docSlug is apps/web/src/lib/docs.ts slugify: a heading's anchor.
func docSlug(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`&[a-z#0-9]+;`).ReplaceAllString(s, "")
	s = strings.NewReplacer("'", "", "’", "").Replace(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// docHeadings returns a page's heading anchors (numbered like docs.ts on a
// repeat) and, separately, those of its H2 and H3 headings.
func docHeadings(t *testing.T, page string) (all map[string]bool, sections []string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(docsDir, page+".md"))
	if err != nil {
		t.Fatalf("docs page %s: %v", page, err)
	}
	all = map[string]bool{}
	seen := map[string]int{}
	fence := false
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "```") {
			fence = !fence
			continue
		}
		if fence || !strings.HasPrefix(line, "#") {
			continue
		}
		depth := len(line) - len(strings.TrimLeft(line, "#"))
		id := docSlug(strings.TrimSpace(line[depth:]))
		if n := seen[id]; n > 0 {
			seen[id]++
			id += "-" + strconv.Itoa(n)
		} else {
			seen[id] = 1
		}
		all[id] = true
		if depth == 2 || depth == 3 {
			sections = append(sections, id)
		}
	}
	return all, sections
}

func TestAgentGuideCoversTheMachineDocs(t *testing.T) {
	lines := readGuide(t)
	referenced := map[string]bool{}
	for _, l := range lines {
		for _, r := range l.refs {
			referenced[strings.TrimPrefix(r, "/docs/")] = true
		}
	}
	for _, page := range guidePages {
		_, sections := docHeadings(t, page)
		have := map[string]bool{}
		for _, id := range sections {
			key := page + "#" + id
			have[key] = true
			if !referenced[key] && guideSkippedSections[key] == "" {
				t.Errorf("/docs/%s has no line in the agent guide (images/guest/agent-guide.md): summarise what it means for an agent there with a <!-- /docs/%s --> reference, or add it to guideSkippedSections with why", key, key)
			}
		}
		for key := range guideSkippedSections {
			if strings.HasPrefix(key, page+"#") && !have[key] {
				t.Errorf("guideSkippedSections names %s, which is no longer a section of /docs/%s", key, page)
			}
		}
	}
}

func TestAgentGuideReferencesExist(t *testing.T) {
	pages := map[string]map[string]bool{}
	for _, l := range readGuide(t) {
		if len(l.refs) == 0 {
			t.Errorf("agent-guide.md:%d has no <!-- /docs/PAGE#ANCHOR --> reference: %q", l.n, l.text)
		}
		for _, r := range l.refs {
			page, anchor, _ := strings.Cut(strings.TrimPrefix(r, "/docs/"), "#")
			if pages[page] == nil {
				if _, err := os.Stat(filepath.Join(docsDir, page+".md")); err != nil {
					t.Errorf("agent-guide.md:%d references %s: there is no page %s.md", l.n, r, page)
					pages[page] = map[string]bool{}
					continue
				}
				pages[page], _ = docHeadings(t, page)
			}
			if anchor != "" && !pages[page][anchor] {
				t.Errorf("agent-guide.md:%d references %s: /docs/%s has no heading with that anchor", l.n, r, page)
			}
		}
	}
}

// guideCommands returns the commands each guide line names: the first word
// of every backticked span and of each part of a pipeline or && list, with
// `sudo` looked through. Paths, variables, flags, URLs and notCommands are
// not commands.
func guideCommands(l guideLine) []string {
	var cmds []string
	for _, sp := range backtickSpans(l.text) {
		if notCommands[sp] != "" {
			continue
		}
		for _, part := range regexp.MustCompile(`\||&&|;`).Split(sp, -1) {
			f := strings.Fields(part)
			for len(f) > 0 {
				w := f[0]
				if notCommands[w] != "" || strings.ContainsAny(w[:1], "/~.$-") || strings.Contains(w, ":") || strings.Contains(w, "@") {
					break
				}
				cmds = append(cmds, w)
				if w != "sudo" {
					break
				}
				f = f[1:]
			}
		}
	}
	return cmds
}

func TestAgentGuideCommandsExist(t *testing.T) {
	root := docsRoot()
	var list []string
	seen := map[string]bool{}
	for _, l := range readGuide(t) {
		for _, sp := range backtickSpans(l.text) {
			if _, ok, ghost := commandPath(root, sp); ok && ghost != "" {
				t.Errorf("agent-guide.md:%d: `%s` names %s, which is not a repose command", l.n, sp, ghost)
			}
		}
		for _, c := range guideCommands(l) {
			needed := false
			for _, n := range l.needs {
				needed = needed || n == c
			}
			switch {
			case c == "repose":
				continue // the laptop's CLI, checked above; the guest has none
			case needed:
				// Rendered only on a guest that has it (agent-guide.nix).
			case guestCommands[c] == "":
				t.Errorf("agent-guide.md:%d tells the agent to run %s, which is not in guestCommands; add where the guest gets it, or mark the line <!-- needs: %s -->", l.n, c, c)
				continue
			}
			entry := c
			if needed {
				entry = c + " needs"
			}
			if !seen[entry] {
				seen[entry] = true
				list = append(list, entry)
			}
		}
	}
	_, imageErr := os.Stat(imageDir)
	if imageErr != nil {
		t.Logf("%s is not on this branch; the guestCommands sources are not checked", imageDir)
	}
	for c, src := range guestCommands {
		file, word, _ := strings.Cut(src, ":")
		if file == "system" || imageErr != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("../..", file))
		if err != nil {
			t.Errorf("guestCommands[%s]: %v", c, err)
			continue
		}
		if !regexp.MustCompile(`(^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(word) + `($|[^A-Za-z0-9_-])`).Match(b) {
			t.Errorf("guestCommands[%s]: %s no longer mentions %s; the guest may not have %s any more", c, file, word, c)
		}
	}

	// The list the guest VM test checks with `command -v`.
	sort.Strings(list)
	want := "# Generated by internal/cli/agent_guide_test.go (go test ./internal/cli -run AgentGuide -update-agent-guide).\n" +
		"# Every command images/guest/agent-guide.md names; \"needs\" ones are rendered only where the guest has them.\n" +
		strings.Join(list, "\n") + "\n"
	if *updateGuideCommands {
		if err := os.WriteFile(guideCommandsPath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(guideCommandsPath)
	if err != nil || string(got) != want {
		t.Errorf("%s is stale; run go test ./internal/cli -run AgentGuide -update-agent-guide", guideCommandsPath)
	}
}
