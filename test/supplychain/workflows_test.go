// Package supplychain checks the CI workflows, the agent-bump scripts and
// install.sh against the rules in DECISIONS I-428..I-430: actions pinned by
// commit, no write token where downloaded code runs, and a CLI release that
// install.sh trusts only when its checksums are signed.
package supplychain

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

type step struct {
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]any    `yaml:"with"`
	Env  map[string]string `yaml:"env"`
}

type job struct {
	Permissions any    `yaml:"permissions"`
	Environment any    `yaml:"environment"`
	Steps       []step `yaml:"steps"`
}

type workflow struct {
	Permissions any            `yaml:"permissions"`
	Jobs        map[string]job `yaml:"jobs"`
}

func loadWorkflows(t *testing.T) map[string]workflow {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), ".github/workflows/*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows found: %v", err)
	}
	out := map[string]workflow{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var w workflow
		if err := yaml.Unmarshal(b, &w); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out[filepath.Base(f)] = w
	}
	return out
}

// writes reports whether a permissions value grants any write scope.
func writes(p any) bool {
	switch v := p.(type) {
	case string:
		return v == "write-all"
	case map[string]any:
		for _, x := range v {
			if x == "write" {
				return true
			}
		}
	}
	return false
}

var pinned = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40}$`)

func TestActionsArePinnedByCommit(t *testing.T) {
	for name, w := range loadWorkflows(t) {
		for jn, j := range w.Jobs {
			for _, s := range j.Steps {
				if s.Uses == "" || strings.HasPrefix(s.Uses, "./") {
					continue
				}
				if !pinned.MatchString(s.Uses) {
					t.Errorf("%s job %s: %q is not pinned to a commit", name, jn, s.Uses)
				}
			}
		}
	}
}

func TestToolsHaveExactVersions(t *testing.T) {
	exact := regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)
	for name, w := range loadWorkflows(t) {
		for jn, j := range w.Jobs {
			for _, s := range j.Steps {
				if strings.Contains(s.Run, "@latest") {
					t.Errorf("%s job %s: a run step installs @latest", name, jn)
				}
				if strings.Contains(s.Run, "nix profile install nixpkgs#") {
					t.Errorf("%s job %s: nixpkgs from the registry, not the flake lock", name, jn)
				}
				if strings.Contains(s.Uses, "goreleaser-action") || strings.Contains(s.Uses, "buf-action") {
					v, _ := s.With["version"].(string)
					if !exact.MatchString(v) {
						t.Errorf("%s job %s: %s runs version %q, want an exact version", name, jn, s.Uses, v)
					}
				}
			}
		}
	}
}

func TestNoWorkflowGrantsWriteToEveryJob(t *testing.T) {
	for name, w := range loadWorkflows(t) {
		if writes(w.Permissions) {
			t.Errorf("%s: workflow-level permissions grant write; grant it to the job that needs it", name)
		}
		if m, ok := w.Permissions.(map[string]any); ok && m["id-token"] != nil {
			t.Errorf("%s: id-token at workflow level; grant it to the job that needs it", name)
		}
	}
}

// The job that downloads and runs agent binaries holds no write token and
// keeps no credentials in .git; the job that can write runs nothing
// downloaded.
func TestBumpAgentsRunsDownloadsWithoutWriteAccess(t *testing.T) {
	w, ok := loadWorkflows(t)["bump-agents.yml"]
	if !ok {
		t.Fatal("bump-agents.yml missing")
	}
	sawBuild, sawPR := false, false
	for jn, j := range w.Jobs {
		runsDownloads := false
		for _, s := range j.Steps {
			if strings.Contains(s.Run, "scripts/bump-agents.sh") || strings.Contains(s.Run, "nix build") ||
				strings.Contains(s.Uses, "install-nix-action") {
				runsDownloads = true
			}
		}
		if runsDownloads {
			sawBuild = true
			if j.Permissions == nil || writes(j.Permissions) {
				t.Errorf("job %s runs downloaded agents with permissions %v", jn, j.Permissions)
			}
			for _, s := range j.Steps {
				if strings.Contains(s.Uses, "actions/checkout") && s.With["persist-credentials"] != false {
					t.Errorf("job %s: checkout keeps credentials in .git", jn)
				}
				for k, v := range s.Env {
					if strings.Contains(v, "secrets.") {
						t.Errorf("job %s: step env %s reads a secret", jn, k)
					}
				}
			}
		}
		if writes(j.Permissions) {
			sawPR = true
			if runsDownloads {
				t.Errorf("job %s has write access and runs downloaded agents", jn)
			}
		}
	}
	if !sawBuild || !sawPR {
		t.Errorf("want a read-only build job and a separate write job; build=%v pr=%v", sawBuild, sawPR)
	}
	sh, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts/bump-agents.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sh), `env -i HOME="$(mktemp -d)" PATH="$PATH" "$out/bin/$b" --version`) {
		t.Error("bump-agents.sh no longer runs the agents under env -i")
	}
	if strings.Contains(string(sh), "git -C \"$root\" push") || strings.Contains(string(sh), "gh pr create") {
		t.Error("bump-agents.sh pushes or opens a PR; that belongs to bump-agents-pr.sh")
	}
}

// A release is drafted by GoReleaser and published only by the job that
// holds the signing key and signs checksums.txt.
func TestReleaseIsSignedBeforePublishing(t *testing.T) {
	w, ok := loadWorkflows(t)["release.yml"]
	if !ok {
		t.Fatal("release.yml missing")
	}
	var signs, publishes string
	for jn, j := range w.Jobs {
		for _, s := range j.Steps {
			for _, v := range s.Env {
				if strings.Contains(v, "secrets.REPOSE_RELEASE_SIGNING_KEY") {
					signs = jn
					if j.Environment != "release" {
						t.Errorf("job %s reads the signing key outside the release environment", jn)
					}
				}
			}
			if strings.Contains(s.Run, "--draft=false") {
				publishes = jn
			}
			if strings.Contains(s.Uses, "goreleaser-action") {
				for k, v := range s.Env {
					if strings.Contains(v, "secrets.") {
						t.Errorf("job %s: GoReleaser sees secret %s", jn, k)
					}
				}
			}
		}
	}
	if signs == "" || signs != publishes {
		t.Errorf("signing job %q, publishing job %q: want the same job", signs, publishes)
	}
	b, err := os.ReadFile(filepath.Join(repoRoot(t), ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Release struct {
			Draft bool `yaml:"draft"`
		} `yaml:"release"`
	}
	if err := yaml.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if !g.Release.Draft {
		t.Error(".goreleaser.yaml publishes directly; want release.draft: true")
	}
}
