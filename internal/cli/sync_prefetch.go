package cli

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The guest's own fetch from GitHub on a first sync (I-203, amended by
// I-712..I-715). When the guest's checkout has no commits yet, the
// superproject and each checked-out submodule whose remote is on
// github.com and whose history is at least hybridThresholdKiB are fetched
// by the guest itself, over its own network, instead of the laptop
// uploading their history as a bundle (I-712 adds the submodules, which
// I-263 always bundled).
//
// It costs no round trip of its own (I-713): the fetches start in the
// background at the top of the apply's script, which runs as soon as its
// ssh opens, while the payload is still streaming up from the laptop.
// The laptop bundles only what its own view of origin lacks
// (laptopBases); once the fetches end, the apply checks that each bundle's
// prerequisites (or, with no bundle, the commit to check out) arrived. If
// one did not (a fetch failed, GitHub lacks a commit the laptop thinks it
// has), the apply stops before it touches the checkout, prints what the
// guest now has, and the laptop sends a second payload bundled against
// that, as the old order did.
//
// The first attempt is anonymous. A public superproject is fetched
// without blobs (--filter=blob:none, I-714): commits and trees only, the
// blobs of the commit checked out arrive with the checkout, and the rest
// of the history follows in the background once the sync is done (`git
// backfill --all`), after which the promisor remote is removed and the
// repository is whole. A repository the anonymous attempt cannot read
// (private) waits for the logins this same apply copies right after its
// payload is unpacked, then fetches in full with gh's credential helper,
// as I-203 always did.

// hybridThresholdKiB is the size of a repository's history (`git
// count-objects -v`'s size-pack, KiB) from which the guest fetches it
// from GitHub instead of the laptop bundling it. Set from measurement
// (I-715): from a host-01 guest a blobless fetch of a 26 MB history takes
// 0.6 s and a whole 7 MB one 0.9 s, and it runs while the payload
// uploads; 4 MiB is 1.7 s of upload at 20 Mbit/s and 13 s at the
// owner's 2.5 Mbit/s.
var hybridThresholdKiB int64 = 4 * 1024

// prefetchAnonArgs are the git options of the anonymous first attempt:
// no credential helper at all, so a private repository fails at once
// instead of using a login that may not have arrived yet. A variable so
// a test can make the anonymous attempt fail.
var prefetchAnonArgs = "-c credential.helper="

// prefetchDir is where, inside the guest checkout's .git, the fetches
// keep their results and the submodules' fetched repositories until the
// apply moves them into place.
const prefetchDir = ".git/repose-prefetch"

// backfillRemote is the promisor remote of a checkout fetched without
// blobs: not origin, whose URL is the SSH form guestd sets (no key to use
// without gh's login) and the user's to change. It has no fetch refspec,
// so `git remote` is all that shows it, and it goes once the backfill has
// the whole history.
const backfillRemote = "repose-backfill"

// backfillMarker marks a checkout whose history the guest still has to
// backfill; the end of every apply starts the backfill while it exists.
const backfillMarker = ".git/repose-backfill"

// prefetchRepo is one repository the guest fetches.
type prefetchRepo struct {
	key string // "top", or "s<i>" for subs[i]
	sub int    // index into subs, -1 for the superproject
	url string
	// partial: a blobless fetch may be tried (the superproject only:
	// a submodule's origin is rewritten by `git submodule sync`, so it
	// cannot stay the promisor its lazy fetches need).
	partial bool
	// want is what the apply needs from it besides the bundle's
	// prerequisites: the commits it checks out.
	want []string
}

// prefetchPlan is every fetch of one first sync.
type prefetchPlan struct {
	repos []prefetchRepo
	// resume: the second payload of a sync whose fetches already ran.
	resume bool
	// full: that second payload carries everything, since the guest no
	// longer has the first.
	full bool
}

func (p *prefetchPlan) active() bool { return p != nil && len(p.repos) > 0 }

func (p *prefetchPlan) top() *prefetchRepo {
	if p == nil {
		return nil
	}
	for i := range p.repos {
		if p.repos[i].sub < 0 {
			return &p.repos[i]
		}
	}
	return nil
}

func (p *prefetchPlan) forSub(i int) *prefetchRepo {
	if p == nil {
		return nil
	}
	for j := range p.repos {
		if p.repos[j].sub == i {
			return &p.repos[j]
		}
	}
	return nil
}

// subCloneURL is the GitHub URL the guest fetches a laptop submodule
// from, or "": the submodule's own origin, as the laptop has it.
func subCloneURL(dir string) string {
	u, err := gitCmd(dir, "config", "--get", "remote.origin.url")
	if err != nil {
		return ""
	}
	return hybridCloneURL(normalizeRemote(u))
}

// subLaptopBases is a submodule's own view of its origin: what the
// guest's fetch most likely brings, so the bases its bundle is made
// against.
func subLaptopBases(dir string) []string {
	out, err := gitCmd(dir, "for-each-ref", "--format=%(objectname)", "refs/remotes/origin")
	if err != nil {
		return nil
	}
	return nonEmptyLines(out)
}

// planPrefetch decides what the guest fetches itself on a sync into a
// checkout with no commits. topURL is the superproject's clone URL ("" for
// none); subs are the laptop's submodules.
func planPrefetch(localRepoDir, topURL string, subs []laptopSub) *prefetchPlan {
	plan := &prefetchPlan{}
	if topURL != "" && gitPackKiB(localRepoDir) >= hybridThresholdKiB {
		plan.repos = append(plan.repos, prefetchRepo{key: "top", sub: -1, url: topURL, partial: true})
	}
	for i, s := range subs {
		if s.Shallow {
			continue
		}
		dir := filepath.Join(localRepoDir, filepath.FromSlash(s.Path))
		u := subCloneURL(dir)
		if u == "" || gitPackKiB(dir) < hybridThresholdKiB {
			continue
		}
		plan.repos = append(plan.repos, prefetchRepo{key: "s" + strconv.Itoa(i), sub: i, url: u, want: []string{s.Head}})
	}
	if len(plan.repos) == 0 {
		return nil
	}
	return plan
}

// prefetchFns is the shell function each background fetch runs: fetch dir
// (".", or a fresh repository under prefetchDir) from url, anonymous
// first (blobless when partial is "yes" and the guest's git has a
// backfill that takes revisions, git 2.54, for its --all), then, if
// that failed, with the logins once the apply has copied them. It writes <key>.res: "partial", "full" or "failed <why>",
// then the milliseconds it took.
//
// Each git runs as a child the function waits for, so the apply's EXIT
// trap, which signals the function, ends the git too: an orphaned fetch
// would go on writing into the checkout after the apply is gone. A
// blobless fetch marks the checkout for its backfill as soon as it
// lands, so an apply that stops after it (a refusal, Ctrl-C, a dropped
// upload) still leaves a checkout the next sync makes whole (I-714).
// The wait for the logins has no limit of its own: it ends when they
// arrive or when the apply's shell is gone, however long the upload.
const prefetchFns = `repose_pf_fetch() {
  set +e
  repose_g=
  trap '[ -z "$repose_g" ] || kill $repose_g 2>/dev/null; exit 143' TERM
  k=$1; d=$2; u=$3; part=$4
  r="$repose_pf/$k"
  s0=$(date +%s%N)
  export GIT_TERMINAL_PROMPT=0 GIT_ASKPASS= SSH_ASKPASS=
  ok=
  if [ "$part" = yes ]; then
    git -C "$d" backfill -h 2>&1 | grep -q 'revision-range' || part=
  fi
  if [ "$part" = yes ]; then
    if git -C "$d" config remote.` + backfillRemote + `.url "$u" && git -C "$d" config remote.` + backfillRemote + `.promisor true && git -C "$d" config remote.` + backfillRemote + `.partialclonefilter blob:none &&
      repose_pf_git git -C "$d" $repose_pf_anon fetch -q --no-tags --filter=blob:none ` + backfillRemote + ` '+refs/heads/*:refs/remotes/origin/*' '+refs/tags/*:refs/tags/*' </dev/null 2>"$r.err"; then
      ok=partial
      : > "$d/` + backfillMarker + `"
    else
      git -C "$d" config --remove-section remote.` + backfillRemote + ` >/dev/null 2>&1
    fi
  elif repose_pf_git git -C "$d" $repose_pf_anon fetch -q --no-tags "$u" '+refs/heads/*:refs/remotes/origin/*' '+refs/tags/*:refs/tags/*' </dev/null 2>"$r.err"; then
    ok=full
  fi
  if [ -z "$ok" ]; then
    while [ ! -e "$repose_pf/creds" ]; do kill -0 $$ 2>/dev/null || exit 0; sleep 0.1; done
    if repose_pf_git git -C "$d" fetch -q --no-tags "$u" '+refs/heads/*:refs/remotes/origin/*' '+refs/tags/*:refs/tags/*' </dev/null 2>"$r.err"; then
      ok=full
    fi
  fi
  if [ -z "$ok" ]; then
    why=$(grep '^fatal:' "$r.err" | head -n 1)
    [ -n "$why" ] || why=$(grep -v '^ *$' "$r.err" | tail -n 1)
    ok="failed $(printf '%s' "${why#fatal: }" | cut -c1-160)"
  fi
  s1=$(date +%s%N)
  printf '%s\n%s\n' "$ok" "$(( (s1 - s0) / 1000000 ))" > "$r.tmp" && mv "$r.tmp" "$r.res"
}
repose_pf_git() { "$@" & repose_g=$!; wait $repose_g; }
`

// prefetchLock is the lock an apply with fetches holds on its checkout
// for as long as it uses prefetchDir, so another sync's probe does not
// clear it under it (prefetchProbeScript). Taken with a bound, and
// best effort: a process that inherited it and lives on (none should;
// the carry and the backfill are started without it) must not stop a
// later sync.
const prefetchLock = ".git/repose-prefetch.lock"

// prefetchStartScript takes the lock and starts the fetches in the
// background, before the payload is unpacked.
func prefetchStartScript(plan *prefetchPlan) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "repose_pf=%s\nrepose_pfp=\nexec 9>%s\nflock -w 30 9 || true\n", prefetchDir, prefetchLock)
	if plan.resume {
		return b.String()
	}
	_, _ = fmt.Fprintf(&b, "repose_pf_anon=%s\n", shQuote(prefetchAnonArgs))
	b.WriteString("rm -rf \"$repose_pf\"\nmkdir -p \"$repose_pf\"\n")
	b.WriteString(prefetchFns)
	for _, r := range plan.repos {
		part := ""
		if r.partial {
			part = "yes"
		}
		dir := "."
		if r.sub >= 0 {
			dir = "$repose_pf/" + r.key
			_, _ = fmt.Fprintf(&b, "git init -q \"%s\"\n", dir)
		}
		_, _ = fmt.Fprintf(&b, "repose_pf_fetch %s \"%s\" %s %s </dev/null >/dev/null 2>&1 &\nrepose_pfp=\"$repose_pfp $!\"\n", r.key, dir, shQuote(r.url), shQuote(part))
	}
	return b.String()
}

// prefetchPayloadDir is where an apply with fetches unpacks its payload:
// under prefetchDir, so that when the fetches fall short the payload
// stays on the guest and the second one carries only the bundles
// (I-713).
const prefetchPayloadDir = "$repose_pf/payload"

// prefetchUnpackScript is applyUnpack for a sync with fetches. Its trap
// also ends any fetch still running, and keeps the payload when the
// apply asked for a second one. A second payload of bundles alone is
// unpacked over the kept one; when that is gone, the apply says
// #needpayload and the laptop sends it whole.
func prefetchUnpackScript(plan *prefetchPlan) string {
	// Absolute: the submodules' lines read it from inside them.
	t := "t=\"$PWD/" + prefetchPayloadDir + "\"\nrepose_keep=\n"
	if plan.resume && !plan.full {
		t += "if [ ! -d \"$t\" ]; then echo '#needpayload'; exit 0; fi\n"
	} else {
		t += "rm -rf \"$t\"\nmkdir -p \"$t\"\n"
	}
	return t + "trap '[ -n \"$repose_keep\" ] || rm -rf \"$t\"; [ -z \"$repose_pfp\" ] || kill $repose_pfp 2>/dev/null' EXIT\ntar -x -C \"$t\"\n"
}

// resendKeeps reports whether a payload entry is sent again when the
// fetches fell short: the bundles, which are made anew against what the
// guest fetched. The rest of the first payload is kept on the guest.
func resendKeeps(name string) bool {
	if name == "bundle" {
		return true
	}
	rest, ok := strings.CutPrefix(name, "sub/")
	if !ok {
		return false
	}
	n, file, ok := strings.Cut(rest, "/")
	if _, err := strconv.Atoi(n); err != nil || !ok {
		return false
	}
	return file == "bundle"
}

// prefetchJoinScript runs right after the payload is unpacked: the carry
// (the logins a private repository's fetch waits for), then the wait for
// the fetches, their results as #pf lines, and the check that every
// commit the apply needs is there. When one is missing it prints
// #needfull and what the guest has (#pftips sections) and exits before
// the checkout is touched.
func prefetchJoinScript(plan *prefetchPlan, carryScript string, topBundle bool, subBundles map[int]bool) string {
	var b strings.Builder
	if carryScript != "" {
		// Without the lock's descriptor: what the carry starts must not
		// hold it.
		b.WriteString("{\n" + carryScript + "} 9>&-\n")
	}
	b.WriteString(": > \"$repose_pf/creds\"\n")
	if !plan.resume {
		b.WriteString("[ -z \"$repose_pfp\" ] || wait $repose_pfp || true\nrepose_pfp=\n")
		for _, r := range plan.repos {
			_, _ = fmt.Fprintf(&b, "if [ -f \"$repose_pf/%[1]s.res\" ]; then { IFS= read -r repose_l; IFS= read -r repose_ms; } < \"$repose_pf/%[1]s.res\"; printf '#pf %[1]s %%s\\n#pfms %[1]s %%s\\n' \"$repose_l\" \"$repose_ms\"; else echo '#pf %[1]s failed the fetch did not finish'; fi\n", r.key)
		}
	}
	b.WriteString("repose_need=\n")
	for _, r := range plan.repos {
		dir := "."
		bundle := "$t/bundle"
		has := topBundle
		if r.sub >= 0 {
			dir = "$repose_pf/" + r.key
			bundle = fmt.Sprintf("$t/sub/%d/bundle", r.sub)
			has = subBundles[r.sub]
		}
		if has {
			_, _ = fmt.Fprintf(&b, "GIT_NO_LAZY_FETCH=1 git -C \"%s\" bundle verify -q \"%s\" >/dev/null 2>&1 || repose_need=yes\n", dir, bundle)
		}
		for _, c := range r.want {
			// In the bundle, or there. The bundle first, and the
			// checkout's own objects without a lazy fetch: in a blobless
			// checkout a missing commit (the laptop's own) would be asked
			// of GitHub, which lacks it, on the apply's critical path.
			inBundle := ""
			if has {
				inBundle = fmt.Sprintf("git -C \"%s\" bundle list-heads \"%s\" 2>/dev/null | grep -q '^%s ' || ", dir, bundle, c)
			}
			_, _ = fmt.Fprintf(&b, "{ %sGIT_NO_LAZY_FETCH=1 git -C \"%s\" cat-file -e %s 2>/dev/null; } || repose_need=yes\n", inBundle, dir, shQuote(c+"^{commit}"))
		}
	}
	b.WriteString("if [ -n \"$repose_need\" ]; then\n  echo '#needfull'\n")
	for _, r := range plan.repos {
		dir := "."
		if r.sub >= 0 {
			dir = "$repose_pf/" + r.key
		}
		_, _ = fmt.Fprintf(&b, "  echo '#pftips %s'\n  git -C \"%s\" for-each-ref --format='%%(objectname)' 2>/dev/null || true\n", r.key, dir)
		if r.sub < 0 {
			// The laptop's bases the fetch brought, though no ref names
			// them (a laptop behind GitHub).
			b.WriteString("  if [ -f \"$t/bases\" ]; then GIT_NO_LAZY_FETCH=1 git cat-file --batch-check='%(objectname) %(objecttype)' < \"$t/bases\" | while read -r o ty; do case $ty in commit|tag) echo \"$o\" ;; esac; done; fi\n")
		}
	}
	// The rest of this payload stays for the second, which carries the
	// bundles alone; the carry has run and is not sent again.
	b.WriteString("  rm -f \"$t/bundle\" \"$t/bases\" \"$t\"/carry.* \"$t\"/sub/*/bundle\n  repose_keep=yes\n  exit 0\nfi\n")
	return b.String()
}

// prefetchSubPlace is the start of a prefetched submodule's lines in the
// apply, in place of `git init`: the repository its fetch made moves into
// the submodule's directory, or, when one is already there, lends it its
// objects.
func prefetchSubPlace(p string, r *prefetchRepo) string {
	src := "$repose_pf/" + r.key
	return fmt.Sprintf(`mkdir -p %[1]s
if [ -d "%[2]s/.git" ]; then
  if [ ! -e %[1]s/.git ]; then mv "%[2]s/.git" %[1]s/.git
  else git -C %[1]s fetch -q --no-tags "$PWD/%[2]s" '+refs/remotes/origin/*:refs/remotes/origin/*' '+refs/tags/*:refs/tags/*'
  fi
fi
[ -e %[1]s/.git ] || git init -q %[1]s
`, p, src)
}

// prefetchEndScript ends an apply: the fetched repositories and the
// payload are gone, the lock is released, and a checkout fetched without
// blobs starts its backfill.
func prefetchEndScript(plan *prefetchPlan) string {
	s := ""
	if plan.active() {
		s = "rm -rf \"$repose_pf\"\nexec 9>&-\n"
	}
	return s + backfillScript
}

// backfillScript starts the backfill of a checkout fetched without blobs
// (its marker, or the promisor remote of a fetch that ended before it
// could write one), once (flock), detached and niced, with no stdio tied
// to the ssh (a redirection inside a backgrounded list leaves the list's
// own stdout on the ssh, which then waits for it) and without the
// prefetch lock's descriptor. It removes the promisor remote and the
// marker once the whole history is there; a backfill that fails (no
// network) leaves them, and the next sync starts it again.
const backfillScript = `if [ -f ` + backfillMarker + ` ] || git config --get remote.` + backfillRemote + `.url >/dev/null 2>&1; then
  nohup nice -n 10 flock -n .git/repose-backfill.lock sh -c 'git backfill --all >/dev/null 2>&1 && { git config --remove-section remote.` + backfillRemote + ` 2>/dev/null; rm -f ` + backfillMarker + `; }' </dev/null >/dev/null 2>&1 9>&- &
fi
`

// prefetchProbeScript is the probe's part: what an earlier first sync
// that stopped part way left (its fetched repositories and kept payload,
// a backfill it never started) is cleared or started, unless an apply
// with fetches is running on the checkout now.
const prefetchProbeScript = `if [ -e ` + prefetchDir + ` ] || [ -f ` + backfillMarker + ` ] || git config --get remote.` + backfillRemote + `.url >/dev/null 2>&1; then
  ( flock -n 9 || exit 0
    rm -rf ` + prefetchDir + `
` + backfillScript + `  ) 9>` + prefetchLock + `
fi
`

// prefetchResult is what the apply said about the fetches.
type prefetchResult struct {
	status map[string]string // key: "partial", "full" or "failed <why>"
	ms     map[string]string
	need   bool
	// needPayload: a second apply found the first payload gone.
	needPayload bool
	tips        map[string][]string
}

func parsePrefetch(out string) prefetchResult {
	r := prefetchResult{status: map[string]string{}, ms: map[string]string{}, tips: map[string][]string{}}
	section := ""
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "#needpayload":
			r.needPayload = true
		case l == "#needfull":
			r.need = true
			section = ""
		case strings.HasPrefix(l, "#pftips "):
			section = strings.TrimSpace(strings.TrimPrefix(l, "#pftips "))
			if _, ok := r.tips[section]; !ok {
				r.tips[section] = nil
			}
		case strings.HasPrefix(l, "#pfms "):
			k, v, _ := strings.Cut(strings.TrimPrefix(l, "#pfms "), " ")
			r.ms[k] = v
		case strings.HasPrefix(l, "#pf "):
			k, v, _ := strings.Cut(strings.TrimPrefix(l, "#pf "), " ")
			r.status[k] = v
		case strings.HasPrefix(l, "#"):
			section = ""
		case section != "" && l != "":
			seen := false
			for _, x := range r.tips[section] {
				seen = seen || x == l
			}
			if !seen {
				r.tips[section] = append(r.tips[section], l)
			}
		}
	}
	return r
}

// fetched reports whether the fetch for key brought the history.
func (r prefetchResult) fetched(key string) bool {
	s := r.status[key]
	return s == "partial" || s == "full"
}

// failedWhy is git's reason when the fetch for key failed, or "".
func (r prefetchResult) failedWhy(key string) string {
	s, ok := r.status[key]
	if !ok || r.fetched(key) {
		return ""
	}
	why := strings.TrimSpace(strings.TrimPrefix(s, "failed"))
	if why == "" {
		why = "git fetch failed"
	}
	return why
}

// observeSyncPayload, when set (tests only), sees the entry names of
// every sync payload before it is sent.
var observeSyncPayload func(names []string)

// filterTar copies the entries of the tar src for which keep is true into
// a new temporary file, read from the start.
func filterTar(src *os.File, keep func(name string) bool) (*os.File, error) {
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	dst, err := os.CreateTemp("", "repose-sync-*.tar")
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*os.File, error) {
		_ = dst.Close()
		_ = os.Remove(dst.Name())
		return nil, err
	}
	tr, tw := tar.NewReader(src), tar.NewWriter(dst)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail(err)
		}
		if !keep(h.Name) {
			continue
		}
		if err := tw.WriteHeader(h); err != nil {
			return fail(err)
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return fail(err)
		}
	}
	if err := tw.Close(); err != nil {
		return fail(err)
	}
	return dst, nil
}

// tarNames lists the entry names of the tar f.
func tarNames(f *os.File) ([]string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	var names []string
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return names, nil
		}
		if err != nil {
			return nil, err
		}
		names = append(names, h.Name)
	}
}
