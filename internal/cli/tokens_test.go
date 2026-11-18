package cli

import (
	"context"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// API tokens (I-1314..I-1320): create prints the token alone on stdout
// and the one sentence on stderr; list shows everything but the token;
// rm revokes; REPOSE_TOKEN makes the CLI a token's caller, with the
// api's refusals in the CLI's words.
func TestTokensCreateListRm(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out, errOut := &discardWriter{}, &discardWriter{}
	e.Out, e.ErrOut = out, errOut

	if err := TokensCreateCmd(ctx, e, "ci", []string{"triggers,read"}, "", ""); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(out.buf.String())
	if !strings.HasPrefix(token, "rpt_") || strings.Contains(token, "\n") || strings.Contains(token, " ") {
		t.Fatalf("stdout is the token alone: %q", out.buf.String())
	}
	if got := errOut.buf.String(); !strings.HasPrefix(got, "ci: read, triggers, every project, until ") || !strings.HasSuffix(got, ". Not shown again.\n") {
		t.Fatalf("stderr: %q", got)
	}

	// Bound to the project, 90 days.
	out.buf.Reset()
	errOut.buf.Reset()
	if err := TokensCreateCmd(ctx, e, "deploy", []string{"read", "projects"}, p.Slug, "90"); err != nil {
		t.Fatal(err)
	}
	if got := errOut.buf.String(); !strings.Contains(got, "todo-app only") {
		t.Fatalf("bound stderr: %q", got)
	}

	// The scope, name and expiry are checked before any call.
	for _, c := range []struct {
		name    string
		scopes  []string
		expires string
		want    string
	}{
		{"x", []string{"root"}, "", "--scope root: a scope is one of"},
		{"x", nil, "", "--scope is required"},
		{"Bad Name", []string{"read"}, "", "a token name is"},
		{"x", []string{"read"}, "400", "--expires 400"},
	} {
		err := TokensCreateCmd(ctx, e, c.name, c.scopes, "", c.expires)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	// A taken name is the api's conflict, in the CLI's words.
	err := TokensCreateCmd(ctx, e, "ci", []string{"read"}, "", "")
	if err == nil || !strings.Contains(err.Error(), "already have a token named ci") {
		t.Fatalf("duplicate: %v", err)
	}

	out.buf.Reset()
	if err := TokensListCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	got := out.buf.String()
	if !strings.HasPrefix(got, "NAME  ") || !strings.Contains(got, "ci ") || !strings.Contains(got, "read,triggers") || !strings.Contains(got, "todo-app") || strings.Contains(got, token) {
		t.Fatalf("list:\n%s", got)
	}
	if !strings.Contains(got, "never") {
		t.Fatalf("never used:\n%s", got)
	}
	e.Quiet = true
	out.buf.Reset()
	if err := TokensListCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); got != "deploy\nci\n" && got != "ci\ndeploy\n" {
		t.Fatalf("quiet: %q", got)
	}
	e.Quiet = false
	e.JSON = true
	out.buf.Reset()
	if err := TokensListCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, `"name": "ci"`) || strings.Contains(got, token) {
		t.Fatalf("json: %s", got)
	}
	e.JSON = false

	// REPOSE_TOKEN: the token's caller. A read works, the account's
	// routes refuse, and the refusal is a sentence.
	te := newLifecycleEnv(t, fake)
	te.Client = newClient(fake.URL()+"/v1", apiTokenSource(token))
	tout := &discardWriter{}
	te.Out = tout
	if err := loginStatus(ctx, te); err != nil {
		t.Fatal(err)
	}
	if got := tout.buf.String(); !strings.Contains(got, "with token ci (read, triggers, every project, until ") {
		t.Fatalf("login --status with a token: %q", got)
	}
	err = TokensListCmd(ctx, te)
	if err == nil || !strings.Contains(err.Error(), "Tokens are made with a login") {
		t.Fatalf("tokens with a token: %v", err)
	}
	if _, err := te.Client.DestroyProject(ctx, p.ID); err == nil || !strings.Contains(err.Error(), "needs projects") {
		t.Fatalf("rm with read,triggers: %v", err)
	}

	// Revoke: the name is gone from the live list, and the token's
	// caller gets the reason, exit 3.
	if err := TokensRmCmd(ctx, e, []string{"ci"}); err != nil {
		t.Fatal(err)
	}
	err = TokensRmCmd(ctx, e, []string{"ci"})
	if err == nil || !strings.Contains(err.Error(), "You have no token named ci") {
		t.Fatalf("rm twice: %v", err)
	}
	e.Quiet = true
	out.buf.Reset()
	if err := TokensListCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); got != "deploy\n" {
		t.Fatalf("after rm: %q", got)
	}
	err = loginStatus(ctx, te)
	var stderr strings.Builder
	if code := exitCodeFor(err, &stderr); code != ExitNotLoggedIn || !strings.Contains(stderr.String(), "REPOSE_TOKEN was refused: this API token was revoked.") {
		t.Fatalf("revoked token: %d %q", code, stderr.String())
	}
}
