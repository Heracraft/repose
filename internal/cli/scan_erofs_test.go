package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Council finding 3 (I-1512): on an Ubuntu machine's checkout neither a
// machine.nix nor a repose.nix takes a half over, and pins go through
// mise, so scan says what run will do there.
func TestScanOnUbuntuMachine(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"package.json": `{"name":"app","engines":{"node":"18"},"scripts":{"dev":"air"}}`,
		"repose.nix":   "{ pkgs, ... }: { }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	home := t.TempDir()
	var nixOut, ubuntuOut discardWriter
	if err := ScanCmd(&nixOut, home, dir, false, scanPersonal{Has: true, Why: "your account has a machine.nix"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(nixOut.buf.String(), "skipped:") {
		t.Fatalf("a NixOS machine's scan lost its skipped lines:\n%s", nixOut.buf.String())
	}
	if err := ScanCmd(&ubuntuOut, home, dir, false, scanPersonal{Has: true, Why: "your account has a machine.nix", Erofs: true}); err != nil {
		t.Fatal(err)
	}
	got := ubuntuOut.buf.String()
	for _, never := range []string{"skipped:", "nix"} {
		if strings.Contains(got, never) {
			t.Fatalf("Ubuntu scan says %q:\n%s", never, got)
		}
	}
	if !strings.Contains(got, "node 18 (package.json engines.node): node 18 through mise") {
		t.Fatalf("Ubuntu scan's node line:\n%s", got)
	}
	if p := precedenceForProject(true, dir, &Project{GuestRuntime: "erofs"}); p.SkipGlobals || p.SkipScripts {
		t.Fatalf("run's carry on an Ubuntu machine skips a half: %+v", p)
	}
	if p := precedenceForProject(true, dir, &Project{GuestRuntime: "nix"}); !p.SkipGlobals || !p.SkipScripts {
		t.Fatalf("run's carry on a NixOS machine lost its precedence: %+v", p)
	}
}
