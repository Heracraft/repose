// Package supplychain checks the CI workflows, the agent-bump scripts and
// install.sh against the rules in DECISIONS I-428..I-430: actions pinned by
// commit, no write token where downloaded code runs, and a CLI release that
// install.sh trusts only when its checksums are signed. It also holds the
// checks on CI's own honesty -- that the steps which must run when something
// above them fails still carry their condition (I-1000), and that the
// conductor's dev-tool harnesses are executed rather than merely present
// (I-996).
package supplychain

import (
	"os"
	"os/exec"
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
	Name string            `yaml:"name"`
	If   string            `yaml:"if"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]any    `yaml:"with"`
	Env  map[string]string `yaml:"env"`
}

type job struct {
	If          string `yaml:"if"`
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
	files, err := filepath.Glob(filepath.Join(repoRoot(t), ".github/workflows/*.y*ml"))
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

// ALWAYS_RUN are the steps that must keep running when an earlier step in
// their job has failed, keyed "<workflow> <job> <step name or run>". Each one
// is here because it was SKIPPED for sixteen days and nobody could see it:
// `go test -race` failed on every run of main from 2026-10-17, GitHub skips a
// job's remaining steps once one fails, and a step that never ran reports as
// "not failing" -- which on the job list is indistinguishable from one that
// ran and passed. ci-green's errcheck fix was correct and unverifiable the
// whole time, and the migration rollback, the only thing proving a migration
// can go `down 1` on a seeded database, was not running either (DECISIONS
// I-970, I-1000).
var ALWAYS_RUN = map[string]string{
	"ci.yml go repose-admin db migrate --down 1 on a seeded database":                     "the only proof a migration can roll back; skipped 2026-10-17..2026-11-02",
	"ci.yml go go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0": "lint cannot run without it",
	"ci.yml go golangci-lint run ./...":                                                   "ci-green's errcheck fix was unverifiable for sixteen days",
	"ci.yml ops the decisions index is current and no id is cited without an entry":       "has never run in CI; must not be hidden by a failure above it",
}

func stepKey(wf, jobName string, s step) string {
	label := s.Name
	if label == "" {
		label = strings.TrimSpace(s.Run)
	}
	return wf + " " + jobName + " " + label
}

// TestAlwaysRunStepsStayUnconditioned is a regression lock on the steps in
// ALWAYS_RUN, and a rule that nothing unconditioned may sit behind them. It is
// NOT a guard on the class: it cannot see a whole job skipped by a condition,
// a `needs:` chain on a failed job, a path-filtered workflow that never fires,
// or a `t.Skip` in the suite -- all of which still report green. "The run was
// green" does not mean every check ran (DECISIONS I-1000).
func TestAlwaysRunStepsStayUnconditioned(t *testing.T) {
	const want = "${{ !cancelled() }}"
	wfs := loadWorkflows(t)
	seen := map[string]bool{}

	for wf, w := range wfs {
		for jobName, j := range w.Jobs {
			gated := false
			for i, s := range j.Steps {
				key := stepKey(wf, jobName, s)
				if reason, ok := ALWAYS_RUN[key]; ok {
					seen[key] = true
					switch {
					case s.If == "":
						t.Errorf("%s job %s: step %q carries no condition; it must be %s, or a failure above it hides the step entirely (%s)", wf, jobName, key, want, reason)
					case strings.Contains(s.If, "always()"):
						t.Errorf("%s job %s: step %q uses always(); I-970 rejected it, because it runs on a deliberate cancellation too and turns one cancel into two misleading failures", wf, jobName, key)
					case strings.TrimSpace(s.If) != want:
						t.Errorf("%s job %s: step %q has if: %q, want exactly %s", wf, jobName, key, s.If, want)
					}
				}
				if strings.Contains(s.If, "!cancelled()") {
					gated = true
					continue
				}
				// The fault: a step appended behind a conditioned one with no
				// condition of its own. Any value passes -- failure(), always()
				// and bump-agents' own conditions are not what this is about.
				if gated && strings.TrimSpace(s.If) == "" {
					t.Errorf("%s job %s: step %d (%q) follows a `!cancelled()` step with no condition of its own, so a failure above it skips it silently; condition it or move it ahead of the gated steps (DECISIONS I-1000)", wf, jobName, i, stepKey(wf, jobName, s))
				}
			}
		}
	}

	// A key that names no step must fail: otherwise renaming a step silences
	// its own lock, which is the shape of fault this file exists to refuse.
	for key, reason := range ALWAYS_RUN {
		if !seen[key] {
			t.Errorf("ALWAYS_RUN names %q and no such step exists; it was renamed or removed, and its lock is now vacuous (%s)", key, reason)
		}
	}
}

// UNRUN waives a harness this test will not execute, path to reason. It ships
// empty on purpose: a line here is the thing being guarded against. An entry
// whose harness now passes also fails, so a stale waiver cannot sit in it --
// the same shape as KNOWN_BAD in ops/dev/release-queue-test.
var UNRUN = map[string]string{
	// Fails 6 of 45 on PRISTINE main (b55d17dd), with none of this branch's
	// changes: add_runs_cli_guards, needs_against_stale_local_main,
	// needs_cli_only, needs_embedded_file, needs_go_shared_package and
	// record_prints_the_line_and_the_detail. Surfaced within minutes of this
	// test existing, which is the test working -- nothing had run this
	// harness, so nobody knew. Not diagnosed here and deliberately not
	// papered over: the five `needs_*`/guards failures all run `go list` in
	// the fixture's scratch module, and `record_prints_the_line_and_the_detail`
	// is the one whose stamps changed when `record` stopped using today's
	// date and today's main, so a stale fixture expectation is the first place
	// to look. Remove this line with the fix, not before; the loop below
	// fails if a waived harness starts passing, so it cannot outlive the bug.
	"ops/dev/release-queue-test": "6 of 45 fail on pristine main b55d17dd, undiagnosed (DECISIONS I-996)",
}

// TestDevToolHarnessesRun executes the conductor's own test harnesses. They
// existed and passed and nothing ran them: `git grep` for either name across
// .github/workflows, ops/check.sh and the justfile returned nothing on
// 2026-11-03. ops/dev/closure-equiv.py decides whether a branch touching
// nix/ is charged a deploy target, so the blindness fixed as I-958 shipped
// unnoticed for as long as the file existed, and `ops/dev/closure-equiv-test`
// was written the same night it was found (DECISIONS I-996).
//
// It globs by convention rather than listing, so a harness added later is
// picked up without anyone remembering to register it -- and it deliberately
// does NOT skip when bash, python3 or git is missing. A guard that skips
// reports as a pass, which is the fault this whole branch is about.
func TestDevToolHarnessesRun(t *testing.T) {
	root := repoRoot(t)
	var found []string
	for _, pat := range []string{"ops/dev/*-test", "ops/dev/*-test.sh", "ops/dev/*_test.py", "scripts/*-test*"} {
		m, err := filepath.Glob(filepath.Join(root, pat))
		if err != nil {
			t.Fatalf("glob %s: %v", pat, err)
		}
		found = append(found, m...)
	}
	if len(found) == 0 {
		t.Fatal("no dev-tool harnesses found at all; the globs are wrong, and an empty scan is not a clean tree")
	}

	ran := map[string]bool{}
	for _, path := range found {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		rel = filepath.ToSlash(rel)
		ran[rel] = true
		if reason, ok := UNRUN[rel]; ok {
			t.Logf("%s: not executed (%s)", rel, reason)
			continue
		}
		t.Run(rel, func(t *testing.T) {
			cmd := exec.Command(path)
			cmd.Dir = root
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s failed (%v); it is a harness nothing else runs, so this is the only place it speaks:\n%s", rel, err, out)
			}
		})
	}

	for rel, reason := range UNRUN {
		if !ran[rel] {
			t.Errorf("UNRUN names %q and no such executable harness exists; the waiver is stale (%s)", rel, reason)
			continue
		}
		cmd := exec.Command(filepath.Join(root, rel))
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Errorf("UNRUN waives %q (%s) and it passes; remove the waiver:\n%s", rel, reason, out)
		}
	}
}
