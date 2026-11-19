package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
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
//   - REPOSE_RELEASE_ASSETS_APPLIANCE=<version>: the appliance version to
//     check instead of applianceVersion.
//   - REPOSE_RELEASE_ASSETS_CLI=latest|none|v0.1.N: which CLI release to
//     check (default latest). A tag also requires releases/latest to
//     resolve to it, since that is what a customer gets.
//   - REPOSE_RELEASE_ASSETS_INSTALL=<url>|none: the install script
//     (default https://repose.run/install.sh).

const (
	envReleaseAssets          = "REPOSE_RELEASE_ASSETS"
	envReleaseAssetsAppliance = "REPOSE_RELEASE_ASSETS_APPLIANCE"
	envReleaseAssetsCLI       = "REPOSE_RELEASE_ASSETS_CLI"
	envReleaseAssetsInstall   = "REPOSE_RELEASE_ASSETS_INSTALL"
	installScriptURL          = "https://repose.run/install.sh"
	// applianceImageMinBytes: the image is about 2 GB compressed
	// (2,081,526,806 bytes for 2026.11.19); an asset under this is a
	// placeholder or a broken upload, not the image.
	applianceImageMinBytes = 1 << 30
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

	version := envOr(envReleaseAssetsAppliance, applianceVersion)
	image := strings.Replace(applianceURL, applianceVersion, version, 1)
	if version != applianceVersion {
		fmt.Printf("appliance %s (the shipped code pins %s)\n", version, applianceVersion)
	}
	fix := `publish it: docs/ops/RUNBOOK.md "The Mac appliance image and install line"`
	if n, ok := c.head(ctx, image, "the appliance image", fix); ok && n < applianceImageMinBytes {
		c.fail("%s is %d bytes, under the %d an image has; the upload is not the image", image, n, applianceImageMinBytes)
	}
	if body, ok := c.get(ctx, image+".sha256", "the appliance image's checksum", fix); ok {
		f := strings.Fields(string(body))
		if len(f) == 0 || !hex64.MatchString(f[0]) {
			c.fail("%s.sha256 does not hold a SHA-256 (%q)", image, firstLineOf(body))
		}
	}

	if body, ok := c.get(ctx, vfkitURL, "vfkit", "bump vfkitVersion and vfkitSHA256 in internal/cli/appliance.go to a published release"); ok {
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != vfkitSHA256 {
			c.fail("%s: checksum %s, not the %s the code pins; every host add would refuse it", vfkitURL, got, vfkitSHA256)
		}
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
}

func (c *releaseAssetCheck) fail(format string, args ...any) {
	c.failed++
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
		"appliance.go:applianceURL":           true, // the image and its .sha256
		"appliance.go:vfkitURL":               true,
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
