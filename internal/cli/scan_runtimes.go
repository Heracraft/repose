package cli

import (
	"regexp"
	"strings"
)

// Ruby and Java pins (DECISIONS I-265). The scan reads .ruby-version, a
// Gemfile's `ruby` line, .java-version, .sdkmanrc and the ruby/java lines
// of .tool-versions (versions() reads that one), resolves each to a
// version the guest base's nixpkgs has, and the tools carry sends it as
// "ruby"/"java" in tools-wanted.json; repose-tools-install puts the
// matching attribute in dev's nix profile, as it does nodejs_<major>.

// The ruby series and java majors the guest base's nixpkgs has, as the
// attributes repose-tools-install adds (ruby_<x>_<y>, jdk<n>_headless).
// nix/guest/base/runtime-versions.json is the same list: the base's build
// fails when one of them is not in its nixpkgs, and
// TestRuntimeVersionsFile fails when the two lists differ. Oldest first.
var (
	rubySeries = []string{"3.3", "3.4", "4.0"}
	javaMajors = []string{"8", "11", "17", "21", "25"}
)

// rubyBins and javaBins are the commands the pinned runtime brings, so a
// script's `bundle exec` or `java -jar` is not a tool of its own.
var (
	rubyBins = setOf("ruby", "gem", "bundle", "bundler", "irb", "rake", "erb", "rdoc", "ri", "racc")
	javaBins = setOf("java", "javac", "jar", "jshell", "javadoc", "javap", "jlink", "jpackage", "keytool", "jcmd", "jstack")
)

// runtimeFiles reads the ruby and java version files at the root, in the
// order their pins win.
func (s *scanner) runtimeFiles(add func(tool, version, source string)) {
	if b := s.read(".ruby-version"); b != nil {
		add("ruby", firstLineOf(b), ".ruby-version")
	}
	if b := s.read("Gemfile"); b != nil {
		if m := gemfileRuby.FindSubmatch(b); m != nil {
			add("ruby", string(m[1]), "Gemfile ruby")
		}
	}
	if b := s.read(".java-version"); b != nil {
		add("java", firstLineOf(b), ".java-version")
	}
	if b := s.read(".sdkmanrc"); b != nil {
		for _, l := range strings.Split(string(b), "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok && strings.TrimSpace(k) == "java" {
				add("java", v, ".sdkmanrc")
			}
		}
	}
}

// gemfileRuby is a Gemfile's `ruby "3.3.0"` (or `ruby '~> 3.3'`) line.
var gemfileRuby = regexp.MustCompile(`(?m)^[ \t]*ruby[ \t]+["']([^"']+)["']`)

// runtimeAttr is the nixpkgs attribute for a ruby series or java major.
func runtimeAttr(tool, v string) string {
	if tool == "ruby" {
		return "ruby_" + strings.ReplaceAll(v, ".", "_")
	}
	return "jdk" + v + "_headless"
}

// runtimePin resolves a ruby or java pin to what the guest installs: the
// same series or major when the base's nixpkgs has it, else the oldest
// one newer than it, else the newest. The first pin of each tool wins.
func (s *scanner) runtimePin(v *scanVersion) {
	cur, want, avail := &s.res.Ruby, rubyWant(v.Version), rubySeries
	if v.Tool == "java" {
		cur, want, avail = &s.res.Java, javaWant(v.Version), javaMajors
	}
	switch {
	case want == "" && v.Tool == "ruby" && !mriRuby(v.Version):
		v.Note = "only MRI ruby is installed; nothing is"
		return
	case want == "":
		v.Note = "pins no one version; nothing is installed"
		return
	case *cur != nil:
		v.Note = "the first pin (" + (*cur).Source + ") wins"
		return
	}
	got := closestVersion(want, avail)
	v.Major = got
	attr := runtimeAttr(v.Tool, got)
	if got == want {
		v.Note = attr + " goes into the machine's nix profile when its " + v.Tool + " is another version"
	} else {
		v.Note = v.Tool + " " + want + " is not in the machine's nixpkgs (it has " + strings.Join(avail, ", ") +
			"); " + attr + ", the closest, goes into the machine's nix profile instead"
	}
	n := *v
	*cur = &n
}

// rubyVer matches the MRI versions a pin names: "3.3.0", "ruby-3.3.0",
// "3.3", "3.4.0-preview1".
var rubyVer = regexp.MustCompile(`^(?:ruby-)?(\d+)\.(\d+)(?:[.-][0-9A-Za-z.-]*)?$`)

// rubyWant is the series ("3.3") a pin names, "~> 3.3.1" included. A
// range (">= 3.2") or another ruby (jruby, truffleruby) names none.
func rubyWant(v string) string {
	v = strings.TrimSpace(v)
	if rest, ok := strings.CutPrefix(v, "~>"); ok {
		v = strings.TrimSpace(rest)
	}
	if m := rubyVer.FindStringSubmatch(v); m != nil {
		return strings.TrimLeft(m[1], "0") + "." + m[2]
	}
	return ""
}

func mriRuby(v string) bool {
	for _, p := range []string{"jruby", "truffleruby", "mruby", "rbx"} {
		if strings.HasPrefix(strings.TrimSpace(v), p) {
			return false
		}
	}
	return true
}

// javaVer finds the version in a pin: the first number at the start or
// after a dash or underscore.
var javaVer = regexp.MustCompile(`(?:^|[-_])(\d+)(?:\.(\d+))?`)

// javaWant is the major a pin names: "21", "17.0.9", "1.8", "1.8.0_292",
// "temurin-17.0.9+9", "21.0.1-tem", "openjdk64-17.0.2",
// "corretto-8.392.08.1".
func javaWant(v string) string {
	m := javaVer.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return ""
	}
	major := m[1]
	if major == "1" && m[2] != "" {
		major = m[2]
	}
	n := atoiSafe(major)
	if n < 6 || n > 99 {
		return ""
	}
	return strings.TrimLeft(major, "0")
}

// closestVersion is want when avail has it, else the oldest newer one,
// else the newest. avail is oldest first.
func closestVersion(want string, avail []string) string {
	for _, a := range avail {
		if compareVersions(a, want) >= 0 {
			return a
		}
	}
	return avail[len(avail)-1]
}

// compareVersions compares dotted numbers.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x = atoiSafe(as[i])
		}
		if i < len(bs) {
			y = atoiSafe(bs[i])
		}
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}
