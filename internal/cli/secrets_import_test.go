package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

func TestParseDotenv(t *testing.T) {
	src := "\ufeff# comment\n" +
		"\n" +
		"PLAIN=value\n" +
		"export EXPORTED = spaced value   \n" +
		"COMMENTED=abc # trailing comment\n" +
		"HASH=abc#def\n" +
		"EMPTY=\n" +
		"SINGLE='lit $HOME \\n # not a comment'\n" +
		"DOUBLE=\"a\\nb \\\"q\\\" \\\\ \\$x\" # comment\n" +
		"PEM=\"-----BEGIN KEY-----\r\n" +
		"abc\n" +
		"-----END KEY-----\"\n" +
		"PLAIN=last wins\n"
	got, err := parseDotenv(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []dotenvEntry{
		{"PLAIN", "last wins", 13},
		{"EXPORTED", "spaced value", 4},
		{"COMMENTED", "abc", 5},
		{"HASH", "abc#def", 6},
		{"EMPTY", "", 7},
		{"SINGLE", "lit $HOME \\n # not a comment", 8},
		{"DOUBLE", "a\nb \"q\" \\ $x", 9},
		{"PEM", "-----BEGIN KEY-----\nabc\n-----END KEY-----", 10},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDotenv:\n got %+v\nwant %+v", got, want)
	}

	for src, wantErr := range map[string]string{
		"A=1\nno equals here\n":    "line 2: no = in it",
		"=value\n":                 "line 1: no name before =",
		"A=\"open\nstill open\n":   "line 1: the \" quote is never closed",
		"A='x' trailing\n":         "line 1: text after the closing quote",
		"A=1\nB=\"secret\" junk\n": "line 2: text after the closing quote",
	} {
		_, err := parseDotenv(src)
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("parseDotenv(%q) = %v, want %q", src, err, wantErr)
		}
		// The error names the line, never its content (a value).
		if err != nil && (strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "still open")) {
			t.Errorf("error carries file content: %v", err)
		}
	}
}

// I-277: import sets each name through the api `secrets set` uses, says
// which it replaced, never prints a value, and sends nothing when a name
// is bad or with --dry-run.
func TestSecretsImport(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	puts := map[string]string{} // name -> value, as the api received it
	var mu sync.Mutex
	target, _ := url.Parse(fake.URL())
	proxy := httputil.NewSingleHostReverseProxy(target)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/secrets/") {
			body, _ := io.ReadAll(r.Body)
			var v struct{ Value string }
			_ = json.Unmarshal(body, &v)
			raw, _ := base64.StdEncoding.DecodeString(v.Value)
			mu.Lock()
			puts[path.Base(r.URL.Path)] = string(raw)
			mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		proxy.ServeHTTP(w, r)
	}))
	defer srv.Close()
	e.Client = newClient(srv.URL+"/v1", staticToken("tok"))
	var out, errOut strings.Builder
	e.Out, e.ErrOut = &out, &errOut
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "izma", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Client.PutSecret(ctx, p.ID, "OLD_KEY", []byte("old")); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("OLD_KEY=new-value-1\nexport NEW_KEY=\"new-value-2\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	names := func() map[string]bool {
		ms, err := e.Client.ListSecrets(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]bool{}
		for _, s := range ms {
			m[s.Name] = true
		}
		return m
	}

	// --dry-run sends nothing.
	if err := SecretsImportCmd(ctx, e, SecretsImportOptions{ProjectArg: "izma", File: envFile, DryRun: true}, nil); err != nil {
		t.Fatal(err)
	}
	if names()["NEW_KEY"] {
		t.Fatal("--dry-run set NEW_KEY")
	}
	if !strings.Contains(out.String(), "Would set 2 on izma from "+envFile+": OLD_KEY (replaced), NEW_KEY") || !strings.Contains(out.String(), "Nothing sent") {
		t.Fatalf("dry run said:\n%s", out.String())
	}

	out.Reset()
	if err := SecretsImportCmd(ctx, e, SecretsImportOptions{ProjectArg: "izma", File: envFile}, nil); err != nil {
		t.Fatal(err)
	}
	if !names()["NEW_KEY"] || !names()["OLD_KEY"] {
		t.Fatalf("secrets after import: %v", names())
	}
	if got := out.String(); !strings.Contains(got, "Set 2 on izma from "+envFile) || !strings.Contains(got, "OLD_KEY (replaced), NEW_KEY") {
		t.Fatalf("summary:\n%s", got)
	}
	for _, v := range []string{"new-value-1", "new-value-2"} {
		if strings.Contains(out.String()+errOut.String(), v) {
			t.Fatalf("a value was printed:\n%s%s", out.String(), errOut.String())
		}
	}
	// The values reached the api as the file said.
	mu.Lock()
	if puts["OLD_KEY"] != "new-value-1" || puts["NEW_KEY"] != "new-value-2" {
		t.Fatalf("the api received %q", puts)
	}
	mu.Unlock()

	// stdin, and a bad name refuses the whole file before anything is sent.
	err = SecretsImportCmd(ctx, e, SecretsImportOptions{ProjectArg: "izma", File: "-"}, strings.NewReader("GOOD_ONE=1\nlower_case=2\nuser_ca.pub=3\n"))
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitUsage || !strings.Contains(ee.msg, "line 2: lower_case is not a secret name") || !strings.Contains(ee.msg, "line 3: user_ca.pub") {
		t.Fatalf("bad names: %v", err)
	}
	if names()["GOOD_ONE"] {
		t.Fatal("GOOD_ONE was set from a file that was refused")
	}

	// A file with nothing in it, and a missing file, are usage errors.
	err = SecretsImportCmd(ctx, e, SecretsImportOptions{ProjectArg: "izma", File: "-"}, strings.NewReader("# only a comment\n"))
	if ee, ok := err.(*exitError); !ok || ee.code != ExitUsage {
		t.Fatalf("empty file: %v", err)
	}
	err = SecretsImportCmd(ctx, e, SecretsImportOptions{ProjectArg: "izma", File: filepath.Join(dir, "missing.env")}, nil)
	if ee, ok := err.(*exitError); !ok || ee.code != ExitUsage {
		t.Fatalf("missing file: %v", err)
	}
}

// I-475: the variables the machine uses to refresh secrets are refused by
// set and by import, before anything is sent.
func TestShellNamesAreRefused(t *testing.T) {
	err := validateImport([]dotenvEntry{{Name: "A", Value: "1", Line: 1}, {Name: "BASH_ENV", Value: "/x", Line: 2}})
	if err == nil || !strings.Contains(err.Error(), "line 2: BASH_ENV is reserved") {
		t.Fatalf("import: %v", err)
	}
	for _, n := range []string{"BASH_ENV", "ENV", "REPOSE_ENV_GEN"} {
		err := SecretsSetCmd(context.Background(), &Env{}, "", n, []byte("x"))
		if err == nil || !strings.Contains(err.Error(), "is reserved") {
			t.Fatalf("set %s: %v", n, err)
		}
	}
}
