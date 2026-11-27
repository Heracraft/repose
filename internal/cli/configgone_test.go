package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

// `repose config add` is gone on an Ubuntu machine (DECISIONS I-1698):
// with names or without, it refuses with exit 2, names apt-get, and
// changes nothing on the account.
func TestConfigAddGoneOnErofs(t *testing.T) {
	fake, e, _, out, _ := newErofsEnv(t)
	ctx := context.Background()
	for _, args := range [][]string{{"ffmpeg"}, {"postgresql", "bun"}, nil} {
		err := ConfigAddCmd(ctx, e, "", args)
		if exitCode(err) != ExitUsage {
			t.Fatalf("add %v: exit %d, %v", args, exitCode(err), err)
		}
		if got := err.Error(); got != configAddGone {
			t.Fatalf("add %v: %q", args, got)
		}
	}
	if n := fake.Calls("PUT /v1/projects/{id}/config"); n != 0 {
		t.Fatalf("%d config PUTs", n)
	}
	if out.buf.Len() != 0 {
		t.Fatalf("stdout %q", out.buf.String())
	}
	if !strings.Contains(configAddGone, "sudo apt-get install") {
		t.Fatalf("line names no apt-get: %q", configAddGone)
	}
}

// `config remove` takes an item an old menu still holds off it (one
// release, I-1698), and names apt-get for anything else.
func TestConfigRemoveOldMenuItemOnErofs(t *testing.T) {
	fake, e, p, out, _ := newErofsEnv(t)
	ctx := context.Background()
	if _, _, err := e.Client.PutConfigMenu(ctx, p.ID, []MenuItem{{ID: "redis"}}); err != nil {
		t.Fatal(err)
	}
	waitMenu(t, e, p.ID, 1)
	err := ConfigRemoveCmd(ctx, e, "", []string{"ffmpeg"})
	if exitCode(err) != ExitUsage || !strings.Contains(err.Error(), "sudo apt-get remove ffmpeg") {
		t.Fatalf("remove ffmpeg: exit %d, %v", exitCode(err), err)
	}
	puts := fake.Calls("PUT /v1/projects/{id}/config")
	out.buf.Reset()
	if err := ConfigRemoveCmd(ctx, e, "", []string{"redis"}); err != nil {
		t.Fatalf("remove redis: %v", err)
	}
	if fake.Calls("PUT /v1/projects/{id}/config") != puts+1 || len(menuOf(t, e, p.ID)) != 0 {
		t.Fatalf("redis not removed: %+v", menuOf(t, e, p.ID))
	}
}

// waitMenu waits for the project's menu to hold n items: the fake makes
// a PUT's menu the project's when its install ends.
func waitMenu(t *testing.T, e *Env, id string, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for len(menuOf(t, e, id)) != n {
		if time.Now().After(deadline) {
			t.Fatalf("menu %+v, want %d items", menuOf(t, e, id), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
