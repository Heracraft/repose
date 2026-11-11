package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// `repose version update` replaces this binary with a release of the CLI
// (DECISIONS I-1086). It makes the checks install.sh makes, in the same
// order: the signature on checksums.txt against the release key, then the
// archive against checksums.txt. Nothing is written unless both pass, and
// the old bytes are never overwritten in place: the new binary is written
// beside the old one and renamed over it, which leaves the running
// process alone and leaves a signed macOS binary's pages untouched.

// releasePubKeyPEM is the public half of the release signing key, the
// same key install.sh carries (DECISIONS I-430). test/supplychain checks
// the two are byte for byte the same, so a rotation has to change both or
// the build fails.
const releasePubKeyPEM = `-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEq1BgpU75F46qQ3iMHL1E0VOqye+4
3/u1m873IxGrYRM2AZyYkK6QqN92G2qBuMvUi+qb4OYeF7KRF1MQ5oiIdw==
-----END PUBLIC KEY-----`

// firstSignedRelease is the first release with a checksums.txt.sig.
// install.sh pins the ones before it by hash instead (DECISIONS I-430);
// this command has no pins and refuses them.
const firstSignedRelease = "v0.1.28"

var (
	// releaseLatestURL is the redirect install.sh follows to turn "latest"
	// into a tag, and releaseDownloadURL is where a tag's assets live.
	// Variables so a test can point both at its own release server.
	releaseLatestURL   = "https://github.com/heracraft/repose/releases/latest"
	releaseDownloadURL = "https://github.com/heracraft/repose/releases/download"
	releasePubKey      = releasePubKeyPEM
)

var releaseTagRe = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// executablePath is this binary's path with symlinks resolved, "" when it
// cannot be read. A variable so a test can place the binary anywhere.
var executablePath = func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// binaryOwner names the package manager that owns path, "" when none
// does. A binary one of these put there is theirs to replace: a write
// into /nix/store breaks the store's hash, and a Homebrew or snap binary
// comes back at the next upgrade.
func binaryOwner(path string) string {
	if path == "" {
		return ""
	}
	slash := filepath.ToSlash(path)
	switch {
	case strings.HasPrefix(slash, "/nix/store/"):
		return "Nix"
	case strings.Contains(slash, "/Cellar/"), strings.HasPrefix(slash, "/opt/homebrew/"), strings.HasPrefix(slash, "/home/linuxbrew/"):
		return "Homebrew"
	case strings.HasPrefix(slash, "/snap/"):
		return "snap"
	case slash == "/usr/bin/repose", slash == "/bin/repose":
		// install.sh never writes there, so something else did.
		return "your system's package manager"
	}
	return ""
}

// plainVersion is a version as the CLI prints it: without the tag's "v", so
// the running build (0.1.43, from the goreleaser ldflags) and a tag
// (v0.1.44) read as the same kind of thing (DECISIONS I-1088).
func plainVersion(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

// shortHomePath writes a path under the home directory with "~".
func shortHomePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return path
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return path
}

// releaseHTTPClient downloads the release. It is its own client: the
// command runs logged out, so it has no Env and no api token, and nothing
// it fetches is authenticated.
func releaseHTTPClient() *http.Client { return &http.Client{Timeout: 2 * time.Minute} }

// latestReleaseTag resolves "latest" the way install.sh does: the tag in
// the Location of the releases/latest redirect, read without following
// it. The api's X-Repose-CLI-Latest header is not used; a release is
// trusted for its signature, and the header is only a hint.
func latestReleaseTag(ctx context.Context) (string, error) {
	c := releaseHTTPClient()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseLatestURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode < 300 || resp.StatusCode > 399 || loc == "" {
		return "", fmt.Errorf("github.com answered %d with no redirect", resp.StatusCode)
	}
	i := strings.LastIndex(loc, "/tag/")
	if i < 0 {
		return "", errors.New("the redirect names no tag")
	}
	return loc[i+len("/tag/"):], nil
}

// getRelease downloads one of a release's assets.
func getRelease(ctx context.Context, c *http.Client, tag, name string) ([]byte, error) {
	url := releaseDownloadURL + "/" + tag + "/" + name
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, &releaseAssetError{name: name, status: resp.StatusCode}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// releaseAssetError is an asset the release does not have, which tells a
// missing signature (checksums.txt.sig) apart from a network failure.
type releaseAssetError struct {
	name   string
	status int
}

func (e *releaseAssetError) Error() string {
	return fmt.Sprintf("%s: the release server answered %d", e.name, e.status)
}

// verifyChecksums checks checksums.txt against the release key, as
// `openssl dgst -sha256 -verify` does in install.sh.
func verifyChecksums(checksums, sig []byte) error {
	block, _ := pem.Decode([]byte(releasePubKey))
	if block == nil {
		return errors.New("the release key is not PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("the release key does not parse: %w", err)
	}
	pub, ok := key.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("the release key is %T, not ECDSA", key)
	}
	sum := sha256.Sum256(checksums)
	if !ecdsa.VerifyASN1(pub, sum[:], sig) {
		return errors.New("the signature does not verify")
	}
	return nil
}

// checksumFor is the SHA-256 checksums.txt gives for name, "" when it
// names no such file. The file is `<hex>  <name>` per line, as sha256sum
// writes it.
func checksumFor(checksums []byte, name string) string {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			return fields[0]
		}
	}
	return ""
}

// binaryFromArchive is the `repose` file inside a release archive.
func binaryFromArchive(archive []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "repose" {
			return io.ReadAll(io.LimitReader(tr, 256<<20))
		}
	}
	return nil, errors.New("the archive holds no repose binary")
}

// lastClause is an error's last clause, so a message carries what went
// wrong without Go's wrapping chain or the URL it was wrapped with.
func lastClause(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		return msg[i+2:]
	}
	return msg
}

// VersionUpdateCmd is `repose version update [VERSION]`: it replaces this
// binary with the release VERSION names, or with the latest release
// (DECISIONS I-1086). out takes the result, errw the progress.
func VersionUpdateCmd(ctx context.Context, out, errw io.Writer, target string) error {
	// The refusals that need no network come first, so a user who cannot
	// be updated this way is told before anything is downloaded.
	if os.Getenv(envInGuest) == "1" {
		return exitf(ExitUsage, "The repose on a machine comes with its base and updates with it.")
	}
	if goos() == "windows" {
		return exitf(ExitGeneric, "repose has no Windows release. Install it inside WSL: https://repose.run/docs/install")
	}
	if _, ok := parseVersion(cliVersion); !ok {
		return exitf(ExitUsage, "This repose is a development build (%s), so there is no release to update it to.", cliVersion)
	}
	exe := executablePath()
	if exe == "" {
		return exitf(ExitGeneric, "Not updated: could not find this repose's own path.")
	}
	if owner := binaryOwner(exe); owner != "" {
		return exitf(ExitGeneric, "%s belongs to %s. Update it with %s.", exe, owner, owner)
	}

	tag := strings.TrimSpace(target)
	switch {
	case tag == "":
	case releaseTagRe.MatchString("v" + strings.TrimPrefix(tag, "v")):
		tag = "v" + strings.TrimPrefix(tag, "v")
	default:
		return exitf(ExitUsage, "%s is not a release tag, which looks like v0.1.40.", target)
	}
	if tag != "" && versionOlder(tag, firstSignedRelease) {
		return exitf(ExitUsage, "%s came out before releases were signed. Install it with install.sh --version %s.", tag, tag)
	}

	// The directory has to be writable before anything is downloaded, and
	// the new binary has to land in it: a rename across file systems is
	// not atomic, and /tmp is often its own.
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".repose-update-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return exitf(ExitGeneric, "Can't write %s. Run it as root: sudo repose version update", dir)
		}
		return exitf(ExitGeneric, "Not updated: could not write in %s (%s).", dir, lastClause(err))
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	if tag == "" {
		tag, err = latestReleaseTag(ctx)
		if err != nil {
			return exitf(ExitGeneric, "Not updated: could not reach github.com (%s).", lastClause(err))
		}
		if !releaseTagRe.MatchString(tag) {
			return exitf(ExitGeneric, "Not updated: github.com named %s as the latest release, which is not a release tag.", tag)
		}
	}
	if plainVersion(tag) == plainVersion(cliVersion) {
		if target != "" {
			_, _ = fmt.Fprintf(out, "repose %s is already installed.\n", plainVersion(tag))
		} else {
			_, _ = fmt.Fprintf(out, "repose %s is the latest release.\n", plainVersion(tag))
		}
		return nil
	}

	archive := fmt.Sprintf("repose_%s_%s_%s.tar.gz", tag, goos(), runtime.GOARCH)
	_, _ = fmt.Fprintf(errw, "Downloading %s...\n", archive)
	c := releaseHTTPClient()
	archiveBytes, err := getRelease(ctx, c, tag, archive)
	var assetErr *releaseAssetError
	switch {
	case errors.As(err, &assetErr) && assetErr.status == http.StatusNotFound:
		return exitf(ExitGeneric, "There is no release %s.", tag)
	case err != nil:
		return exitf(ExitGeneric, "Not updated: could not reach github.com (%s).", lastClause(err))
	}
	checksums, err := getRelease(ctx, c, tag, "checksums.txt")
	if err != nil {
		return exitf(ExitGeneric, "Not updated: could not reach github.com (%s).", lastClause(err))
	}
	sig, err := getRelease(ctx, c, tag, "checksums.txt.sig")
	switch {
	case errors.As(err, &assetErr) && assetErr.status == http.StatusNotFound:
		return exitf(ExitGeneric, "Not updated: release %s has no signature (checksums.txt.sig).", tag)
	case err != nil:
		return exitf(ExitGeneric, "Not updated: could not reach github.com (%s).", lastClause(err))
	}
	if err := verifyChecksums(checksums, sig); err != nil {
		return exitf(ExitGeneric, "Not updated: the signature on checksums.txt of %s does not verify.", tag)
	}
	sum := sha256.Sum256(archiveBytes)
	if want := checksumFor(checksums, archive); want == "" || want != hex.EncodeToString(sum[:]) {
		return exitf(ExitGeneric, "Not updated: %s does not match checksums.txt.", archive)
	}

	bin, err := binaryFromArchive(archiveBytes)
	if err != nil {
		return exitf(ExitGeneric, "Not updated: %s does not hold a repose binary (%s).", archive, lastClause(err))
	}
	if err := os.WriteFile(tmpPath, bin, 0o755); err != nil {
		return exitf(ExitGeneric, "Not updated: could not write in %s (%s).", dir, lastClause(err))
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return exitf(ExitGeneric, "Not updated: could not make %s executable (%s).", shortHomePath(tmpPath), lastClause(err))
	}
	// The downloaded binary says its own version before it replaces one
	// that works. A wrong architecture, a truncated file or a release
	// built from the wrong tag all stop here.
	printed, err := exec.CommandContext(ctx, tmpPath, "version").Output()
	if err != nil {
		return exitf(ExitGeneric, "Not updated: the downloaded repose did not start (%s).", lastClause(err))
	}
	// "repose <version> (herakraft)": the version is the second word.
	if fields := strings.Fields(string(printed)); len(fields) < 2 || fields[1] != plainVersion(tag) {
		return exitf(ExitGeneric, "Not updated: the downloaded repose says %q, not %s.", strings.TrimSpace(string(printed)), plainVersion(tag))
	}
	if err := os.Rename(tmpPath, exe); err != nil {
		return exitf(ExitGeneric, "Not updated: could not replace %s (%s).", shortHomePath(exe), lastClause(err))
	}

	_, _ = fmt.Fprintf(out, "Updated %s from %s to %s.\n", shortHomePath(exe), plainVersion(cliVersion), plainVersion(tag))
	// DECISIONS I-15: Arch Linux ships an unrelated `repose`. install.sh
	// says when it comes first on PATH; so does this, since the user just
	// updated a binary their shell may not be running.
	if first, err := exec.LookPath("repose"); err == nil {
		if resolved, err := filepath.EvalSymlinks(first); err == nil {
			first = resolved
		}
		if first != exe {
			_, _ = fmt.Fprintf(out, "Another repose is first on your PATH, at %s; this one is at %s.\n", shortHomePath(first), shortHomePath(exe))
		}
	}
	return nil
}
