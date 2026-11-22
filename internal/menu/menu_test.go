package menu

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/hostd/nixbuild"
)

func load(t *testing.T) *Catalog {
	t.Helper()
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The Go allowlist is a copy of nix/guest/system-allowlist.json, which the
// composer reads at evaluation; the two must not drift.
func TestAllowlistMatchesNix(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "nix", "guest", "system-allowlist.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Allowed []string `json:"allowed"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Allowed, SystemAllowlist) {
		t.Fatalf("allowlists differ:\n nix %v\n go  %v", f.Allowed, SystemAllowlist)
	}
	for _, p := range SystemAllowlist {
		for _, forbidden := range []string{"networking.", "users.", "boot.", "virtualisation.", "services.openssh"} {
			if strings.HasPrefix(p, forbidden) {
				t.Fatalf("%q is on the allowlist; SECURITY.md forbids %s", p, forbidden)
			}
		}
	}
}

// Every catalog entry renders on its own, validates, and round-trips
// through the generated header.
func TestEveryEntryRendersAndRoundTrips(t *testing.T) {
	c := load(t)
	if len(c.Entries) < 15 {
		t.Fatalf("catalog has only %d entries", len(c.Entries))
	}
	for _, e := range c.Entries {
		t.Run(e.ID, func(t *testing.T) {
			sel := Selection{{ID: e.ID}}
			if err := c.Validate(sel); err != nil {
				t.Fatal(err)
			}
			frag, err := c.Render(sel)
			if err != nil {
				t.Fatal(err)
			}
			if !IsGenerated(frag) || !strings.Contains(frag, "# "+e.ID+" ("+e.Group+")") {
				t.Fatalf("rendered fragment:\n%s", frag)
			}
			if e.NixOS != "" && !strings.Contains(frag, "repose.system = [") {
				t.Fatalf("service without repose.system:\n%s", frag)
			}
			if e.NixOS == "" && strings.Contains(frag, "repose.system") {
				t.Fatalf("package with repose.system:\n%s", frag)
			}
			if strings.Contains(frag, "{{") {
				t.Fatalf("unrendered template:\n%s", frag)
			}
			back, ok := ParseGenerated(frag)
			if !ok {
				t.Fatal("ParseGenerated failed")
			}
			want, _ := c.Normalize(sel)
			if !reflect.DeepEqual(back, want) {
				t.Fatalf("round trip: got %v want %v", back, want)
			}
			// Every option value renders too.
			for _, o := range e.Options {
				for _, v := range o.Values {
					if _, err := c.Render(Selection{{ID: e.ID, Options: map[string]string{o.ID: v}}}); err != nil {
						t.Fatalf("option %s=%s: %v", o.ID, v, err)
					}
				}
			}
		})
	}
}

func TestPublicShape(t *testing.T) {
	c := load(t)
	pub := c.Public()
	b, err := json.Marshal(pub[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"id"`, `"label"`, `"group"`, `"kind"`, `"description"`} {
		if !strings.Contains(string(b), k) {
			t.Fatalf("public entry lacks %s: %s", k, b)
		}
	}
	if strings.Contains(string(b), "pkgs.") || strings.Contains(string(b), `"hm"`) {
		t.Fatalf("public entry leaks snippets: %s", b)
	}
	pg, _ := c.Entry("postgresql")
	pb, _ := json.Marshal(c.Public()[indexOf(c, "postgresql")])
	if !strings.Contains(string(pb), `"options":[{"id":"version","type":"enum","values":["15","16","17"],"default":"16"}]`) {
		t.Fatalf("postgresql public entry: %s (entry %+v)", pb, pg)
	}
}

func indexOf(c *Catalog, id string) int {
	for i, e := range c.Entries {
		if e.ID == id {
			return i
		}
	}
	return -1
}

func TestValidateRejects(t *testing.T) {
	c := load(t)
	cases := []struct {
		sel  Selection
		want string
	}{
		{Selection{{ID: "nope"}}, `unknown catalog id "nope"`},
		{Selection{{ID: "bun"}, {ID: "bun"}}, `catalog id "bun" selected twice`},
		{Selection{{ID: "bun", Options: map[string]string{"version": "1"}}}, `bun has no option "version"`},
		{Selection{{ID: "postgresql", Options: map[string]string{"version": "9"}}}, `postgresql: option version: "9" is not one of [15 16 17]`},
		{Selection{{}}, "a menu item needs an id or a package"},
		{Selection{{ID: "bun", Package: "gcc"}}, "a menu item has either id (with options) or package, not both"},
		{Selection{{Package: "gcc", Options: map[string]string{"version": "1"}}}, "a menu item has either id (with options) or package, not both"},
		{Selection{{Package: "a;b"}}, `"a;b" is not a nixpkgs attribute path (letters, digits, _ - + and dots, at most 200 characters)`},
		{Selection{{Package: "gcc"}, {Package: "gcc"}}, `package "gcc" selected twice`},
		{Selection{{Package: "bun"}}, `"bun" is a catalog id; select it as {"id": "bun"}`},
	}
	for _, cse := range cases {
		err := c.Validate(cse.sel)
		if err == nil {
			t.Fatalf("%v: accepted", cse.sel)
		}
		me, ok := err.(*Error)
		if !ok || me.Code != "invalid" || me.Message != cse.want {
			t.Fatalf("%v: got %v, want invalid: %s", cse.sel, err, cse.want)
		}
	}
}

func TestRenderTwoEntriesWithOptions(t *testing.T) {
	c := load(t)
	frag, err := c.Render(Selection{{ID: "postgresql", Options: map[string]string{"version": "17"}}, {ID: "bun"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		Header + "\n",
		`# repose-menu: [{"id":"bun"},{"id":"postgresql","options":{"version":"17"}}]`,
		"    # bun (runtimes): Bun\n",
		"    # postgresql (databases): PostgreSQL version 17\n",
		"package = pkgs.postgresql_17;",
		"home.packages = [ pkgs.postgresql_17 ];",
		"repose.system = [",
	} {
		if !strings.Contains(frag, want) {
			t.Fatalf("missing %q in:\n%s", want, frag)
		}
	}
	// Catalog order, not selection order.
	if strings.Index(frag, "# bun") > strings.Index(frag, "# postgresql") {
		t.Fatalf("entries not in catalog order:\n%s", frag)
	}
}

// The lint that runs on every catalog load: a service touching the
// firewall is refused (checklist: "rejects a test entry with
// networking.firewall").
func TestLintRejectsForbiddenPrefix(t *testing.T) {
	bad := []byte(`
- id: evil
  recipe: evil
  label: Evil
  group: databases
  kind: service
  description: opens the firewall
  nixos: |
    services.postgresql.enable = true;
    networking.firewall.enable = false;
`)
	_, err := Parse(bad)
	if err == nil || !strings.Contains(err.Error(), `option "networking.firewall.enable" is outside the allowlist`) {
		t.Fatalf("lint did not reject: %v", err)
	}
	for _, snippet := range []string{
		`services.openssh.settings.PermitRootLogin = "yes";`,
		`users.users.dev.extraGroups = [ "root" ];`,
		`boot.kernelParams = [ "init=/bin/sh" ];`,
		`virtualisation.docker.enable = false;`,
		"services.redis.servers.dev.enable = true;\n\nsystemd.services.x.script = \"true\";",
	} {
		if err := LintSnippet(snippet); err == nil {
			t.Fatalf("lint accepted %q", snippet)
		}
	}
	// Strings and nested braces do not fool it.
	ok := "services.postgresql = {\n  enable = true;\n  authentication = ''\n    networking.firewall = no;\n  '';\n  settings = { \"log_line_prefix\" = \"networking\"; };\n};\nservices.redis.servers.dev.enable = true; # networking.x = 1\n"
	if err := LintSnippet(ok); err != nil {
		t.Fatalf("lint rejected a fine snippet: %v", err)
	}
	if got := topLevelPaths(ok); !reflect.DeepEqual(got, []string{"services.postgresql", "services.redis.servers.dev.enable"}) {
		t.Fatalf("paths: %v", got)
	}
}

func TestCatalogRejectsBadEntries(t *testing.T) {
	cases := []struct{ yaml, want string }{
		{"- id: Bad_ID\n  label: x\n  group: g\n  kind: package\n  description: d\n  hm: 'a = 1;'\n", "is not [a-z]"},
		{"- id: a\n  recipe: a\n  label: x\n  group: g\n  kind: thing\n  description: d\n  hm: 'a = 1;'\n", "kind"},
		{"- id: a\n  recipe: a\n  label: x\n  group: g\n  kind: package\n  description: d\n  hm: 'home.packages = [ pkgs.{{.v}} ];'\n", "map has no entry"},
		{"- id: a\n  recipe: a\n  label: x\n  group: g\n  kind: service\n  description: d\n  hm: 'a = 1;'\n", "needs a nixos snippet"},
		{"- id: a\n  recipe: a\n  label: x\n  group: g\n  kind: package\n  description: d\n  hm: 'a = 1;'\n  options:\n    - id: v\n      type: enum\n      values: ['1']\n      default: '2'\n", "not among values"},
		{"- id: a\n  recipe: a\n  label: x\n  group: g\n  kind: package\n  description: d\n  hm: 'a = 1;'\n- id: a\n  recipe: a\n  label: y\n  group: g\n  kind: package\n  description: d\n  hm: 'a = 1;'\n", "duplicate id"},
	}
	for _, c := range cases {
		_, err := Parse([]byte(c.yaml))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%q: got %v, want %s", c.yaml, err, c.want)
		}
	}
}

// Every rendered fragment parses as Nix, when nix is on PATH.
func TestRenderedFragmentsParse(t *testing.T) {
	if _, err := exec.LookPath("nix-instantiate"); err != nil {
		t.Skip("nix-instantiate not on PATH")
	}
	c := load(t)
	all := Selection{}
	for _, e := range c.Entries {
		all = append(all, Item{ID: e.ID})
	}
	all = append(all, Item{Package: "gcc"}, Item{Package: "python312Packages.black"})
	frag, err := c.Render(all)
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "fragment.nix")
	if err := os.WriteFile(f, []byte(frag), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("nix-instantiate", "--parse", f).CombinedOutput(); err != nil {
		t.Fatalf("nix-instantiate --parse: %v\n%s\n--- fragment ---\n%s", err, out, frag)
	}
}

// The whole catalog composes on the platform base: an eval of guestSystem
// with every entry selected, plus raw packages, through the exact
// restricted command hostd uses. Needs the repository (git) and nix;
// REPOSE_NIX_TESTS=1.
func TestRealNixAllEntriesEvaluate(t *testing.T) {
	c := load(t)
	all := Selection{}
	for _, e := range c.Entries {
		all = append(all, Item{ID: e.ID})
	}
	all = append(all, Item{Package: "gcc"}, Item{Package: "air"}, Item{Package: "python312Packages.black"}, Item{Package: "nodejs_22"})
	frag, err := c.Render(all)
	if err != nil {
		t.Fatal(err)
	}
	out, err := realNixEval(t, frag)
	if err != nil {
		t.Fatalf("nix eval: %v\n%s", err, tail(out, 4000))
	}
	drv := strings.TrimSpace(out)
	drv = drv[strings.LastIndex(drv, "\n")+1:]
	t.Logf("all %d entries evaluate: %s", len(all), drv)
}

// A package nixpkgs does not have fails evaluation with PackageNotFound's
// line as the summary hostd reports (nixbuild.MapEvalError), and a
// non-package attribute with its own line. REPOSE_NIX_TESTS=1.
func TestRealNixMissingPackage(t *testing.T) {
	c := load(t)
	for name, want := range map[string]string{
		"no-such-package-repose": PackageNotFound("no-such-package-repose"),
		"python312Packages":      `nixpkgs attribute "python312Packages" is not a package; search https://search.nixos.org/packages`,
		// Unfree and not on nix/guest/unfree-allowlist.nix: nixpkgs' own refusal.
		"unrar": "Refusing to evaluate package 'unrar-",
	} {
		frag, err := c.Render(Selection{{Package: "gcc"}, {Package: name}})
		if err != nil {
			t.Fatal(err)
		}
		out, err := realNixEval(t, frag)
		if err == nil {
			t.Fatalf("%s: evaluation succeeded", name)
		}
		summary := strings.SplitN(nixbuild.MapEvalError(out).Message, "\n", 2)[0]
		if !strings.HasPrefix(summary, want) {
			t.Fatalf("%s: summary %q, want prefix %q\n%s", name, summary, want, tail(out, 3000))
		}
		t.Logf("%s: %s", name, summary)
	}
}

// realNixEval evaluates guestSystem's drvPath with frag as the fragment,
// the way hostd does (docs/interfaces/nix-build-contract.md).
func realNixEval(t *testing.T, frag string) (string, error) {
	t.Helper()
	if os.Getenv("REPOSE_NIX_TESTS") == "" {
		t.Skip("set REPOSE_NIX_TESTS=1")
	}
	if _, err := exec.LookPath("nix"); err != nil {
		t.Skip("nix not on PATH")
	}
	rootOut, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not in a git checkout")
	}
	root := strings.TrimSpace(string(rootOut))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fragment.nix"), []byte(frag), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := os.ReadFile(filepath.Join(root, "nix", "flake.lock"))
	if err != nil {
		t.Fatal(err)
	}
	var l struct {
		Nodes map[string]struct {
			Locked map[string]any `json:"locked"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(lock, &l); err != nil {
		t.Fatal(err)
	}
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	var uris []string
	for _, n := range l.Nodes {
		if str(n.Locked, "type") == "github" {
			uris = append(uris, "github:"+str(n.Locked, "owner")+"/"+str(n.Locked, "repo")+"/"+str(n.Locked, "rev")+"?narHash="+strings.ReplaceAll(str(n.Locked, "narHash"), "=", "%3D"))
		}
	}
	uris = append(uris, "path:"+dir)
	cmd := exec.Command("nix", "eval", "--raw", "--no-write-lock-file", "--show-trace",
		"--option", "restrict-eval", "true", "--option", "allow-import-from-derivation", "false",
		"--option", "pure-eval", "true", "--option", "eval-cache", "false",
		"--option", "allowed-uris", strings.Join(uris, " "), "--max-call-depth", "10000",
		"--override-input", "fragment", "path:"+dir,
		"git+file://"+root+"?dir=nix#guestSystem.config.system.build.toplevel.drvPath")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// docs/features/config-examples/menu-postgres-and-bun.nix is what the
// renderer produces for that selection, byte for byte, so the example the
// docs show and nix flake check builds is the real output. Regenerate with
// REPOSE_WRITE_EXAMPLE=1 go test ./internal/menu -run TestExampleFragmentIsCurrent.
func TestExampleFragmentIsCurrent(t *testing.T) {
	c := load(t)
	frag, err := c.Render(Selection{{ID: "bun"}, {ID: "postgresql", Options: map[string]string{"version": "16"}}})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join("..", "..", "docs", "features", "config-examples", "menu-postgres-and-bun.nix")
	if os.Getenv("REPOSE_WRITE_EXAMPLE") != "" {
		if err := os.WriteFile(p, []byte(frag), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != frag {
		t.Fatalf("%s is stale; regenerate with REPOSE_WRITE_EXAMPLE=1\n--- want ---\n%s", p, frag)
	}
}

// A package name is only ever a nixpkgs attribute path: nothing that can
// end a Nix string, interpolate, or climb a path gets through.
func TestValidPackage(t *testing.T) {
	for _, ok := range []string{"gcc", "air", "nodejs_22", "python312Packages.black", "nodePackages.typescript", "_1password-cli", "gtk+3", "go-tools", strings.Repeat("a", 200)} {
		if !ValidPackage(ok) {
			t.Errorf("rejected %q", ok)
		}
	}
	for _, bad := range []string{"", "a;b", "${x}", "../x", ".x", "x.", "a..b", `"gcc"`, "'gcc'", "g cc", " gcc", "gcc\n", "gcc\\", "pkgs.gcc; rm", "a/b", "1gcc", "-gcc", "a.1b", "a=b", "(gcc)", "[gcc]", "a{b}", strings.Repeat("a", 201)} {
		if ValidPackage(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestRenderPackages(t *testing.T) {
	c := load(t)
	sel := Selection{{Package: "python312Packages.black"}, {ID: "bun"}, {Package: "gcc"}, {Package: "air"}}
	frag, err := c.Render(sel)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		Header + "\n" + `# repose-menu: [{"id":"bun"},{"package":"air"},{"package":"gcc"},{"package":"python312Packages.black"}]` + "\n{ config, pkgs, lib, ... }:\nlet\n",
		"    # bun (runtimes): Bun\n",
		"    # extra packages from nixpkgs: air, gcc, python312Packages.black\n",
		"        (nixpkg [ \"air\" ])\n        (nixpkg [ \"gcc\" ])\n        (nixpkg [ \"python312Packages\" \"black\" ])\n",
		`throw "nixpkgs has no package \"${name}\"; search https://search.nixos.org/packages"`,
	} {
		if !strings.Contains(frag, want) {
			t.Fatalf("missing %q in:\n%s", want, frag)
		}
	}
	back, ok := ParseGenerated(frag)
	if !ok {
		t.Fatal("ParseGenerated failed")
	}
	want, _ := c.Normalize(sel)
	if !reflect.DeepEqual(back, want) || len(back) != 4 || back[1].Package != "air" {
		t.Fatalf("round trip: got %v want %v", back, want)
	}
	// Rendering the recovered selection gives the same fragment.
	again, err := c.Render(back)
	if err != nil || again != frag {
		t.Fatalf("re-render differs (%v):\n%s", err, again)
	}
	// Packages alone, and nothing at all, are still generated fragments.
	for _, s := range []Selection{{{Package: "gcc"}}, {}} {
		f, err := c.Render(s)
		if err != nil || !IsGenerated(f) {
			t.Fatalf("%v: %v\n%s", s, err, f)
		}
	}
	// A catalog-only selection has no helper, so existing fragments do not change.
	if f, _ := c.Render(Selection{{ID: "bun"}}); strings.Contains(f, "nixpkg") {
		t.Fatalf("helper without packages:\n%s", f)
	}
	// nixPath refuses what ValidPackage refuses, whoever calls it.
	if _, err := nixPath(`a"b`); err == nil {
		t.Fatal(`nixPath accepted a"b`)
	}
	if PackageNotFound("foo") != `nixpkgs has no package "foo"; search https://search.nixos.org/packages` {
		t.Fatal(PackageNotFound("foo"))
	}
}

// The selections nix/guest/checks.nix composes (menu-fixtures/) are what
// the renderer produces, byte for byte. Regenerate with
// REPOSE_WRITE_EXAMPLE=1 go test ./internal/menu -run TestMenuFixturesAreCurrent.
func TestMenuFixturesAreCurrent(t *testing.T) {
	c := load(t)
	for name, sel := range menuFixtures {
		frag, err := c.Render(sel)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join("..", "..", "nix", "guest", "menu-fixtures", name+".nix")
		if os.Getenv("REPOSE_WRITE_EXAMPLE") != "" {
			if err := os.WriteFile(p, []byte(frag), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != frag {
			t.Fatalf("%s is stale; regenerate with REPOSE_WRITE_EXAMPLE=1\n--- want ---\n%s", p, frag)
		}
	}
}

var menuFixtures = map[string]Selection{
	"packages":        {{ID: "bun"}, {Package: "gcc"}, {Package: "air"}, {Package: "python312Packages.black"}},
	"missing-package": {{Package: "gcc"}, {Package: "no-such-package-repose"}},
}
