package cli

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const maxSyncFileBytes = 100 << 20 // 100 MB, 07-cli.md §5.5d "skip files over 100 MB with a warning"

// SyncOptions configures the sync step of `repose run` (07-cli.md §5.5).
type SyncOptions struct {
	StashRemote   bool
	DiscardRemote bool
	Exclude       []string
	// NoRemote is a project created with --name in a directory that has no
	// git remote: there are no remote-tracking refs to carry and no
	// origin to point at. The commits travel the same way as for every
	// other project (I-150).
	NoRemote bool
	// RemoteURL is the project's normalised remote ("github.com/a/b"); the
	// guest gets an `origin` pointing at it when it has none, the way
	// guestd's SetupProject names it (I-107), so an agent can push.
	RemoteURL string
	// BeforeApply runs between the probe and the apply with the guest's
	// carry markers: `run` sends the tool logins and the carry there, so
	// the checkout lands in a guest whose git already knows the user
	// (I-150) and the carry skips what the guest already has (I-195..
	// I-197) without a round trip of its own.
	BeforeApply func(markers map[string]string) error
	// Carry, when set, is called after BeforeApply with the same markers
	// and returns the tool logins and the carry, built and not sent: the
	// apply's ssh runs them first, so `run` spends one round trip on the
	// sync's writes, not two (DECISIONS I-224). A first sync that clones
	// in the guest (I-203) sends them on their own before the clone,
	// which needs gh's login in place. Their outcome is in the summary.
	Carry func(markers map[string]string) (*credCarry, error)
	// Probe, when set, returns the probe's reply in place of running it:
	// `run` started it before the api answered (I-223). When it failed,
	// the probe runs again here.
	Probe func() ([]byte, error)
	// Env is the laptop's gitignored .env files (I-197), written after the
	// checkout in the apply's own ssh, unless the guest's marker says it
	// has exactly these.
	Env []envFile
	// FirstOnly is `repose run`'s sync (DECISIONS I-367): the checkout is
	// synced only into a guest that has no commit yet. A guest with one
	// is left alone, whatever the laptop has, and only the logins and
	// the carry go; the summary says whether the laptop had work to
	// send, for the line that names `repose sync`.
	FirstOnly bool
	// EnvLater, when set, is called after the probe for the .env files in
	// place of Env: `run` lists them (a walk of every ignored file) while
	// the probe's ssh is in flight rather than before it.
	EnvLater func() []envFile
	// EnvOff: config.toml leaves the .env files on the laptop (DECISIONS
	// I-422). None is written, and a guest copy an earlier carry wrote is
	// removed while it is byte for byte the laptop's file.
	EnvOff bool
}

func (o SyncOptions) envFiles() []envFile {
	if o.EnvLater != nil {
		return o.EnvLater()
	}
	return o.Env
}

// SyncSummary is what step 5e prints.
type SyncSummary struct {
	Modified  int
	Untracked int
	Commits   int // commits the laptop sent that the machine did not have
	Detached  bool
	Diverged  bool // the machine's branch has commits the laptop does not and they could not be merged; left alone
	// Merged: the guest's branch had commits the laptop does not, and the
	// laptop's commit was merged into it (I-574).
	Merged bool
	// GuestKept counts the guest's own changes (files dirty or untracked
	// there) that the sync left in place: none of them is a path the sync
	// writes (I-573).
	GuestKept int
	// StashedLastSync: the guest still held the previous sync's changes
	// and nothing else, and they were stashed ("repose run: last sync")
	// before this sync's were laid down (I-210).
	StashedLastSync bool
	// StashedFiles and StashRef: --stash-remote or --discard-remote moved
	// the machine's changes to that many files to its git stash, as the
	// stash commit StashRef (DECISIONS I-618).
	StashedFiles int
	StashRef     string
	Branch       string
	Head         string
	SkippedBig   []string
	// SkippedDirs are dependency and cache directories left behind
	// (defaultSkipDirs); SkippedCap counts files past maxUntrackedBytes.
	SkippedDirs []string
	SkippedCap  int
	// EnvFiles counts the .env files written; EnvKept names the ones the
	// guest kept because its copy was newer (I-197).
	EnvFiles int
	EnvKept  []string
	// EnvRemoved counts the copies removed under EnvOff; EnvLeft names
	// the ones left because they differ from the laptop's.
	EnvRemoved int
	EnvLeft    []string
	// ClonedFrom is the host the guest cloned from on a first sync
	// (I-203); CloneFailed is why it could not, when it tried.
	ClonedFrom  string
	CloneFailed string
	// Unchanged: the guest already had exactly this sync's result, and
	// only the carry (if anything) was sent (I-224).
	Unchanged bool
	// GuestAhead: the guest changed since the last sync (GuestFiles
	// uncommitted files, or commits when 0) and the laptop had nothing new
	// to send, so the checkout was left alone and only the carry went
	// (I-248).
	GuestAhead bool
	GuestFiles int
	// GuestCommits: with GuestAhead, the guest's HEAD is not the commit
	// and branch the last sync left (commits, or another branch).
	GuestCommits bool
	// GuestCommitCount is how many commits the guest's HEAD has that the
	// laptop lacks, -1 when the probe could not count them; GuestBranch
	// is the guest's branch, "" when detached (DECISIONS I-618).
	GuestCommitCount int
	GuestBranch      string
	// Copied and Carried are the outcome of SyncOptions.Carry: the logins
	// copied, as syncCredentialsAndCarry returns them.
	Copied  []string
	Carried *carryOutcome
	// SubFailed are "<path>\t<git's last line>" for shallow submodules the
	// guest could not fetch itself; SubNotSent are shallow submodules whose
	// changes on the laptop did not travel (I-263).
	SubFailed  []string
	SubNotSent []string
	// Skipped: FirstOnly found a checkout already there and left it
	// alone (I-367). LaptopAhead: the laptop has work that checkout
	// never took (Modified, Untracked and Commits count it).
	Skipped     bool
	LaptopAhead bool
	// Checkout is the checkout's directory under the guest's home;
	// Created says this sync made it, the machine's first (I-368).
	Checkout string
	Created  bool
}

// skipCheckout is syncGuest for a run whose guest already has a
// checkout (FirstOnly, I-367): the checkout is not touched, the logins
// and the carry go in one ssh (none when nothing changed), and the
// summary records whether the laptop has work the guest never took.
func skipCheckout(ctx context.Context, t sshTarget, localRepoDir string, opts SyncOptions, probe guestProbe, wantRefs []string, nothingNew bool, branch, head string, modified, untracked int) (*SyncSummary, error) {
	s := &SyncSummary{Branch: branch, Head: head, Skipped: true, Checkout: probe.checkout}
	if !nothingNew {
		n, err := countCommitsToSend(localRepoDir, wantRefs, probe.tips)
		if err != nil {
			return nil, err
		}
		s.Commits, s.Modified, s.Untracked = n, modified, untracked
		// A key that differs with nothing to count is a laptop whose
		// state matches the guest's by other means (the guest pulled the
		// same commits): nothing to send, so nothing to say.
		s.LaptopAhead = n > 0 || modified > 0 || untracked > 0
	}
	if opts.BeforeApply != nil {
		if err := opts.BeforeApply(probe.markers); err != nil {
			return nil, err
		}
	}
	if opts.Carry == nil {
		return s, nil
	}
	carry, err := opts.Carry(probe.markers)
	if err != nil {
		return nil, err
	}
	if opts.EnvOff && (probe.markers["env"] != "" || probe.envCarried) {
		// The checkout is left alone, but .env copies an earlier carry
		// wrote are removed now, not at the next `repose sync` (I-422).
		if err := carry.p.file("env/rm", envRemoveList(opts.envFiles())); err != nil {
			return nil, err
		}
		carry.p.line("(cd " + homeShell(probe.checkout) + "\n" + envRemoveScript + ")")
	}
	if carry.p.empty() {
		s.Copied, s.Carried = carry.copied, &carryOutcome{}
		return s, nil
	}
	out, err := carry.p.run(ctx, t)
	if err != nil {
		return nil, stepFailed("copy your tool logins to the machine", err, "")
	}
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if rest, ok := strings.CutPrefix(l, "#envremoved "); ok {
			_, _ = fmt.Sscanf(rest, "%d", &s.EnvRemoved)
		}
		if rest, ok := strings.CutPrefix(l, "#envleft "); ok {
			s.EnvLeft = append(s.EnvLeft, rest)
		}
	}
	s.Copied, s.Carried = carry.finish(string(out))
	return s, nil
}

// freshShellScript, on the sync that made the checkout, moves the tmux
// session's `shell` window into it: the session started in the home
// directory, since the machine had no checkout then (I-368). Only a shell
// sitting idle in the home directory is replaced; anything else is left
// as it is. "" on every other sync.
//
// On a machine that runs herdr it runs repose-herdr-workspace instead,
// which gives herdr a workspace in the new checkout (guest-conventions.md
// "herdr", Workspace), then gives that workspace the label `checkout`
// when a base before I-597 named it after the folder, and closes the
// `home` workspace when it is one idle shell (I-596): the server made it
// at the first boot, before this sync made the checkout. A base without
// the command skips the step; it runs in a subshell, since its variables
// are the scripts' usual names. The unit counts as running while it is
// activating too: the server's own workspace step may still be waiting
// for herdr then.
func freshShellScript(slug string, probe guestProbe) string {
	if !probe.created {
		return ""
	}
	w := shQuote("=" + slug + ":shell")
	return fmt.Sprintf(`repose_p=$(tmux display-message -p -t %[1]s '#{pane_current_path} #{pane_current_command}' 2>/dev/null || true)
case "$repose_p" in "$HOME bash"|"$HOME -bash"|"$HOME sh") tmux respawn-pane -k -t %[1]s -c %[2]s 2>/dev/null || true ;; esac
`, w, homeShell(probe.checkout)) + `if command -v repose-herdr-workspace >/dev/null 2>&1 && ` + herdrUpScript + `; then (
repose-herdr-workspace >/dev/null 2>&1 || true
` + herdrMainVar(slug) + herdrRenameShell + herdrTidyShell + `) || true; fi
`
}

// dirtyTreeError is 07-cli.md §6's exit 6: the guest changed files that
// this sync would write (I-573), named here. Guest changes at other paths
// never refuse; they are kept.
type dirtyTreeError struct{ files []string }

// dirtyListMax is how many of the overlapping files the refusal names;
// the rest are counted.
const dirtyListMax = 8

func (e *dirtyTreeError) Error() string {
	var b strings.Builder
	files := "1 file"
	if len(e.files) != 1 {
		files = fmt.Sprintf("%d files", len(e.files))
	}
	_, _ = fmt.Fprintf(&b, "Not synced: the machine changed %s that your laptop changed too:\n", files)
	for i, f := range e.files {
		if i == dirtyListMax && len(e.files) > dirtyListMax+1 {
			_, _ = fmt.Fprintf(&b, "  and %d more\n", len(e.files)-dirtyListMax)
			break
		}
		_, _ = fmt.Fprintf(&b, "  %s\n", f)
	}
	b.WriteString("`repose sync --stash-remote` moves the machine's changes to its git stash first.")
	return b.String()
}

// guestProbe is what the first round trip learns about the guest's
// checkout.
type guestProbe struct {
	dirty []string
	// syncedOnly is a dirty tree that is exactly what the last sync left
	// (I-210): the laptop's own diff and untracked files, no agent work.
	syncedOnly bool
	// guestFiles counts the guest's changed files (every untracked file,
	// not its directory) that are not the last sync's own by the per-path
	// record; hasGuestFiles when the probe reported it, which it does for
	// a dirty tree that is not syncedOnly (I-573).
	guestFiles    int
	hasGuestFiles bool
	// envNewer are carried .env files the guest edited since the last
	// carry while the laptop's did not change (I-215).
	envNewer []string
	// envCarried: an earlier carry wrote .env files here (its marker, or
	// #envmissing for a set the probe no longer trusts).
	envCarried bool
	tips       []string // every commit a ref (or HEAD) on the machine points at
	hasOrigin  bool
	// subTips are the commits every ref (or HEAD) of each checked-out
	// submodule in the guest points at, by path (I-263).
	subTips map[string][]string
	markers map[string]string // the carry's markers (carry.go)
	// head is the guest's HEAD commit, headRef its .git/HEAD line
	// ("ref: refs/heads/main", or a commit when detached), and syncKey
	// the key the last sync that completed recorded (I-224).
	head, headRef, syncKey string
	// syncHas: the guest still has every commit the last completed sync
	// recorded under its key (the laptop's HEAD and origin/<branch> of
	// that sync, and each bundled submodule's HEAD in that submodule), so
	// a laptop with the same key has none to send, even when no guest ref
	// points at a commit the laptop knows (I-284).
	syncHas bool
	// ahead counts the commits on the guest's HEAD that neither the
	// laptop's HEAD nor its origin/<branch> had at the last completed
	// sync; hasAhead when the probe could count them (DECISIONS I-618).
	ahead    int
	hasAhead bool
	// checkout is the checkout's directory under the home (I-368), and
	// created says this probe made it: the machine's first sync.
	checkout string
	created  bool
}

// syncedFP holds the shell functions every dirtiness judgement of the
// sync goes through (I-210).
//
// repose_git is git with the settings a carried laptop config could turn
// against the sync forced back: status.showUntrackedFiles=no would hide
// the untracked files the sync wrote, and submodule.recurse=true would
// make a stash or reset reach into a submodule, and a repository using
// Git LFS in a guest without git-lfs would fail every add and stash
// (filter.lfs.required=false stores the file as it is instead: the
// fingerprint only has to be stable). GIT_LFS_SKIP_SMUDGE=1 keeps the
// guest's git-lfs (I-525) from downloading objects, or waiting on LFS
// credentials, in a sync's checkout: LFS files stay pointer files until
// `git lfs pull`. repose_dirty is the dirty list, submodules included.
//
// repose_fp fingerprints the checkout as it stands: HEAD and the tree
// `git add -A` would record (index, working tree and every untracked file
// that is not ignored), built in a copy of the index so the real one is
// untouched and only changed files are hashed (cp -p keeps the index's
// mtime, which git's racy-clean check compares against), and with a throwaway
// object directory (the real one as its alternate) so a probe leaves no
// objects behind. A submodule is only its commit in that tree, so an edit
// inside one would not change it: each checked-out submodule (nested ones
// too, repose_sublist) adds its own HEAD and tree, so any change inside
// one changes the fingerprint (I-263; before it, any submodule change
// made it "failed"). The apply stores it after laying down the laptop's
// diff and untracked files; the next probe compares, so the tree the sync
// itself made dirty is not taken for an agent's work.
const syncedFP = `export GIT_LFS_SKIP_SMUDGE=1
repose_c="-c status.showUntrackedFiles=normal -c submodule.recurse=false -c filter.lfs.required=false"
repose_git() { git $repose_c "$@"; }
repose_dirty() { repose_git status --porcelain --ignore-submodules=none; }
repose_tab=$(printf '\t')
repose_sublist() {
  [ -f "${1:-.}/.gitmodules" ] || return 0
  git -C "${1:-.}" -c core.quotePath=false ls-files -s 2>/dev/null | while IFS= read -r l; do
    case $l in 160000\ *) ;; *) continue ;; esac
    p=${l#*"$repose_tab"}
    p=${1:+$1/}$p
    [ -e "$p/.git" ] || continue
    printf '%s\n' "$p"
    repose_sublist "$p"
  done
}
repose_fp() {
  r=$(repose_fp1)
  s=$(repose_sublist | while IFS= read -r p; do printf ' %s=%s' "$p" "$(cd "$p" && repose_fp1)"; done)
  case "$r$s" in *failed*) echo failed ;; *) printf '%s%s\n' "$r" "$s" ;; esac
}
repose_fp1() {
  i=$(mktemp)
  o=$(mktemp -d)
  x=$(git rev-parse --git-path index)
  a=$(cd "$(git rev-parse --git-path objects)" && pwd)
  if [ -f "$x" ]; then cp -p "$x" "$i"; else rm -f "$i"; fi
  # Plain git with the options, not repose_git: a shell need not export
  # assignments that precede a function call.
  if GIT_INDEX_FILE=$i GIT_OBJECT_DIRECTORY=$o GIT_ALTERNATE_OBJECT_DIRECTORIES=$a git $repose_c add -A >/dev/null 2>&1 &&
     tr=$(GIT_INDEX_FILE=$i GIT_OBJECT_DIRECTORY=$o GIT_ALTERNATE_OBJECT_DIRECTORIES=$a git $repose_c write-tree 2>/dev/null); then
    printf '%s %s\n' "$(git rev-parse -q --verify HEAD || echo none)" "$tr"
  else
    echo failed
  fi
  rm -rf "$i" "$o"
}
repose_synced=$(git rev-parse --git-path repose-synced)
`

// probeScript creates the checkout if it is missing (an empty guest, or
// one whose SetupProject has not run), then reports the dirty list,
// whether that dirt is only what the last sync wrote (I-210), the
// commits the guest has refs to, and whether it has an origin. One ssh.
// probeScript is the sync's first ssh. It finds the checkout (checkoutVar)
// and, on a machine with none, makes it at ~/<want> (I-368) and says so.
func probeScript(slug, want, extra string) string {
	return fmt.Sprintf(`set -e
%s%s%scd "$repose_co"
[ -d .git ] || git init -q
%s%s
%s
st=$(repose_dirty)
echo '#status'
[ -z "$st" ] || printf '%%s\n' "$st"
echo '#synced'
if [ -n "$st" ] && [ -s "$repose_synced" ] && [ "$(repose_fp)" = "$(cat "$repose_synced")" ]; then echo yes
elif [ -n "$st" ]; then printf '#guestfiles %%s\n' "$(repose_guestcount)"; fi
echo '#tips'
git for-each-ref --format='%%(objectname)'
repose_sublist | while IFS= read -r p; do printf '#sub %%s\n' "$p"; git -C "$p" for-each-ref --format='%%(objectname)'; git -C "$p" rev-parse -q --verify HEAD || true; done
echo '#head'
git rev-parse -q --verify HEAD || true
[ -f .git/HEAD ] && IFS= read -r repose_h < .git/HEAD && printf '#headref %%s\n' "$repose_h"
[ -f "$repose_synced-key" ] && IFS= read -r repose_k < "$repose_synced-key" && printf '#synckey %%s\n' "$repose_k"
[ -f "$repose_synced-key" ] && git rev-parse -q --verify HEAD >/dev/null && printf '#ahead %%s\n' "$(tail -n +2 "$repose_synced-key" | while IFS=' ' read -r c p; do [ -n "$p" ] || printf '^%%s\n' "$c"; done | git rev-list --count --stdin HEAD 2>/dev/null || true)"
[ -f "$repose_synced-key" ] && tail -n +2 "$repose_synced-key" | { repose_n=0; while IFS=' ' read -r c p; do repose_n=1; if [ -n "$p" ]; then git -C "$p" cat-file -e "$c^{commit}" 2>/dev/null || exit 1; else git cat-file -e "$c^{commit}" 2>/dev/null || exit 1; fi; done; [ "$repose_n" = 1 ]; } && echo '#synchas'
echo '#origin'
git remote get-url origin >/dev/null 2>&1 && echo yes || true
%s%s`, checkoutVar(slug, extra), checkoutCreate(slug, want, extra), checkoutReport, syncedFP, syncGuardFns, envPathsCheck, credsMissingScript(), markerScript())
}

// credsMissingScript prints `#credsmissing` when a login file the last
// copy wrote is gone from the guest, so the logins go again.
func credsMissingScript() string {
	return fmt.Sprintf("if [ -f %s ]; then while IFS= read -r p; do [ -e \"$p\" ] || { echo '#credsmissing'; break; }; done < %s; fi\n", credsPathsFile, credsPathsFile)
}

func parseProbe(out string) guestProbe {
	p := guestProbe{markers: parseMarkers(out)}
	section, sub := "", ""
	seen := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "#marker ") {
			continue
		}
		if l == "#created" {
			p.created = true
			continue
		}
		if l == "#checkout" || strings.HasPrefix(l, "#checkout ") {
			p.checkout = strings.TrimSpace(strings.TrimPrefix(l, "#checkout"))
			continue
		}
		if rest, ok := strings.CutPrefix(l, "#envnewer "); ok {
			p.envNewer = append(p.envNewer, rest)
			continue
		}
		if rest, ok := strings.CutPrefix(l, "#headref "); ok {
			p.headRef = strings.TrimSpace(rest)
			continue
		}
		if rest, ok := strings.CutPrefix(l, "#synckey "); ok {
			p.syncKey = strings.TrimSpace(rest)
			continue
		}
		if rest, ok := strings.CutPrefix(l, "#guestfiles "); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil {
				p.guestFiles, p.hasGuestFiles = n, true
			}
			continue
		}
		if rest, ok := strings.CutPrefix(l, "#ahead "); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil {
				p.ahead, p.hasAhead = n, true
			}
			continue
		}
		if l == "#synchas" {
			p.syncHas = true
			continue
		}
		if rest, ok := strings.CutPrefix(l, "#sub "); ok {
			section, sub = "#sub", rest
			if p.subTips == nil {
				p.subTips = map[string][]string{}
			}
			continue
		}
		if l == "#credsmissing" {
			delete(p.markers, credsMarker) // a login file is gone: send them again
			continue
		}
		if l == "#envmissing" {
			delete(p.markers, "env") // a written file is gone: send the set again
			p.envCarried = true
			continue
		}
		switch l {
		case "#status", "#synced", "#tips", "#head", "#origin":
			section = l
			continue
		}
		if strings.TrimSpace(l) == "" {
			continue
		}
		switch section {
		case "#status":
			p.dirty = append(p.dirty, l)
		case "#synced":
			p.syncedOnly = strings.TrimSpace(l) == "yes"
		case "#tips", "#head":
			t := strings.TrimSpace(l)
			if section == "#head" {
				p.head = t
			}
			if !seen[t] {
				seen[t] = true
				p.tips = append(p.tips, t)
			}
		case "#origin":
			p.hasOrigin = strings.TrimSpace(l) == "yes"
		case "#sub":
			p.subTips[sub] = append(p.subTips[sub], strings.TrimSpace(l))
		}
	}
	return p
}

// syncRoot is the directory a run syncs from cwd: the repository's root,
// or cwd itself outside one (which syncPrecheck then refuses).
func syncRoot(cwd string) string {
	if root := gitRepoRoot(cwd); root != "" {
		return root
	}
	return cwd
}

// syncPrecheck is every refusal the sync makes of the laptop's checkout
// alone: not a git repository, no commit yet, a shallow clone. run and
// sync call it before they create or start anything (DECISIONS I-353),
// so the refusal no longer comes after a machine has booted for nothing;
// syncGuest calls it again for its other callers.
//
// offerNoSync is a `repose run`, which has --no-sync; `repose sync` has
// not, so its refusals do not name it (DECISIONS I-628).
func syncPrecheck(localRepoDir string, offerNoSync bool) error {
	or := ""
	if offerNoSync {
		or = ", or pass --no-sync"
	}
	if gitRepoRoot(localRepoDir) == "" {
		return exitf(ExitUsage, "repose syncs your work through git, and %s is not a git checkout. Run `git init && git add -A && git commit -m init` there first%s.", localRepoDir, or)
	}
	if _, err := gitHeadCommit(localRepoDir); err != nil {
		return errNoCommits(offerNoSync)
	}
	if shallow, _ := gitCmd(localRepoDir, "rev-parse", "--is-shallow-repository"); shallow == "true" {
		return exitf(ExitUsage, "Your checkout is a shallow clone, so repose cannot send its history to the machine. Run `git fetch --unshallow` and try again%s.", or)
	}
	return nil
}

func errNoCommits(offerNoSync bool) error {
	or := ""
	if offerNoSync {
		or = ", or pass --no-sync"
	}
	return exitf(ExitUsage, "Your checkout has no commits yet, so there is nothing to sync. Commit once (`git add -A && git commit -m init`) and run again%s.", or)
}

// syncGuest runs the whole sync step against localRepoDir's git state
// (I-150): the laptop sends the commits itself, as a git bundle of what
// the guest lacks, so the guest never needs credentials for origin. Two
// ssh round trips: a probe, then one payload (bundle, diff, untracked
// tar) and one script that applies it.
func syncGuest(ctx context.Context, t sshTarget, localRepoDir, slug string, opts SyncOptions) (*SyncSummary, error) {
	if err := syncPrecheck(localRepoDir, false); err != nil {
		return nil, err
	}
	head, err := gitHeadCommit(localRepoDir)
	if err != nil {
		return nil, errNoCommits(false)
	}
	branch, err := gitCurrentBranch(localRepoDir)
	if err != nil {
		return nil, stepFailed("read the current branch", err, "")
	}
	timingf("sync local checks done")

	var out []byte
	if opts.Probe != nil {
		out, err = opts.Probe()
	}
	if opts.Probe == nil || err != nil {
		out, err = runSSH(ctx, t, probeScript(slug, checkoutName(localRepoDir), t.Checkout), nil)
	}
	if err != nil {
		if nc := noCheckoutError(err); nc != nil {
			return nil, nc
		}
		return nil, stepFailed("read the machine's checkout", err, "")
	}
	probe := parseProbe(string(out))

	// What the laptop would send, and its key, before anything is sent:
	// all of it is local, and whether the laptop has anything new since
	// the sync the guest last took decides what a dirty guest tree means
	// (I-248).
	// What the guest should end up with: HEAD's commit, and, for a
	// project with a remote, the laptop's view of origin/<branch> so the
	// agent's `git status` and `git push` know where origin stands.
	track := ""
	if branch != "" && !opts.NoRemote {
		if sha, err := gitCmd(localRepoDir, "rev-parse", "-q", "--verify", "refs/remotes/origin/"+branch); err == nil {
			track = sha
		}
	}
	wantRefs := []string{"HEAD"}
	if track != "" {
		wantRefs = append(wantRefs, "refs/remotes/origin/"+branch)
	}
	localDirty, err := gitTrackedDirty(localRepoDir)
	if err != nil {
		return nil, stepFailed("read your working tree", err, "")
	}
	key := sha256.New()
	_, _ = fmt.Fprintf(key, "%s\x00%s\x00%s\x00%s\x00%s\x00%v\x00", syncKeyVersion, head, branch, track, opts.RemoteURL, opts.NoRemote)
	stagedDiff, unstagedDiff, err := gitDiffsBinary(localRepoDir)
	if err != nil {
		return nil, stepFailed("diff your working tree", err, "")
	}
	_, _ = fmt.Fprintf(key, "%d\x00%s%d\x00%s", len(stagedDiff), stagedDiff, len(unstagedDiff), unstagedDiff)
	noDiff := strings.TrimSpace(stagedDiff) == "" && strings.TrimSpace(unstagedDiff) == ""
	// Submodules travel on their own (I-263): their state goes in the key,
	// their untracked files in the one untracked tar.
	subs, err := readLaptopSubs(localRepoDir)
	if err != nil {
		return nil, err
	}
	superOps, err := gitlinkIndexOps(localRepoDir)
	if err != nil {
		return nil, stepFailed("read your submodules", err, "")
	}
	hashSubs(key, subs)
	_, _ = fmt.Fprintf(key, "\x00%d\x00%s", len(superOps), superOps)
	untracked, err := gitUntrackedFiles(localRepoDir)
	if err != nil {
		return nil, stepFailed("list your untracked files", err, "")
	}
	subFiles, err := subUntracked(localRepoDir, subs)
	if err != nil {
		return nil, err
	}
	untracked = append(untracked, subFiles...)
	untracked, skipped, skippedDirs, skippedCap, err := filterUntracked(localRepoDir, untracked, opts.Exclude)
	if err != nil {
		return nil, err
	}
	var untrackedTar []byte
	if len(untracked) > 0 {
		if untrackedTar, err = tarFiles(localRepoDir, untracked); err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintf(key, "\x00%d\x00", len(untrackedTar))
		_, _ = key.Write(untrackedTar)
	}
	syncKey := hex.EncodeToString(key.Sum(nil))

	// nothingNew: the guest took exactly this sync last time and has every
	// commit it would send, so the laptop has nothing to lay over what
	// is there. Whatever changed in the guest since (an agent's edits or
	// commits) is left alone and the run attaches (I-248); the flags
	// still force a sync.
	subCommits, err := planSubCommits(localRepoDir, subs, probe.subTips)
	if err != nil {
		return nil, err
	}
	nothingNew := false
	if !opts.StashRemote && !opts.DiscardRemote && probe.syncKey != "" && probe.syncKey == syncKey {
		// The same key means the same HEAD and origin/<branch> as the
		// last completed sync, whose commits the guest recorded and still
		// has (syncHas). The count over the guest's tips is the fallback
		// for a guest whose key file predates that record; on its own it
		// cannot see a guest that pulled past every commit the laptop
		// knows (I-284).
		if probe.syncHas {
			nothingNew = true
		} else {
			n, err := countCommitsToSend(localRepoDir, wantRefs, probe.tips)
			if err != nil {
				return nil, err
			}
			nothingNew = n == 0 && subCommits == 0
		}
	}
	if opts.FirstOnly && len(probe.tips) > 0 {
		return skipCheckout(ctx, t, localRepoDir, opts, probe, wantRefs, nothingNew, branch, head, len(localDirty), len(untracked))
	}
	// A guest with its own changes is not refused here: the apply finds
	// which of them are at paths this sync writes, against the tree as it
	// is then, and refuses only for those (I-573).
	guestChanged := len(probe.dirty) > 0 && !probe.syncedOnly
	if opts.BeforeApply != nil {
		if err := opts.BeforeApply(probe.markers); err != nil {
			return nil, err
		}
	}
	endPrep := timeSpan("sync payload build")
	// The first sync of a large GitHub repository clones in the guest,
	// after the credentials (gh's helper) are in place (I-203).
	cloneURL := ""
	if len(probe.tips) == 0 && !opts.NoRemote {
		if url := hybridCloneURL(opts.RemoteURL); url != "" && gitPackKiB(localRepoDir) >= hybridThresholdKiB {
			cloneURL = url
		}
	}
	var carry *credCarry
	var carried *carryOutcome
	var copied []string
	if opts.Carry != nil {
		if carry, err = opts.Carry(probe.markers); err != nil {
			return nil, err
		}
		if carry.p.empty() {
			copied, carried, carry = carry.copied, &carryOutcome{}, nil
		} else if cloneURL != "" {
			out, err := carry.p.run(ctx, t)
			if err != nil {
				return nil, stepFailed("copy your tool logins to the machine", err, "")
			}
			copied, carried = carry.finish(string(out))
			carry = nil
		}
	}
	var cloned, cloneFailed string
	if cloneURL != "" {
		{
			url := cloneURL
			tips, ok, why, err := hybridFetch(ctx, t, probe.checkout, url, laptopBases(localRepoDir))
			if err != nil {
				return nil, err
			}
			if ok {
				probe.tips = tips
				cloned = remoteHost(opts.RemoteURL)
			} else {
				cloneFailed = why
			}
		}
	}

	revs, err := revsToSend(localRepoDir, wantRefs, probe.tips)
	if err != nil {
		return nil, err
	}
	summary := &SyncSummary{Branch: branch, Head: head, ClonedFrom: cloned, CloneFailed: cloneFailed, Copied: copied, Carried: carried, Checkout: probe.checkout, Created: probe.created}
	if summary.Commits, err = countRevs(localRepoDir, revs); err != nil {
		return nil, err
	}
	superCommits := summary.Commits
	summary.Commits += subCommits

	payload, err := os.CreateTemp("", "repose-sync-*.tar")
	if err != nil {
		return nil, err
	}
	defer func() { _ = payload.Close(); _ = os.Remove(payload.Name()) }()
	tw := tar.NewWriter(payload)

	var bundleRefs []string
	if superCommits > 0 {
		bundle, err := os.CreateTemp("", "repose-bundle-*")
		if err != nil {
			return nil, err
		}
		bundlePath := bundle.Name()
		_ = bundle.Close()
		defer func() { _ = os.Remove(bundlePath) }()
		if _, err := gitCmdStdin(localRepoDir, strings.Join(revs, "\n")+"\n", "bundle", "create", "-q", bundlePath, "--stdin"); err != nil {
			return nil, stepFailed("pack your commits for the machine (git bundle)", err, "")
		}
		// A ref whose commit the guest already has is left out of the
		// bundle; fetch exactly the ones it carries.
		heads, err := gitCmd(localRepoDir, "bundle", "list-heads", bundlePath)
		if err != nil {
			return nil, stepFailed("read the bundle back", err, "")
		}
		bundleRefs = nil
		for _, l := range nonEmptyLines(heads) {
			if _, ref, ok := strings.Cut(l, " "); ok {
				bundleRefs = append(bundleRefs, ref)
			}
		}
		if err := tarAddFile(tw, "bundle", bundlePath); err != nil {
			return nil, err
		}
	}

	summary.Modified = len(localDirty)
	for _, s := range subs {
		summary.Modified += s.Dirty
		if s.Shallow && s.Dirty > 0 {
			summary.SubNotSent = append(summary.SubNotSent, s.Path)
		}
	}
	subScript, err := addSubsToApply(tw, localRepoDir, subs)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(stagedDiff) != "" {
		if err := tarAddBytes(tw, "staged.diff", []byte(stagedDiff)); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(unstagedDiff) != "" {
		if err := tarAddBytes(tw, "unstaged.diff", []byte(unstagedDiff)); err != nil {
			return nil, err
		}
	}
	// Every path the laptop's own work writes in the guest, for the
	// apply's overlap check and its record of what this sync left (I-573):
	// both sides of the diffs, the untracked files, and each bundled
	// submodule (checked out and patched as a whole).
	diffNames, err := gitDiffNames(localRepoDir)
	if err != nil {
		return nil, stepFailed("diff your working tree", err, "")
	}
	var pathsZ bytes.Buffer
	for _, group := range [][]string{diffNames, untracked} {
		for _, n := range group {
			pathsZ.WriteString(filepath.ToSlash(n) + "\x00")
		}
	}
	for _, sb := range subs {
		if !sb.Shallow {
			pathsZ.WriteString(sb.Path + "\x00")
		}
	}
	if pathsZ.Len() > 0 {
		if err := tarAddBytes(tw, "paths", pathsZ.Bytes()); err != nil {
			return nil, err
		}
	}
	// The identity the carry gives the guest's git (the checkout's own
	// user.name and user.email, as creds.go reads them), for the merge of
	// a diverged branch, which runs before the carry could matter
	// (I-574). In the payload, never on a command line.
	if name, email := gitIdentity(localRepoDir); name != "" || email != "" {
		if err := tarAddBytes(tw, "ident", []byte(name+"\n"+email+"\n")); err != nil {
			return nil, err
		}
	}
	summary.SkippedBig = skipped
	summary.SkippedDirs = skippedDirs
	summary.SkippedCap = skippedCap
	if len(untracked) > 0 {
		if err := tarAddBytes(tw, "untracked.tar", untrackedTar); err != nil {
			return nil, err
		}
		summary.Untracked = len(untracked)
	}
	carryScript := ""
	if carry != nil {
		if carryScript, err = addCarryToApply(tw, carry.p); err != nil {
			return nil, err
		}
	}
	var envScript string
	if opts.EnvOff {
		envScript, err = addEnvRemoveToApply(tw, opts.envFiles(), probe.markers["env"] != "" || probe.envCarried)
	} else {
		envScript, err = addEnvToApply(tw, opts.envFiles(), probe.markers)
	}
	if err != nil {
		return nil, err
	}
	if envScript == "" && !opts.EnvOff {
		// Not sent, so the apply will not say which guest copies it kept:
		// the probe's report of the ones edited since is that line.
		summary.EnvKept = append(summary.EnvKept, probe.envNewer...)
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if _, err := payload.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	endPrep()
	if st, err := payload.Stat(); err == nil {
		timingf("sync payload %dB commits=%d", st.Size(), summary.Commits)
	}
	subsClean := superOps == ""
	for _, s := range subs {
		subsClean = subsClean && !s.changed()
	}
	asLeft := guestAsLastSyncLeft(probe, syncKey, head, branch, summary.Commits, noDiff && len(untracked) == 0 && subsClean)
	summary.Unchanged = !opts.StashRemote && !opts.DiscardRemote && cloned == "" && envScript == "" && asLeft &&
		(probe.hasOrigin || opts.NoRemote || originURLFor(opts.RemoteURL) == "")
	if nothingNew && !asLeft && cloned == "" {
		// The guest moved on from the last sync (an agent's edits,
		// commits or branch) and the laptop has nothing new: leave the
		// guest's work where it is (I-248).
		summary.GuestAhead = true
		if guestChanged {
			// The guest's own changes, not the last sync's still in the
			// tree beside them (I-573).
			summary.GuestFiles = len(probe.dirty)
			if probe.hasGuestFiles {
				summary.GuestFiles = probe.guestFiles
			}
		}
		summary.GuestCommits = probe.head != head || probe.headRef != wantHeadRef(head, branch)
		summary.GuestCommitCount = -1
		if summary.GuestCommits {
			summary.GuestBranch = strings.TrimPrefix(probe.headRef, "ref: refs/heads/")
			if summary.GuestBranch == probe.headRef {
				summary.GuestBranch = "" // detached
			}
			if known, err := commitsKnownLocally(localRepoDir, []string{probe.head}); err == nil && len(known) == 1 {
				// The laptop fetched the guest's HEAD already.
				summary.GuestCommitCount = 0
			} else if probe.hasAhead {
				summary.GuestCommitCount = probe.ahead
			}
		}
	}
	var script string
	if summary.Unchanged || summary.GuestAhead {
		// Nothing the apply would change: the guest's tree is what the
		// last sync left, and the laptop sends exactly what it sent then
		// (I-224). Only the carry, when it has something, still goes.
		timingf("sync: unchanged since the last sync")
		if carryScript == "" {
			return summary, nil
		}
		script = "set -e\n" + applyUnpack + carryScript
	} else {
		// The key is cleared before the checkout is touched and written
		// once the apply has finished, so a sync that stopped half way
		// never matches.
		script = applyScript(probe.checkout, head, branch, track, bundleRefs, len(bundleRefs) > 0, opts, probe, subScript+superOps) + envScript + recordSyncedScript + freshShellScript(slug, probe) +
			fmt.Sprintf("printf '%%s\\n' %s > \"$repose_synced-key\"\n", syncedKeyLines(syncKey, head, track, subs))
		// After the overlap check, before anything touches the checkout:
		// the stash and the merge below need the identity the git part
		// carries, and a refused sync copies nothing.
		script = strings.Replace(script, applyCarryHere, carryScript+": > \"$repose_synced-key\"\n", 1)
	}
	res, err := runSSH(ctx, t, script, payload)
	if err != nil {
		if se, ok := err.(*sshError); ok && strings.Contains(se.Stderr, carryFailed) {
			return nil, stepFailed("copy your tool logins to the machine", err, "")
		}
		if se, ok := err.(*sshError); ok && strings.Contains(se.Stderr, syncBusy) {
			return nil, busyError(se.Stderr, opts)
		}
		if se, ok := err.(*sshError); ok && strings.Contains(se.Stderr, syncOverlap) {
			// The guest changed paths this sync writes: nothing was
			// touched, and the refusal names only those (I-573).
			return nil, &exitError{code: ExitDirtyRemoteTree, msg: (&dirtyTreeError{files: overlapFiles(se.Stderr)}).Error()}
		}
		if se, ok := err.(*sshError); ok && (strings.Contains(se.Stderr, "patch does not apply") || strings.Contains(se.Stderr, "patch failed")) {
			return nil, stepFailed("apply your uncommitted changes on the machine", err, "Commit or stash them on the laptop and run again.")
		}
		return nil, stepFailed("sync your checkout to the machine", err, "")
	}
	var carryOut strings.Builder
	for _, l := range strings.Split(string(res), "\n") {
		l = strings.TrimSpace(l)
		if rest, ok := strings.CutPrefix(l, carryLinePrefix); ok {
			carryOut.WriteString(rest + "\n")
			continue
		}
		switch l {
		case "#detached":
			summary.Detached = true
		case "#diverged":
			summary.Detached, summary.Diverged = true, true
		case "#merged":
			summary.Merged = true
		case "#stashedsync":
			summary.StashedLastSync = true
		}
		if rest, ok := strings.CutPrefix(l, "#stashed "); ok {
			_, _ = fmt.Sscanf(rest, "%d %s", &summary.StashedFiles, &summary.StashRef)
		}
		if rest, ok := strings.CutPrefix(l, "#guestkept "); ok {
			_, _ = fmt.Sscanf(rest, "%d", &summary.GuestKept)
		}
		if rest, ok := strings.CutPrefix(l, "#subfailed "); ok {
			summary.SubFailed = append(summary.SubFailed, rest)
		}
		if rest, ok := strings.CutPrefix(l, "#kept "); ok {
			summary.EnvKept = append(summary.EnvKept, rest)
		}
		if rest, ok := strings.CutPrefix(l, "#envfiles "); ok {
			_, _ = fmt.Sscanf(rest, "%d", &summary.EnvFiles)
		}
		if rest, ok := strings.CutPrefix(l, "#envremoved "); ok {
			_, _ = fmt.Sscanf(rest, "%d", &summary.EnvRemoved)
		}
		if rest, ok := strings.CutPrefix(l, "#envleft "); ok {
			summary.EnvLeft = append(summary.EnvLeft, rest)
		}
	}
	if carry != nil {
		summary.Copied, summary.Carried = carry.finish(carryOut.String())
	}
	return summary, nil
}

// revsToSend is the rev-list input for what the laptop sends: wantRefs,
// minus every guest tip this checkout also has.
func revsToSend(localRepoDir string, wantRefs, tips []string) ([]string, error) {
	known, err := commitsKnownLocally(localRepoDir, tips)
	if err != nil {
		return nil, stepFailed("compare commits with the machine", err, "")
	}
	revs := append([]string(nil), wantRefs...)
	for _, k := range known {
		revs = append(revs, "^"+k)
	}
	return revs, nil
}

func countRevs(localRepoDir string, revs []string) (int, error) {
	out, err := gitCmdStdin(localRepoDir, strings.Join(revs, "\n")+"\n", "rev-list", "--count", "--stdin")
	if err != nil {
		return 0, stepFailed("count the commits to send", err, "")
	}
	n := 0
	_, _ = fmt.Sscanf(strings.TrimSpace(out), "%d", &n)
	return n, nil
}

func countCommitsToSend(localRepoDir string, wantRefs, tips []string) (int, error) {
	revs, err := revsToSend(localRepoDir, wantRefs, tips)
	if err != nil {
		return 0, err
	}
	return countRevs(localRepoDir, revs)
}

// syncedKeyLines is what an apply writes to .git/repose-synced-key, as
// quoted printf arguments: the key, then the commits this sync delivers
// for the next probe to check are still there (I-284): HEAD, the
// laptop's origin/<branch>, and "<sha> <path>" for each bundled
// submodule's HEAD, checked inside that submodule. A shallow submodule
// is fetched by the guest, not bundled, and is not listed. The key covers
// every one of these, so an equal key names the same commits.
func syncedKeyLines(key, head, track string, subs []laptopSub) string {
	lines := []string{key, head}
	if track != "" {
		lines = append(lines, track)
	}
	for _, s := range subs {
		if !s.Shallow && s.Head != "" {
			lines = append(lines, s.Head+" "+s.Path)
		}
	}
	for i, l := range lines {
		lines[i] = shQuote(l)
	}
	return strings.Join(lines, " ")
}

// syncKeyVersion is folded into the sync key; a change to what an apply
// does bumps it, so no guest skips the first apply of the new shape.
const syncKeyVersion = "sync-3"

// guestAsLastSyncLeft reports whether the probe found the guest exactly
// as the last completed sync left it, and that sync sent what this one
// would (the same key): no commits to send, HEAD on the same commit and
// branch, and the tree either clean (a laptop tree with no changes) or
// dirty with nothing but the last sync's changes (I-210's fingerprint).
func guestAsLastSyncLeft(p guestProbe, key, head, branch string, commits int, laptopClean bool) bool {
	if p.syncKey != key || commits != 0 || p.head != head {
		return false
	}
	if p.headRef != wantHeadRef(head, branch) {
		return false
	}
	if laptopClean {
		return len(p.dirty) == 0
	}
	return len(p.dirty) > 0 && p.syncedOnly
}

// wantHeadRef is the .git/HEAD line a sync of head on branch leaves.
func wantHeadRef(head, branch string) string {
	if branch != "" {
		return "ref: refs/heads/" + branch
	}
	return head
}

// applyUnpack is the apply's unpack of its payload; the carry's script
// goes right after it.
const applyUnpack = "t=$(mktemp -d)\ntrap 'rm -rf \"$t\"' EXIT\ntar -x -C \"$t\"\n"

// carryFailed is the apply's stderr when the logins or the carry's
// top-level lines failed, the case in which the carry's own ssh used to
// fail and the run stopped before the sync.
const carryFailed = "repose: could not copy the tool logins"

// carryLinePrefix marks the carry's reply lines inside the apply's.
const carryLinePrefix = "#carry "

// addCarryToApply puts the carry's payload (script and tar) into the
// apply's tar and returns the lines that run it: the same script, fed
// the same tar, as its own ssh would have run, its reply lines prefixed
// so they do not mix with the apply's (I-224).
func addCarryToApply(tw *tar.Writer, p *guestPayload) (string, error) {
	if err := p.tw.Close(); err != nil {
		return "", err
	}
	if observePayload != nil {
		observePayload(p.script.String(), p.buf.Bytes())
	}
	if err := tarAddBytes(tw, "carry.sh", []byte(p.script.String())); err != nil {
		return "", err
	}
	if err := tarAddBytes(tw, "carry.tar", p.buf.Bytes()); err != nil {
		return "", err
	}
	return fmt.Sprintf(`bash "$t/carry.sh" < "$t/carry.tar" > "$t/carry.out" || { echo %s >&2; exit 1; }
while IFS= read -r l || [ -n "$l" ]; do printf '%s%%s
' "$l"; done < "$t/carry.out"
`, shQuote(carryFailed), carryLinePrefix), nil
}

// applyScript is the second round trip: unpack the payload, fetch the
// bundle, decide between a fast-forward, a merge and a detached checkout,
// refuse if the guest changed a path the sync writes, set the last
// sync's own changes (or, if asked, all of the guest's) aside, move the
// refs, check out, and lay the staged and unstaged diffs and the
// untracked files on top. The guest's other changes stay where they are.
func applyScript(dir, head, branch, track string, bundleRefs []string, hasBundle bool, opts SyncOptions, probe guestProbe, subScript string) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "set -e\ncd %s\n", homeShell(dir))
	b.WriteString(syncedFP)
	b.WriteString(syncGuardFns)
	b.WriteString(applyUnpack)
	if hasBundle {
		// Objects only: no ref moves and the tree is untouched, so the
		// overlap check below can read the commit it would check out.
		_, _ = fmt.Fprintf(&b, "git fetch -q \"$t/bundle\" %s\n", strings.Join(bundleRefs, " "))
	}
	// A merge, rebase, cherry-pick, revert, am or bisect of the guest's
	// own in progress: nothing the sync does can keep it (a stash cannot
	// hold one), so the apply refuses before anything is touched, unless
	// --discard-remote asked for the guest's state to go (I-573).
	if opts.DiscardRemote {
		b.WriteString("repose_wasbusy=; if repose_busy >/dev/null; then repose_wasbusy=yes; fi\n")
	} else {
		_, _ = fmt.Fprintf(&b, "if repose_state=$(repose_busy); then { echo %s; printf '#busy %%s\\n' \"$repose_state\"; } >&2; exit 3; fi\n", shQuote(syncBusy))
	}
	// What the checkout moves to: the laptop's commit, or, when the
	// guest's branch has commits the laptop lacks, the tree of a merge of
	// the two when git can make it cleanly and the guest's commits leave
	// the laptop's uncommitted paths alone (I-574).
	_, _ = fmt.Fprintf(&b, "repose_target=%s\nrepose_merge=\nrepose_merging=\nrepose_via=\n", head)
	if branch != "" {
		br := shQuote("refs/heads/" + branch)
		// The committer is the identity this sync carries (the laptop's
		// user.name and user.email, in $t/ident), so a guest that gets it
		// only from this sync's carry, which runs after the overlap check,
		// can still merge. The guest need not be on that branch: the sync
		// moves the checkout to the laptop's branch either way, so a
		// guest on another branch (or detached) is switched to it first,
		// and the overlap check also counts the paths that switch
		// changes ($repose_via), so git never refuses it over a file the
		// guest changed.
		_, _ = fmt.Fprintf(&b, `if cur=$(git rev-parse -q --verify %[1]s) && ! git merge-base --is-ancestor "$cur" %[2]s && repose_with_ident git var GIT_COMMITTER_IDENT >/dev/null 2>&1; then
  if repose_mt=$(git merge-tree --write-tree --no-messages "$cur" %[2]s 2>/dev/null) && repose_mb=$(git merge-base "$cur" %[2]s) && ! repose_touches "$repose_mb" "$cur"; then
    repose_target=$(printf '%%s\n' "$repose_mt" | head -n 1)
    repose_merge=yes
    if [ "$(git symbolic-ref -q HEAD || true)" != %[1]s ]; then repose_via=$cur; fi
  fi
fi
`, br, head)
	}
	// The probe found nothing but the last sync's own changes: if the
	// tree is still exactly as that sync left it (its fingerprint, which
	// a guest synced by an older CLI has without the per-path record),
	// all of it goes to the stash and there is nothing to check.
	whole := len(probe.dirty) > 0 && probe.syncedOnly && !opts.StashRemote && !opts.DiscardRemote
	if whole {
		b.WriteString("repose_whole=\nif [ \"$(repose_fp)\" = \"$(cat \"$repose_synced\" 2>/dev/null)\" ]; then repose_whole=yes; fi\n")
	}
	if !opts.StashRemote && !opts.DiscardRemote {
		// Exit 3 naming each path the guest changed that the sync would
		// write; nothing has been touched yet (I-573).
		if whole {
			b.WriteString("[ -n \"$repose_whole\" ] || ")
		}
		b.WriteString("repose_guard \"$repose_target\" \"$repose_via\"\n")
	}
	b.WriteString(applyCarryHere)
	switch {
	case opts.DiscardRemote:
		// The git operation in progress ends where HEAD is, and every
		// change goes to the stash before the tree is cleared, so nothing
		// the agent wrote is lost (DECISIONS I-618). A merge's unmerged
		// index entries would refuse the stash: a mixed reset clears them
		// and leaves the files as they are.
		b.WriteString("if [ -n \"$repose_wasbusy\" ]; then repose_unbusy; git merge --quit >/dev/null 2>&1 || true; if git rev-parse -q --verify HEAD >/dev/null; then repose_git reset -q; fi; fi\n")
		b.WriteString(inEverySub(stashAllScript(stashDiscardMsg, false) + "if git rev-parse -q --verify HEAD >/dev/null; then repose_git reset -q --hard; fi; repose_git clean -fdq"))
		b.WriteString(stashAllScript(stashDiscardMsg, true))
		b.WriteString("if git rev-parse -q --verify HEAD >/dev/null; then repose_git reset -q --hard; fi\nrepose_git clean -fdq\n")
	case opts.StashRemote:
		b.WriteString(inEverySub(stashAllScript(stashRemoteMsg, false)))
		b.WriteString(stashAllScript(stashRemoteMsg, true))
	case whole:
		// The last sync's own changes and nothing else, which the laptop
		// still has (or has replaced): stashed whole, not thrown away, so
		// a write that lands after this check is still recoverable. If
		// the tree moved since the probe, only the paths the last sync
		// left as they were go to the stash (I-210, I-573).
		b.WriteString("if [ -n \"$repose_whole\" ]; then\n")
		b.WriteString(inEverySub("repose_git stash push -q -u -m 'repose run: last sync'\n" + pruneSyncStashes))
		b.WriteString("repose_git stash push -q -u -m 'repose run: last sync'\necho '#stashedsync'\n" + pruneSyncStashes)
		b.WriteString("else\nrepose_stash_own\nfi\n")
	default:
		b.WriteString("repose_stash_own\n")
	}
	if track != "" {
		ref := shQuote("refs/remotes/origin/" + branch)
		_, _ = fmt.Fprintf(&b, "if ! cur=$(git rev-parse -q --verify %s) || git merge-base --is-ancestor \"$cur\" %s; then git update-ref %s %s; fi\n", ref, track, ref, track)
	}
	if !opts.NoRemote && !probe.hasOrigin && opts.RemoteURL != "" {
		if u := originURLFor(opts.RemoteURL); u != "" {
			_, _ = fmt.Fprintf(&b, "git remote add origin %s 2>/dev/null || true\n", shQuote(u))
		}
	}
	if branch == "" {
		_, _ = fmt.Fprintf(&b, "repose_git checkout -q --detach %s\necho '#detached'\n", head)
	} else {
		br := shQuote(branch)
		// A branch behind the laptop is fast-forwarded; one with commits
		// the laptop lacks gets the merge decided above, with the user's
		// carried identity, unsigned and without the repository's hooks,
		// after switching to it when the guest is on another branch;
		// failing that (git refuses the switch, or the merge) it is left
		// where it is and the laptop's commit is checked out detached
		// (I-150, I-574). The guest's other branch keeps its commits.
		_, _ = fmt.Fprintf(&b, `if cur=$(git rev-parse -q --verify refs/heads/%[1]s); then
  if git merge-base --is-ancestor "$cur" %[2]s; then repose_git checkout -q -B %[1]s %[2]s
  elif [ -n "$repose_merge" ] && ! repose_busy >/dev/null && { [ "$(git symbolic-ref -q HEAD || true)" = %[4]s ] || repose_git checkout -q %[1]s -- 2>/dev/null; } && repose_merging=yes && repose_with_ident git $repose_c merge -q --no-ff --no-edit --no-verify --no-autostash --no-verify-signatures --no-gpg-sign -m %[3]s %[2]s >/dev/null 2>&1; then echo '#merged'
  else
    if [ -n "$repose_merging" ] && git rev-parse -q --verify MERGE_HEAD >/dev/null; then git merge --abort; fi
    repose_git checkout -q --detach %[2]s; echo '#diverged'
  fi
else
  repose_git checkout -q -b %[1]s %[2]s
fi
`, br, head, shQuote(mergeMessage(branch)), shQuote("refs/heads/"+branch))
		if track != "" {
			_, _ = fmt.Fprintf(&b, "git branch -q --set-upstream-to=%s %s >/dev/null 2>&1 || true\n", shQuote("origin/"+branch), br)
		}
	}
	// Staged work goes into the index and the tree, unstaged work into
	// the tree only, so `git status` on the guest reads as it does on the
	// laptop (I-258).
	b.WriteString("if [ -s \"$t/staged.diff\" ]; then git apply --index \"$t/staged.diff\"; fi\n")
	b.WriteString("if [ -s \"$t/unstaged.diff\" ]; then git apply \"$t/unstaged.diff\"; fi\n")
	// Submodules after the superproject's diffs (a new one's .gitmodules
	// entry is among them) and before the untracked tar, which holds
	// their untracked files too (I-263).
	b.WriteString(subScript)
	b.WriteString("if [ -f \"$t/untracked.tar\" ]; then tar -x -f \"$t/untracked.tar\"; fi\n")
	return b.String()
}

// stashRemoteMsg and stashDiscardMsg name the stash --stash-remote and
// --discard-remote make of the machine's changes (DECISIONS I-618).
const (
	stashRemoteMsg  = "repose sync --stash-remote"
	stashDiscardMsg = "repose sync --discard-remote"
)

// stashAllScript stashes every change in the checkout, untracked files
// included, under msg, when it has a commit to stash against and
// anything to stash. report prints "#stashed <files> <stash commit>" for
// the summary: the superproject's count, every untracked file on its own.
func stashAllScript(msg string, report bool) string {
	s := fmt.Sprintf(`if git rev-parse -q --verify HEAD >/dev/null; then
  repose_sn=$(repose_git status --porcelain -uall --ignore-submodules=none | grep -c '' || true)
  if [ "$repose_sn" -gt 0 ]; then
    repose_git stash push -q -u -m %s
`, shQuote(msg))
	if report {
		// A short id of digits alone reads as stash@{N} to `git stash
		// apply`, so that one is printed whole.
		s += "    repose_sh=$(git rev-parse --short 'stash@{0}' 2>/dev/null || true)\n" +
			"    case $repose_sh in *[a-f]*) ;; *) repose_sh=$(git rev-parse 'stash@{0}' 2>/dev/null || true) ;; esac\n" +
			"    printf '#stashed %s %s\\n' \"$repose_sn\" \"$repose_sh\"\n"
	}
	return s + "  fi\nfi\n"
}

// mergeMessage is the subject of the merge commit a sync makes on the
// guest's branch (I-574).
func mergeMessage(branch string) string {
	return "Merge the laptop's " + branch + " (repose sync)"
}

// applyCarryHere marks where syncGuest puts the carry and the clearing of
// the sync key: after the overlap check, before the checkout is touched.
const applyCarryHere = "# repose: carry and key\n"

// syncBusy is the apply's stderr when the guest's checkout has a merge,
// rebase, cherry-pick, revert, am or bisect in progress; "#busy <state>"
// follows (I-573).
const syncBusy = "repose: the machine's checkout is in the middle of a git operation"

// busyError is the refusal for syncBusy: exit 6, the state in one line.
func busyError(stderr string, opts SyncOptions) error {
	state := "git operation"
	for _, l := range strings.Split(stderr, "\n") {
		if s, ok := strings.CutPrefix(l, "#busy "); ok && s != "" {
			state = "git " + strings.TrimSpace(s)
		}
	}
	stash := ""
	if opts.StashRemote {
		stash = ", which `--stash-remote` can't keep"
	}
	return exitf(ExitDirtyRemoteTree, "Not synced: the machine's checkout is in the middle of a %s%s. Finish or abort it there, or run `repose sync --discard-remote` to end it and move the machine's changes to its git stash.", state, stash)
}

// syncOverlap is the apply's stderr when the guest changed paths this
// sync writes; each such path follows on its own line after "#overlap ".
const syncOverlap = "repose: the machine changed files this sync writes"

// overlapFiles reads the paths the apply named after syncOverlap.
func overlapFiles(stderr string) []string {
	var files []string
	for _, l := range strings.Split(stderr, "\n") {
		if f, ok := strings.CutPrefix(l, "#overlap "); ok && f != "" {
			files = append(files, f)
		}
	}
	return files
}

// syncGuardFns are the apply's overlap check and its record of what the
// sync left (I-573). They need bash (read with an empty delimiter) and
// gawk (NUL records), which the base has: repose_awk calls gawk by name,
// so an awk earlier on the user's PATH (busybox, mawk) is not used.
// repose_busy prints the git operation in progress in the checkout
// (merge, rebase, am, cherry-pick, revert, bisect) and fails when there
// is none; repose_unbusy ends any of them where HEAD is, for
// --discard-remote. repose_with_ident runs a command with the identity
// in $t/ident (the laptop's user.name and user.email) as its author and
// committer. repose_guestcount prints how many of the checkout's changed
// files (every untracked file on its own) are not the last sync's own,
// for the probe. $t is the unpacked payload, whose `paths` lists,
// NUL-separated, every path the laptop's own work writes: both sides of
// its staged and unstaged diffs, its untracked files and its bundled
// submodules.
//
// repose_hash names a path's content: a blob hash for a file, the link
// target for a symlink, the fingerprint of a checked-out submodule (its
// HEAD and its `git add -A` tree), "-" for nothing there, "?" for
// anything else, which never matches.
//
// .git/repose-synced-paths is the record: the HEAD the last apply left,
// then "<hash>\0<path>\0" for each of that sync's paths as it left them.
// repose_own prints, NUL-separated, the ones still exactly so with HEAD
// unchanged: the last sync's own changes, not the guest's.
//
// repose_guard TARGET takes the paths the checkout to TARGET (a commit
// or a tree) changes, plus the laptop's paths, and the guest's dirty and
// untracked files (every file, -uall; a rename counts at both paths). A
// guest path that is the last sync's own goes to $t/ownd for the stash.
// Any other that equals a written path, sits under one, or holds one
// below it is an overlap: all of them are printed after syncOverlap on
// stderr and the apply exits 3 before anything is touched. The rest are
// the guest's own changes elsewhere, kept, and counted as #guestkept.
//
// repose_touches BASE TIP: whether the commits BASE..TIP change any of
// the laptop's paths, which would put a merge's version under the
// laptop's uncommitted work.
var syncGuardFns = `repose_awk() { if command -v gawk >/dev/null 2>&1; then gawk "$@"; else awk "$@"; fi; }
repose_busy() {
  repose_gp() { [ -e "$(git rev-parse --git-path "$1")" ]; }
  if repose_gp MERGE_HEAD; then echo merge
  elif repose_gp rebase-merge; then echo rebase
  elif repose_gp rebase-apply/applying; then echo am
  elif repose_gp rebase-apply; then echo rebase
  elif repose_gp CHERRY_PICK_HEAD; then echo cherry-pick
  elif repose_gp REVERT_HEAD; then echo revert
  elif repose_gp sequencer; then echo cherry-pick
  elif repose_gp BISECT_LOG; then echo bisect
  else return 1
  fi
}
repose_unbusy() {
  git rebase --quit >/dev/null 2>&1 || true
  git am --quit >/dev/null 2>&1 || true
  git cherry-pick --quit >/dev/null 2>&1 || true
  git revert --quit >/dev/null 2>&1 || true
  if [ -e "$(git rev-parse --git-path BISECT_LOG)" ]; then git bisect reset HEAD >/dev/null 2>&1 || true; fi
}
repose_with_ident() {
  (
    if [ -f "$t/ident" ]; then
      { IFS= read -r repose_n || true; IFS= read -r repose_m || true; } < "$t/ident"
      if [ -n "$repose_n" ]; then export GIT_AUTHOR_NAME="$repose_n" GIT_COMMITTER_NAME="$repose_n"; fi
      if [ -n "$repose_m" ]; then export GIT_AUTHOR_EMAIL="$repose_m" GIT_COMMITTER_EMAIL="$repose_m"; fi
    fi
    "$@"
  )
}
repose_guestcount() {
  repose_d=$(mktemp -d)
  repose_git status --porcelain=v1 -z -uall --ignore-submodules=none > "$repose_d/st"
  repose_own > "$repose_d/own"
  repose_awk -v RS='\0' '
    FILENAME == ARGV[1] { if ($0 != "") own[$0] = 1; next }
    {
      if (orig) { orig = 0; p = $0 } else { if ($0 == "") next; xy = substr($0, 1, 2); p = substr($0, 4); if (xy ~ /[RC]/) orig = 1 }
      sub(/\/$/, "", p)
      if (!(p in own)) n++
    }
    END { print n + 0 }' "$repose_d/own" "$repose_d/st"
  rm -rf "$repose_d"
}
repose_hash() {
  if [ -e "$1/.git" ]; then (cd "./$1" && repose_fp1)
  elif [ -L "$1" ]; then printf 'l:%s\n' "$(readlink -- "$1")"
  elif [ -f "$1" ]; then git hash-object --no-filters -- "$1" 2>/dev/null || echo '?'
  elif [ -e "$1" ]; then echo '?'
  else echo -
  fi
}
repose_own() {
  [ -s "$repose_synced-paths" ] || return 0
  {
    IFS= read -r -d '' h || return 0
    [ "$h" = "$(git rev-parse -q --verify HEAD || echo none)" ] || return 0
    while IFS= read -r -d '' h && IFS= read -r -d '' p; do
      case $h in '?'|failed|*' failed') continue ;; esac
      if [ "$h" = "$(repose_hash "$p")" ]; then printf '%s\0' "$p"; fi
    done
  } < "$repose_synced-paths"
}
repose_guard() {
  : > "$t/w"
  if [ -f "$t/paths" ]; then cat "$t/paths" >> "$t/w"; fi
  if git rev-parse -q --verify HEAD >/dev/null; then
    git diff --name-only -z --no-renames --ignore-submodules=none HEAD "$1" -- >> "$t/w"
    if [ -n "${2:-}" ]; then git diff --name-only -z --no-renames --ignore-submodules=none HEAD "$2" -- >> "$t/w"; fi
  else
    git ls-tree -r -z --name-only "$1" >> "$t/w"
  fi
  repose_git status --porcelain=v1 -z -uall --ignore-submodules=none > "$t/st"
  repose_own > "$t/own"
  : > "$t/ownd"; : > "$t/kept"; : > "$t/overlap"
  repose_awk -v RS='\0' -v ownd="$t/ownd" -v kept="$t/kept" -v overlap="$t/overlap" '
    FILENAME == ARGV[1] { if ($0 == "") next; w[$0] = 1; n = split($0, a, "/"); d = a[1]; for (i = 2; i <= n; i++) { wd[d] = 1; d = d "/" a[i] } next }
    FILENAME == ARGV[2] { if ($0 != "") own[$0] = 1; next }
    {
      if (orig) { orig = 0; p = $0 } else { if ($0 == "") next; xy = substr($0, 1, 2); p = substr($0, 4); if (xy ~ /[RC]/) orig = 1 }
      sub(/\/$/, "", p)
      if (p in own) { printf "%s%c", p, 0 > ownd; next }
      hit = (p in w) || (p in wd)
      n = split(p, a, "/"); d = a[1]
      for (i = 2; i <= n && !hit; i++) { if (d in w) hit = 1; d = d "/" a[i] }
      if (hit) printf "%s\n", p > overlap; else printf "%s\n", p > kept
    }' "$t/w" "$t/own" "$t/st"
  if [ -s "$t/overlap" ]; then
    { echo '` + syncOverlap + `'; sed 's/^/#overlap /' "$t/overlap"; } >&2
    exit 3
  fi
  if [ -s "$t/kept" ]; then printf '#guestkept %s\n' "$(grep -c '' "$t/kept")"; fi
}
repose_stash_own() {
  [ -s "$t/ownd" ] || return 0
  while IFS= read -r -d '' p; do
    if [ -e "$p/.git" ]; then (cd "./$p" && repose_git stash push -q -u -m 'repose run: last sync') || exit 1; fi
  done < "$t/ownd"
  GIT_LITERAL_PATHSPECS=1 git $repose_c stash push -q -u -m 'repose run: last sync' --pathspec-from-file="$t/ownd" --pathspec-file-nul
  echo '#stashedsync'
` + pruneSyncStashes + `}
repose_touches() {
  [ -s "$t/paths" ] || return 1
  git diff --name-only -z --no-renames "$1" "$2" -- > "$t/agent"
  repose_awk -v RS='\0' '
    FILENAME == ARGV[1] { if ($0 == "") next; a[$0] = 1; n = split($0, s, "/"); d = s[1]; for (i = 2; i <= n; i++) { ad[d] = 1; d = d "/" s[i] } next }
    {
      if ($0 == "") next
      if (($0 in a) || ($0 in ad)) f = 1
      n = split($0, s, "/"); d = s[1]
      for (i = 2; i <= n; i++) { if (d in a) f = 1; d = d "/" s[i] }
    }
    END { exit !f }' "$t/agent" "$t/paths"
}
`

// inEverySub runs cmd in each checked-out submodule of the guest's
// checkout, nested ones included, before the superproject's own: a stash
// or reset there does not reach into them (submodule.recurse=false).
func inEverySub(cmd string) string {
	return "repose_sublist | while IFS= read -r repose_p; do (cd \"$repose_p\" || exit 1; " + strings.TrimRight(cmd, "\n") + "\n) || exit 1; done\n"
}

// syncStashKeep is how many "repose run: last sync" stashes the guest
// keeps; one is made per run from a dirty laptop tree (I-210).
const syncStashKeep = 10

// pruneSyncStashes drops every "repose run: last sync" stash past the
// newest syncStashKeep, oldest first so the indices of the ones still to
// drop do not move. It matches the whole subject git records for
// `stash push -m` ("On <branch>: <message>", a branch name has no colon),
// so the user's stashes and --stash-remote's and --discard-remote's are never
// touched. Each is dropped only while its index still names the commit
// listed: a stash an agent pushes meanwhile shifts every index, and then
// nothing more is dropped this time.
var pruneSyncStashes = fmt.Sprintf(`n=0
drop=""
while IFS=' ' read -r ref sha subj; do
  case $subj in
    "On "*": repose run: last sync")
      br=${subj#On }; br=${br%%": repose run: last sync"}
      case $br in *:*) continue ;; esac
      n=$((n+1))
      [ "$n" -gt %d ] && drop="$ref=$sha $drop"
      ;;
  esac
done <<EOF
$(git stash list --format='%%gd %%H %%gs')
EOF
for d in $drop; do
  ref=${d%%%%=*}; sha=${d#*=}
  [ "$(git rev-parse -q --verify "$ref" 2>/dev/null)" = "$sha" ] || break
  git stash drop -q "$ref"
done
`, syncStashKeep)

// recordSyncedScript ends the apply. When the sync left the tree dirty
// with nothing but its own work (the laptop's diff, its untracked files),
// the fingerprint of that state is stored in the checkout's .git for the
// next probe (I-210); a clean tree needs none, and neither does one where
// the guest's own changes were kept, which are not the sync's (I-573).
// The per-path record (syncGuardFns) is written either way.
const recordSyncedScript = `if [ -s "$t/kept" ]; then
  rm -f "$repose_synced"
elif [ -n "$(repose_dirty | head -n 1)" ]; then
  fp=$(repose_fp)
  if [ "$fp" = failed ]; then rm -f "$repose_synced"; else printf '%s\n' "$fp" > "$repose_synced"; fi
else
  rm -f "$repose_synced"
fi
{
  printf '%s\0' "$(git rev-parse -q --verify HEAD || echo none)"
  if [ -f "$t/paths" ]; then
    while IFS= read -r -d '' p; do printf '%s\0%s\0' "$(repose_hash "$p")" "$p"; done < "$t/paths"
  fi
} > "$repose_synced-paths"
`

// originURLFor is guestd's rule for the origin it sets (internal/guestd/
// project originURL, I-107): the api's normalised "host/owner/repo"
// becomes the SSH clone URL "git@host:owner/repo.git".
func originURLFor(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	if strings.Contains(remote, "://") || strings.HasPrefix(remote, "git@") {
		return remote
	}
	host, path, ok := strings.Cut(remote, "/")
	if !ok || host == "" || path == "" {
		return ""
	}
	return "git@" + host + ":" + strings.TrimSuffix(path, ".git") + ".git"
}

// commitsKnownLocally filters the guest's tips to the ones this checkout
// has as commits (or tags), the only ones `--not` can use.
func commitsKnownLocally(dir string, tips []string) ([]string, error) {
	if len(tips) == 0 {
		return nil, nil
	}
	out, err := gitCmdStdin(dir, strings.Join(tips, "\n")+"\n", "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return nil, err
	}
	var known []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && (f[1] == "commit" || f[1] == "tag") {
			known = append(known, f[0])
		}
	}
	return known, nil
}

// gitCmdStdin is gitCmd with stdin, for the --stdin forms that keep long
// rev lists off the command line.
func gitCmdStdin(dir, stdin string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return out.String(), nil
}

// maxUntrackedBytes caps what one sync sends of untracked files in total:
// past it the rest is skipped with a warning naming the biggest
// directories, rather than packing gigabytes into memory (owner's run on
// 2026-09-23, a pnpm tree under an un-ignored cms/node_modules).
const maxUntrackedBytes = 500 << 20

// defaultSkipDirs are directories that never travel, wherever they sit in
// the tree and whether or not .gitignore mentions them: dependency trees
// and build caches the guest recreates itself (an install there is
// faster than shipping them, and they are full of symlinks and
// platform-specific binaries that would be wrong in the guest anyway).
var defaultSkipDirs = map[string]bool{
	"node_modules": true, ".pnpm-store": true, "bower_components": true,
	".venv": true, "venv": true, "__pycache__": true, ".mypy_cache": true, ".pytest_cache": true, ".ruff_cache": true, ".tox": true,
	".turbo": true, ".next": true, ".nuxt": true, ".svelte-kit": true, ".parcel-cache": true, ".angular": true,
	".gradle": true, ".terraform": true, ".direnv": true,
}

// skippedDir is the shortest leading directory of f that is a default
// skip ("cms/node_modules" for "cms/node_modules/.pnpm/x/y"), or "".
func skippedDir(f string) string {
	parts := strings.Split(filepath.ToSlash(f), "/")
	for i, part := range parts[:len(parts)-1] {
		if defaultSkipDirs[part] {
			return strings.Join(parts[:i+1], "/")
		}
	}
	return ""
}

// filterUntracked drops default-skipped directories (named once each in
// skippedDirs), anything sync.exclude matches, files over the size cap
// (named in skippedBig), and everything past the total cap (skippedCap
// counts them). Directories and anything that is not a regular file or
// a symlink are dropped silently; symlinks are kept and travel as links.
func filterUntracked(root string, files, exclude []string) (kept, skippedBig, skippedDirs []string, skippedCap int, err error) {
	seenDir := map[string]bool{}
	var total int64
	for _, f := range files {
		if d := skippedDir(f); d != "" {
			if !seenDir[d] {
				seenDir[d] = true
				skippedDirs = append(skippedDirs, d)
			}
			continue
		}
		if matchesAny(exclude, f) {
			continue
		}
		info, statErr := os.Lstat(filepath.Join(root, f))
		if statErr != nil {
			continue // gone between listing and syncing; nothing to send
		}
		mode := info.Mode()
		if mode&os.ModeSymlink == 0 && !mode.IsRegular() {
			continue
		}
		if mode.IsRegular() && info.Size() > maxSyncFileBytes {
			skippedBig = append(skippedBig, f)
			continue
		}
		if total+info.Size() > maxUntrackedBytes {
			skippedCap++
			continue
		}
		total += info.Size()
		kept = append(kept, f)
	}
	return kept, skippedBig, skippedDirs, skippedCap, nil
}

// matchesAny reports whether a sync.exclude pattern matches the path, its
// base name, or any leading directory of it (so "dist" and "web/dist"
// both exclude everything under web/dist).
func matchesAny(patterns []string, path string) bool {
	path = filepath.ToSlash(path)
	parts := strings.Split(path, "/")
	for _, p := range patterns {
		p = strings.TrimSuffix(filepath.ToSlash(p), "/")
		if ok, _ := filepath.Match(p, path); ok {
			return true
		}
		if ok, _ := filepath.Match(p, parts[len(parts)-1]); ok {
			return true
		}
		for i := range parts[:len(parts)-1] {
			if ok, _ := filepath.Match(p, parts[i]); ok {
				return true
			}
			if ok, _ := filepath.Match(p, strings.Join(parts[:i+1], "/")); ok {
				return true
			}
		}
	}
	return false
}

// tarFiles packs the files filterUntracked kept. A symlink travels as a
// symlink (pnpm's node_modules is made of them, and reading one that
// points at a directory as a file is what failed the owner's sync); a
// file that turned into something else since the listing is skipped.
func tarFiles(root string, files []string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		path := filepath.Join(root, f)
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		name := filepath.ToSlash(f)
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				continue
			}
			if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: name, Linkname: filepath.ToSlash(target), Mode: 0o777, ModTime: info.ModTime()}); err != nil {
				return nil, err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue // unreadable (permissions, a race): one file must not sink the whole sync
		}
		hdr := &tar.Header{Name: name, Mode: int64(info.Mode().Perm()), Size: int64(len(b)), ModTime: info.ModTime()}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(b); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func tarAddBytes(tw *tar.Writer, name string, b []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(b))}); err != nil {
		return err
	}
	_, err := tw.Write(b)
	return err
}

func tarAddFile(tw *tar.Writer, name, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: info.Size()}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

func (s *SyncSummary) String() string {
	if s.GuestAhead {
		return guestAheadLine(s)
	}
	line := fmt.Sprintf("Synced: %d modified, %d untracked", s.Modified, s.Untracked)
	switch {
	case s.EnvFiles == 1:
		line += ", 1 env file"
	case s.EnvFiles > 1:
		line += fmt.Sprintf(", %d env files", s.EnvFiles)
	}
	switch {
	case s.Commits == 1:
		line += " (1 new commit)"
	case s.Commits > 1:
		line += fmt.Sprintf(" (%d new commits)", s.Commits)
	}
	if s.Merged {
		line += ", merged with the machine's " + s.Branch
	}
	if s.ClonedFrom != "" {
		line += ", history cloned from " + s.ClonedFrom
	}
	switch {
	case s.GuestKept == 1:
		line += "; kept the machine's changes to 1 file"
	case s.GuestKept > 1:
		line += fmt.Sprintf("; kept the machine's changes to %d files", s.GuestKept)
	}
	if s.StashedFiles > 0 {
		line += fmt.Sprintf("; stashed the machine's changes to %s (git stash %s)", plural(s.StashedFiles, "1 file", fmt.Sprintf("%d files", s.StashedFiles)), s.StashRef)
	}
	if s.StashedLastSync {
		line += "; the last sync's changes stashed on the machine"
	}
	if s.Unchanged {
		line += "; the machine already had them"
	}
	return line
}

// Warnings are the stderr lines that go with the summary.
func (s *SyncSummary) Warnings() []string {
	var w []string
	short := s.Head
	if len(short) > 7 {
		short = short[:7]
	}
	for _, f := range s.SubFailed {
		p, why, _ := strings.Cut(f, "\t")
		w = append(w, fmt.Sprintf("Submodule %s is empty on the machine: it is a shallow clone on your laptop, so the machine fetched it itself, and that failed (%s).", p, why))
	}
	for _, p := range s.SubNotSent {
		w = append(w, fmt.Sprintf("Your changes inside submodule %s were not sent: it is a shallow clone on your laptop. Run `git -C %s fetch --unshallow` to send them next time.", p, p))
	}
	if s.CloneFailed != "" {
		w = append(w, fmt.Sprintf("The machine could not clone from GitHub (%s), so the history was sent from your laptop instead.", s.CloneFailed))
	}
	for _, k := range s.EnvKept {
		w = append(w, fmt.Sprintf("Kept the machine's %s: it is newer than the laptop's.", k))
	}
	switch {
	case s.EnvRemoved == 1:
		w = append(w, "Removed the .env file an earlier repose run copied to the machine: repose secrets choose has env off.")
	case s.EnvRemoved > 1:
		w = append(w, fmt.Sprintf("Removed the %d .env files an earlier repose run copied to the machine: repose secrets choose has env off.", s.EnvRemoved))
	}
	if len(s.EnvLeft) > 0 {
		w = append(w, fmt.Sprintf("Left %s on the machine: it changed there since it was copied.", strings.Join(s.EnvLeft, ", ")))
	}
	if s.Diverged {
		w = append(w, fmt.Sprintf("The machine's %s has commits that could not be merged with yours, so it was left as it is and the machine is on %s, detached. `git fetch repose` brings that branch here.", s.Branch, short))
	}
	for _, f := range s.SkippedBig {
		w = append(w, fmt.Sprintf("Skipped %s: over 100 MB.", f))
	}
	if len(s.SkippedDirs) > 0 {
		w = append(w, fmt.Sprintf("Not sent: %s (dependencies and caches).", strings.Join(s.SkippedDirs, ", ")))
	}
	if s.SkippedCap > 0 {
		w = append(w, fmt.Sprintf("Skipped %d untracked files past the 500 MB limit for one sync. Commit what matters, or add large directories to .gitignore or `sync.exclude`.", s.SkippedCap))
	}
	return w
}

// guestAheadLine is a sync's whole report when the laptop had nothing
// new and the machine moved on since the last sync (I-248): its commits
// the laptop lacks and its uncommitted files, each counted on its own
// (DECISIONS I-618).
func guestAheadLine(s *SyncSummary) string {
	var parts []string
	switch n := s.GuestCommitCount; {
	case !s.GuestCommits || n == 0:
	case n < 0:
		parts = append(parts, "commits"+onBranch(s.GuestBranch)+" your laptop doesn't have")
	default:
		parts = append(parts, plural(n, "1 commit", fmt.Sprintf("%d commits", n))+onBranch(s.GuestBranch)+" your laptop doesn't have")
	}
	if s.GuestFiles > 0 {
		parts = append(parts, "uncommitted changes to "+plural(s.GuestFiles, "1 file", fmt.Sprintf("%d files", s.GuestFiles)))
	}
	if len(parts) == 0 {
		// Only the last sync's own changes are left, or commits the
		// laptop fetched already: nothing the laptop lacks.
		return nothingNewLine
	}
	return "Nothing new to sync. The machine has " + strings.Join(parts, ", and ") + "."
}

func onBranch(b string) string {
	if b == "" {
		return ""
	}
	return " on " + b
}

// nothingNewLine is a sync's whole report when the machine already has
// exactly what the laptop would send.
const nothingNewLine = "Nothing new to sync: the machine already has this checkout."
