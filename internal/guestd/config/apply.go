package config

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// Error codes of an ApplyMenu's final ApplyMenuProgress
// (docs/interfaces/vsock-guestd.md).
const (
	CodeMenuFailed      = sysdep.CodeMenuFailed
	CodeInvalidArgument = sysdep.CodeInvalidArgument
)

// Deadline bounds one ApplyMenu. The slowest single steps measured for
// these recipes are an apt-get install of dotnet-sdk-8.0 or
// rabbitmq-server (one to three minutes on a cold mirror) and a mise
// `go install` of gopls and dlv (two to five), and a whole menu of the
// catalog's 25 entries is the worst case, so an hour stops only a
// handler that would otherwise live forever.
const Deadline = 60 * time.Minute

// KeepaliveInterval is how often a running ApplyMenu sends an empty
// progress line (DECISIONS I-1558): hostd gives up on a guestd that sent
// nothing for hostd's MenuIdle (two minutes), and never on an apply
// that is only slow.
const KeepaliveInterval = 30 * time.Second

// StepTimeout bounds one command (an apt-get install, a mise use, a
// download): the slowest of them above, three times over.
const StepTimeout = 20 * time.Minute

// CheckTimeout bounds one in-place check (dpkg-query, mise where,
// systemctl is-active), which read local state and return in well under
// a second.
const CheckTimeout = 30 * time.Second

// DefaultReadyTimeout is how long a service has to listen after its
// unit started. PostgreSQL's first start runs initdb (two to four
// seconds); MariaDB's runs mariadb-install-db (about five). A recipe sets
// its own when it needs more (RabbitMQ's Erlang start is ten to twenty).
const DefaultReadyTimeout = 60 * time.Second

// MaxLine caps one progress line (the contract's 1 KB).
const MaxLine = 1024

// packageRe is a Ubuntu package name as the contract takes it.
var packageRe = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,127}$`)

// ValidPackage reports whether name is a package name ApplyMenu takes.
func ValidPackage(name string) bool { return packageRe.MatchString(name) }

// PortWaiter waits until something listens on 127.0.0.1:port.
type PortWaiter func(ctx context.Context, port int, timeout time.Duration) error

// Progress takes one line for the user.
type Progress func(line string)

// Handler applies menus. One apply runs at a time; a second waits.
type Handler struct {
	paths   sysdep.Paths
	run     sysdep.Runner
	wait    PortWaiter
	log     *slog.Logger
	recipes *Recipes
	arch    string

	mu sync.Mutex
}

// New builds the handler. wait nil is the real check of /proc/net/tcp.
func New(p sysdep.Paths, run sysdep.Runner, wait PortWaiter, log *slog.Logger) *Handler {
	h := &Handler{paths: p, run: run, wait: wait, log: log, arch: runtime.GOARCH}
	if h.wait == nil {
		h.wait = func(ctx context.Context, port int, timeout time.Duration) error {
			return WaitListening(ctx, p, port, timeout)
		}
	}
	// The embedded file is checked by a test; a guestd built from a tree
	// where it does not parse refuses every ApplyMenu instead of
	// failing to start.
	r, err := LoadRecipes()
	if err == nil {
		h.recipes = r
	} else if log != nil {
		log.Error("the menu recipes do not load", "event", "apply_menu", "error_code", sysdep.CodeInternal)
	}
	return h
}

// SetArch overrides the architecture downloads are picked for (tests).
func (h *Handler) SetArch(arch string) { h.arch = arch }

// appliedItem is one item of menu-applied.json.
type appliedItem struct {
	Key     string            `json:"key"`
	ID      string            `json:"id,omitempty"`
	Options map[string]string `json:"options,omitempty"`
	Package string            `json:"package,omitempty"`
	OK      bool              `json:"ok"`
	// AptInstalled is the packages this item's apply installed that
	// were not installed before it, which is what its removal removes.
	AptInstalled []string `json:"apt_installed,omitempty"`
}

// applied is /run/repose/rw/menu-applied.json.
type applied struct {
	RevisionID string        `json:"revision_id,omitempty"`
	Items      []appliedItem `json:"items"`
}

// want is one item of the requested menu, resolved.
type want struct {
	key     string
	label   string
	id      string
	pkg     string
	opts    map[string]string
	r       *Rendered
	prevApt []string
}

// failure is one item that did not apply.
type failure struct {
	label  string
	reason string
}

// apply is the state of one ApplyMenu.
type apply struct {
	h          *Handler
	progress   Progress
	aptUpdated bool
	failures   []failure
	changed    int
	// cur is the item being worked on: the prefix of its step lines.
	cur string
}

// step says one step of the current item: "<item>: <what>" (DECISIONS
// I-1536). The CLI shows it as the running phase of the item's line.
func (a *apply) step(what string) {
	if a.cur != "" {
		a.progress(a.cur + ": " + what)
	}
}

// Apply makes the guest match spec and returns the final
// ApplyMenuProgress (done true). With replay, the call hostd makes at a
// boot, it does nothing when the base did not change and the menu is the
// one last applied (DECISIONS I-1460).
func (h *Handler) Apply(ctx context.Context, spec *hostdv1.MenuSpec, replay bool, progress Progress) *guestdv1.ApplyMenuProgress {
	h.mu.Lock()
	defer h.mu.Unlock()
	start := time.Now()
	if progress == nil {
		progress = func(string) {}
	}
	send := func(line string) { progress(capLine(line)) }

	if h.recipes == nil {
		return final(sysdep.CodeInternal, "the menu recipes in this guest do not load")
	}
	wants, err := h.resolve(spec)
	if err != nil {
		h.log.Warn("menu refused", "event", "apply_menu", "items", len(spec.GetItems()), "error_code", CodeInvalidArgument)
		return final(CodeInvalidArgument, err.Error())
	}

	prev, _ := h.loadApplied()
	upgraded := fileExists(h.paths.BaseUpgraded())
	if replay && !upgraded && sameMenu(prev, wants) {
		h.log.Info("menu unchanged", "event", "apply_menu", "items", len(wants), "replay", true, "duration_ms", time.Since(start).Milliseconds())
		send("nothing to do")
		return final("", "nothing to do")
	}

	a := &apply{h: h, progress: send}
	next := applied{RevisionID: spec.GetRevisionId()}

	// Removals first: an item an earlier menu had and this one does not.
	keep := map[string]bool{}
	for _, w := range wants {
		keep[w.key] = true
	}
	prevApt := map[string][]string{}
	if prev != nil {
		for _, it := range prev.Items {
			prevApt[it.Key] = it.AptInstalled
			if keep[it.Key] {
				continue
			}
			if err := a.remove(ctx, it); err != nil {
				a.fail(labelOf(it), err.Error())
				// Kept in the record, so the next apply tries again.
				it.OK = false
				next.Items = append(next.Items, it)
			}
		}
	}

	for _, w := range wants {
		w.prevApt = prevApt[w.key]
		item := appliedItem{Key: w.key, ID: w.id, Options: w.opts, Package: w.pkg}
		installed, err := a.ensure(ctx, w)
		item.AptInstalled = mergeSorted(w.prevApt, installed)
		if err != nil {
			a.fail(w.label, err.Error())
		} else {
			item.OK = true
		}
		next.Items = append(next.Items, item)
	}

	// After a base upgrade, packages installed outside the menu come
	// back too. One that no longer installs is said and not failed: it
	// would otherwise keep the marker, and replay, at every boot.
	if upgraded {
		a.replayManual(ctx, wants)
	}

	if err := h.saveApplied(next); err != nil {
		a.fail("menu record", "could not be written")
	}

	code, line := "", "nothing to do"
	switch {
	case len(a.failures) > 0:
		code, line = CodeMenuFailed, failureLine(a.failures)
	case a.changed > 0:
		line = "menu applied"
	}
	if code == "" && upgraded {
		if err := os.Remove(h.paths.BaseUpgraded()); err != nil && !os.IsNotExist(err) {
			code, line = CodeMenuFailed, "the base-upgrade marker could not be removed"
		}
	}
	if code == "" {
		send(line)
	}
	h.log.Info("menu applied", "event", "apply_menu", "items", len(wants), "changed", a.changed,
		"failed", len(a.failures), "replay", replay, "base_upgraded", upgraded,
		"error_code", code, "duration_ms", time.Since(start).Milliseconds())
	return final(code, line)
}

func final(code, line string) *guestdv1.ApplyMenuProgress {
	return &guestdv1.ApplyMenuProgress{Line: capLine(line), Done: true, ErrorCode: code}
}

func (a *apply) fail(label, reason string) {
	a.failures = append(a.failures, failure{label: label, reason: reason})
	a.progress(label + " failed: " + reason)
}

func failureLine(fs []failure) string {
	if len(fs) == 1 {
		return fs[0].label + " failed: " + fs[0].reason
	}
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		parts = append(parts, f.label+" ("+f.reason+")")
	}
	return fmt.Sprintf("%d items failed: %s", len(fs), strings.Join(parts, "; "))
}

// resolve validates the whole menu before anything runs: one bad item
// refuses the request and changes nothing.
func (h *Handler) resolve(spec *hostdv1.MenuSpec) ([]*want, error) {
	env := Env{Arch: h.arch, Codename: h.codename(), ServicesDir: "/var/lib/repose/services"}
	var out []*want
	seen := map[string]bool{}
	for i, it := range spec.GetItems() {
		id, pkg := it.GetId(), it.GetPackage()
		switch {
		case id != "" && pkg != "":
			return nil, fmt.Errorf("item %d has both an id and a package", i+1)
		case id == "" && pkg == "":
			return nil, fmt.Errorf("item %d has neither an id nor a package", i+1)
		case pkg != "":
			if !ValidPackage(pkg) {
				return nil, fmt.Errorf("item %d: %q is not a package name", i+1, pkg)
			}
			key := "pkg:" + pkg
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, &want{key: key, label: pkg, pkg: pkg})
		default:
			rec, ok := h.recipes.Get(id)
			if !ok {
				return nil, fmt.Errorf("item %d: no catalog entry %q", i+1, id)
			}
			if err := rec.ValidateOptions(it.GetOptions()); err != nil {
				return nil, err
			}
			opts := rec.Defaults(it.GetOptions())
			r, err := rec.Render(opts, env)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", id, err)
			}
			key := "id:" + id
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, &want{key: key, label: id, id: id, opts: opts, r: r})
		}
	}
	return out, nil
}

// sameMenu is true when prev applied exactly these items, each with
// these options, and every one of them succeeded.
func sameMenu(prev *applied, wants []*want) bool {
	if prev == nil {
		return len(wants) == 0
	}
	if len(prev.Items) != len(wants) {
		return false
	}
	for i, it := range prev.Items {
		w := wants[i]
		if !it.OK || it.Key != w.key || !sameOptions(it.Options, w.opts) {
			return false
		}
	}
	return true
}

func sameOptions(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func labelOf(it appliedItem) string {
	if it.Package != "" {
		return it.Package
	}
	return it.ID
}

// ensure puts one item in place, or finds it there. installed is the
// apt packages it installed that were not there before.
func (a *apply) ensure(ctx context.Context, w *want) (installed []string, err error) {
	if w.pkg != "" {
		return a.ensurePackage(ctx, w)
	}
	r := w.r
	if r.Recipe.Kind == KindBase {
		return nil, nil
	}
	if ok, _ := a.inPlace(ctx, r); ok {
		return nil, nil
	}
	a.changed++
	a.cur = w.label
	defer func() { a.cur = "" }()
	a.progress("installing " + w.label)

	for _, s := range r.AptSources {
		if err := a.ensureAptSource(ctx, s); err != nil {
			return nil, err
		}
	}
	if r.Pre != "" {
		if err := a.rootSh(ctx, r.Pre, "setup"); err != nil {
			return nil, err
		}
	}
	if len(r.Mask) > 0 {
		if err := a.root(ctx, "systemctl", append([]string{"systemctl", "mask"}, r.Mask...)...); err != nil {
			return nil, err
		}
	}
	if len(r.Apt) > 0 {
		before := a.aptStatus(ctx, r.Apt)
		if err := a.aptInstall(ctx, r.Apt); err != nil {
			return nil, err
		}
		for _, p := range r.Apt {
			if !before[p] {
				installed = append(installed, p)
			}
		}
	}
	if r.Download != nil {
		a.step("downloading " + r.Recipe.Download.Name + " " + r.Recipe.Download.Version)
		if err := a.download(ctx, r); err != nil {
			return installed, err
		}
	}
	for _, spec := range r.Mise {
		a.step("installing " + spec + " with mise")
		if err := a.dev(ctx, "mise", "mise", "use", "-g", spec); err != nil {
			return installed, err
		}
	}
	for _, f := range r.Files {
		if err := a.writeFile(f); err != nil {
			return installed, err
		}
	}
	if r.Recipe.Service != nil {
		if err := a.ensureService(ctx, r); err != nil {
			return installed, err
		}
	}
	if r.Post != "" {
		if err := a.rootSh(ctx, r.Post, "setup"); err != nil {
			return installed, err
		}
	}
	if r.PostDev != "" {
		if err := a.devSh(ctx, r.PostDev); err != nil {
			return installed, err
		}
	}
	if r.Check != "" {
		if err := a.rootSh(ctx, r.Check, "check"); err != nil {
			return installed, errors.New("installed, but its check failed")
		}
	}
	a.progress(r.Ready)
	return installed, nil
}

// inPlace is true when everything the recipe makes is there: its apt
// packages installed, its mise tools installed, its files and unit as
// rendered, its unit active, and its own check passing.
func (a *apply) inPlace(ctx context.Context, r *Rendered) (bool, error) {
	for _, s := range r.AptSources {
		if !fileHas(a.h.paths.Etc("apt", "sources.list.d", "repose-"+s.Name+".list"), s.Line+"\n") {
			return false, nil
		}
	}
	if len(r.Apt) > 0 {
		st := a.aptStatus(ctx, r.Apt)
		for _, p := range r.Apt {
			if !st[p] {
				return false, nil
			}
		}
	}
	if r.Download != nil && r.Recipe.Download.Install == "binary" {
		if !a.check(ctx, "sh", "sh", "-c", sumCheck(r.Download.SHA256, "/usr/local/bin/"+r.Recipe.Download.Name)) {
			return false, nil
		}
	}
	for _, spec := range r.Mise {
		if !a.checkDev(ctx, "mise", "where", spec) {
			return false, nil
		}
	}
	for _, f := range r.Files {
		if !fileHas(a.h.guestPath(f.Path), f.Content) {
			return false, nil
		}
	}
	if r.Recipe.Service != nil {
		unit := UnitName(r.Recipe.ID)
		if !fileHas(filepath.Join(a.h.paths.SystemdUnitDir(), unit), unitFile(r)) {
			return false, nil
		}
		if !a.check(ctx, "systemctl", "systemctl", "is-active", "--quiet", unit) {
			return false, nil
		}
	}
	if r.Check != "" && !a.check(ctx, "sh", "sh", "-c", r.Check) {
		return false, nil
	}
	return true, nil
}

// UnitName is a menu service's unit.
func UnitName(id string) string { return "repose-svc-" + id + ".service" }

// unitFile is the whole unit for a service recipe.
func unitFile(r *Rendered) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Written by guestd for the menu item %s; ApplyMenu rewrites it.\n", r.Recipe.ID)
	b.WriteString("[Unit]\n")
	fmt.Fprintf(&b, "Description=repose menu service %s\n", r.Recipe.ID)
	b.WriteString("After=network.target\n\n")
	b.WriteString("[Service]\n")
	fmt.Fprintf(&b, "User=%s\n", r.Recipe.Service.User)
	b.WriteString("Restart=on-failure\nRestartSec=2\n")
	b.WriteString(strings.TrimRight(r.Unit, "\n") + "\n\n")
	b.WriteString("[Install]\nWantedBy=multi-user.target\n")
	return b.String()
}

func (a *apply) ensureService(ctx context.Context, r *Rendered) error {
	svc := r.Recipe.Service
	// The package normally makes its user; a download has none.
	if !a.check(ctx, "id", "id", "-u", svc.User) {
		if err := a.root(ctx, "useradd", "useradd", "--system", "--no-create-home", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin", svc.User); err != nil {
			return err
		}
	}
	// The data directory is on the volume and outlives the base, whose
	// fresh install of the package may give the user another uid: it is
	// handed back to the user when its owner is not.
	if err := os.MkdirAll(a.h.guestPath(r.DataDir), 0o755); err != nil {
		return fmt.Errorf("could not make its data directory")
	}
	own := fmt.Sprintf(`d=%s; [ "$(stat -c %%U "$d")" = %s ] || chown -R %s: "$d"; chmod 0700 "$d"`, shellQuote(r.DataDir), svc.User, svc.User)
	if err := a.rootSh(ctx, own, "chown"); err != nil {
		return err
	}
	unit := UnitName(r.Recipe.ID)
	path := filepath.Join(a.h.paths.SystemdUnitDir(), unit)
	body := unitFile(r)
	changed := !fileHas(path, body)
	if changed {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("could not write its unit")
		}
		if err := sysdep.WriteFileAtomic(path, []byte(body), 0o644, 0, 0); err != nil {
			return fmt.Errorf("could not write its unit")
		}
		if err := a.root(ctx, "systemctl", "systemctl", "daemon-reload"); err != nil {
			return err
		}
	}
	if err := a.root(ctx, "systemctl", "systemctl", "enable", unit); err != nil {
		return err
	}
	verb := "start"
	if changed {
		verb = "restart"
	}
	a.step("starting " + unit)
	if err := a.root(ctx, "systemctl", "systemctl", verb, unit); err != nil {
		return fmt.Errorf("its unit did not start (%v)", err)
	}
	timeout := DefaultReadyTimeout
	if svc.ReadyTimeoutS > 0 {
		timeout = time.Duration(svc.ReadyTimeoutS) * time.Second
	}
	if err := a.h.wait(ctx, svc.Port, timeout); err != nil {
		return fmt.Errorf("nothing listened on 127.0.0.1:%d within %d s", svc.Port, int(timeout.Seconds()))
	}
	return nil
}

func (a *apply) ensurePackage(ctx context.Context, w *want) ([]string, error) {
	var installed []string
	st := a.aptStatus(ctx, []string{w.pkg})
	if !st[w.pkg] {
		a.changed++
		a.cur = w.pkg
		defer func() { a.cur = "" }()
		a.progress("installing " + w.pkg)
		if err := a.aptInstall(ctx, []string{w.pkg}); err != nil {
			return nil, err
		}
		installed = []string{w.pkg}
		a.progress(w.pkg + " installed")
	}
	// apt-get install marks the package manual, and PR 2's dpkg hook
	// writes it into apt-manual.list; guestd never writes that file
	// (DECISIONS I-1476).
	return installed, nil
}

// replayManual installs again what apt-manual.list names and the menu
// does not, after a base upgrade dropped the overlay's upper directory.
func (a *apply) replayManual(ctx context.Context, wants []*want) {
	inMenu := map[string]bool{}
	for _, w := range wants {
		if w.pkg != "" {
			inMenu[w.pkg] = true
		}
	}
	var todo []string
	for _, name := range a.h.manualList() {
		if !inMenu[name] {
			todo = append(todo, name)
		}
	}
	if len(todo) == 0 {
		return
	}
	st := a.aptStatus(ctx, todo)
	for _, name := range todo {
		if st[name] {
			continue
		}
		a.changed++
		a.cur = name
		a.progress("installing " + name + " again")
		if err := a.aptInstall(ctx, []string{name}); err != nil {
			a.progress("could not install " + name + " again: " + err.Error())
			continue
		}
		a.progress(name + " installed")
	}
	a.cur = ""
}

// remove undoes an item the menu no longer has. A service's data
// directory and packages stay.
func (a *apply) remove(ctx context.Context, it appliedItem) error {
	a.changed++
	if it.Package != "" {
		a.progress("removing " + it.Package)
		if contains(it.AptInstalled, it.Package) {
			if err := a.aptRemove(ctx, []string{it.Package}); err != nil {
				return err
			}
		}
		a.progress(it.Package + " removed")
		return nil
	}
	rec, ok := a.h.recipes.Get(it.ID)
	if !ok {
		return nil
	}
	r, err := rec.Render(rec.Defaults(it.Options), Env{Arch: a.h.arch, Codename: a.h.codename(), ServicesDir: "/var/lib/repose/services"})
	if err != nil {
		return err
	}
	a.progress("removing " + it.ID)
	if rec.Service != nil {
		unit := UnitName(rec.ID)
		path := filepath.Join(a.h.paths.SystemdUnitDir(), unit)
		if fileExists(path) {
			if err := a.root(ctx, "systemctl", "systemctl", "disable", "--now", unit); err != nil {
				return err
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return errors.New("could not remove its unit")
			}
			if err := a.root(ctx, "systemctl", "systemctl", "daemon-reload"); err != nil {
				return err
			}
		}
	}
	for _, spec := range r.Mise {
		if err := a.dev(ctx, "mise", "mise", "unuse", "-g", miseTool(spec)); err != nil {
			return err
		}
	}
	if rec.Kind != KindService && len(it.AptInstalled) > 0 {
		if err := a.aptRemove(ctx, it.AptInstalled); err != nil {
			return err
		}
	}
	if r.Remove != "" {
		if err := a.rootSh(ctx, r.Remove, "removal"); err != nil {
			return err
		}
	}
	if rec.Service != nil {
		a.progress(it.ID + " removed; its data stays in " + r.DataDir)
	} else {
		a.progress(it.ID + " removed")
	}
	return nil
}

// miseTool is a mise spec without its version: what `mise unuse` takes.
func miseTool(spec string) string {
	if i := strings.LastIndex(spec, "@"); i > 0 {
		return spec[:i]
	}
	return spec
}

func (a *apply) ensureAptSource(ctx context.Context, s AptSource) error {
	keyring := keyringPath(s.Name)
	fetch := fmt.Sprintf(`set -e
%s && exit 0
t=$(mktemp -d); trap 'rm -rf "$t"' EXIT
curl -fsSL --retry 3 -o "$t/k" %s
%s
install -D -m 0644 "$t/k" %s`, sumCheck(s.KeySHA256, keyring), shellQuote(s.KeyURL), sumCheck(s.KeySHA256, `"$t/k"`), keyring)
	if err := a.rootSh(ctx, fetch, "key download"); err != nil {
		return err
	}
	list := a.h.paths.Etc("apt", "sources.list.d", "repose-"+s.Name+".list")
	if fileHas(list, s.Line+"\n") {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(list), 0o755); err != nil {
		return errors.New("could not add its apt source")
	}
	if err := sysdep.WriteFileAtomic(list, []byte(s.Line+"\n"), 0o644, 0, 0); err != nil {
		return errors.New("could not add its apt source")
	}
	// The new source needs its own index, even when this apply already
	// updated.
	a.aptUpdated = false
	return nil
}

func (a *apply) download(ctx context.Context, r *Rendered) error {
	d := r.Recipe.Download
	art := r.Download
	var install string
	switch d.Install {
	case "binary":
		install = fmt.Sprintf(`install -m 0755 "$t/f" /usr/local/bin/%s`, d.Name)
	case "awscli":
		install = `unzip -q "$t/f" -d "$t" && "$t/aws/install" --update -i /usr/local/aws-cli -b /usr/local/bin`
	}
	script := fmt.Sprintf(`set -e
t=$(mktemp -d); trap 'rm -rf "$t"' EXIT
curl -fsSL --retry 3 -o "$t/f" %s
%s
%s`, shellQuote(art.URL), sumCheck(art.SHA256, `"$t/f"`), install)
	return a.rootSh(ctx, script, "download")
}

// sumCheck is a shell line that exits 0 when file's sha256 is sum.
func sumCheck(sum, file string) string {
	return fmt.Sprintf(`echo "%s  "%s | sha256sum -c --status 2>/dev/null`, sum, file)
}

func (a *apply) aptUpdate(ctx context.Context) error {
	if a.aptUpdated {
		return nil
	}
	a.step("updating package lists")
	if err := a.runStep(ctx, "apt-get", sysdep.RunSpec{Argv: []string{"apt-get", "-o", "DPkg::Lock::Timeout=300", "-q", "update"}, Env: a.h.rootEnv(), Background: true}); err != nil {
		return err
	}
	a.aptUpdated = true
	return nil
}

func (a *apply) aptInstall(ctx context.Context, pkgs []string) error {
	if err := a.aptUpdate(ctx); err != nil {
		return err
	}
	argv := append([]string{"apt-get", "-o", "DPkg::Lock::Timeout=300", "-q", "-y", "--no-install-recommends", "install"}, pkgs...)
	ph := &aptPhases{step: a.step}
	// Below the agent (DECISIONS I-1560): an install shares the machine
	// with an agent at work, whose own liveness checks give up on it
	// when apt takes an equal share (ENG-186).
	return a.runStep(ctx, "apt-get", sysdep.RunSpec{Argv: argv, Env: a.h.rootEnv(), OnLine: ph.line, Background: true})
}

var (
	aptCountRe = regexp.MustCompile(`^\d+ upgraded, (\d+) newly installed`)
	aptSizeRe  = regexp.MustCompile(`^Need to get (.+) of archives\.`)
)

// aptPhases turns apt-get install's own lines into one step line per
// phase as it starts: how many packages and how much to download, then
// unpacking, then setting up. Nothing apt prints is passed on as it is
// (DECISIONS I-1536).
type aptPhases struct {
	step                   func(string)
	count                  string
	said, unpack, settings bool
}

func (p *aptPhases) line(l string) {
	switch {
	case aptCountRe.MatchString(l):
		p.count = aptCountRe.FindStringSubmatch(l)[1]
	case aptSizeRe.MatchString(l):
		if p.count != "" && p.count != "0" && !p.said {
			p.said = true
			p.step("installing " + packages(p.count) + " (" + aptSizeRe.FindStringSubmatch(l)[1] + ")")
		}
	case strings.HasPrefix(l, "Unpacking ") && !p.unpack:
		if p.count != "" && p.count != "0" && !p.said {
			p.said = true
			p.step("installing " + packages(p.count))
		}
		p.unpack = true
		p.step("unpacking")
	case strings.HasPrefix(l, "Setting up ") && !p.settings:
		p.settings = true
		p.step("setting up")
	}
}

func packages(n string) string {
	if n == "1" {
		return "1 package"
	}
	return n + " packages"
}

// aptError is the last "E: " line in apt's output: apt's own words for
// why it failed, which the user reads (DECISIONS I-1536).
func aptError(out []byte) string {
	last := ""
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "E: ") {
			last = l
		}
	}
	if len(last) > 300 {
		last = last[:300]
	}
	return last
}

func (a *apply) aptRemove(ctx context.Context, pkgs []string) error {
	argv := append([]string{"apt-get", "-o", "DPkg::Lock::Timeout=300", "-q", "-y", "remove"}, pkgs...)
	return a.runStep(ctx, "apt-get", sysdep.RunSpec{Argv: argv, Env: a.h.rootEnv(), Background: true})
}

// aptStatus is which of pkgs dpkg has installed.
func (a *apply) aptStatus(ctx context.Context, pkgs []string) map[string]bool {
	out := map[string]bool{}
	cctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	argv := append([]string{"dpkg-query", "-W", "-f=${Package} ${db:Status-Abbrev}\\n"}, pkgs...)
	res, err := a.h.run.Run(cctx, sysdep.RunSpec{Argv: argv, Env: a.h.rootEnv(), MaxOutput: 64 << 10})
	if err != nil {
		return out
	}
	sc := bufio.NewScanner(bytes.NewReader(res.Stdout))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && strings.HasPrefix(f[1], "ii") {
			// dpkg-query names a multiarch package name:arch.
			name, _, _ := strings.Cut(f[0], ":")
			out[name] = true
		}
	}
	return out
}

// root runs argv as root; a non-zero exit is an error naming the tool
// and the code, which is what the user reads.
func (a *apply) root(ctx context.Context, tool string, argv ...string) error {
	return a.runStep(ctx, tool, sysdep.RunSpec{Argv: argv, Env: a.h.rootEnv()})
}

func (a *apply) dev(ctx context.Context, tool string, argv ...string) error {
	return a.runStep(ctx, tool, sysdep.RunSpec{Argv: argv, User: "dev", Dir: a.h.paths.Home(), Env: a.h.devEnv()})
}

func (a *apply) rootSh(ctx context.Context, script, what string) error {
	return a.runStep(ctx, what, sysdep.RunSpec{Argv: []string{"sh", "-c", script}, Env: a.h.rootEnv()})
}

func (a *apply) devSh(ctx context.Context, script string) error {
	return a.runStep(ctx, "setup", sysdep.RunSpec{Argv: []string{"sh", "-c", script}, User: "dev", Dir: a.h.paths.Home(), Env: a.h.devEnv()})
}

func (a *apply) runStep(ctx context.Context, what string, spec sysdep.RunSpec) error {
	cctx, cancel := context.WithTimeout(ctx, StepTimeout)
	defer cancel()
	spec.MaxOutput = 8 << 10
	res, err := a.h.run.Run(cctx, spec)
	if err != nil {
		if ctx.Err() != nil {
			// The whole apply's bound, not this step's.
			return fmt.Errorf("%s was stopped: the menu did not finish within %d min", what, int(Deadline.Minutes()))
		}
		if cctx.Err() != nil {
			return fmt.Errorf("%s did not finish within %d min", what, int(StepTimeout.Minutes()))
		}
		return fmt.Errorf("%s could not run", what)
	}
	if res.ExitCode != 0 {
		if what == "apt-get" {
			if e := aptError(append(append([]byte(nil), res.Stdout...), res.Stderr...)); e != "" {
				return fmt.Errorf("%s (%s exited %d)", e, what, res.ExitCode)
			}
		}
		return fmt.Errorf("%s exited %d", what, res.ExitCode)
	}
	return nil
}

func (a *apply) check(ctx context.Context, _ string, argv ...string) bool {
	cctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	res, err := a.h.run.Run(cctx, sysdep.RunSpec{Argv: argv, Env: a.h.rootEnv(), MaxOutput: 4 << 10})
	return err == nil && res.ExitCode == 0
}

func (a *apply) checkDev(ctx context.Context, argv ...string) bool {
	cctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	res, err := a.h.run.Run(cctx, sysdep.RunSpec{Argv: argv, User: "dev", Dir: a.h.paths.Home(), Env: a.h.devEnv(), MaxOutput: 4 << 10})
	return err == nil && res.ExitCode == 0
}

func (a *apply) writeFile(f File) error {
	mode, err := strconv.ParseUint(f.Mode, 8, 32)
	if err != nil {
		return errors.New("a recipe file has a bad mode")
	}
	path := a.h.guestPath(f.Path)
	if fileHas(path, f.Content) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errors.New("could not write its configuration")
	}
	if err := sysdep.WriteFileAtomic(path, []byte(f.Content), os.FileMode(mode), 0, 0); err != nil {
		return errors.New("could not write its configuration")
	}
	return nil
}

func (h *Handler) rootEnv() []string {
	return append(sysdep.DevEnv(h.paths, "root"), "DEBIAN_FRONTEND=noninteractive")
}

func (h *Handler) devEnv() []string {
	return append(sysdep.DevEnv(h.paths, "dev"), "MISE_YES=1")
}

// guestPath is an absolute guest path under the test root.
func (h *Handler) guestPath(p string) string {
	if h.paths.Root == "" {
		return p
	}
	return filepath.Join(h.paths.Root, p)
}

// codename is VERSION_CODENAME from /etc/os-release, noble when absent.
func (h *Handler) codename() string {
	b, err := os.ReadFile(h.paths.Etc("os-release"))
	if err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "VERSION_CODENAME="); ok {
				v = strings.Trim(v, `"`)
				if regexp.MustCompile(`^[a-z]+$`).MatchString(v) {
					return v
				}
			}
		}
	}
	return "noble"
}

func (h *Handler) loadApplied() (*applied, error) {
	b, err := os.ReadFile(h.paths.MenuApplied())
	if err != nil {
		return nil, err
	}
	var a applied
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func (h *Handler) saveApplied(a applied) error {
	if a.Items == nil {
		a.Items = []appliedItem{}
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(h.paths.MenuApplied()), 0o755); err != nil {
		return err
	}
	return sysdep.WriteFileAtomic(h.paths.MenuApplied(), append(b, '\n'), 0o600, 0, 0)
}

// manualList is every name in the apt-manual lists, sorted, each a valid
// package name. /run/repose/rw/apt-manual.list is the dpkg hook's (the
// base rewrites it whole at every dpkg run); the two legacy paths are
// read for one release (DECISIONS I-1476). guestd only reads them.
func (h *Handler) manualList() []string {
	seen := map[string]bool{}
	for _, path := range []string{h.paths.AptManual(), h.paths.AptManualLegacy(), h.paths.AptManualRotated()} {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			name := strings.TrimSpace(line)
			if ValidPackage(name) {
				seen[name] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func mergeSorted(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range append(append([]string(nil), a...), b...) {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fileHas(path, content string) bool {
	b, err := os.ReadFile(path)
	return err == nil && string(b) == content
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func capLine(s string) string {
	if len(s) <= MaxLine {
		return s
	}
	return s[:MaxLine]
}

// WaitListening polls /proc/net/tcp and tcp6 until a socket listens on
// port, or timeout passes.
func WaitListening(ctx context.Context, p sysdep.Paths, port int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		if Listening(p, port) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// Listening reports whether /proc/net/tcp or tcp6 has a listening socket
// on port.
func Listening(p sysdep.Paths, port int) bool {
	for _, name := range []string{"tcp", "tcp6"} {
		b, err := os.ReadFile(filepath.Join(p.Proc(), "net", name))
		if err != nil {
			continue
		}
		lines := strings.Split(string(b), "\n")
		if len(lines) < 2 {
			continue
		}
		for _, line := range lines[1:] {
			f := strings.Fields(line)
			if len(f) < 4 || f[3] != "0A" {
				continue
			}
			_, hexPort, ok := strings.Cut(f[1], ":")
			if !ok {
				continue
			}
			if n, err := strconv.ParseUint(hexPort, 16, 32); err == nil && int(n) == port {
				return true
			}
		}
	}
	return false
}
