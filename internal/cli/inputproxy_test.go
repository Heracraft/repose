package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeFiles is the laptop's file system for scanner tests.
func fakeFiles(paths ...string) func(string) bool {
	m := map[string]bool{}
	for _, p := range paths {
		m[p] = true
	}
	return func(p string) bool { return m[p] }
}

var scanFiles = fakeFiles(
	"/Users/me/Desktop/shot.png",
	"/Users/me/Desktop/Screen Shot 2026-09-26 at 10.00.00.png",
	"/Users/me/notes (1).txt",
	"/home/me/Pictures/a b.jpg",
	"/tmp/x.png",
	"/etc/hosts",
	"/Users/me/.ssh/id_ed25519",
	"/Users/me/.env",
	"/Users/me/Downloads/server.pem",
	"/Users/me/Downloads/id_ed25519",
	"/Users/me/Downloads/id_ed25519.pub",
	"/Users/me/Downloads/cert.P12",
)

// render is what the proxy would write for acts, with a stand-in for
// each drop and Ctrl+V, so a test reads as one string.
func render(acts []inputAction) string {
	var b strings.Builder
	for _, a := range acts {
		switch a.kind {
		case actPass:
			b.Write(a.raw)
		case actFiles:
			fmt.Fprintf(&b, "<drop %q%s>", a.files, a.trail)
		case actCtrlV:
			fmt.Fprintf(&b, "<ctrl-v %q>", a.raw)
		}
	}
	return b.String()
}

func feedAll(s *inputScanner, chunks ...[]byte) string {
	var acts []inputAction
	for _, c := range chunks {
		acts = append(acts, s.feed(c)...)
	}
	acts = append(acts, s.flush()...)
	return render(acts)
}

func bp(s string) string { return "\x1b[200~" + s + "\x1b[201~" }

func TestInputScannerTable(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain typing", "hello world\r", "hello world\r"},
		{"arrow keys and escape", "\x1b[A\x1b[B\x1bOP\x1b", "\x1b[A\x1b[B\x1bOP\x1b"},
		{"ctrl-v byte", "a\x16b", "a<ctrl-v \"\\x16\">b"},
		{"ctrl-v csi u", "\x1b[118;5u", "<ctrl-v \"\\x1b[118;5u\">"},
		{"ctrl-v modifyOtherKeys", "\x1b[27;5;118~", "<ctrl-v \"\\x1b[27;5;118~\">"},
		{"other csi u key", "\x1b[99;5u", "\x1b[99;5u"},
		{"text paste", bp("some text"), bp("some text")},
		{"paste of a missing path", bp("/Users/me/nope.png"), bp("/Users/me/nope.png")},
		{"paste of a relative path", bp("shot.png"), bp("shot.png")},
		{"paste of a system path", bp("/etc/hosts"), bp("/etc/hosts")},
		{"paste of a file in a hidden dir", bp("/Users/me/.ssh/id_ed25519"), bp("/Users/me/.ssh/id_ed25519")},
		{"paste of a hidden file", bp("/Users/me/.env"), bp("/Users/me/.env")},
		{"plain hidden file", "/Users/me/.env", "/Users/me/.env"},
		{"paste of a pem file", bp("/Users/me/Downloads/server.pem"), bp("/Users/me/Downloads/server.pem")},
		{"paste of an ssh key", bp("/Users/me/Downloads/id_ed25519"), bp("/Users/me/Downloads/id_ed25519")},
		{"paste of a key store, upper case", bp("/Users/me/Downloads/cert.P12"), bp("/Users/me/Downloads/cert.P12")},
		{"a key among pictures", bp("/tmp/x.png /Users/me/Downloads/server.pem"), bp("/tmp/x.png /Users/me/Downloads/server.pem")},
		{"paste of a public key", bp("/Users/me/Downloads/id_ed25519.pub"), `<drop ["/Users/me/Downloads/id_ed25519.pub"]>`},
		{"one path", bp("/Users/me/Desktop/shot.png"), `<drop ["/Users/me/Desktop/shot.png"]>`},
		{"Terminal.app escaped, trailing space", bp(`/Users/me/Desktop/Screen\ Shot\ 2026-09-26\ at\ 10.00.00.png `), `<drop ["/Users/me/Desktop/Screen Shot 2026-09-26 at 10.00.00.png"] >`},
		{"escaped parens", bp(`/Users/me/notes\ \(1\).txt`), `<drop ["/Users/me/notes (1).txt"]>`},
		{"WezTerm spaces only", bp(`/Users/me/notes\ (1).txt`), `<drop ["/Users/me/notes (1).txt"]>`},
		{"single quoted", bp(`'/home/me/Pictures/a b.jpg' `), `<drop ["/home/me/Pictures/a b.jpg"] >`},
		{"double quoted", bp(`"/home/me/Pictures/a b.jpg"`), `<drop ["/home/me/Pictures/a b.jpg"]>`},
		{"file uri", bp("file:///home/me/Pictures/a%20b.jpg\r\n"), `<drop ["/home/me/Pictures/a b.jpg"]>`},
		{"file uri localhost", bp("file://localhost/tmp/x.png"), `<drop ["/tmp/x.png"]>`},
		{"file uri other host", bp("file://laptop2/tmp/x.png"), bp("file://laptop2/tmp/x.png")},
		{"two files, spaces", bp(`/tmp/x.png /Users/me/Desktop/shot.png`), `<drop ["/tmp/x.png" "/Users/me/Desktop/shot.png"]>`},
		{"two files, newlines", bp("/tmp/x.png\n/home/me/Pictures/a b.jpg\n"), `<drop ["/tmp/x.png" "/home/me/Pictures/a b.jpg"]>`},
		{"unescaped path with spaces", bp("/home/me/Pictures/a b.jpg"), `<drop ["/home/me/Pictures/a b.jpg"]>`},
		{"a file and a word", bp("/tmp/x.png please"), bp("/tmp/x.png please")},
		{"unterminated quote", bp(`'/tmp/x.png`), bp(`'/tmp/x.png`)},
		{"mixed with typing", "ab" + bp("/tmp/x.png") + "cd\x16e", `ab<drop ["/tmp/x.png"]>cd<ctrl-v "\x16">e`},
		{"plain drop without markers", "/tmp/x.png", `<drop ["/tmp/x.png"]>`},
		{"plain quoted drop", "'/home/me/Pictures/a b.jpg' ", `<drop ["/home/me/Pictures/a b.jpg"] >`},
		{"plain text starting with a slash", "/help\r", "/help\r"},
		{"plain path followed by enter", "/tmp/x.png\r", "/tmp/x.png\r"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &inputScanner{isFile: scanFiles}
			if got := feedAll(s, []byte(c.in)); got != c.want {
				t.Fatalf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

// Input cut at every offset, into two reads and into single bytes, gives
// what one read gives: a sequence or a paste split by a read boundary is
// put back together. The plain-drop reading is per read by design, so
// these inputs start with something else.
func TestInputScannerSplitAtEveryOffset(t *testing.T) {
	inputs := []string{
		"x" + bp(`/Users/me/Desktop/Screen\ Shot\ 2026-09-26\ at\ 10.00.00.png `) + "y",
		"a\x1b[118;5ub\x1b[27;5;118~c\x16d",
		"q" + bp("just text") + "\x1b[A\x1b",
		"k" + bp("/tmp/x.png /Users/me/Desktop/shot.png") + bp("/etc/hosts"),
		"z\x1b[200\x1b[201~",
	}
	for _, in := range inputs {
		want := feedAll(&inputScanner{isFile: scanFiles}, []byte(in))
		for i := 1; i < len(in); i++ {
			s := &inputScanner{isFile: scanFiles}
			if got := feedAll(s, []byte(in[:i]), []byte(in[i:])); got != want {
				t.Fatalf("%q split at %d:\ngot  %q\nwant %q", in, i, got, want)
			}
		}
		var bytesIn [][]byte
		for i := 0; i < len(in); i++ {
			bytesIn = append(bytesIn, []byte{in[i]})
		}
		if got := feedAll(&inputScanner{isFile: scanFiles}, bytesIn...); got != want {
			t.Fatalf("%q byte by byte:\ngot  %q\nwant %q", in, got, want)
		}
	}
}

// Nothing is held back that is not a cut watched sequence: a plain key
// comes out of the same feed, and a lone ESC waits only for the timer.
func TestInputScannerHoldsOnlyCutSequences(t *testing.T) {
	s := &inputScanner{isFile: scanFiles}
	if got := render(s.feed([]byte("a"))); got != "a" {
		t.Fatalf("a key was held: %q", got)
	}
	if held, _ := s.holding(); held {
		t.Fatal("holding after a plain key")
	}
	if got := render(s.feed([]byte("\x1b"))); got != "" {
		t.Fatalf("lone ESC went out before the timer: %q", got)
	}
	held, d := s.holding()
	if !held || d != holdEscape {
		t.Fatalf("holding = %v %v, want the escape hold", held, d)
	}
	if got := render(s.flush()); got != "\x1b" {
		t.Fatalf("flush = %q", got)
	}
	// ESC then a key that is no sequence of ours: out at once, in order.
	if got := render(s.feed([]byte("\x1b"))) + render(s.feed([]byte("x"))); got != "\x1bx" {
		t.Fatalf("got %q", got)
	}
	// An unfinished paste waits for the long hold, then goes as it came.
	if got := render(s.feed([]byte("\x1b[200~/tmp/x.p"))); got != "" {
		t.Fatalf("partial paste went out: %q", got)
	}
	if held, d := s.holding(); !held || d != holdPaste {
		t.Fatalf("holding = %v %v, want the paste hold", held, d)
	}
	if got := render(s.flush()); got != "\x1b[200~/tmp/x.p" {
		t.Fatalf("flush = %q", got)
	}
}

// A paste too long to be a drop goes through as it came, end marker and
// all, and the scanner is back to plain input.
func TestInputScannerLongPastePassesThrough(t *testing.T) {
	s := &inputScanner{isFile: scanFiles}
	body := bytes.Repeat([]byte("x"), maxPasteScan+10)
	in := append(append(append([]byte("\x1b[200~"), body...), "\x1b[201~"...), 'z')
	var got []byte
	for i := 0; i < len(in); i += 4096 {
		end := min(i+4096, len(in))
		for _, a := range s.feed(in[i:end]) {
			if a.kind != actPass {
				t.Fatalf("action %v in a long paste", a.kind)
			}
			got = append(got, a.raw...)
		}
	}
	for _, a := range s.flush() {
		got = append(got, a.raw...)
	}
	if !bytes.Equal(got, in) {
		t.Fatalf("long paste changed: %d bytes in, %d out", len(in), len(got))
	}
}

func TestShellWords(t *testing.T) {
	cases := map[string][]string{
		`a b`:                 {"a", "b"},
		`a\ b`:                {"a b"},
		`'a b' "c d"`:         {"a b", "c d"},
		`"a\"b" 'x'\''y'`:     {`a"b`, "x'y"},
		"a\\\nb":              {"ab"},
		`/p/it\'s\ here.png`:  {"/p/it's here.png"},
		`"$HOME\$x"`:          {"$HOME$x"},
		"  lead\ttab\n\nnl  ": {"lead", "tab", "nl"},
	}
	for in, want := range cases {
		got, ok := shellWords(in)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("shellWords(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{`'open`, `"open`, `trailing\`} {
		if _, ok := shellWords(bad); ok {
			t.Errorf("shellWords(%q) ok", bad)
		}
	}
}

func TestBracketedEscapesGuestPaths(t *testing.T) {
	got := string(bracketed([]string{"/home/dev/app/docs/a b(1).png", "/tmp/repose-paste/x.png"}, " "))
	want := "\x1b[200~/home/dev/app/docs/a\\ b\\(1\\).png /tmp/repose-paste/x.png \x1b[201~"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	// Each escaped path reads back as itself.
	w, ok := shellWords(strings.TrimSuffix(strings.TrimPrefix(got, "\x1b[200~"), "\x1b[201~"))
	if !ok || w[0] != "/home/dev/app/docs/a b(1).png" {
		t.Fatalf("round trip = %q", w)
	}
}

func TestDropGuestName(t *testing.T) {
	ts := mustTime(t, "2026-09-26T10:00:00.123Z")
	cases := map[string]string{
		"Screen Shot 2026-09-26 at 10.00.00.png": "Screen-Shot-2026-09-26-at-10.00.00.png",
		"report.pdf":                             "report.pdf",
		".hidden":                                "hidden",
		"ÜBER$(rm -rf).txt":                      "-BER--rm--rf-.txt",
	}
	for base, want := range cases {
		got := dropGuestName(ts, 1, base)
		if got != pasteGuestDir+"/20260926-100000-123-2-"+want {
			t.Errorf("dropGuestName(%q) = %q", base, got)
		}
	}
	long := strings.Repeat("a", 200) + ".png"
	if got := dropGuestName(ts, 0, long); !strings.HasSuffix(got, ".png") || len(got) > len(pasteGuestDir)+120 {
		t.Errorf("long name = %q", got)
	}
}

// A drop is judged by the file it would read, after every link: a plain
// name that leads to a key, a hidden file or a system file is not a drop.
func TestDropFileFollowsLinks(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) string {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	writeData := func(rel, data string) string {
		p := write(rel)
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	link := func(target, rel string) string {
		p := filepath.Join(dir, rel)
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	key := write(".ssh/id_ed25519")
	shot := write("Pictures/shot.png")
	if err := os.Mkdir(filepath.Join(dir, "links"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		want string // "" for not a drop
	}{
		{shot, shot},
		{link(shot, "links/shot.png"), shot},
		{key, ""},
		{link(key, "links/notes.txt"), ""},
		{link(filepath.Join(dir, ".ssh"), "links/keys") + "/id_ed25519", ""},
		{link("/etc/hosts", "links/hosts.txt"), ""},
		{write("Downloads/server.pem"), ""},
		{link(filepath.Join(dir, "Downloads/server.pem"), "links/server.txt"), ""},
		{writeData("Downloads/server.key", "-----BEGIN EC PRIVATE KEY-----\nMHc\n-----END EC PRIVATE KEY-----\n"), ""},
		{writeData("Downloads/backup.txt", "my key:\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3Bl\n"), ""},
		{writeData("Downloads/secret.asc", "-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nlQ\n"), ""},
		{writeData("Downloads/secret.gpg", "\x95\x01\xd8\x04"), ""},
		{writeData("Downloads/secret-new.gpg", "\xc5\x58\x04"), ""},
		{writeData("Downloads/talk.key", "PK\x03\x04 keynote"), filepath.Join(dir, "Downloads/talk.key")},
		{writeData("Downloads/public.asc", "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nmQ\n"), filepath.Join(dir, "Downloads/public.asc")},
		{writeData("Downloads/release.tar.gz.asc", "-----BEGIN PGP SIGNATURE-----\n\niQ\n"), filepath.Join(dir, "Downloads/release.tar.gz.asc")},
		{writeData("Downloads/notes.txt.gpg", "\x85\x01\x0c\x03"), filepath.Join(dir, "Downloads/notes.txt.gpg")},
		{writeData("Downloads/pubring.gpg", "\x99\x01\x0d\x04"), filepath.Join(dir, "Downloads/pubring.gpg")},
		{filepath.Join(dir, "Pictures"), ""},
		{filepath.Join(dir, "missing.png"), ""},
	}
	for _, c := range cases {
		got, ok := dropFile(c.path)
		if ok != (c.want != "") || got != c.want {
			t.Errorf("dropFile(%s) = %q, %v; want %q", c.path, got, ok, c.want)
		}
		if localDropFile(c.path) != ok {
			t.Errorf("localDropFile(%s) disagrees with dropFile", c.path)
		}
	}
}
