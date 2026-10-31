package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// sync.exclude is a [sync] table in TOML; the quoted "sync.exclude" key
// is the spelling older configs used. Both must reach the sync.
func TestLoadConfigSyncExclude(t *testing.T) {
	cases := map[string][]string{
		"[sync]\nexclude = [\"dist\", \"*.mp4\"]\n": {"dist", "*.mp4"},
		"\"sync.exclude\" = [\"dist\"]\n":           {"dist"},
		"default_class = \"small\"\n":               nil,
	}
	for body, want := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(configPath(dir), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := loadConfig(dir)
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if !slices.Equal(cfg.SyncExclude, want) {
			t.Errorf("%q: SyncExclude = %v, want %v", body, cfg.SyncExclude, want)
		}
	}
}

// An unknown default_size is refused when the file is read, as an
// unknown default_multiplexer beside it always was. Without it the
// value went into the create request and came back as an api refusal
// naming something the user never typed on that command (I-922).
func TestConfigRefusesAnUnknownDefaultSize(t *testing.T) {
	for _, c := range []struct{ toml, want string }{
		{"default_size = \"huge\"\n", `default_size is "huge"`},
		{"default_class = \"medium\"\n", `default_class is "medium"`},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(c.toml), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := loadConfig(dir)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("loadConfig(%q) = %v, want an error naming %s", c.toml, err, c.want)
		}
		if !strings.Contains(err.Error(), "small, large or xl") {
			t.Errorf("error %q does not say what it takes", err)
		}
	}
	// Every real size is accepted.
	for _, size := range []string{"small", "large", "xl"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("default_size = \""+size+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if cfg, err := loadConfig(dir); err != nil || cfg.DefaultClass != size {
			t.Fatalf("loadConfig(%s) = %+v %v", size, cfg, err)
		}
	}
}
