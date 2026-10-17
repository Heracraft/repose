package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseSSE(t *testing.T) {
	raw := "id: 1\ndata: {\"seq\":1,\"line\":\"evaluating\"}\n\n" +
		"id: 2\ndata: {\"seq\":2,\"line\":\"building\"}\n\n" +
		"event: done\ndata: {\"state\":\"done\"}\n\n"
	var frames []SSEFrame
	if err := parseSSE(strings.NewReader(raw), func(f SSEFrame) { frames = append(frames, f) }); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want 3: %+v", len(frames), frames)
	}
	if frames[0].ID != "1" || frames[0].Data != `{"seq":1,"line":"evaluating"}` {
		t.Fatalf("frame 0 = %+v", frames[0])
	}
	if frames[2].Event != "done" {
		t.Fatalf("frame 2 event = %q, want done", frames[2].Event)
	}
}

func TestStreamBuildLogRendersLinesAndDoneState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("id: 1\ndata: {\"seq\":1,\"line\":\"evaluating configuration\"}\n\n"))
		_, _ = w.Write([]byte("id: 2\ndata: {\"seq\":2,\"line\":\"copying path '/nix/store/abc-hello'\"}\n\n"))
		_, _ = w.Write([]byte("event: done\ndata: {\"state\":\"done\"}\n\n"))
	}))
	defer srv.Close()

	client := newClient(srv.URL, staticToken("tok"))
	var buf bytes.Buffer
	state, lastSeq, err := StreamBuildLog(context.Background(), client, "p1", "op1", &buf, 0)
	if err != nil {
		t.Fatal(err)
	}
	if state != "done" || lastSeq != 2 {
		t.Fatalf("state=%q lastSeq=%d", state, lastSeq)
	}
	// Nix's line carries the prefix; repose's own step line does not
	// (I-755).
	out := buf.String()
	if !strings.Contains(out, "nix › copying path '/nix/store/abc-hello'") {
		t.Fatalf("output missing Nix's line: %q", out)
	}
	if !strings.Contains(out, "\nevaluating configuration\n") && !strings.HasPrefix(out, "evaluating configuration\n") {
		t.Fatalf("step line not printed as it is: %q", out)
	}
	if strings.Contains(out, "nix › evaluating configuration") {
		t.Fatalf("step line printed as Nix's output: %q", out)
	}
}

func TestRenderBuildErrorGolden(t *testing.T) {
	fragment := "{ pkgs, ... }:\n{\n  home.packages = with pkgs; [\n    nodejs_25\n  ];\n}\n"
	msg := "attribute 'nodejs_25' missing at fragment.nix:4:5"
	var buf bytes.Buffer
	RenderBuildError(&buf, "eval_failed", msg, "./repose.nix", []byte(fragment))
	want := "config error: attribute 'nodejs_25' missing at repose.nix:4:5\n" +
		"   at repose.nix:4:5\n" +
		"      3 |   home.packages = with pkgs; [\n" +
		"      4 |     nodejs_25\n" +
		"        |     ^\n"
	if buf.String() != want {
		t.Fatalf("RenderBuildError mismatch:\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestRenderBuildErrorWithoutFragmentSource(t *testing.T) {
	var buf bytes.Buffer
	RenderBuildError(&buf, "eval_failed", "evaluation exceeded 60 s", "./repose.nix", nil)
	want := "config error: evaluation exceeded 60 s\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

// The api stores an op's error as {code, message} (I-42); the first real
// secret-in-fragment refusal rendered as "error: " because the CLI read
// it as a string (DECISIONS I-114). Both shapes decode, and the contract's
// prefixes follow the code.
func TestOpErrorDecodesObjectAndStringAndPrefixes(t *testing.T) {
	var op Op
	if err := json.Unmarshal([]byte(`{"state":"error","error":{"code":"invalid","message":"fragment contains the value of secret X"}}`), &op); err != nil {
		t.Fatal(err)
	}
	if op.Error.Code != "invalid" || op.Error.String() != "fragment contains the value of secret X" {
		t.Fatalf("object form: %+v", op.Error)
	}
	if err := json.Unmarshal([]byte(`{"state":"error","error":"host unreachable"}`), &op); err != nil {
		t.Fatal(err)
	}
	if op.Error.Message != "host unreachable" || op.Error.Code != "" {
		t.Fatalf("string form: %+v", op.Error)
	}
	for code, want := range map[string]string{"eval_failed": "config error: ", "invalid": "config error: ", "closure_too_large": "config too large: ", "build_failed": "", "build_timeout": "", "internal": "error: "} {
		if got := buildErrorPrefix(code); got != want {
			t.Fatalf("prefix for %s = %q, want %q", code, got, want)
		}
	}
	var buf bytes.Buffer
	RenderBuildError(&buf, "build_timeout", "build timed out after 30 minutes while building sleep-forever-1.0", "./repose.nix", nil)
	if buf.String() != "build timed out after 30 minutes while building sleep-forever-1.0\n" {
		t.Fatalf("build_timeout rendering: %q", buf.String())
	}
}

// A build failure is known only when the log stream ends, and the op the
// CLI read before streaming has no error yet: waitOp must read the op
// again rather than return the stale one with its state flipped, which
// rendered every failed build as "error:" and nothing on host-01 (I-127).
func TestWaitOpReadsTheOpAgainAfterTheStreamEnds(t *testing.T) {
	var reads int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects/p/ops/o", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&reads, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_, _ = io.WriteString(w, `{"state":"running","log_url":"/v1/projects/p/ops/o/log"}`)
			return
		}
		_, _ = io.WriteString(w, `{"state":"error","error":{"code":"eval_failed","fragment_line":1,"message":"attribute 'ripgrepp' missing at fragment.nix:1:36"}}`)
	})
	mux.HandleFunc("/v1/projects/p/ops/o/log", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "id: 1\ndata: {\"seq\":1,\"line\":\"evaluating configuration\"}\n\nevent: done\ndata: {\"state\":\"error\"}\n\n")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newClient(srv.URL+"/v1", staticToken("t"))
	var out bytes.Buffer
	op, err := waitOp(context.Background(), c, "p", "o", &out)
	if err != nil {
		t.Fatal(err)
	}
	if op.State != "error" || op.Error.Code != "eval_failed" || !strings.Contains(op.Error.Message, "ripgrepp") {
		t.Fatalf("op after the stream: state %q error %+v", op.State, op.Error)
	}
	if !strings.Contains(out.String(), "evaluating configuration") {
		t.Fatalf("log not streamed: %q", out.String())
	}
	if atomic.LoadInt32(&reads) < 2 {
		t.Fatal("the op was not read again after the stream ended")
	}
}

// The verbatim block after the summary is printed (I-128): for a closure
// over the cap that is the ten largest paths, which host-01's run never
// showed.
func TestRenderBuildErrorPrintsTheVerbatimBlock(t *testing.T) {
	var buf bytes.Buffer
	msg := "closure is 26.6 GB, limit is 20 GB; largest paths:\n  21 GB  /nix/store/aaaa-m3-twenty-one-gb\n  701.7 MB  /nix/store/bbbb-chromium\n"
	RenderBuildError(&buf, "closure_too_large", msg, "./repose.nix", nil)
	want := "config too large: closure is 26.6 GB, limit is 20 GB; largest paths:\n\n  21 GB  /nix/store/aaaa-m3-twenty-one-gb\n  701.7 MB  /nix/store/bbbb-chromium\n"
	if buf.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
	buf.Reset()
	RenderBuildError(&buf, "eval_failed", "attribute 'x' missing at fragment.nix:1:5\n\nerror: attribute 'x' missing\n       at /var/lib/repose/builds/r/fragment.nix:1:5:", "./f.nix", []byte("{ x = y; }\n"))
	out := buf.String()
	if !strings.HasPrefix(out, "config error: attribute 'x' missing at f.nix:1:5\n   at f.nix:1:5\n") || !strings.Contains(out, "\n\nerror: attribute 'x' missing\n") {
		t.Fatalf("context then the verbatim block:\n%s", out)
	}
}
