package config

import (
	"sort"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/menu"
)

// The catalog is what a user picks from and the recipes are how a guest
// installs it: every catalog id has a recipe under the same id, no
// recipe names an id the catalog lacks, and the options agree value for
// value, so an option the api accepts is one the guest takes.
func TestEveryCatalogEntryHasARecipe(t *testing.T) {
	cat, err := menu.Load()
	if err != nil {
		t.Fatal(err)
	}
	recs, err := LoadRecipes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Entries) != len(recs.List) {
		t.Errorf("catalog has %d entries, recipes %d", len(cat.Entries), len(recs.List))
	}
	for _, e := range cat.Entries {
		rec, ok := recs.Get(e.ID)
		if !ok {
			t.Errorf("catalog entry %q has no recipe", e.ID)
			continue
		}
		if len(rec.Options) != len(e.Options) {
			t.Errorf("%s: catalog has %d options, recipe %d", e.ID, len(e.Options), len(rec.Options))
		}
		for _, o := range e.Options {
			ro, ok := rec.Options[o.ID]
			if !ok {
				t.Errorf("%s: recipe lacks option %s", e.ID, o.ID)
				continue
			}
			a, b := append([]string(nil), o.Values...), append([]string(nil), ro.Values...)
			sort.Strings(a)
			sort.Strings(b)
			if strings.Join(a, ",") != strings.Join(b, ",") || o.Default != ro.Default {
				t.Errorf("%s option %s: catalog %v default %s, recipe %v default %s", e.ID, o.ID, o.Values, o.Default, ro.Values, ro.Default)
			}
		}
		// The catalog's kind is the group a user sees; a service in one
		// is a service in the other.
		if (e.Kind == "service") != (rec.Kind == KindService) {
			t.Errorf("%s: catalog kind %s, recipe kind %s", e.ID, e.Kind, rec.Kind)
		}
	}
	for _, rec := range recs.List {
		if _, ok := cat.Entry(rec.ID); !ok {
			t.Errorf("recipe %q has no catalog entry", rec.ID)
		}
	}
}

// Every option value renders every template of its recipe, on both
// architectures, so no choice a user can make fails at apply time.
func TestEveryOptionValueRenders(t *testing.T) {
	recs, err := LoadRecipes()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for i := range recs.List {
		rec := &recs.List[i]
		combos := []map[string]string{rec.Defaults(nil)}
		for name, o := range rec.Options {
			for _, v := range o.Values {
				combos = append(combos, rec.Defaults(map[string]string{name: v}))
			}
		}
		for _, opts := range combos {
			for _, arch := range []string{"amd64", "arm64"} {
				r, err := rec.Render(opts, Env{Arch: arch, Codename: "noble"})
				if err != nil {
					t.Fatalf("%s %v %s: %v", rec.ID, opts, arch, err)
				}
				if strings.Contains(r.Unit+r.Pre+r.Post+r.Ready, "<no value>") {
					t.Fatalf("%s %v: a template left <no value>", rec.ID, opts)
				}
				if rec.Service != nil && !strings.HasPrefix(r.DataDir, "/var/lib/repose/services/"+rec.ID) {
					t.Fatalf("%s: data dir %s is not under /var/lib/repose/services/%s", rec.ID, r.DataDir, rec.ID)
				}
				n++
			}
		}
	}
	if n < 2*25 {
		t.Fatalf("rendered %d combinations, want at least 50 (25 entries on two arches)", n)
	}
}

// A service's unit listens on localhost only: every recipe's unit names
// 127.0.0.1, and none names 0.0.0.0.
func TestServicesListenOnLocalhost(t *testing.T) {
	recs, err := LoadRecipes()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for i := range recs.List {
		rec := &recs.List[i]
		if rec.Service == nil {
			continue
		}
		n++
		r, err := rec.Render(rec.Defaults(nil), Env{Arch: "amd64", Codename: "noble"})
		if err != nil {
			t.Fatal(err)
		}
		body := r.Unit
		for _, f := range r.Files {
			body += f.Content
		}
		if !strings.Contains(body, "127.0.0.1") || strings.Contains(body, "0.0.0.0") {
			t.Errorf("%s: unit and config do not bind 127.0.0.1 only:\n%s", rec.ID, body)
		}
	}
	if n != 7 {
		t.Fatalf("%d service recipes, want 7", n)
	}
}

func TestParseRecipesRefusesABadFile(t *testing.T) {
	for name, body := range map[string]string{
		"unknown kind":   "version: 1\nrecipes:\n  - {id: x, kind: daemon, ready: r}\n",
		"no ready line":  "version: 1\nrecipes:\n  - {id: x, kind: tool}\n",
		"duplicate":      "version: 1\nrecipes:\n  - {id: x, kind: tool, ready: r}\n  - {id: x, kind: tool, ready: r}\n",
		"service no svc": "version: 1\nrecipes:\n  - {id: x, kind: service, ready: r}\n",
		"bad pin":        "version: 1\nrecipes:\n  - id: x\n    kind: tool\n    ready: r\n    check: 'true'\n    download: {name: x, install: binary, amd64: {url: 'https://a', sha256: abc}, arm64: {url: 'https://a', sha256: abc}}\n",
		"bad template":   "version: 1\nrecipes:\n  - {id: x, kind: tool, ready: '{{.nope}}'}\n",
		"wrong version":  "version: 2\nrecipes: []\n",
	} {
		if _, err := ParseRecipes([]byte(body)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}
