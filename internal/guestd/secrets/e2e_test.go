package secrets

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// privateTmux runs guestd's tmux commands against a private socket, as the
// test user rather than dev, through the real exec runner.
type privateTmux struct {
	tmux, sock string
	argvs      [][]string
}

func (r *privateTmux) Run(ctx context.Context, spec sysdep.RunSpec) (sysdep.RunResult, error) {
	if spec.Argv[0] != "tmux" {
		return sysdep.RunResult{}, nil // sshd reloads and the like
	}
	r.argvs = append(r.argvs, spec.Argv)
	spec.Argv = append([]string{r.tmux, "-S", r.sock}, spec.Argv[1:]...)
	spec.User = ""
	return sysdep.ExecRunner{}.Run(ctx, spec)
}

// End to end on one machine, everything real but the uid: guestd's Write
// with the exec runner, the BASH_ENV loader, a tmux server and an agent-like
// bash in a tmux window. The agent's own .envrc value and its per-command
// override survive later writes; after more writes to other names than
// any history bound, a secret it holds as delivered is rotated, a removed
// one dropped and a new one added, a new window gets the whole current set,
// a value far past tmux's 16 KiB command limit included, and no value is in
// any process's command line.
func TestEndToEndAgentInTmux(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("no tmux")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	dir, err := os.MkdirTemp("/tmp", "e2e")
	must(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p := sysdep.Paths{Root: filepath.Join(dir, "root")}
	run := &privateTmux{tmux: tmux, sock: filepath.Join(dir, "sock")}
	h := New(p, run, quietLog())
	h.uid, h.gid = -1, -1
	loader := loaderFor(t, p.SecretsRefresh())

	start := exec.Command(tmux, "-S", run.sock, "-f", "/dev/null", "new-session", "-d", "-s", "e2e", "sleep 60")
	start.Env = []string{"PATH=" + os.Getenv("PATH"), "BASH_ENV=" + loader, "HOME=" + dir}
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("tmux: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command(tmux, "-S", run.sock, "kill-server").Run() })
	ctx := context.Background()

	waitFor := func(path string) string {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			if b, err := os.ReadFile(path); err == nil && strings.HasSuffix(string(b), "\n") {
				return strings.TrimSpace(string(b))
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never written", path)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	must(t, h.Write(ctx, []*guestdv1.Secret{
		secret("DATABASE_URL", "prod-db"), secret("STRIPE_KEY", "live-key"),
		secret("ROTATED", "r1"), secret("GONE", "g"),
	}))

	// The agent: its environment comes from tmux, then its .envrc sets
	// DATABASE_URL; each step waits for the test, then runs commands the way
	// an agent does.
	agent := filepath.Join(dir, "agent.sh")
	must(t, os.WriteFile(agent, []byte(`
		export DATABASE_URL=local-db
		step() { while [ ! -e "$D/go$1" ]; do read -r -t 0.05 <> <(:) || :; done
		  bash -c 'echo "$DATABASE_URL $ROTATED ${GONE-<unset>} ${OTHER-<unset>} ${#BIG}"' > "$D/out$1.tmp"
		  STRIPE_KEY=sk_test bash -c 'echo "$STRIPE_KEY"' >> "$D/out$1.tmp"
		  mv "$D/out$1.tmp" "$D/out$1"; }
		echo "$DATABASE_URL $ROTATED" > "$D/out0"
		step 1; step 2
	`), 0o644))
	must(t, exec.Command(tmux, "-S", run.sock, "new-window", "-d", "-e", "D="+dir, bash+" "+agent).Run())
	if got := waitFor(filepath.Join(dir, "out0")); got != "local-db r1" {
		t.Fatalf("agent started with %q", got)
	}

	// An unrelated write.
	must(t, h.Write(ctx, []*guestdv1.Secret{
		secret("DATABASE_URL", "prod-db"), secret("STRIPE_KEY", "live-key"),
		secret("ROTATED", "r1"), secret("GONE", "g"), secret("OTHER", "x"),
	}))
	must(t, os.WriteFile(filepath.Join(dir, "go1"), nil, 0o600))
	if got := waitFor(filepath.Join(dir, "out1")); got != "local-db r1 g x 0\nsk_test" {
		t.Fatalf("after an unrelated write the agent's commands see %q", got)
	}

	// Many writes to other names, as an import of a long .env makes (one
	// write per line), then a rotation, a removal and a 40 KiB value.
	many := []*guestdv1.Secret{
		secret("DATABASE_URL", "prod-db"), secret("STRIPE_KEY", "live-key"),
		secret("ROTATED", "r1"), secret("GONE", "g"), secret("OTHER", "x"),
	}
	for i := 0; i < 2*keepValues; i++ {
		many = append(many, secret(fmt.Sprint("IMPORTED_", i), "i"))
		must(t, h.Write(ctx, many))
	}
	// mark makes this run's values unique, so the command line scan below
	// cannot match another program that merely mentions this test.
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	mark := hex.EncodeToString(rnd[:])
	big := mark + strings.Repeat("b", 40<<10-len(mark))
	must(t, h.Write(ctx, []*guestdv1.Secret{
		secret("DATABASE_URL", "prod-db-2"), secret("STRIPE_KEY", "live-key-2"),
		secret("ROTATED", "r2"), secret("OTHER", "x"), secret("BIG", big), secret("MARK", "m-"+mark),
	}))
	must(t, os.WriteFile(filepath.Join(dir, "go2"), nil, 0o600))
	if got := waitFor(filepath.Join(dir, "out2")); got != "local-db r2 <unset> x 40960\nsk_test" {
		t.Fatalf("after a rotation the agent's commands see %q", got)
	}

	// A new window, whose command is not bash, gets the current set from
	// tmux's global environment.
	must(t, exec.Command(tmux, "-S", run.sock, "new-window", "-d",
		"sh -c 'echo \"$DATABASE_URL $STRIPE_KEY $ROTATED ${GONE-<unset>} ${#BIG}\" > "+dir+"/win.tmp; mv "+dir+"/win.tmp "+dir+"/win'").Run())
	if got := waitFor(filepath.Join(dir, "win")); got != "prod-db-2 live-key-2 r2 <unset> 40960" {
		t.Fatalf("a new window sees %q", got)
	}

	// No value ever travelled as an argument: not in what guestd ran, and
	// not in any process's command line now.
	for _, argv := range run.argvs {
		if a := strings.Join(argv, " "); strings.Contains(a, "prod-db") || strings.Contains(a, "bbbb") {
			t.Fatalf("a value in tmux's argv: %.80s", a)
		}
	}
	cmdlines, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, c := range cmdlines {
		b, err := os.ReadFile(c)
		if err != nil {
			continue
		}
		if strings.Contains(string(b), mark) {
			t.Fatalf("%s holds a secret value: %.80q", c, b)
		}
	}
}
