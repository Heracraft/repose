package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestAPITokensWithTheRealCLI is the end-to-end check (DECISIONS I-1324):
// the real `repose` binary, built from this tree, against this real api
// on a real Postgres. A login mints a token; REPOSE_TOKEN makes the CLI
// the token's caller; a call outside the scopes is refused in the CLI's
// words; a revoke ends it. Skipped under -short (the build takes a
// while) and when go is not on PATH.
func TestAPITokensWithTheRealCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: the real CLI is not built")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}
	e := newEnv(t)
	login := e.signIn(t, "sub-cli-tok", "clitok")
	r := e.do(t, login, "POST", "/projects", map[string]any{"name": "pa", "class": "small"})
	pa := r.body["id"].(string)
	e.waitOp(t, r)

	// The binary.
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	bin := filepath.Join(t.TempDir(), "repose")
	build := exec.Command("go", "build", "-o", bin, "./cmd/repose")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	// A laptop logged in as the user: the login's access token in the
	// CLI's credentials file, under a home of its own.
	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "repose")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	creds, _ := json.Marshal(map[string]any{"access_token": login, "expires_at": time.Now().Add(time.Hour), "logto_issuer": e.logto.Issuer()})
	if err := os.WriteFile(filepath.Join(cfg, "credentials.json"), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(token string, args ...string) (stdout, stderr string, code int) {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
			"REPOSE_API_URL="+e.api.URL+"/v1", "REPOSE_NO_SPINNER=1", "REPOSE_NO_BROWSER=1", "TERM=dumb")
		if token != "" {
			cmd.Env = append(cmd.Env, "REPOSE_TOKEN="+token)
		}
		var o, er bytes.Buffer
		cmd.Stdout, cmd.Stderr = &o, &er
		err := cmd.Run()
		code = 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return o.String(), er.String(), code
	}

	// Mint with the login: the token alone on stdout, the sentence on stderr.
	out, errOut, code := run("", "tokens", "create", "ci", "--scope", "read,triggers")
	if code != 0 {
		t.Fatalf("tokens create: %d %s %s", code, out, errOut)
	}
	token := strings.TrimSpace(out)
	if !strings.HasPrefix(token, "rpt_") || len(token) != 47 || strings.ContainsAny(out, " \t") {
		t.Fatalf("stdout: %q", out)
	}
	if !strings.HasPrefix(errOut, "ci: read, triggers, every project, until ") || !strings.HasSuffix(errOut, ". Not shown again.\n") {
		t.Fatalf("stderr: %q", errOut)
	}
	out, _, code = run("", "tokens")
	if code != 0 || !strings.Contains(out, "NAME") || !strings.Contains(out, "ci ") || !strings.Contains(out, "read,triggers") || strings.Contains(out, token) {
		t.Fatalf("tokens list: %d %q", code, out)
	}

	// The token's caller: who it is, what it sees, what it is refused.
	out, errOut, code = run(token, "login", "--status")
	if code != 0 || !strings.Contains(out, "clitok with token ci (read, triggers, every project, until ") {
		t.Fatalf("login --status with the token: %d %q %q", code, out, errOut)
	}
	out, errOut, code = run(token, "ls")
	if code != 0 || !strings.Contains(out, "pa") {
		t.Fatalf("ls with the token: %d %q %q", code, out, errOut)
	}
	req, _ := http.NewRequest("GET", e.api.URL+"/v1/projects/"+pa, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("curl with the token: %v %v", err, resp)
	}
	resp.Body.Close()
	_, errOut, code = run(token, "stop", "pa")
	if code == 0 || !strings.Contains(errOut, "token's scopes are read, triggers; this call needs projects (forbidden).") {
		t.Fatalf("stop with a read token: %d %q", code, errOut)
	}
	_, errOut, code = run(token, "tokens")
	if code == 0 || !strings.Contains(errOut, "Tokens are made with a login, not with REPOSE_TOKEN.") {
		t.Fatalf("tokens with the token: %d %q", code, errOut)
	}

	// Revoke with the login; the token's next call is refused with why.
	if _, errOut, code = run("", "tokens", "rm", "ci"); code != 0 {
		t.Fatalf("tokens rm: %d %q", code, errOut)
	}
	_, errOut, code = run(token, "ls")
	if code != 3 || !strings.Contains(errOut, "REPOSE_TOKEN was refused: this API token was revoked.") {
		t.Fatalf("ls after revoke: %d %q", code, errOut)
	}
	// Logs carry the token's id and never the token.
	if strings.Contains(e.logText(), token) {
		t.Fatal("the token reached the api's log")
	}
	if !strings.Contains(e.logText(), "token_id") {
		t.Fatal("token_id missing from the api's log")
	}
}
