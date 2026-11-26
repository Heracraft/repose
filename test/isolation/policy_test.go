package isolation

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/guestd/sample"
)

// These tests run in ordinary CI. They pin the promises that the privacy
// policy makes in words to the message shapes and lists that keep them.

// The sentence the privacy policy must carry verbatim
// (docs/workstreams/14-security.md §5 "Policy text requirements").
const sampleBoundary = "We sample the processes running in your environment once a minute and " +
	"record their names, CPU time, memory use and network bytes. We never " +
	"record command-line arguments, environment variables, file paths, file " +
	"contents, terminal contents, or the prompts you give to any agent."

var spaces = regexp.MustCompile(`\s+`)

func normalise(s string) string { return spaces.ReplaceAllString(strings.TrimSpace(s), " ") }

func fieldNames(m protoreflect.Message) []string {
	fds := m.Descriptor().Fields()
	out := make([]string, 0, fds.Len())
	for i := 0; i < fds.Len(); i++ {
		out = append(out, string(fds.Get(i).Name()))
	}
	sort.Strings(out)
	return out
}

func assertFields(t *testing.T, m protoreflect.Message, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := fieldNames(m)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s carries fields %v; the privacy policy allows exactly %v. A new field needs docs/SECURITY.md and the policy text first.",
			m.Descriptor().FullName(), got, want)
	}
}

// ProcSample carries a process name, CPU and memory. Nothing else, ever.
func TestProcSampleCarriesOnlyNameCPUAndMemory(t *testing.T) {
	assertFields(t, (&hostdv1.ProcSample{}).ProtoReflect(), "comm", "cpu_ns_delta", "rss_bytes")
	// last_input_unix (I-1272) is a clock time, never what was typed; the
	// privacy policy's metering paragraph and SECURITY.md name it.
	assertFields(t, (&hostdv1.GuestSignals{}).ProtoReflect(), "ssh_sessions", "tmux_clients", "agents", "docker_containers", "guestd_ok", "last_input_unix")
	// agent_id and terminal (I-1676) add nothing about the tenant's
	// processes: an opaque keyed hash (never a pid; hostd drops any
	// other form, TestCleanSignalsKeepsTheAgentID) and the window name
	// tmux_window already carries, with its multiplexer. SECURITY.md
	// says so; the policy's metering paragraph needs no change.
	assertFields(t, (&hostdv1.AgentProc{}).ProtoReflect(), "agent", "tmux_window", "state", "agent_id", "terminal")
}

func readDoc(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

// The privacy policy contains the process-sample boundary verbatim, and the
// terms contain the hosted-use statement in substance.
func TestPolicyTextContainsTheRequiredPassages(t *testing.T) {
	privacy := normalise(readDoc(t, "apps/web/src/content/legal/privacy.md"))
	if !strings.Contains(privacy, normalise(sampleBoundary)) {
		t.Fatal("apps/web/src/content/legal/privacy.md does not contain the process-sample sentence verbatim")
	}
	security := normalise(readDoc(t, "docs/SECURITY.md"))
	if !strings.Contains(security, normalise(sampleBoundary)) {
		t.Fatal("docs/SECURITY.md does not quote the process-sample sentence; the policy and the threat model must say the same words")
	}
	terms := normalise(readDoc(t, "apps/web/src/content/legal/terms.md"))
	for _, phrase := range []string{
		"run inside your environment under your own account",
		"does not hold, proxy, or resell those credentials",
		"responsible for complying with each provider's terms",
	} {
		if !strings.Contains(terms, phrase) {
			t.Fatalf("apps/web/src/content/legal/terms.md lacks the hosted-use statement phrase %q", phrase)
		}
	}
}

// The abuse watch list in docs/SECURITY.md and internal/guestd/sample/watch.go
// are the same set.
func TestWatchListMatchesSecurityDoc(t *testing.T) {
	doc := readDoc(t, "docs/SECURITY.md")
	_, after, ok := strings.Cut(doc, "## The abuse watch list")
	if !ok {
		t.Fatal("docs/SECURITY.md has no 'The abuse watch list' section")
	}
	_, block, ok := strings.Cut(after, "```\n")
	if !ok {
		t.Fatal("no code block under the watch list heading")
	}
	block, _, _ = strings.Cut(block, "```")
	docNames := strings.Fields(block)
	sort.Strings(docNames)
	code := sample.WatchList()
	sort.Strings(code)
	if strings.Join(docNames, " ") != strings.Join(code, " ") {
		t.Fatalf("watch list differs:\n docs/SECURITY.md: %v\n watch.go:         %v", docNames, code)
	}
}

// `rg '.claude/.credentials.json' cmd internal` shows only explicit
// exclusions (docs/workstreams/14-security.md §5 "Secrets handling review").
// Every hit must be an exclusion or an existence check, never a copy. The
// CLI's own ~/.config/repose/credentials.json (docs/interfaces/cli-config.md)
// is a different file and is not matched. The exclusion words may sit on
// the line itself or the two lines before or after, since Go comments wrap.
func TestClaudeCredentialsAppearOnlyAsAnExclusion(t *testing.T) {
	root := repoRoot(t)
	var hits []string
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			lines := strings.Split(string(b), "\n")
			for i, line := range lines {
				if !strings.Contains(line, ".claude/.credentials.json") {
					continue
				}
				window := line
				for _, j := range []int{i - 2, i - 1, i + 1, i + 2} {
					if j >= 0 && j < len(lines) {
						window += " " + lines[j]
					}
				}
				l := strings.ToLower(window)
				rel, _ := filepath.Rel(root, path)
				hit := rel + ":" + itoa(i+1) + ": " + strings.TrimSpace(line)
				if !strings.Contains(l, "never") && !strings.Contains(l, "exclu") && !strings.Contains(l, "not copied") && !strings.Contains(l, "exists-check") {
					t.Errorf("mentions Claude's credentials file without being an exclusion: %s", hit)
				}
				hits = append(hits, hit)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("credentials.json hits (all exclusions): %d\n%s", len(hits), strings.Join(hits, "\n"))
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
