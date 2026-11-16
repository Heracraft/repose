package wsframe

import (
	"bufio"
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Round trips at every length boundary of §5.2's three length forms, in
// both directions, with the raw bytes kept whole: a proxy passes Raw on
// unchanged, so a byte lost there is a byte lost from a message.
func TestFramesRoundTrip(t *testing.T) {
	for _, n := range []int{0, 5, 125, 126, 65535, 65536, 200000} {
		for _, masked := range []bool{false, true} {
			p := bytes.Repeat([]byte("x"), n)
			raw := Encode(OpText, p, masked)
			f, err := ReadFrame(bufio.NewReader(bytes.NewReader(raw)), 1<<20)
			if err != nil || !f.Fin || f.Op != OpText || !bytes.Equal(f.Payload, p) || !bytes.Equal(f.Raw, raw) {
				t.Errorf("n=%d masked=%v: %v", n, masked, err)
			}
		}
	}
}

// A masked client frame is unmasked, and the mask is not left in Payload.
func TestMaskedClientFrameUnmasks(t *testing.T) {
	raw := Encode(OpBinary, []byte("secret-pixels"), true)
	if bytes.Contains(raw, []byte("secret-pixels")) {
		t.Fatal("a masked frame carried its payload in the clear")
	}
	f, err := ReadFrame(bufio.NewReader(bytes.NewReader(raw)), 1<<20)
	if err != nil || string(f.Payload) != "secret-pixels" {
		t.Fatalf("payload %q, err %v", f.Payload, err)
	}
}

// A fragmented message reassembles, and a control frame between its
// fragments reaches onControl rather than the message.
func TestFragmentedMessageReassembles(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{0x01, 0x03, 'a', 'b', 'c'})
	buf.Write([]byte{0x89, 0x00}) // ping
	buf.Write([]byte{0x80, 0x02, 'd', 'e'})
	var controls int
	op, payload, _, err := ReadMessage(bufio.NewReader(&buf), 1<<20, func(Frame) error { controls++; return nil })
	if err != nil || op != OpText || string(payload) != "abcde" || controls != 1 {
		t.Fatalf("op=%v payload=%q controls=%d err=%v", op, payload, controls, err)
	}
}

// A reserved bit means an extension nobody agreed to (compression), and
// the connection ends rather than guessing at the payload.
func TestReservedBitIsAnError(t *testing.T) {
	_, _, _, err := ReadMessage(bufio.NewReader(bytes.NewReader([]byte{0xC1, 0x01, 'x'})), 1<<20, func(Frame) error { return nil })
	if err == nil {
		t.Fatal("a frame with RSV1 set was accepted")
	}
}

// One byte over the caller's cap is refused, as a frame and as a message
// assembled from fragments under it: the cap is what stops a peer that
// declares a 16 EiB payload from being allocated for.
func TestOverTheCapIsRefused(t *testing.T) {
	raw := Encode(OpBinary, bytes.Repeat([]byte("x"), 1001), false)
	if _, err := ReadFrame(bufio.NewReader(bytes.NewReader(raw)), 1000); err == nil {
		t.Fatal("a frame one byte over the cap was accepted")
	}
	if _, err := ReadFrame(bufio.NewReader(bytes.NewReader(Encode(OpBinary, bytes.Repeat([]byte("x"), 1000), false))), 1000); err != nil {
		t.Fatalf("a frame at the cap was refused: %v", err)
	}
	var buf bytes.Buffer
	buf.Write(append([]byte{0x02, 0x64}, bytes.Repeat([]byte("x"), 100)...))
	buf.Write(append([]byte{0x80, 0x64}, bytes.Repeat([]byte("x"), 100)...))
	if _, _, _, err := ReadMessage(bufio.NewReader(&buf), 150, func(Frame) error { return nil }); err == nil {
		t.Fatal("fragments summing over the cap were accepted")
	}
}

// Accept answers 101 with the RFC's key, and refuses anything that is not
// a version-13 upgrade before it hijacks the connection.
func TestAcceptUpgrade(t *testing.T) {
	// The example of RFC 6455 §1.3.
	if got := AcceptKey("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept key %q", got)
	}
	for _, tc := range []struct {
		name   string
		header http.Header
	}{
		{"no upgrade", http.Header{"Sec-Websocket-Version": {"13"}, "Sec-Websocket-Key": {"k"}}},
		{"wrong version", http.Header{"Upgrade": {"websocket"}, "Sec-Websocket-Version": {"8"}, "Sec-Websocket-Key": {"k"}}},
		{"no key", http.Header{"Upgrade": {"websocket"}, "Sec-Websocket-Version": {"13"}}},
	} {
		r := httptest.NewRequest("GET", "/v1/browser/stream", nil)
		r.Header = tc.header
		if _, _, err := Accept(httptest.NewRecorder(), r); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
}
