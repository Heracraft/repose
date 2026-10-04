package supplychain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func need(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
}

// bumpCheck runs bump-agents-pr.sh --check-only on a candidate built from
// the committed versions.json by edit.
func bumpCheck(t *testing.T, edit func(m map[string]any)) (string, error) {
	t.Helper()
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "nix/overlay/agents/versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	out, _ := json.Marshal(m)
	cand := filepath.Join(t.TempDir(), "versions.json")
	if err := os.WriteFile(cand, out, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(root, "scripts/bump-agents-pr.sh"), "--check-only", cand)
	o, err := cmd.CombinedOutput()
	return string(o), err
}

func TestBumpAgentsPRAcceptsOnlyValueChanges(t *testing.T) {
	need(t, "bash", "jq", "git")
	entry := func(m map[string]any, a string) map[string]any { return m[a].(map[string]any) }

	out, err := bumpCheck(t, func(map[string]any) {})
	if err != nil || !strings.Contains(out, "nothing to bump") {
		t.Fatalf("unchanged file: err=%v out=%s", err, out)
	}

	out, err = bumpCheck(t, func(m map[string]any) {
		e := entry(m, "opencode")
		e["version"] = "9.9.9"
		e["x86_64-linux"] = map[string]any{
			"url":  "https://github.com/anomalyco/opencode/releases/download/v9.9.9/opencode-linux-x64.tar.gz",
			"hash": "sha256-MEbgQE/cYPuAMH56R4JLoHR3NkF4pNCbqoVISW3W1Ds=",
		}
	})
	if err != nil || !strings.Contains(out, "agents: opencode 9.9.9") {
		t.Fatalf("a real bump: err=%v out=%s", err, out)
	}

	refused := map[string]func(m map[string]any){
		"new agent": func(m map[string]any) {
			m["evil"] = map[string]any{"version": "1.0.0", "url": "https://github.com/x/y", "hash": "sha256-MEbgQE/cYPuAMH56R4JLoHR3NkF4pNCbqoVISW3W1Ds="}
		},
		"new key in an entry": func(m map[string]any) { entry(m, "codex")["postInstall"] = "curl x | sh" },
		"url on another host": func(m map[string]any) {
			entry(m, "claude-code")["x86_64-linux"].(map[string]any)["url"] = "https://example.com/claude"
		},
		"plain http": func(m map[string]any) {
			entry(m, "claude-code")["x86_64-linux"].(map[string]any)["url"] = "http://downloads.claude.ai/claude"
		},
		"hash not sha256 SRI": func(m map[string]any) {
			entry(m, "chrome-devtools-mcp")["hash"] = "sha512-abc"
		},
		"version with a newline": func(m map[string]any) { entry(m, "pi-coding-agent")["version"] = "1.0\nx" },
		"number for a string":    func(m map[string]any) { entry(m, "gemini-cli")["version"] = 3 },
	}
	for name, edit := range refused {
		out, err := bumpCheck(t, edit)
		if err == nil {
			t.Errorf("%s: accepted: %s", name, out)
		}
	}
}

// fakeRelease writes a release directory for version holding one archive
// for this machine and a checksums.txt, and returns the archive name. The
// binary carries build, so two calls with different builds always give
// different archives, even within the same second (tar keeps mtimes at
// one-second resolution).
func fakeRelease(t *testing.T, dir, version string, build int) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "repose")
	body := fmt.Sprintf("#!/bin/sh\necho repose %s build %d\n", version, build)
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	arch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[runtime.GOARCH]
	archive := "repose_" + version + "_" + runtime.GOOS + "_" + arch + ".tar.gz"
	if o, err := exec.Command("tar", "-czf", filepath.Join(dir, archive), "-C", filepath.Dir(bin), "repose").CombinedOutput(); err != nil {
		t.Fatalf("tar: %v %s", err, o)
	}
	b, err := os.ReadFile(filepath.Join(dir, archive))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	sums := hex.EncodeToString(sum[:]) + "  " + archive + "\n"
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
	return archive
}

func openssl(t *testing.T, args ...string) {
	t.Helper()
	if o, err := exec.Command("openssl", args...).CombinedOutput(); err != nil {
		t.Fatalf("openssl %v: %v %s", args, err, o)
	}
}

type installResult struct {
	out       string
	err       error
	installed bool
}

func runInstall(t *testing.T, base, pubkey, version string) installResult {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command("sh", filepath.Join(repoRoot(t), "install.sh"), "--version", version)
	cmd.Env = append(os.Environ(),
		"HOME="+home, "SHELL=/bin/sh",
		"REPOSE_INSTALL_BASE_URL="+base,
		"REPOSE_INSTALL_PUBKEY="+pubkey,
	)
	o, err := cmd.CombinedOutput()
	_, statErr := os.Stat(filepath.Join(home, ".local/bin/repose"))
	return installResult{string(o), err, statErr == nil}
}

func TestInstallShRequiresASignedChecksumsFile(t *testing.T) {
	need(t, "sh", "curl", "tar", "openssl")
	keys := t.TempDir()
	key, pub := filepath.Join(keys, "k.pem"), filepath.Join(keys, "k.pub.pem")
	other, otherPub := filepath.Join(keys, "o.pem"), filepath.Join(keys, "o.pub.pem")
	openssl(t, "ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", key)
	openssl(t, "ec", "-in", key, "-pubout", "-out", pub)
	openssl(t, "ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", other)
	openssl(t, "ec", "-in", other, "-pubout", "-out", otherPub)

	const version = "v9.0.0-test"
	dir := t.TempDir()
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()
	sums := filepath.Join(dir, "checksums.txt")
	sig := filepath.Join(dir, "checksums.txt.sig")

	fakeRelease(t, dir, version, 1)
	if r := runInstall(t, srv.URL, pub, version); r.err == nil || r.installed || !strings.Contains(r.out, "has no signature") {
		t.Errorf("unsigned release: err=%v installed=%v out=%s", r.err, r.installed, r.out)
	}

	openssl(t, "dgst", "-sha256", "-sign", other, "-out", sig, sums)
	if r := runInstall(t, srv.URL, pub, version); r.err == nil || r.installed || !strings.Contains(r.out, "does not verify") {
		t.Errorf("signed with another key: err=%v installed=%v out=%s", r.err, r.installed, r.out)
	}

	openssl(t, "dgst", "-sha256", "-sign", key, "-out", sig, sums)
	if r := runInstall(t, srv.URL, pub, version); r.err != nil || !r.installed {
		t.Errorf("signed release: err=%v installed=%v out=%s", r.err, r.installed, r.out)
	}

	// Archive swapped after signing: the signed checksum no longer matches.
	b, _ := os.ReadFile(sums)
	fakeRelease(t, dir, version, 2)
	if err := os.WriteFile(sums, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if r := runInstall(t, srv.URL, pub, version); r.err == nil || r.installed || !strings.Contains(r.out, "checksum verification failed") {
		t.Errorf("swapped archive: err=%v installed=%v out=%s", r.err, r.installed, r.out)
	}

	// checksums.txt rewritten to match the swapped archive, old signature.
	fakeRelease(t, dir, version, 3)
	if r := runInstall(t, srv.URL, pub, version); r.err == nil || r.installed || !strings.Contains(r.out, "does not verify") {
		t.Errorf("rewritten checksums.txt: err=%v installed=%v out=%s", r.err, r.installed, r.out)
	}
}

// Releases cut before signing are checked against the checksums.txt hash
// install.sh pins for them, and need no signature.
func TestInstallShPinsReleasesBeforeSigning(t *testing.T) {
	need(t, "sh", "curl", "tar")
	const version = "v0.1.27"
	// v0.1.27's checksums.txt as published.
	published := "" +
		"8d255fea626db0f446c5ba94420db081a5253faf1b5e8479ce2b8f4730503d8b  repose_v0.1.27_darwin_amd64.tar.gz\n" +
		"11d0515f1e7f6698b69605da028efaf4d2f3a8236981b13a72b107c1757a02a3  repose_v0.1.27_darwin_arm64.tar.gz\n" +
		"67c5ff28656c2fdccfc13d69ddc364c234cff8bace5c142c74a309bc56ee8e04  repose_v0.1.27_linux_amd64.tar.gz\n" +
		"9e12a9f0bf2265a22f75356fb8a220bdc757483c122ae9fd6e06a1a9901c2815  repose_v0.1.27_linux_arm64.tar.gz\n"
	dir := t.TempDir()
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()

	// A rebuilt release whose checksums.txt matches its own archive is not
	// the published one, so it is refused before the archive is checked.
	fakeRelease(t, dir, version, 4)
	if r := runInstall(t, srv.URL, "", version); r.err == nil || r.installed || !strings.Contains(r.out, "is not the one published") {
		t.Errorf("replaced checksums.txt: err=%v installed=%v out=%s", r.err, r.installed, r.out)
	}

	// The published checksums.txt passes the pin and needs no signature; the
	// fake archive then fails its checksum, which shows the pin was the
	// check that let it through.
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(published), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runInstall(t, srv.URL, "", version)
	if r.installed || strings.Contains(r.out, "is not the one published") || strings.Contains(r.out, "signature") ||
		!strings.Contains(r.out, "checksum verification failed") {
		t.Errorf("published checksums.txt: err=%v installed=%v out=%s", r.err, r.installed, r.out)
	}
}
