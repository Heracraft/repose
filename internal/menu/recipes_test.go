package menu

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// recipeFile is guestd's recipe file (PR 4, DECISIONS I-1476). Until it
// is on this branch the test reads the copy in testdata, and says so.
var recipeFile = filepath.Join("..", "guestd", "config", "recipes.yaml")

type recipeDoc struct {
	Version int `yaml:"version"`
	Recipes []struct {
		ID      string `yaml:"id"`
		Options map[string]struct {
			Values  []string `yaml:"values"`
			Default string   `yaml:"default"`
		} `yaml:"options"`
	} `yaml:"recipes"`
}

func loadRecipes(t *testing.T) recipeDoc {
	t.Helper()
	path := recipeFile
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		path = filepath.Join("testdata", "recipes.yaml")
		t.Logf("%s is not on this branch; reading the copy in %s", recipeFile, path)
		b, err = os.ReadFile(path)
	}
	if err != nil {
		t.Fatal(err)
	}
	var d recipeDoc
	if err := yaml.Unmarshal(b, &d); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if d.Version != 1 || len(d.Recipes) == 0 {
		t.Fatalf("%s: version %d with %d recipes", path, d.Version, len(d.Recipes))
	}
	return d
}

// Every catalog entry names a recipe guestd has, and the recipe accepts
// exactly the entry's options: a selection the api takes is one an erofs
// machine can install (DECISIONS I-1486).
func TestEveryEntryNamesARecipe(t *testing.T) {
	c := load(t)
	d := loadRecipes(t)
	byID := map[string]int{}
	for i, r := range d.Recipes {
		byID[r.ID] = i
	}
	for _, e := range c.Entries {
		i, ok := byID[e.Recipe]
		if !ok {
			t.Errorf("%s: recipe %q is not in guestd's recipes", e.ID, e.Recipe)
			continue
		}
		r := d.Recipes[i]
		if len(r.Options) != len(e.Options) {
			t.Errorf("%s: %d options, recipe %s has %d", e.ID, len(e.Options), r.ID, len(r.Options))
		}
		for _, o := range e.Options {
			ro, ok := r.Options[o.ID]
			if !ok {
				t.Errorf("%s: option %s is not in recipe %s", e.ID, o.ID, r.ID)
				continue
			}
			if !reflect.DeepEqual(ro.Values, o.Values) || ro.Default != o.Default {
				t.Errorf("%s: option %s is %v default %s, recipe %s has %v default %s", e.ID, o.ID, o.Values, o.Default, r.ID, ro.Values, ro.Default)
			}
		}
	}
}

func TestCatalogNeedsARecipe(t *testing.T) {
	_, err := Parse([]byte("- id: a\n  label: x\n  group: g\n  kind: package\n  description: d\n  hm: 'a = 1;'\n"))
	if err == nil || !strings.Contains(err.Error(), `recipe ""`) {
		t.Fatalf("got %v, want a missing recipe refused", err)
	}
}

func TestValidAptPackage(t *testing.T) {
	for _, ok := range []string{"ffmpeg", "libpq-dev", "g++", "python3.12-venv", "0ad", strings.Repeat("a", 128)} {
		if !ValidAptPackage(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "FFmpeg", "-rf", ".x", "a b", "a;b", "a$b", "python312Packages.black_x", "a/b", strings.Repeat("a", 129)} {
		if ValidAptPackage(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestValidateForRuntime(t *testing.T) {
	c := load(t)
	apt := Selection{{ID: "postgresql"}, {Package: "python3.12-venv"}}
	nixAttr := Selection{{Package: "python312Packages.black"}}
	if err := c.ValidateFor(RuntimeErofs, apt); err != nil {
		t.Fatalf("erofs refused an apt name: %v", err)
	}
	if err := c.ValidateFor(RuntimeNix, apt); err == nil {
		t.Fatal("nix took python3.12-venv, which is no nixpkgs attribute path")
	}
	if err := c.ValidateFor(RuntimeErofs, nixAttr); err == nil || !strings.Contains(err.Error(), "not an apt package name") {
		t.Fatalf("erofs took a nixpkgs attribute path: %v", err)
	}
	for _, rt := range []string{RuntimeNix, ""} {
		if err := c.ValidateFor(rt, nixAttr); err != nil {
			t.Fatalf("%q refused a nixpkgs attribute path: %v", rt, err)
		}
	}
	if err := c.ValidateFor("docker", nixAttr); err == nil {
		t.Fatal("an unknown runtime validated")
	}
	if err := c.ValidateFor(RuntimeErofs, Selection{{ID: "nope"}}); err == nil {
		t.Fatal("erofs took an unknown catalog id")
	}
	if err := c.ValidateFor(RuntimeErofs, Selection{{Package: "postgresql"}}); err == nil {
		t.Fatal("erofs took a catalog id as a package")
	}
}
