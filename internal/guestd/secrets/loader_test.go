package secrets

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// The BASH_ENV loader of nix/guest/base/bash-env.sh (DECISIONS I-475), run by
// a real bash against a secrets.refresh in a temp dir. The copy has the
// file's path replaced; nothing else in it changes.

const loaderSource = "../../../nix/guest/base/bash-env.sh"

func loaderFor(t *testing.T, refreshPath string) string {
	t.Helper()
	b, err := os.ReadFile(loaderSource)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "/run/repose/secrets.refresh") {
		t.Fatal("the loader no longer names /run/repose/secrets.refresh")
	}
	path := filepath.Join(t.TempDir(), "bash-env.sh")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(b), "/run/repose/secrets.refresh", refreshPath)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// genOf is the generation guestd wrote last.
func genOf(t *testing.T, p sysdep.Paths) string {
	t.Helper()
	first, _, _ := strings.Cut(readEnv(t, p), "\n")
	g, ok := strings.CutPrefix(first, "export REPOSE_ENV_GEN=")
	if !ok || !genRe.MatchString(g) {
		t.Fatalf("secrets.env first line = %q", first)
	}
	return g
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
	h, p, _ := newHandler(t)
	ctx := context.Background()
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("OLD_ONE", "old")}))
	inherited := []string{"OLD_ONE=old", "REPOSE_ENV_GEN=" + genOf(t, p)}
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("NEW_ONE", "n;1")}))
	refresh := p.SecretsRefresh()
	content, err := os.ReadFile(refresh)
	must(t, err)
	loader := loaderFor(t, refresh)

	cases := []struct {
		name  string
		setup func()
		want  string
	}{
		{"missing", func() { must(t, os.Chmod(refresh, 0o600)); must(t, os.Remove(refresh)) }, "0|a b|ehuBc|set -o pipefail|<unset>|old\n"},
		{"present", func() { must(t, os.WriteFile(refresh, content, 0o400)) }, "0|a b|ehuBc|set -o pipefail|n;1|<unset>\n"},
		{"unreadable", func() { must(t, os.Chmod(refresh, 0)) }, "0|a b|ehuBc|set -o pipefail|<unset>|old\n"},
		{"empty", func() { must(t, os.Chmod(refresh, 0o600)); must(t, os.WriteFile(refresh, nil, 0o600)) }, "0|a b|ehuBc|set -o pipefail|<unset>|old\n"},
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
	must(t, os.Chmod(refresh, 0o600))
	must(t, os.WriteFile(refresh, content, 0o400))
	script := filepath.Join(t.TempDir(), "script.sh")
	must(t, os.WriteFile(script, []byte("set -u\n"+probe+"\n"), 0o644))
	r := runBash(t, loader, inherited, script, "x")
	if r.code != 0 || r.stderr != "" || r.stdout != "0|x|huB|set +o pipefail|n;1|<unset>\n" {
		t.Fatalf("script: exit %d, stderr %q, stdout %q", r.code, r.stderr, r.stdout)
	}
}

// show prints NAME=value or NAME=<unset> for each name, as a child bash of a
// process with env sees them.
func show(t *testing.T, loader string, env []string, names ...string) string {
	t.Helper()
	args := append([]string{"-euo", "pipefail", "-c",
		`for n in "$@"; do if [ -n "${!n+set}" ]; then printf '%s=%s ' "$n" "${!n}"; else printf '%s=<unset> ' "$n"; fi; done`,
		"bash"}, names...)
	r := runBash(t, loader, env, args...)
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	return strings.TrimSpace(r.stdout)
}

// A parent that already holds the current generation keeps a value it set on
// purpose for its child.
func TestLoaderKeepsAnOverrideWhenTheGenerationIsCurrent(t *testing.T) {
	h, p, _ := newHandler(t)
	must(t, h.Write(context.Background(), []*guestdv1.Secret{secret("NEW_ONE", "file")}))
	loader := loaderFor(t, p.SecretsRefresh())
	if got := show(t, loader, []string{"REPOSE_ENV_GEN=" + genOf(t, p), "NEW_ONE=mine"}, "NEW_ONE"); got != "NEW_ONE=mine" {
		t.Fatalf("current generation: %s", got)
	}
}

// The case reviewers reproduced: an agent formed from generation 1, whose
// .envrc then set DATABASE_URL and whose command line set STRIPE_KEY, keeps
// both after any number of later writes, while a secret it still holds as
// delivered is rotated and a removed one is dropped.
func TestLoaderKeepsOverridesAcrossLaterWrites(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	must(t, h.Write(ctx, []*guestdv1.Secret{
		secret("DATABASE_URL", "prod-db"), secret("STRIPE_KEY", "live-key"),
		secret("ROTATED", "r1"), secret("GONE", "g"), secret("DROPPED", "d"),
	}))
	g1 := genOf(t, p)
	loader := loaderFor(t, p.SecretsRefresh())
	agent := []string{"REPOSE_ENV_GEN=" + g1,
		"DATABASE_URL=local-db", // from .envrc, after the profile
		"STRIPE_KEY=sk_test",    // STRIPE_KEY=sk_test ./run-tests.sh
		"ROTATED=r1", "GONE=g",  // as delivered
		// DROPPED: the agent's sandbox removed it
	}
	names := []string{"DATABASE_URL", "STRIPE_KEY", "ROTATED", "GONE", "DROPPED", "OTHER"}

	// An unrelated write: nothing the agent holds changes.
	must(t, h.Write(ctx, []*guestdv1.Secret{
		secret("DATABASE_URL", "prod-db"), secret("STRIPE_KEY", "live-key"),
		secret("ROTATED", "r1"), secret("GONE", "g"), secret("DROPPED", "d"), secret("OTHER", "x"),
	}))
	want := "DATABASE_URL=local-db STRIPE_KEY=sk_test ROTATED=r1 GONE=g DROPPED=<unset> OTHER=x"
	if got := show(t, loader, agent, names...); got != want {
		t.Fatalf("after an unrelated write:\n got %s\nwant %s", got, want)
	}

	// Rotate every secret and remove GONE and DATABASE_URL: only what the
	// agent still holds as delivered follows.
	must(t, h.Write(ctx, []*guestdv1.Secret{
		secret("STRIPE_KEY", "live-key-2"), secret("ROTATED", "r2"), secret("DROPPED", "d2"), secret("OTHER", "x"),
	}))
	want = "DATABASE_URL=local-db STRIPE_KEY=sk_test ROTATED=r2 GONE=<unset> DROPPED=<unset> OTHER=x"
	if got := show(t, loader, agent, names...); got != want {
		t.Fatalf("after a rotation:\n got %s\nwant %s", got, want)
	}

	// A process formed from the newest generation, the same way.
	g3 := genOf(t, p)
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("STRIPE_KEY", "live-key-3"), secret("OTHER", "x")}))
	fresh := []string{"REPOSE_ENV_GEN=" + g3, "STRIPE_KEY=live-key-2", "ROTATED=r2", "DROPPED=d2", "OTHER=x"}
	want = "DATABASE_URL=<unset> STRIPE_KEY=live-key-3 ROTATED=<unset> GONE=<unset> DROPPED=<unset> OTHER=x"
	if got := show(t, loader, fresh, names...); got != want {
		t.Fatalf("from generation 3:\n got %s\nwant %s", got, want)
	}
}

// A removed secret that the project's .envrc now provides stays.
func TestLoaderKeepsAnEnvrcValueOfARemovedSecret(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("OPENAI_API_KEY", "sk-secret")}))
	g1 := genOf(t, p)
	must(t, h.Write(ctx, nil))
	loader := loaderFor(t, p.SecretsRefresh())
	if got := show(t, loader, []string{"REPOSE_ENV_GEN=" + g1, "OPENAI_API_KEY=sk-envrc"}, "OPENAI_API_KEY"); got != "OPENAI_API_KEY=sk-envrc" {
		t.Fatalf("envrc value: %s", got)
	}
	if got := show(t, loader, []string{"REPOSE_ENV_GEN=" + g1, "OPENAI_API_KEY=sk-secret"}, "OPENAI_API_KEY"); got != "OPENAI_API_KEY=<unset>" {
		t.Fatalf("delivered value: %s", got)
	}
}

// A process with no generation (an ssh login, a user unit, an agent started
// before this guestd) or one older than keepGens gets the secrets it lacks
// and keeps every value it has.
func TestLoaderWithAnUnknownGeneration(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("A", "a0"), secret("B", "b0")}))
	old := genOf(t, p)
	for i := 1; i <= keepGens; i++ {
		must(t, h.Write(ctx, []*guestdv1.Secret{secret("A", fmt.Sprint("a", i)), secret("B", "b0")}))
	}
	loader := loaderFor(t, p.SecretsRefresh())
	last := fmt.Sprint("A=a", keepGens, " B=b0")
	if got := show(t, loader, nil, "A", "B"); got != last {
		t.Fatalf("no generation, nothing held: %s", got)
	}
	if got := show(t, loader, []string{"A=mine"}, "A", "B"); got != "A=mine B=b0" {
		t.Fatalf("no generation, A held: %s", got)
	}
	if got := show(t, loader, []string{"REPOSE_ENV_GEN=" + old, "A=a0"}, "A", "B"); got != "A=a0 B=b0" {
		t.Fatalf("a generation older than %d: %s", keepGens, got)
	}
	// The one before it is still known.
	st := h.readState()
	if len(st.Gens) != keepGens {
		t.Fatalf("kept %d generations, want %d", len(st.Gens), keepGens)
	}
	if got := show(t, loader, []string{"REPOSE_ENV_GEN=" + st.Gens[0].Gen, "A=a1", "B=b0"}, "A", "B"); got != last {
		t.Fatalf("the oldest kept generation: %s", got)
	}
}

// Every tricky value, rotated in, reaches a stale child byte for byte, and
// one the child holds as delivered is compared byte for byte too.
func TestLoaderTrickyValues(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	var first []*guestdv1.Secret
	held := []string{}
	for n, v := range trickyValues {
		first = append(first, secret(n, "x"+v))
		held = append(held, n+"=x"+v)
	}
	must(t, h.Write(ctx, first))
	held = append(held, "REPOSE_ENV_GEN="+genOf(t, p))
	must(t, h.Write(ctx, trickyList()))
	loader := loaderFor(t, p.SecretsRefresh())
	for name, want := range trickyValues {
		r := runBash(t, loader, held, "-euo", "pipefail", "-c", `printf %s "${!1}"`, "bash", name)
		if r.code != 0 || r.stderr != "" || r.stdout != want {
			t.Errorf("%s: exit %d, stderr %q, got %q, want %q", name, r.code, r.stderr, r.stdout, want)
		}
	}
	// And from sh, which is how /etc/profile.d/repose.sh runs it.
	if sh, err := exec.LookPath("sh"); err == nil {
		cmd := exec.Command(sh, "-c", `. "$1"; printf %s "$QUOTE"`, "sh", loader)
		cmd.Env = append([]string{"PATH="}, held...)
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != trickyValues["QUOTE"] {
			t.Fatalf("sh: %v %q", err, out)
		}
	}
}

// The marker survives a sandbox that drops variables whose names look
// sensitive (Codex's shell_environment_policy).
func TestGenerationVariableLooksHarmless(t *testing.T) {
	for _, w := range []string{"SECRET", "KEY", "TOKEN", "PASS", "AUTH", "CRED"} {
		if strings.Contains(genVar, w) {
			t.Fatalf("%s contains %s", genVar, w)
		}
	}
}

// bash -x traces nothing of the loader, so no value reaches a log.
func TestLoaderUnderXtracePrintsNoValue(t *testing.T) {
	h, p, _ := newHandler(t)
	must(t, h.Write(context.Background(), []*guestdv1.Secret{secret("NEW_ONE", "do-not-print")}))
	loader := loaderFor(t, p.SecretsRefresh())
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
	loader := loaderFor(t, p.SecretsRefresh())

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
