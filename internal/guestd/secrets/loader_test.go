package secrets

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
)

// The BASH_ENV loader of nix/guest/base/bash-env.sh (DECISIONS I-475), run by
// a real bash against a secrets.env in a temp dir. The copy has the file's
// path replaced; nothing else in it changes.

const loaderSource = "../../../nix/guest/base/bash-env.sh"

func loaderFor(t *testing.T, envPath string) string {
	t.Helper()
	b, err := os.ReadFile(loaderSource)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "/run/repose/secrets.env") {
		t.Fatal("the loader no longer names /run/repose/secrets.env")
	}
	path := filepath.Join(t.TempDir(), "bash-env.sh")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(b), "/run/repose/secrets.env", envPath)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type bashResult struct {
	stdout, stderr string
	code           int
}

// runBash runs `bash <args>` with only PATH-less basics in its environment,
// plus env. An empty PATH proves the loader runs no other program.
func runBash(t *testing.T, loader string, env []string, args ...string) bashResult {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	cmd := exec.Command(bash, args...)
	cmd.Env = append([]string{"BASH_ENV=" + loader, "PATH="}, env...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return bashResult{out.String(), errb.String(), code}
}

// The probe prints what a caller would notice: $? at the start, the
// positional parameters, the shell options and two variables.
const probe = `s=$?; printf '%s|%s|%s|%s|%s|%s\n' "$s" "$*" "$-" "$(shopt -po pipefail)" "${NEW_ONE-<unset>}" "${OLD_ONE-<unset>}"`

func TestLoaderStrictModeAndFileStates(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads an unreadable file")
	}
	dir := t.TempDir()
	envPath := filepath.Join(dir, "secrets.env")
	loader := loaderFor(t, envPath)
	const content = "export REPOSE_SECRETS_GEN=00000000000000aa\n# comment\nexport NEW_ONE='n;1'\nunset OLD_ONE\n"
	inherited := []string{"OLD_ONE=old", "REPOSE_SECRETS_GEN=0000000000000001"}

	cases := []struct {
		name  string
		setup func()
		want  string
	}{
		{"missing", func() { _ = os.Remove(envPath) }, "0|a b|ehuBc|set -o pipefail|<unset>|old\n"},
		{"present", func() { must(t, os.WriteFile(envPath, []byte(content), 0o400)) }, "0|a b|ehuBc|set -o pipefail|n;1|<unset>\n"},
		{"unreadable", func() { must(t, os.Chmod(envPath, 0)) }, "0|a b|ehuBc|set -o pipefail|<unset>|old\n"},
		{"empty", func() { must(t, os.Chmod(envPath, 0o600)); must(t, os.WriteFile(envPath, nil, 0o600)) }, "0|a b|ehuBc|set -o pipefail|<unset>|old\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.setup()
			r := runBash(t, loader, inherited, "-euo", "pipefail", "-c", probe, "bash", "a", "b")
			if r.code != 0 || r.stderr != "" || r.stdout != c.want {
				t.Fatalf("exit %d, stderr %q, stdout %q, want %q", r.code, r.stderr, r.stdout, c.want)
			}
		})
	}
	// set -u alone, and a script file rather than -c.
	must(t, os.Chmod(envPath, 0o600))
	must(t, os.WriteFile(envPath, []byte(content), 0o400))
	script := filepath.Join(dir, "script.sh")
	must(t, os.WriteFile(script, []byte("set -u\n"+probe+"\n"), 0o644))
	r := runBash(t, loader, inherited, script, "x")
	if r.code != 0 || r.stderr != "" || r.stdout != "0|x|huB|set +o pipefail|n;1|<unset>\n" {
		t.Fatalf("script: exit %d, stderr %q, stdout %q", r.code, r.stderr, r.stdout)
	}
}

// A parent that already holds the current generation keeps a value it set on
// purpose for its child; one with an older generation gets the file.
func TestLoaderKeepsAnOverrideWhenTheGenerationIsCurrent(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "secrets.env")
	loader := loaderFor(t, envPath)
	must(t, os.WriteFile(envPath, []byte("export REPOSE_SECRETS_GEN=00000000000000aa\nexport NEW_ONE='file'\n"), 0o600))

	r := runBash(t, loader, []string{"REPOSE_SECRETS_GEN=00000000000000aa", "NEW_ONE=mine"}, "-c", `printf %s "$NEW_ONE"`)
	if r.stdout != "mine" {
		t.Fatalf("current generation: NEW_ONE = %q, want the caller's", r.stdout)
	}
	r = runBash(t, loader, []string{"REPOSE_SECRETS_GEN=0000000000000001", "NEW_ONE=stale"}, "-c", `printf '%s %s' "$NEW_ONE" "$REPOSE_SECRETS_GEN"`)
	if r.stdout != "file 00000000000000aa" {
		t.Fatalf("old generation: got %q", r.stdout)
	}
}

// bash -x traces nothing of the loader, so no value reaches a log.
func TestLoaderUnderXtracePrintsNoValue(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "secrets.env")
	loader := loaderFor(t, envPath)
	must(t, os.WriteFile(envPath, []byte("export REPOSE_SECRETS_GEN=00000000000000aa\nexport NEW_ONE='do-not-print'\n"), 0o600))
	r := runBash(t, loader, nil, "-xeu", "-c", `echo "$-"`)
	if r.stderr != "+ echo ehuxBc\n" || r.stdout != "ehuxBc\n" {
		t.Fatalf("stderr %q, stdout %q", r.stderr, r.stdout)
	}
	r = runBash(t, loader, nil, "-v", "-c", `:`)
	if strings.Contains(r.stderr, "do-not-print") {
		t.Fatalf("bash -v echoed a value: %q", r.stderr)
	}
}

// What an agent does: a long-lived bash (the agent's shell parent) starts,
// guestd writes a new secret and removes one, and the next `bash -c` the
// parent runs sees the change.
func TestLongLivedParentSeesAWriteInItsNextChild(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("OLD_ONE", "old"), secret("KEEP", "k")}))
	loader := loaderFor(t, p.SecretsEnv())

	dir := t.TempDir()
	goFile, outFile := filepath.Join(dir, "go"), filepath.Join(dir, "out")
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	parent := exec.Command(bash, "-euo", "pipefail", "-c", `
		printf '%s %s\n' "${OLD_ONE-<unset>}" "${NEW_ONE-<unset>}" > "$1.parent"
		while [ ! -e "$2" ]; do read -r -t 0.05 <> <(:) || :; done
		bash -c 'printf "%s %s %s\n" "${NEW_ONE-<unset>}" "${OLD_ONE-<unset>}" "$KEEP"' > "$1"
	`, "parent", outFile, goFile)
	parent.Env = []string{"BASH_ENV=" + loader, "PATH=" + os.Getenv("PATH")}
	must(t, parent.Start())
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(outFile + ".parent"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("parent did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	must(t, h.Write(ctx, []*guestdv1.Secret{secret("KEEP", "k"), secret("NEW_ONE", "new")}))
	must(t, os.WriteFile(goFile, nil, 0o600))
	must(t, parent.Wait())

	b, _ := os.ReadFile(outFile + ".parent")
	if string(b) != "old <unset>\n" {
		t.Fatalf("parent started with %q", b)
	}
	b, _ = os.ReadFile(outFile)
	if string(b) != "new <unset> k\n" {
		t.Fatalf("child of the long-lived parent sees %q, want the new secret and not the removed one", b)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
