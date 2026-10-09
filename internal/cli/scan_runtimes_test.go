package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRubyWant(t *testing.T) {
	for in, want := range map[string]string{
		"3.3.0": "3.3", "ruby-3.3.6": "3.3", "3.4": "3.4", "~> 3.2.2": "3.2", "3.4.0-preview1": "3.4",
		"2.7.8": "2.7", " 3.3.0 ": "3.3",
		">= 3.2": "", "jruby-9.4.5.0": "", "truffleruby-24.1.0": "", "3": "", "latest": "", "": "",
	} {
		if got := rubyWant(in); got != want {
			t.Errorf("rubyWant(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJavaWant(t *testing.T) {
	for in, want := range map[string]string{
		"21": "21", "17.0.9": "17", "1.8": "8", "1.8.0_292": "8", "temurin-17.0.9+9": "17",
		"21.0.1-tem": "21", "openjdk64-17.0.2": "17", "corretto-8.392.08.1": "8",
		"adoptopenjdk-openj9-11.0.20+8": "11", "graalvm-community-21.0.1": "21", "openjdk-25": "25",
		"latest": "", "": "", "1": "", "temurin": "",
	} {
		if got := javaWant(in); got != want {
			t.Errorf("javaWant(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClosestVersion(t *testing.T) {
	for _, c := range []struct {
		want  string
		avail []string
		got   string
	}{
		{"3.3", rubySeries, "3.3"}, {"3.2", rubySeries, "3.3"}, {"2.7", rubySeries, "3.3"},
		{"3.5", rubySeries, "4.0"}, {"4.1", rubySeries, "4.0"},
		{"17", javaMajors, "17"}, {"9", javaMajors, "11"}, {"22", javaMajors, "25"}, {"26", javaMajors, "25"},
	} {
		if got := closestVersion(c.want, c.avail); got != c.got {
			t.Errorf("closestVersion(%q) = %q, want %q", c.want, got, c.got)
		}
	}
}

// Each pin file is read, the first pin of each runtime wins, and a
// version the base lacks says what goes in instead.
func TestScanRuntimePins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".tool-versions"), "ruby 3.2.2\njava temurin-17.0.9+9\n")
	writeFile(t, filepath.Join(dir, ".ruby-version"), "3.4.1\n")
	writeFile(t, filepath.Join(dir, "Gemfile"), "source \"https://rubygems.org\"\nruby \"3.4.1\"\ngem \"rails\"\n")
	writeFile(t, filepath.Join(dir, ".sdkmanrc"), "# sdk env\njava=21.0.1-tem\n")
	sc := scanProject(dir)
	if sc.Ruby == nil || sc.Ruby.Major != "3.3" || sc.Ruby.Source != ".tool-versions" {
		t.Fatalf("ruby = %+v", sc.Ruby)
	}
	if !strings.Contains(sc.Ruby.Note, "ruby 3.2 is not in the machine's nixpkgs (it has 3.3, 3.4, 4.0); ruby_3_3, the closest") {
		t.Fatalf("ruby note = %q", sc.Ruby.Note)
	}
	if sc.Java == nil || sc.Java.Major != "17" || sc.Java.Note != "jdk17_headless goes into the machine's nix profile when its java is another version" {
		t.Fatalf("java = %+v", sc.Java)
	}
	var notes []string
	for _, v := range sc.Versions {
		notes = append(notes, v.Tool+" "+v.Source+": "+v.Note)
	}
	want := []string{
		"ruby .tool-versions: " + sc.Ruby.Note,
		"java .tool-versions: " + sc.Java.Note,
		"ruby .ruby-version: the first pin (.tool-versions) wins",
		"ruby Gemfile ruby: the first pin (.tool-versions) wins",
		"java .sdkmanrc: the first pin (.tool-versions) wins",
	}
	if !reflect.DeepEqual(notes, want) {
		t.Fatalf("versions:\n%s\nwant:\n%s", strings.Join(notes, "\n"), strings.Join(want, "\n"))
	}

	// The carry sends what the guest installs.
	tc := newToolsCarry(nil, sc)
	if tc == nil || tc.Wanted.Ruby != "3.3" || tc.Wanted.Java != "17" {
		t.Fatalf("carry = %+v", tc)
	}
	if !strings.Contains(string(tc.JSON), `"ruby":"3.3","java":"17"`) {
		t.Fatalf("json = %s", tc.JSON)
	}
}

func TestScanRuntimeOnlyFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".java-version"), "1.8\n")
	writeFile(t, filepath.Join(dir, ".ruby-version"), "jruby-9.4.5.0\n")
	sc := scanProject(dir)
	if sc.Java == nil || sc.Java.Major != "8" {
		t.Fatalf("java = %+v", sc.Java)
	}
	if sc.Ruby != nil {
		t.Fatalf("ruby = %+v", sc.Ruby)
	}
	if v := sc.Versions[0]; v.Tool != "ruby" || v.Note != "only MRI ruby is installed; nothing is" {
		t.Fatalf("versions = %+v", sc.Versions)
	}
	// A pin alone is enough for a list.
	if tc := newToolsCarry(nil, sc); tc == nil || tc.Wanted.Java != "8" || len(tc.Wanted.Items) != 0 {
		t.Fatalf("carry = %+v", tc)
	}
}

// The base's list (nix/guest/base/runtime-versions.json, checked against
// its nixpkgs at build time) and the CLI's are the same.
func TestRuntimeVersionsFile(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "nix", "guest", "base", "runtime-versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct{ Ruby, Java []string }
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Ruby, rubySeries) || !reflect.DeepEqual(f.Java, javaMajors) {
		t.Fatalf("runtime-versions.json = %+v; scan_runtimes.go has ruby %v, java %v", f, rubySeries, javaMajors)
	}
}
