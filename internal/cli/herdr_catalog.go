package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/heracraft/repose/internal/multiplexer"
)

// The laptop herdr's machine list (DECISIONS I-510). A laptop herdr keeps
// SSH machines in its sidebar; the CLI keeps repose's there: it adds a
// running herdr project that has no entry and removes an entry whose
// project is gone. It owns exactly the entries whose target is
// `<slug>.repose`, made by it, by hand or by the old tutorial, and never
// touches any other. It only runs herdr's own commands (`machine list
// --json`, `machine add`, `machine remove`), never edits herdr's files,
// and says nothing unless an add fails (cli-config.md "The laptop's
// herdr").

// lookHerdr finds the laptop's herdr; tests replace it.
var lookHerdr = exec.LookPath

// herdrMachine is one `herdr machine list --json` row.
type herdrMachine struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Target  string `json:"target"`
	Session string `json:"session"`
	Enabled bool   `json:"enabled"`
}

// reposeTarget matches the targets repose owns and captures the slug.
var reposeTarget = regexp.MustCompile(`^([a-z0-9][a-z0-9-]*)\.repose$`)

// ownedSlug is the slug of an entry repose owns, "" for any other.
func ownedSlug(m herdrMachine) string {
	if s := reposeTarget.FindStringSubmatch(strings.ToLower(strings.TrimSpace(m.Target))); s != nil {
		return s[1]
	}
	return ""
}

// herdrCatalogPlan is the reconcile's decision: slugs to add, entry ids
// to remove.
type herdrCatalogPlan struct {
	Add    []string
	Remove []string
}

// planHerdrCatalog compares herdr's entries with the account's projects.
// A running herdr project that is not temporary and has no entry (a
// disabled one counts as an entry) is added; an entry repose owns whose
// slug is no live project is removed, and so is each entry past the
// first for a live slug (the enabled one kept when there is one), left
// by two adds that raced before addOnce's lock; nothing else is touched.
func planHerdrCatalog(entries []herdrMachine, projects []Project) herdrCatalogPlan {
	live := map[string]bool{}
	for _, p := range projects {
		if p.State != "destroying" && p.State != "destroyed" {
			live[p.Slug] = true
		}
	}
	keep := map[string]herdrMachine{}
	for _, m := range entries {
		slug := ownedSlug(m)
		if slug == "" || !live[slug] {
			continue
		}
		if k, ok := keep[slug]; !ok || (!k.Enabled && m.Enabled) {
			keep[slug] = m
		}
	}
	has := map[string]bool{}
	var plan herdrCatalogPlan
	for _, m := range entries {
		slug := ownedSlug(m)
		if slug == "" {
			continue
		}
		has[slug] = true
		if !live[slug] || keep[slug].ID != m.ID {
			plan.Remove = append(plan.Remove, m.ID)
		}
	}
	for _, p := range projects {
		if p.State == "running" && p.ExpiresAt == nil && multiplexer.Normalize(p.Multiplexer) == multiplexer.Herdr && !has[p.Slug] {
			plan.Add = append(plan.Add, p.Slug)
		}
	}
	sort.Strings(plan.Add)
	return plan
}

// herdrRun runs one laptop herdr command with no terminal (stdin is
// /dev/null, so herdr never asks), returning stdout and the last line of
// stderr.
func (h *laptopHerdrCLI) run(ctx context.Context, timeout time.Duration, args ...string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.path, args...)
	cmd.Stdin = nil
	// `machine add` runs ssh to the machine: its ssh-prepare should do
	// its work, as for any ssh the CLI did not start itself.
	cmd.Env = envWithoutSSHPrepared()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	last := ""
	if lines := nonEmptyLines(stderr.String()); len(lines) > 0 {
		last = strings.TrimSpace(lines[len(lines)-1])
	}
	return stdout.Bytes(), last, err
}

func (h *laptopHerdrCLI) machines(ctx context.Context) ([]herdrMachine, error) {
	out, _, err := h.run(ctx, 5*time.Second, "machine", "list", "--json")
	if err != nil {
		return nil, err
	}
	var ms []herdrMachine
	if err := json.Unmarshal(out, &ms); err != nil {
		return nil, err
	}
	return ms, nil
}

// add is `herdr machine add <slug>.repose --label <slug>
// --remote-session default`, which prepares the machine's herdr over SSH
// (about a second on a running machine) and saves the entry.
func (h *laptopHerdrCLI) add(ctx context.Context, slug string) error {
	_, last, err := h.run(ctx, 60*time.Second, "machine", "add", slug+".repose", "--label", slug, "--remote-session", "default")
	if err != nil {
		if last == "" {
			last = err.Error()
		}
		return fmt.Errorf("%s", last)
	}
	return nil
}

// herdrLockName is the lock under ~/.config/repose that addOnce holds
// (cli-config.md).
const herdrLockName = "herdr-sidebar.lock"

// errHerdrList is a `machine list` that failed inside addOnce; the
// reconcile says nothing about it, as for its own list.
var errHerdrList = errors.New("herdr machine list failed")

// addOnce adds slug unless herdr has an entry for it, reading the list
// again under a lock every repose process takes for its adds: herdr's
// `machine add --label` makes a second entry for the same target, so two
// commands at once (two panes, or ensureEntry beside a background add)
// would each add one. enabled is the entry's state.
func (h *laptopHerdrCLI) addOnce(ctx context.Context, slug string) (enabled bool, err error) {
	if dir, derr := configDir(); derr == nil && os.MkdirAll(dir, 0o700) == nil {
		if unlock, lerr := lockFile(filepath.Join(dir, herdrLockName)); lerr == nil {
			defer unlock()
		}
	}
	entries, err := h.machines(ctx)
	if err != nil {
		return false, errHerdrList
	}
	for _, m := range entries {
		if ownedSlug(m) == slug {
			return m.Enabled, nil
		}
	}
	if err := h.add(ctx, slug); err != nil {
		return false, err
	}
	return true, nil
}

func (h *laptopHerdrCLI) remove(ctx context.Context, id string) {
	_, _, _ = h.run(ctx, 5*time.Second, "machine", "remove", id)
}

// herdrAddFailed prints the one line a failed add gets, once per slug
// and process.
var herdrAddFailed sync.Map

func sayHerdrAddFailed(warn func(string), slug string, err error) {
	if _, dup := herdrAddFailed.LoadOrStore(slug, true); dup || warn == nil {
		return
	}
	warn(fmt.Sprintf("Could not add %s to herdr's sidebar: %s", slug, oneLine(err.Error())))
}

// herdrAdds are the adds in flight; a command that ends soon after
// starting them (a sync) waits for them so herdr is not left halfway.
var herdrAdds sync.WaitGroup

// herdrCatalogReady says the laptop can take part: herdr 0.9.0 or newer
// on PATH and repose's Include in ~/.ssh/config, without which herdr's
// ssh cannot reach `<slug>.repose`.
func herdrCatalogReady() *laptopHerdrCLI {
	lh := laptopHerdr()
	if lh == nil {
		return nil
	}
	usc, err := userSSHConfig()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(usc)
	if err != nil || !bytes.Contains(b, []byte(includeLine)) {
		return nil
	}
	return lh
}

// syncHerdrMachines is the reconcile. Removes run now (a local file
// change in herdr); adds, when adds is set, run in the background. A
// herdr that is too old or whose list fails does nothing and says
// nothing.
func syncHerdrMachines(ctx context.Context, projects []Project, adds bool, warn func(string)) {
	syncHerdrMachinesWith(ctx, projects, adds, true, warn)
}

// syncHerdrMachinesWith is syncHerdrMachines with removes optional: a
// list that is not the account's whole list (herdrSyncFor with no api
// read) must remove nothing.
func syncHerdrMachinesWith(ctx context.Context, projects []Project, adds, removes bool, warn func(string)) {
	lh := herdrCatalogReady()
	if lh == nil {
		return
	}
	entries, err := lh.machines(ctx)
	if err != nil {
		return
	}
	plan := planHerdrCatalog(entries, projects)
	for _, id := range plan.Remove {
		if removes {
			lh.remove(ctx, id)
		}
	}
	if !adds {
		return
	}
	for _, slug := range plan.Add {
		herdrAdds.Add(1)
		go func(slug string) {
			defer herdrAdds.Done()
			if _, err := lh.addOnce(context.WithoutCancel(ctx), slug); err != nil && !errors.Is(err, errHerdrList) {
				sayHerdrAddFailed(warn, slug, err)
			}
		}(slug)
	}
}

// waitHerdrAdds waits up to d for the adds in flight.
func waitHerdrAdds(d time.Duration) {
	done := make(chan struct{})
	go func() { herdrAdds.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

// forgetHerdrMachine is `repose rm`'s part: the entry for slug goes, if
// repose owns one.
func forgetHerdrMachine(ctx context.Context, slug string) {
	lh := herdrCatalogReady()
	if lh == nil {
		return
	}
	entries, err := lh.machines(ctx)
	if err != nil {
		return
	}
	for _, m := range entries {
		if ownedSlug(m) == slug {
			lh.remove(ctx, m.ID)
		}
	}
}

// ensureEntry is the attach's part (rule 1 of the attach rule): true
// when slug has an enabled entry, adding one now when it has none. An
// entry the user disabled stays disabled, and the attach takes another
// path.
func (h *laptopHerdrCLI) ensureEntry(e *Env, slug string) bool {
	if herdrCatalogReady() == nil {
		return false
	}
	enabled, err := h.addOnce(context.Background(), slug)
	if err != nil && !errors.Is(err, errHerdrList) {
		sayHerdrAddFailed(func(s string) { e.warn("%s", s) }, slug, err)
	}
	return err == nil && enabled
}

// heldLines holds warning lines while an attach owns the terminal, so a
// sidebar add that fails mid-attach never prints over the pane
// (features/run-and-attach.md: helper output goes "never over the
// pane"); release prints what was held and lets later lines through.
type heldLines struct {
	mu    sync.Mutex
	held  bool
	lines []string
	out   func(string)
}

func (h *heldLines) say(s string) {
	h.mu.Lock()
	if h.held {
		h.lines = append(h.lines, s)
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	h.out(s)
}

func (h *heldLines) release() {
	h.mu.Lock()
	h.held = false
	lines := h.lines
	h.lines = nil
	h.mu.Unlock()
	for _, l := range lines {
		h.out(l)
	}
}

// herdrSyncFor is run's and attach's reconcile, in the background, with
// the projects connect read (e.listed). A command that read no list (the
// fast attach, a run whose connection was already up) makes no api call
// for one (I-223): it reconciles the current project alone, adding and
// never removing. The current project is added only when the guest runs
// herdr (runsHerdr). A failed add's line is held until release,
// which the caller runs once the terminal is its own again (after the
// attach, or at once for a command that attaches nothing).
func (e *Env) herdrSyncFor(ctx context.Context, current *Project, runsHerdr bool) (release func()) {
	h := &heldLines{held: true, out: func(s string) { e.warn("%s", s) }}
	if herdrCatalogReady() == nil {
		return h.release
	}
	herdrAdds.Add(1)
	go func() {
		defer herdrAdds.Done()
		projects, whole := e.listed, true
		if projects == nil {
			if current == nil {
				return
			}
			projects, whole = []Project{*current}, false
		}
		projects = append([]Project(nil), projects...)
		for i := range projects {
			if current == nil || projects[i].ID != current.ID {
				continue
			}
			// The current project goes by what its guest runs: one
			// switched to herdr while running tmux waits for its next
			// start (I-502). The command works on it, so it runs (a
			// cached project carries no state).
			projects[i].Multiplexer = multiplexer.Tmux
			if runsHerdr {
				projects[i].Multiplexer = multiplexer.Herdr
			}
			projects[i].State = "running"
		}
		syncHerdrMachinesWith(context.WithoutCancel(ctx), projects, true, whole, h.say)
	}()
	return h.release
}
