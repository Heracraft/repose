package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// Council finding 2 (I-1510): machine.nix reaches no Ubuntu machine, so on
// an account whose machines all run the Ubuntu base `config --global`
// refuses before it writes anything, and on a mixed account it says which
// machines it does not reach.
func TestGlobalOnUbuntuAccount(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	fake.SetDefaultGuestRuntime("erofs")
	e, _ := newRoundtripEnv(t, fake)
	e.HomeDir = filepath.Dir(e.Dir)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out = out

	want := "Your machines run the Ubuntu base and don't use machine.nix; `repose config add NAME` installs a package on one."
	err := GlobalPackagesCmd(ctx, e, []string{"ripgrep"}, true)
	if exitCode(err) != ExitGeneric || err.Error() != want {
		t.Fatalf("add on an Ubuntu account: %v (exit %d)", err, exitCode(err))
	}
	if _, serr := os.Stat(e.machineNixPath()); serr == nil {
		t.Fatal("the refusal wrote machine.nix on the laptop")
	}
	path := filepath.Join(t.TempDir(), "m.nix")
	if werr := os.WriteFile(path, []byte("{ pkgs, ... }: { home.packages = [ ]; }\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	if err := GlobalApplyCmd(ctx, e, path); exitCode(err) != ExitGeneric || err.Error() != want {
		t.Fatalf("apply on an Ubuntu account: %v", err)
	}
	edited := false
	if err := GlobalEditCmd(ctx, e, func(string) error { edited = true; return nil }); exitCode(err) != ExitGeneric || edited {
		t.Fatalf("edit on an Ubuntu account: %v, editor opened %v", err, edited)
	}
	if n := fake.Calls("PUT /v1/me/config"); n != 0 {
		t.Fatalf("%d machine.nix saves on an Ubuntu account", n)
	}

	// A Nix machine beside it: saved, and the Ubuntu one is named.
	fake.SetDefaultGuestRuntime("nix")
	if _, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "legacy", Class: "small"}); err != nil {
		t.Fatal(err)
	}
	out.buf.Reset()
	if err := GlobalPackagesCmd(ctx, e, []string{"ripgrep"}, true); err != nil {
		t.Fatalf("add on a mixed account: %v", err)
	}
	if got := out.buf.String(); !strings.Contains(got, "Saved machine.nix to your account") || !strings.Contains(got, "todo-app runs the Ubuntu base and doesn't use machine.nix.") {
		t.Fatalf("mixed add said:\n%s", got)
	}
}
