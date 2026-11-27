package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/machost"
)

// The URLs this package fetches on a customer's machine must answer 200 to
// a client with no credentials, because that is the client a customer is.
// On 2026-11-19 CLI v0.1.51 was published pinning an appliance image whose
// release had never been published: `repose host add` printed "the server
// answered 404" on every Mac for 47 minutes. The same evening the appliance
// release, once published, became GitHub's "latest", so install.sh
// resolved the version to `appliance-2026.11.19` and every install on every
// platform failed for 26 minutes. Nothing in the release machinery looked
// at either (DECISIONS I-1398).
//
// TestReleaseAssetsAnswerAnonymously is that look. It is skipped unless
// REPOSE_RELEASE_ASSETS=1, since it needs the network; `ops/dev/release-assets`
// sets it, and `release-queue add`, `release.yml` and `release-assets.yml`
// run that. The URLs come from the constants the shipped code uses, not
// from a list kept here, and TestReleaseAssetURLsAreAllChecked holds the
// two together.
//
// Overrides, for showing it red and for checking a tag that is not the
// latest one:
//   - REPOSE_RELEASE_ASSETS_MAC=<version>|none: the hostd release whose
//     manifest-darwin.json and bundle `repose host add` installs on a Mac,
//     instead of macHostVersion (DECISIONS I-1684, I-1686).
//   - REPOSE_RELEASE_ASSETS_CLI=latest|none|v0.1.N: which CLI release to
//     check (default latest). A tag also requires releases/latest to
//     resolve to it, since that is what a customer gets.
//   - REPOSE_RELEASE_ASSETS_INSTALL=<url>|none: the install script
//     (default https://repose.run/install.sh).
//   - REPOSE_RELEASE_ASSETS_GUEST=<version>|none|unpublished: the erofs
//     guest base release guest-<version> (DECISIONS I-1500). No default
//     here: the base version is a database row, not a constant, so
//     ops/dev/release-assets passes the newest guest-* release, or
//     "unpublished" when there is none. Unset is "none".
//   - REPOSE_RELEASE_ASSETS_HOSTD=<version>|none|unpublished: the
//     release hostd-<version> whose manifest.json an owned host's hostd
//     updates from (DECISIONS I-1496): ops/dev/release-assets passes the
//     newest hostd-* release that carries one. Unset is "none".
//
// The Mac's bundle is the one the code pins (macHostVersion): its
// manifest must verify against the release key and name a bundle that
// answers, matches its checksum and unpacks.

const (
	envReleaseAssets        = "REPOSE_RELEASE_ASSETS"
	envReleaseAssetsMac     = "REPOSE_RELEASE_ASSETS_MAC"
	envReleaseAssetsCLI     = "REPOSE_RELEASE_ASSETS_CLI"
	envReleaseAssetsInstall = "REPOSE_RELEASE_ASSETS_INSTALL"
	envReleaseAssetsGuest   = "REPOSE_RELEASE_ASSETS_GUEST"
	envReleaseAssetsHostd   = "REPOSE_RELEASE_ASSETS_HOSTD"
	installScriptURL        = "https://repose.run/install.sh"
	// guestFileMinBytes: the smallest of a guest bundle's files is the
	// initramfs, a few MiB; a file under 1 MiB is a placeholder.
	guestFileMinBytes = 1 << 20
	// guestPartMaxBytes: a bundle file over GitHub's 2 GiB asset cap is
	// published in parts of at most 1900 MiB (DECISIONS I-1504).
	guestPartMaxBytes = 1900 << 20
)

// guestArches and guestFiles are the names of a guest-<version>
// release: <arch>-<file> for both arches, and one SHA256SUMS listing all
// eight (DECISIONS I-1500).
var (
	guestArches = []string{"amd64", "arm64"}
	guestFiles  = []string{"vmlinuz", "initramfs.img", "rootfs.erofs", "manifest.json"}
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestReleaseAssetsAnswerAnonymously(t *testing.T) {
	if os.Getenv(envReleaseAssets) != "1" {
		t.Skipf("set %s=1 to check the release assets over the network (ops/dev/release-assets)", envReleaseAssets)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c := &releaseAssetCheck{t: t, client: anonymousClient(), noRedirect: anonymousClient()}
	c.noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	if version := envOr(envReleaseAssetsMac, macHostVersion); version != "none" {
		c.macHost(ctx, version)
	}

	switch v := envOr(envReleaseAssetsGuest, "none"); v {
	case "none":
	case "unpublished":
		c.checked++
		c.fail("no guest-<version> release is published (gh release list shows none); publish it: .github/workflows/guest-image.yml, then repose-admin base publish --format erofs")
	default:
		c.guestRelease(ctx, v)
	}

	switch v := envOr(envReleaseAssetsHostd, "none"); v {
	case "none":
	case "unpublished":
		c.checked++
		c.fail("no hostd-<version> release with a manifest.json is published; publish it: an appliance-* tag runs the hostd job in .github/workflows/appliance-image.yml")
	default:
		c.hostdRelease(ctx, v)
	}

	switch want := envOr(envReleaseAssetsCLI, "latest"); want {
	case "none":
	default:
		c.cliRelease(ctx, want)
	}

	switch u := envOr(envReleaseAssetsInstall, installScriptURL); u {
	case "none":
	default:
		if body, ok := c.get(ctx, u, "the install script", "the web deploy serves it (docs/ops/RELEASE.md)"); ok {
			for _, s := range []string{"RELEASE_PUBKEY='", "/releases/latest", "/releases/download/"} {
				if !strings.Contains(string(body), s) {
					c.fail("%s does not contain %q; it is not install.sh", u, s)
				}
			}
		}
	}

	if c.failed > 0 {
		fmt.Printf("%d of %d do not answer; the release does not go\n", c.failed, c.checked)
		return
	}
	fmt.Printf("%d assets answer 200 anonymously\n", c.checked)
}

// macHost checks what `repose host add` fetches on a Mac: the pinned
// release's manifest-darwin.json, signed by the release key, and the
// bundle it names, whole, matching its checksum (DECISIONS I-1686).
func (c *releaseAssetCheck) macHost(ctx context.Context, version string) {
	if version != macHostVersion {
		fmt.Printf("mac host %s (the shipped code pins %s)\n", version, macHostVersion)
	}
	fix := "publish it: an appliance-" + version + " tag runs the mac and hostd jobs in .github/workflows/appliance-image.yml"
	body, ok := c.get(ctx, machost.ManifestURL(version), "the Mac's manifest", fix)
	if !ok {
		return
	}
	m, err := machost.ParseManifest(body, machost.ReleasePubKeyPEM)
	if err != nil {
		c.fail("%s: %v; every Mac would refuse it", machost.ManifestURL(version), err)
		return
	}
	if m.Version != version {
		c.fail("%s names %s, not %s", machost.ManifestURL(version), m.Version, version)
	}
	b, ok := c.get(ctx, m.Darwin.ARM64.URL, "the Mac's bundle", fix)
	if !ok {
		return
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != m.Darwin.ARM64.SHA256 {
		c.fail("%s: checksum %s, not the manifest's %s", m.Darwin.ARM64.URL, got, m.Darwin.ARM64.SHA256)
		return
	}
	dir := filepath.Join(c.t.TempDir(), "bundle")
	if v, err := machost.ExtractBundle(bytes.NewReader(b), dir); err != nil || v != version {
		c.fail("%s does not unpack as bundle %s: %v", m.Darwin.ARM64.URL, version, err)
	}
}

// guestRelease checks what hostd fetches for an erofs guest on its base:
// SHA256SUMS naming the eight files, each file, and each arch's manifest
// naming this version (DECISIONS I-1478, I-1500). A GitHub release asset
// is capped at 2 GiB and the amd64 rootfs is larger, so each of the six
// bundle files may instead be published as <name>.part0, <name>.part1,
// ..., each at most guestPartMaxBytes; SHA256SUMS then lists the parts
// and not the whole (DECISIONS I-1504).
func (c *releaseAssetCheck) guestRelease(ctx context.Context, version string) {
	base := releaseDownloadURL + "/guest-" + version + "/"
	fix := "publish it: .github/workflows/guest-image.yml builds and uploads guest-" + version
	// parts[name] is the part numbers SHA256SUMS lists for a bundle file.
	parts := map[string][]int{}
	if sums, ok := c.get(ctx, base+"SHA256SUMS", "the guest release's SHA256SUMS", fix); ok {
		listed := map[string]bool{}
		for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && hex64.MatchString(f[0]) {
				listed[strings.TrimPrefix(f[1], "*")] = true
			}
		}
		known := map[string]bool{}
		var missing []string
		for _, a := range guestArches {
			for _, f := range guestFiles {
				name := a + "-" + f
				known[name] = true
				var nums []int
				if f != "manifest.json" {
					for l := range listed {
						if n, ok := partNumber(l, name); ok {
							nums = append(nums, n)
							known[l] = true
						}
					}
				}
				sort.Ints(nums)
				switch {
				case len(nums) > 0 && listed[name]:
					c.fail("%s lists %s both whole and in parts; publish one or the other", base+"SHA256SUMS", name)
				case len(nums) > 0:
					for i, n := range nums {
						if n != i {
							c.fail("%s lists %s.part%d but not %s.part%d; the parts are not the whole file", base+"SHA256SUMS", name, n, name, i)
							break
						}
					}
					parts[name] = nums
				case !listed[name]:
					missing = append(missing, name)
				}
			}
		}
		var extra []string
		for l := range listed {
			if !known[l] {
				extra = append(extra, l)
			}
		}
		sort.Strings(extra)
		if len(missing) > 0 {
			c.fail("%s does not list %v, whole or as .part0, .part1, ...", base+"SHA256SUMS", missing)
		}
		if len(extra) > 0 {
			c.fail("%s lists %v, which are not files of a guest release", base+"SHA256SUMS", extra)
		}
	}
	for _, a := range guestArches {
		for _, f := range guestFiles {
			name := a + "-" + f
			if f == "manifest.json" {
				body, ok := c.get(ctx, base+name, "the "+a+" guest manifest", fix)
				if !ok {
					continue
				}
				var m struct {
					Version string `json:"version"`
					Arch    string `json:"arch"`
				}
				if err := json.Unmarshal(body, &m); err != nil {
					c.fail("%s is not JSON: %v", base+name, err)
				} else if m.Version != version || m.Arch != a {
					c.fail("%s names version %q arch %q, not %s %s", base+name, m.Version, m.Arch, version, a)
				}
				continue
			}
			if nums, ok := parts[name]; ok {
				var total int64
				for _, n := range nums {
					part := fmt.Sprintf("%s.part%d", name, n)
					sz, ok := c.head(ctx, base+part, fmt.Sprintf("part %d of the %s guest %s", n, a, f), fix)
					if !ok || sz < 0 {
						continue
					}
					total += sz
					if sz > guestPartMaxBytes {
						c.fail("%s is %d bytes, over the %d a part may be; GitHub refuses an asset over 2 GiB", base+part, sz, guestPartMaxBytes)
					}
				}
				if total > 0 && total < guestFileMinBytes {
					c.fail("the parts of %s add up to %d bytes, under the %d a bundle file has; the upload is not the file", base+name, total, guestFileMinBytes)
				}
				continue
			}
			if n, ok := c.head(ctx, base+name, "the "+a+" guest "+f, fix); ok && n >= 0 && n < guestFileMinBytes {
				c.fail("%s is %d bytes, under the %d a bundle file has; the upload is not the file", base+name, n, guestFileMinBytes)
			}
		}
	}
}

// partNumber is N when asset is name.partN.
func partNumber(asset, name string) (int, bool) {
	rest, ok := strings.CutPrefix(asset, name+".part")
	if !ok || rest == "" {
		return 0, false
	}
	n := 0
	for _, r := range rest {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	if len(rest) > 1 && rest[0] == '0' {
		return 0, false
	}
	return n, true
}

// hostdRelease checks the manifest of hostd-<version>, the release an
// owned host's hostd updates from (I-1496), and every binary it names.
func (c *releaseAssetCheck) hostdRelease(ctx context.Context, version string) {
	u := releaseDownloadURL + "/hostd-" + version + "/manifest.json"
	fix := "publish it: an appliance-* tag runs the hostd job in .github/workflows/appliance-image.yml"
	body, ok := c.get(ctx, u, "the hostd-"+version+" manifest", fix)
	if !ok {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		c.fail("%s is not JSON: %v", u, err)
		return
	}
	if v, _ := m["version"].(string); v != version {
		c.fail("%s names version %q, not %s", u, v, version)
	}
	urls := manifestURLs(m)
	if len(urls) == 0 {
		c.fail("%s names no url; an appliance's hostd has nothing to fetch", u)
	}
	for _, b := range urls {
		c.head(ctx, b, "a hostd binary "+u+" names", fix)
	}
}

// manifestURLs is every string field named "url" in a decoded JSON
// value, sorted, so the per-arch shape of the manifest is not pinned here.
func manifestURLs(v any) []string {
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, x := range t {
				if s, ok := x.(string); ok && k == "url" {
					out = append(out, s)
					continue
				}
				walk(x)
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(v)
	sort.Strings(out)
	return out
}

// cliRelease checks what install.sh and `repose version update` fetch: the
// tag releases/latest resolves to, then checksums.txt, its signature
// against the release key the CLI carries, and the four archives it names.
func (c *releaseAssetCheck) cliRelease(ctx context.Context, want string) {
	tag, ok := c.latestTag(ctx, want)
	if !ok {
		return
	}
	base := releaseDownloadURL + "/" + tag + "/"
	fix := "docs/ops/RELEASE.md \"The release signing key\": the sign job publishes the draft"
	checksums, ok := c.get(ctx, base+"checksums.txt", "checksums.txt", fix)
	if !ok {
		return
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(checksums)), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && hex64.MatchString(f[0]) {
			names = append(names, f[1])
		}
	}
	sort.Strings(names)
	var expect []string
	for _, p := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"} {
		expect = append(expect, "repose_"+tag+"_"+p+".tar.gz")
	}
	if strings.Join(names, " ") != strings.Join(expect, " ") {
		c.fail("%s names %v, not the four archives %v", base+"checksums.txt", names, expect)
	}
	if sig, ok := c.get(ctx, base+"checksums.txt.sig", "checksums.txt.sig", fix); ok {
		if err := verifyChecksums(checksums, sig); err != nil {
			c.fail("%s: %v; install.sh and repose version update refuse this release", base+"checksums.txt.sig", err)
		}
	}
	for _, n := range names {
		c.head(ctx, base+n, n, fix)
	}
}

// latestTag is the tag releases/latest redirects to, which must be a CLI
// release: on 2026-11-19 it was appliance-2026.11.19 for 26 minutes. When
// a tag is wanted, the redirect is read again for up to two minutes, since
// GitHub serves the old answer for a while after a release is published.
func (c *releaseAssetCheck) latestTag(ctx context.Context, want string) (string, bool) {
	var tag string
	deadline := time.Now().Add(2 * time.Minute)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseLatestURL, nil)
		if err != nil {
			c.fail("%s: %v", releaseLatestURL, err)
			return "", false
		}
		resp, err := c.noRedirect.Do(req)
		if err != nil {
			c.fail("%s: %v", releaseLatestURL, err)
			return "", false
		}
		_ = resp.Body.Close()
		loc := resp.Header.Get("Location")
		if resp.StatusCode/100 != 3 || !strings.Contains(loc, "/releases/tag/") {
			c.fail("%s answered %d, not a redirect to a release", releaseLatestURL, resp.StatusCode)
			return "", false
		}
		tag = loc[strings.LastIndex(loc, "/")+1:]
		if want == "latest" || tag == want || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Second)
	}
	c.checked++
	if !releaseTagRe.MatchString(tag) {
		c.fail("%s resolves to %s, which is not a CLI release: install.sh would fetch repose_%s_<os>_<arch>.tar.gz; gh release edit <the newest v0.1.N> --latest, and publish every other release with --latest=false", releaseLatestURL, tag, tag)
		return "", false
	}
	if want != "latest" && tag != want {
		c.fail("%s resolves to %s, not %s; a customer installs %s", releaseLatestURL, tag, want, tag)
		return "", false
	}
	fmt.Printf("302 %s -> %s\n", releaseLatestURL, tag)
	return tag, true
}

type releaseAssetCheck struct {
	t          *testing.T
	client     *http.Client
	noRedirect *http.Client
	checked    int
	failed     int
	// errs, when set, collects the failures instead of failing t: the
	// offline test of the check itself reads them.
	errs *[]string
}

func (c *releaseAssetCheck) fail(format string, args ...any) {
	c.failed++
	if c.errs != nil {
		*c.errs = append(*c.errs, fmt.Sprintf(format, args...))
		return
	}
	c.t.Errorf(format, args...)
}

// head is a request for an asset's status and size without its body.
func (c *releaseAssetCheck) head(ctx context.Context, url, what, fix string) (int64, bool) {
	c.checked++
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		c.fail("%s: %v", url, err)
		return 0, false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.fail("%s: %v", url, err)
		return 0, false
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.fail("%d %s (%s; %s)", resp.StatusCode, url, what, fix)
		return 0, false
	}
	fmt.Printf("200 %d %s\n", resp.ContentLength, url)
	return resp.ContentLength, true
}

// get is an asset's body, up to 256 MiB.
func (c *releaseAssetCheck) get(ctx context.Context, url, what, fix string) ([]byte, bool) {
	c.checked++
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		c.fail("%s: %v", url, err)
		return nil, false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.fail("%s: %v", url, err)
		return nil, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		c.fail("%d %s (%s; %s)", resp.StatusCode, url, what, fix)
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		c.fail("%s: reading it: %v", url, err)
		return nil, false
	}
	fmt.Printf("200 %d %s\n", len(body), url)
	return body, true
}

// anonymousClient is what a customer's machine is: no cookie, no token, no
// netrc. Go's client sends none of those unless asked, and nothing here
// asks; GITHUB_TOKEN in the environment is not read.
func anonymousClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Minute}
}

// TestReleaseAssetURLsAreAllChecked walks this package's shipped sources
// for release download URLs and asserts each one is a URL the network
// test above checks. A fetch added to the code without a line here is
// how the check goes quiet while the code fetches something new.
func TestReleaseAssetURLsAreAllChecked(t *testing.T) {
	covered := map[string]bool{
		"versionupdate.go:releaseLatestURL":   true,
		"versionupdate.go:releaseDownloadURL": true,
	}
	re := regexp.MustCompile(`^\s*(\w+)\s*=\s*"https://[^"]*/releases/(download|latest)`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(src), "\n") {
			m := re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			found++
			if !covered[f+":"+m[1]] {
				t.Errorf("%s pins a release URL in %s that TestReleaseAssetsAnswerAnonymously does not check", f, m[1])
			}
		}
	}
	if found != len(covered) {
		t.Errorf("found %d release URLs in the package's sources, the check covers %d", found, len(covered))
	}
}

// TestReleaseAssetsCheckGuestAndHostd runs the guest-<version> and
// hostd-<version> checks against a local server, offline: a complete
// release passes, and each way of being incomplete is named (I-1500).
func TestReleaseAssetsCheckGuestAndHostd(t *testing.T) {
	const version = "2026.11.22.1"
	big := strings.Repeat("x", guestFileMinBytes)
	files := map[string]string{}
	var sums []string
	for _, a := range guestArches {
		for _, f := range guestFiles {
			body := big
			if f == "manifest.json" {
				body = `{"version":"` + version + `","arch":"` + a + `"}`
			}
			files["/guest-"+version+"/"+a+"-"+f] = body
			sum := sha256.Sum256([]byte(body))
			sums = append(sums, hex.EncodeToString(sum[:])+"  "+a+"-"+f)
		}
	}
	files["/guest-"+version+"/SHA256SUMS"] = strings.Join(sums, "\n") + "\n"
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hostd-2026.11.22.2/manifest.json" {
			_, _ = fmt.Fprintf(w, `{"version":"2026.11.22.2","arches":{"arm64":{"url":%q,"sha256":"x"},"amd64":{"url":%q,"sha256":"x"}}}`,
				srvURL+"/hostd-2026.11.22.2/hostd-linux-arm64", srvURL+"/hostd-2026.11.22.2/hostd-linux-amd64")
			return
		}
		if r.URL.Path == "/hostd-2026.11.22.2/hostd-linux-arm64" || r.URL.Path == "/hostd-2026.11.22.2/hostd-linux-amd64" {
			_, _ = io.WriteString(w, "bin")
			return
		}
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	srvURL = srv.URL
	old := releaseDownloadURL
	releaseDownloadURL = srv.URL
	defer func() { releaseDownloadURL = old }()

	run := func(fn func(*releaseAssetCheck)) []string {
		var errs []string
		c := &releaseAssetCheck{t: t, client: srv.Client(), errs: &errs}
		fn(c)
		return errs
	}
	ctx := context.Background()
	if errs := run(func(c *releaseAssetCheck) { c.guestRelease(ctx, version) }); len(errs) != 0 {
		t.Errorf("a complete guest release failed: %v", errs)
	}
	if errs := run(func(c *releaseAssetCheck) { c.hostdRelease(ctx, "2026.11.22.2") }); len(errs) != 0 {
		t.Errorf("a complete hostd release failed: %v", errs)
	}

	delete(files, "/guest-"+version+"/arm64-rootfs.erofs")
	errs := run(func(c *releaseAssetCheck) { c.guestRelease(ctx, version) })
	if len(errs) != 1 || !strings.Contains(errs[0], "404") || !strings.Contains(errs[0], "arm64-rootfs.erofs") || !strings.Contains(errs[0], "guest-image.yml") {
		t.Errorf("a missing arm64 rootfs: %v, want one 404 naming it and the fix", errs)
	}
	files["/guest-"+version+"/arm64-rootfs.erofs"] = big

	files["/guest-"+version+"/amd64-manifest.json"] = `{"version":"2026.11.21.1","arch":"amd64"}`
	errs = run(func(c *releaseAssetCheck) { c.guestRelease(ctx, version) })
	if len(errs) != 1 || !strings.Contains(errs[0], "2026.11.21.1") {
		t.Errorf("a manifest of another version: %v, want it named", errs)
	}

	files["/guest-"+version+"/amd64-manifest.json"] = `{"version":"` + version + `","arch":"amd64"}`

	// The amd64 rootfs in two parts (I-1504): SHA256SUMS lists the parts
	// and not the whole, and the whole is not uploaded.
	const rootfs = "amd64-rootfs.erofs"
	delete(files, "/guest-"+version+"/"+rootfs)
	files["/guest-"+version+"/"+rootfs+".part0"] = big
	files["/guest-"+version+"/"+rootfs+".part1"] = "tail"
	withSums := func(names ...string) {
		var lines []string
		for _, l := range sums {
			if !strings.HasSuffix(l, "  "+rootfs) {
				lines = append(lines, l)
			}
		}
		for _, n := range names {
			lines = append(lines, strings.Repeat("0", 64)+"  "+n)
		}
		files["/guest-"+version+"/SHA256SUMS"] = strings.Join(lines, "\n") + "\n"
	}
	withSums(rootfs+".part0", rootfs+".part1")
	if errs := run(func(c *releaseAssetCheck) { c.guestRelease(ctx, version) }); len(errs) != 0 {
		t.Errorf("a release with the rootfs in parts failed: %v", errs)
	}

	delete(files, "/guest-"+version+"/"+rootfs+".part1")
	errs = run(func(c *releaseAssetCheck) { c.guestRelease(ctx, version) })
	if len(errs) != 1 || !strings.Contains(errs[0], "404") || !strings.Contains(errs[0], rootfs+".part1") || !strings.Contains(errs[0], "part 1 of the amd64 guest rootfs.erofs") {
		t.Errorf("a missing part: %v, want one 404 naming %s.part1", errs, rootfs)
	}
	files["/guest-"+version+"/"+rootfs+".part1"] = "tail"

	withSums(rootfs+".part0", rootfs+".part2")
	errs = run(func(c *releaseAssetCheck) { c.guestRelease(ctx, version) })
	if len(errs) == 0 || !strings.Contains(errs[0], rootfs+".part1") {
		t.Errorf("SHA256SUMS skipping a part number: %v, want %s.part1 named", errs, rootfs)
	}

	withSums(rootfs, rootfs+".part0")
	errs = run(func(c *releaseAssetCheck) { c.guestRelease(ctx, version) })
	if len(errs) == 0 || !strings.Contains(errs[0], "both whole and in parts") {
		t.Errorf("SHA256SUMS listing whole and parts: %v, want it refused", errs)
	}

	withSums()
	errs = run(func(c *releaseAssetCheck) { c.guestRelease(ctx, version) })
	if len(errs) == 0 || !strings.Contains(errs[0], "does not list ["+rootfs+"]") {
		t.Errorf("SHA256SUMS listing neither: %v, want %s named", errs, rootfs)
	}

	errs = run(func(c *releaseAssetCheck) { c.hostdRelease(ctx, "2026.11.22.1") })
	if len(errs) != 1 || !strings.Contains(errs[0], "404") || !strings.Contains(errs[0], "hostd-2026.11.22.1") {
		t.Errorf("a hostd release with no manifest: %v, want one 404 naming it", errs)
	}
}
