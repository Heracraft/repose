package supplychain

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The release signing key has two copies: install.sh carries it for the
// first install, and the CLI carries it so `repose version update` can
// make the same check (DECISIONS I-430, I-1086). release.yml reads the
// one in install.sh to check the signature it just made, so a rotation
// that changes one and not the other ships a CLI that refuses every
// release, or install.sh and the CLI trusting different keys. This test
// fails until both change.
func TestReleaseKeyIsTheSameInInstallShAndTheCLI(t *testing.T) {
	root := repoRoot(t)
	shKey := extract(t,
		filepath.Join(root, "install.sh"),
		regexp.MustCompile(`(?s)RELEASE_PUBKEY='(-----BEGIN PUBLIC KEY-----.*?-----END PUBLIC KEY-----)'`))
	goKey := extract(t,
		filepath.Join(root, "internal/cli/versionupdate.go"),
		regexp.MustCompile("(?s)releasePubKeyPEM = `(-----BEGIN PUBLIC KEY-----.*?-----END PUBLIC KEY-----)`"))
	if shKey != goKey {
		t.Errorf("install.sh carries\n%s\nand internal/cli carries\n%s", shKey, goKey)
	}
}

func extract(t *testing.T, path string, re *regexp.Regexp) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := re.FindSubmatch(b)
	if m == nil {
		t.Fatalf("%s holds no release key matching %s", path, re)
	}
	return strings.TrimSpace(string(m[1]))
}
