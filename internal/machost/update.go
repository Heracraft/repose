package machost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReleasesURL is where the updater finds the newest hostd-<version>
// release, anonymously, as the Linux updater does (I-1496).
const ReleasesURL = "https://api.github.com/repos/Heracraft/repose/releases?per_page=30"

// ReleaseDownload is where a release's assets are.
const ReleaseDownload = "https://github.com/Heracraft/repose/releases/download"

// ManifestURL is the darwin manifest of one hostd release.
func ManifestURL(version string) string {
	return ReleaseDownload + "/hostd-" + version + "/" + ManifestFile
}

// Updater is repose-hostd-update on a Mac (DECISIONS I-1496, I-1686):
// the newest signed bundle is unpacked beside the running one, checked,
// and made current by one rename of a symlink; hostd restarts and every
// machine keeps running, since each VM is its own launchd job. A
// launcher that changed reaches a running machine by a park and a
// restore, and only in an unlocked session (I-1662); otherwise at the
// machine's next start. A hostd that does not answer within HealthWait
// is rolled back.
type Updater struct {
	Paths       Paths
	ReleasesURL string // "" is ReleasesURL
	ManifestURL string // a manifest to use instead of the releases lookup (a lab's)
	PubKeyPEM   string // "" is ReleasePubKeyPEM
	Force       bool   // install even when not newer
	HTTP        *http.Client
	// Exec runs a command and returns its combined output.
	Exec func(ctx context.Context, name string, args ...string) ([]byte, error)
	// Healthy is nil once the restarted hostd answers on its socket.
	Healthy func(ctx context.Context) error
	// CycleLaunchers parks and restores every running machine, so each
	// runs the new repose-vm; it returns false with a reason when the
	// session cannot restore (locked, nobody logged in).
	CycleLaunchers func(ctx context.Context) (bool, string, error)
	HealthWait     time.Duration // "" is 60 s
	Log            func(event string, kv ...any)
}

// ErrRolledBack is an update whose hostd did not come up; the previous
// version is current again.
var ErrRolledBack = errors.New("the new hostd did not answer; the previous version is current again")

func (u *Updater) log(event string, kv ...any) {
	if u.Log != nil {
		u.Log(event, kv...)
	}
}

// CurrentVersion is the running bundle's VERSION, "" when none is current.
func (p Paths) CurrentVersion() string {
	b, err := os.ReadFile(filepath.Join(p.Current(), "VERSION"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// currentDir is the versions/ directory current points at.
func (p Paths) currentDir() string {
	t, err := os.Readlink(p.Current())
	if err != nil {
		return ""
	}
	return filepath.Base(t)
}

// Run is one update pass. nil when the Mac is current or was updated.
func (u *Updater) Run(ctx context.Context) error {
	m, err := u.manifest(ctx)
	if err != nil {
		return err
	}
	cur := u.Paths.CurrentVersion()
	if !u.Force && cur != "" && CompareVersions(m.Version, cur) <= 0 {
		u.log("current", "version", cur, "newest", m.Version)
		return nil
	}
	if err := os.MkdirAll(u.Paths.Versions(), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(u.Paths.UpdateState(), "bundle.tmp")
	if err := u.fetch(ctx, m.Darwin.ARM64, tmp); err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	staged := u.Paths.Version(m.Version) + ".new"
	_ = os.RemoveAll(staged)
	f, err := os.Open(tmp)
	if err != nil {
		return err
	}
	v, err := ExtractBundle(f, staged)
	_ = f.Close()
	if err != nil {
		return err
	}
	if v != m.Version {
		_ = os.RemoveAll(staged)
		return fmt.Errorf("the bundle says %s, the manifest %s", v, m.Version)
	}
	out, err := u.Exec(ctx, filepath.Join(staged, "bin", "hostd"), "version")
	if got := strings.TrimSpace(string(out)); err != nil || got != "hostd "+m.Version {
		_ = os.RemoveAll(staged)
		return fmt.Errorf("the bundle's hostd answers %q, not hostd %s", got, m.Version)
	}
	dest := u.Paths.Version(m.Version)
	if _, err := os.Stat(dest); err == nil {
		if m.Version == cur {
			// A forced reinstall of the running version: the running
			// files stay until the swap is done.
			dest = dest + ".r" + fmt.Sprint(time.Now().Unix())
		} else {
			_ = os.RemoveAll(dest)
		}
	}
	if err := os.Rename(staged, dest); err != nil {
		return err
	}
	prev := u.Paths.currentDir()
	prevLauncher, _ := FileSHA256(u.Paths.Bin("repose-vm"))
	if err := u.swap(filepath.Base(dest)); err != nil {
		return err
	}
	u.log("swapped", "from", prev, "to", m.Version)
	if err := u.restartHostd(ctx); err != nil || u.waitHealthy(ctx) != nil {
		if prev == "" {
			return fmt.Errorf("the new hostd did not answer: %v", err)
		}
		_ = u.swap(prev)
		_ = u.restartHostd(ctx)
		u.log("rolled_back", "to", prev)
		return ErrRolledBack
	}
	newLauncher, _ := FileSHA256(u.Paths.Bin("repose-vm"))
	if prevLauncher != "" && newLauncher != prevLauncher && u.CycleLaunchers != nil {
		ok, why, err := u.CycleLaunchers(ctx)
		switch {
		case err != nil:
			u.log("launcher_cycle_failed", "err", err.Error())
		case !ok:
			u.log("launcher_waits", "reason", why)
		default:
			u.log("launcher_cycled")
		}
	}
	u.prune(filepath.Base(dest), prev)
	u.log("updated", "version", m.Version)
	return nil
}

// swap points current at versions/<dir> with one rename.
func (u *Updater) swap(dir string) error {
	return SwapCurrent(u.Paths, dir)
}

// SwapCurrent makes versions/<dir> current: a new symlink renamed over
// the old one, so a reader sees one version or the other, never none.
func SwapCurrent(p Paths, dir string) error {
	tmp := p.Current() + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(filepath.Join("versions", dir), tmp); err != nil {
		return err
	}
	return os.Rename(tmp, p.Current())
}

func (u *Updater) restartHostd(ctx context.Context) error {
	_, err := u.Exec(ctx, "/bin/launchctl", "kickstart", "-k", "system/"+HostdLabel)
	return err
}

func (u *Updater) waitHealthy(ctx context.Context) error {
	if u.Healthy == nil {
		return nil
	}
	wait := u.HealthWait
	if wait == 0 {
		wait = 60 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		err := u.Healthy(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// prune keeps the current version and the one before it.
func (u *Updater) prune(keep ...string) {
	ents, err := os.ReadDir(u.Paths.Versions())
	if err != nil {
		return
	}
	for _, e := range ents {
		name := e.Name()
		kept := false
		for _, k := range keep {
			if k != "" && name == k {
				kept = true
			}
		}
		if !kept {
			_ = os.RemoveAll(filepath.Join(u.Paths.Versions(), name))
		}
	}
}

func (u *Updater) client() *http.Client {
	if u.HTTP != nil {
		return u.HTTP
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func (u *Updater) get(ctx context.Context, url string, limit int64) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "repose-hostd-update")
	resp, err := u.client().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("%s answered %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	return b, resp.StatusCode, err
}

func (u *Updater) pub() string {
	if u.PubKeyPEM != "" {
		return u.PubKeyPEM
	}
	return ReleasePubKeyPEM
}

// manifest finds and verifies the newest manifest, and keeps it; a
// lookup GitHub refuses (403, 429: its anonymous limit is 60 an hour per
// address) uses the one kept, as on Linux.
func (u *Updater) manifest(ctx context.Context) (Manifest, error) {
	kept := filepath.Join(u.Paths.UpdateState(), ManifestFile)
	url := u.ManifestURL
	if url == "" {
		rel := u.ReleasesURL
		if rel == "" {
			rel = ReleasesURL
		}
		body, code, err := u.get(ctx, rel, 8<<20)
		if err != nil {
			if code == http.StatusForbidden || code == http.StatusTooManyRequests {
				b, kerr := os.ReadFile(kept)
				if kerr != nil {
					return Manifest{}, fmt.Errorf("the releases lookup was refused (%d) and no manifest is kept", code)
				}
				u.log("lookup_refused_using_kept", "status", code)
				return ParseManifest(b, u.pub())
			}
			return Manifest{}, err
		}
		url, err = NewestManifestURL(body)
		if err != nil {
			return Manifest{}, err
		}
	}
	b, _, err := u.get(ctx, url, 1<<20)
	if err != nil {
		return Manifest{}, err
	}
	m, err := ParseManifest(b, u.pub())
	if err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(u.Paths.UpdateState(), 0o700); err == nil {
		_ = os.WriteFile(kept+".tmp", b, 0o600)
		_ = os.Rename(kept+".tmp", kept)
	}
	return m, nil
}

// NewestManifestURL picks, from GitHub's releases list, the newest
// non-draft hostd-<version> carrying a manifest-darwin.json, in version
// order, and returns that asset's URL.
func NewestManifestURL(releases []byte) (string, error) {
	var rs []struct {
		Tag    string `json:"tag_name"`
		Draft  bool   `json:"draft"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(releases, &rs); err != nil {
		return "", fmt.Errorf("releases: %w", err)
	}
	best, url := "", ""
	for _, r := range rs {
		v, ok := strings.CutPrefix(r.Tag, "hostd-")
		if r.Draft || !ok || !ValidVersion(v) {
			continue
		}
		for _, a := range r.Assets {
			if a.Name == ManifestFile && (best == "" || CompareVersions(v, best) > 0) {
				best, url = v, a.URL
			}
		}
	}
	if url == "" {
		return "", errors.New("no hostd release carries a " + ManifestFile)
	}
	return url, nil
}

// fetch downloads a to dest and checks its SHA-256.
func (u *Updater) fetch(ctx context.Context, a Asset, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	body, _, err := u.get(ctx, a.URL, 1<<30)
	if err != nil {
		return err
	}
	if got := sha256Hex(body); got != a.SHA256 {
		return fmt.Errorf("the bundle's checksum is %s, the manifest's %s", got, a.SHA256)
	}
	return os.WriteFile(dest, body, 0o600)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
