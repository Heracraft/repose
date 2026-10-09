package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The input proxy (DECISIONS I-280). On macOS and Linux, `repose run` and
// `repose attach` run ssh on a pty of their own instead of becoming ssh,
// and pass the terminal's input through, byte for byte, with two
// exceptions:
//
//   - A bracketed paste that is only paths of files on this computer (what
//     a terminal sends when a file is dropped on it) copies the files to
//     the machine and pastes their paths there instead. A file of the
//     synced checkout is not copied: its path in the guest's checkout is
//     pasted.
//   - Ctrl+V with an image on the laptop's clipboard copies the image and
//     pastes its path, as `repose paste` does (I-252). Without an image the
//     key goes through, so vim and friends are unaffected.
//
// On macOS a clipboard watcher (clipwatch.go, I-341) makes Cmd+V with an
// image a drop too, by giving the clipboard the path of a copy.
//
// Claude Code attaches an image whose absolute path arrives as a bracketed
// paste, plain or quoted or backslash-escaped, several separated by spaces
// (checked against Claude Code 2.1.280, I-280). Everything here is pure
// over byte streams except dropHandler, which talks to the guest.
//
// REPOSE_NO_INPUT_PROXY=1 (or the old REPOSE_INPUT_PROXY=0) turns it off:
// the CLI becomes ssh as before.

// inputProxyEnabled is the kill switch; the platform and terminal checks
// are runInputProxy's.
func inputProxyEnabled() bool {
	return os.Getenv(envNoInputProxy) != "1" && os.Getenv(envInputProxy) != "0" && goos() != "windows"
}

var (
	pasteStartSeq = []byte("\x1b[200~")
	pasteEndSeq   = []byte("\x1b[201~")
)

// ctrlVSeqs are the ways a terminal sends Ctrl+V: the control byte, and
// the two extended-key encodings tmux may ask the terminal for (CSI u,
// and xterm's modifyOtherKeys; the guest's tmux has extended-keys on,
// I-264).
var ctrlVSeqs = [][]byte{
	{0x16},
	[]byte("\x1b[118;5u"),
	[]byte("\x1b[27;5;118~"),
}

// watchedSeqs are the escape sequences whose prefix, cut by a read
// boundary, is held back until the rest arrives (or holdEscape passes).
var watchedSeqs = [][]byte{pasteStartSeq, ctrlVSeqs[1], ctrlVSeqs[2]}

const (
	// maxPasteScan is the largest paste looked at as a possible drop; a
	// longer one goes through as it came.
	maxPasteScan = 64 << 10
	// maxDropTokens bounds the words of a paste checked as file paths.
	maxDropTokens = 100
	// holdEscape is how long a lone ESC (or another cut prefix of a
	// watched sequence) waits for the rest before it is sent as it is. A
	// terminal writes a sequence at once, so the wait only ever delays an
	// Escape key press, by this much.
	holdEscape = 30 * time.Millisecond
	// holdPaste is how long an unfinished bracketed paste waits for its
	// end marker before it is sent as it is.
	holdPaste = time.Second
)

type inputActionKind int

const (
	actPass  inputActionKind = iota // write raw to the session
	actFiles                        // a drop: copy files, paste their guest paths
	actCtrlV                        // Ctrl+V: paste the clipboard image, else raw
)

// inputAction is one thing the proxy does with input, in order.
type inputAction struct {
	kind inputActionKind
	// raw is the input as the terminal sent it: what goes to the session
	// for actPass, and for the others when they fall back.
	raw []byte
	// files are the dropped files' absolute local paths (actFiles).
	files []string
	// trail is " " when the drop ended with a space (Terminal.app adds
	// one), kept after the guest paths.
	trail string
}

// inputScanner splits terminal input into inputActions. It is a pure
// function of the bytes fed to it, plus isFile.
type inputScanner struct {
	// isFile reports a regular file on this computer at an absolute path.
	isFile func(string) bool
	pend   []byte // a cut prefix of a watched sequence
	in     bool   // inside a bracketed paste
	paste  []byte // the paste so far, without its start marker
}

// holding reports input held back, and for how long it may wait.
func (s *inputScanner) holding() (bool, time.Duration) {
	switch {
	case s.in:
		return true, holdPaste
	case len(s.pend) > 0:
		return true, holdEscape
	}
	return false, 0
}

// flush gives up waiting: whatever is held goes through as it came.
func (s *inputScanner) flush() []inputAction {
	var raw []byte
	if s.in {
		raw = append(append(raw, pasteStartSeq...), s.paste...)
		s.in, s.paste = false, nil
	}
	if len(s.pend) > 0 {
		raw = append(raw, s.pend...)
		s.pend = nil
	}
	if len(raw) == 0 {
		return nil
	}
	return []inputAction{{kind: actPass, raw: raw}}
}

// feed takes one read of terminal input.
func (s *inputScanner) feed(p []byte) []inputAction {
	if !s.in && len(s.pend) == 0 {
		// A terminal that was not asked for bracketed paste sends a drop
		// as plain text; a whole read that is only file paths is one (a
		// person typing sends a key per read).
		if files, trail, ok := s.plainDrop(p); ok {
			return []inputAction{{kind: actFiles, raw: bytes.Clone(p), files: files, trail: trail}}
		}
	}
	data := make([]byte, 0, len(s.pend)+len(p))
	data = append(append(data, s.pend...), p...)
	s.pend = nil
	var out []inputAction
	pass := func(b []byte) {
		if len(b) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].kind == actPass {
			out[n-1].raw = append(out[n-1].raw, b...)
			return
		}
		out = append(out, inputAction{kind: actPass, raw: bytes.Clone(b)})
	}
	for len(data) > 0 {
		if s.in {
			s.paste = append(s.paste, data...)
			data = nil
			i := bytes.Index(s.paste, pasteEndSeq)
			if i < 0 {
				if len(s.paste) > maxPasteScan {
					pass(pasteStartSeq)
					pass(s.paste)
					s.in, s.paste = false, nil
				}
				break
			}
			content := s.paste[:i]
			data = bytes.Clone(s.paste[i+len(pasteEndSeq):])
			s.in = false
			raw := make([]byte, 0, len(content)+len(pasteStartSeq)+len(pasteEndSeq))
			raw = append(append(append(raw, pasteStartSeq...), content...), pasteEndSeq...)
			s.paste = nil
			if files, trail, ok := parseDrop(string(content), s.isFile); ok {
				out = append(out, inputAction{kind: actFiles, raw: raw, files: files, trail: trail})
			} else {
				pass(raw)
			}
			continue
		}
		k := bytes.IndexAny(data, "\x16\x1b")
		if k < 0 {
			pass(data)
			break
		}
		pass(data[:k])
		data = data[k:]
		if data[0] == 0x16 {
			out = append(out, inputAction{kind: actCtrlV, raw: []byte{0x16}})
			data = data[1:]
			continue
		}
		if bytes.HasPrefix(data, pasteStartSeq) {
			s.in, s.paste = true, nil
			data = data[len(pasteStartSeq):]
			continue
		}
		matched := false
		for _, seq := range ctrlVSeqs[1:] {
			if bytes.HasPrefix(data, seq) {
				out = append(out, inputAction{kind: actCtrlV, raw: bytes.Clone(seq)})
				data = data[len(seq):]
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if cutWatched(data) {
			s.pend = bytes.Clone(data)
			break
		}
		pass(data[:1])
		data = data[1:]
	}
	return out
}

// cutWatched reports b as a proper prefix of a watched sequence.
func cutWatched(b []byte) bool {
	for _, seq := range watchedSeqs {
		if len(b) < len(seq) && bytes.HasPrefix(seq, b) {
			return true
		}
	}
	return false
}

// plainDrop is a read that is a whole drop without paste markers: it
// starts like a path, has no control characters but newlines, and is
// only paths of existing files.
func (s *inputScanner) plainDrop(p []byte) ([]string, string, bool) {
	if len(p) < 2 {
		return nil, "", false
	}
	if c := p[0]; c != '/' && c != '\'' && c != '"' && !bytes.HasPrefix(p, []byte("file://")) {
		return nil, "", false
	}
	for _, c := range p {
		if (c < 0x20 && c != '\n') || c == 0x7f {
			return nil, "", false
		}
	}
	return parseDrop(string(p), s.isFile)
}

// parseDrop reads a paste as the paths a terminal sends for dropped
// files: backslash-escaped (Terminal.app, iTerm2, Ghostty, WezTerm),
// single- or double-quoted (kitty, GNOME Terminal, Konsole, WezTerm's
// Windows styles), file:// URIs, one per line or separated by spaces, or a
// single unquoted path with spaces in it. ok only when every word is an
// absolute path of an existing regular file outside the system's own
// directories; anything else is a paste of text.
func parseDrop(text string, isFile func(string) bool) (files []string, trail string, ok bool) {
	body := strings.TrimRight(text, " \t\r\n")
	if len(body) < len(text) && text[len(body)] == ' ' {
		trail = " "
	}
	body = strings.TrimLeft(body, " \t\r\n")
	if body == "" {
		return nil, "", false
	}
	if c := body[0]; c != '/' && c != '\'' && c != '"' && !strings.HasPrefix(body, "file://") {
		return nil, "", false
	}
	var candidates [][]string
	if w, ok := shellWords(body); ok {
		candidates = append(candidates, w)
	}
	var lines []string
	for _, l := range strings.Split(body, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	candidates = append(candidates, lines, []string{body})
	for _, words := range candidates {
		if len(words) == 0 || len(words) > maxDropTokens {
			continue
		}
		paths := make([]string, 0, len(words))
		for _, w := range words {
			p, ok := dropPath(w)
			if !ok || !isFile(p) {
				paths = nil
				break
			}
			paths = append(paths, p)
		}
		if paths != nil {
			return paths, trail, true
		}
	}
	return nil, "", false
}

// systemDirs are where a pasted path is a path the user means literally
// (a config file on the machine has the same path), never a drop.
var systemDirs = []string{
	"/etc/", "/usr/", "/bin/", "/sbin/", "/lib/", "/lib64/", "/lib32/", "/proc/",
	"/sys/", "/dev/", "/nix/", "/opt/", "/boot/", "/run/", "/srv/", "/snap/",
	"/var/lib/", "/var/log/", "/var/run/", "/System/", "/Library/", "/Applications/",
	"/private/etc/",
}

// dropPath turns one word of a drop into an absolute local path.
func dropPath(w string) (string, bool) {
	if strings.HasPrefix(w, "file://") {
		u, err := url.Parse(w)
		if err != nil || (u.Host != "" && u.Host != "localhost") {
			return "", false
		}
		w = u.Path
	}
	if !strings.HasPrefix(w, "/") {
		return "", false
	}
	for _, d := range systemDirs {
		if strings.HasPrefix(w, d) {
			return "", false
		}
	}
	// A hidden file, or one in a hidden directory (~/.ssh, ~/.aws), is
	// never a drop: pasting "the path an agent asked for" must not carry
	// a key to the machine. Nor is a file named like a key.
	if strings.Contains(w, "/.") || keyFileName(filepath.Base(w)) {
		return "", false
	}
	return w, true
}

// keyExts are the extensions of private keys and key stores.
var keyExts = map[string]bool{
	".pem": true, ".p12": true, ".pfx": true, ".p8": true, ".ppk": true,
	".jks": true, ".keystore": true, ".kdbx": true, ".keychain": true, ".keychain-db": true,
}

// keyFileName is a file name that holds a private key or a key store:
// an SSH key (id_ed25519, not id_ed25519.pub) or one of keyExts.
func keyFileName(name string) bool {
	n := strings.ToLower(name)
	for _, k := range []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"} {
		if strings.HasPrefix(n, k) && !strings.HasSuffix(n, ".pub") {
			return true
		}
	}
	return keyExts[filepath.Ext(n)]
}

// dropFile is what a dropped path names, once every symlink in it is
// followed: the file that would be read. ok is false when that file
// would not be a drop by its own path (hidden, under a system
// directory, named like a key) or is not a regular file, so a plain
// link to ~/.ssh/id_ed25519 is never read.
func dropFile(p string) (string, bool) {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false
	}
	if _, ok := dropPath(real); !ok {
		return "", false
	}
	fi, err := os.Stat(real)
	if err != nil || !fi.Mode().IsRegular() || holdsPrivateKey(real) {
		return "", false
	}
	return real, true
}

// keySniffBytes is how much of a file holdsPrivateKey reads.
const keySniffBytes = 4 << 10

// holdsPrivateKey reports a file that starts with a private key whatever
// its name: a PEM or OpenSSH key (server.key, a key saved as .txt), an
// armored PGP secret key (.asc), or a binary OpenPGP secret key (.gpg,
// .pgp). Keynote's .key files, public keys, signatures and encrypted
// files are not keys and still copy. A file that can't be read is not a
// drop either.
func holdsPrivateKey(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return true
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, keySniffBytes)
	n, _ := io.ReadFull(f, buf)
	buf = buf[:n]
	if bytes.Contains(buf, []byte("PRIVATE KEY-----")) || bytes.Contains(buf, []byte("-----BEGIN PGP PRIVATE KEY BLOCK-----")) {
		return true
	}
	if ext := strings.ToLower(filepath.Ext(p)); (ext == ".gpg" || ext == ".pgp") && n > 0 {
		return openPGPPacketTag(buf[0]) == 5 // secret-key packet, RFC 9580 §5
	}
	return false
}

// openPGPPacketTag is the packet type a first byte starts, or -1.
func openPGPPacketTag(b byte) int {
	switch {
	case b&0x80 == 0:
		return -1
	case b&0x40 != 0:
		return int(b & 0x3f)
	default:
		return int(b>>2) & 0x0f
	}
}

// shellWords splits s the way a POSIX shell splits words, with quotes and
// backslashes; ok is false for an unterminated quote or a lone backslash.
func shellWords(s string) ([]string, bool) {
	var words []string
	var cur strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			if i+1 >= len(s) {
				return nil, false
			}
			i++
			if s[i] != '\n' {
				cur.WriteByte(s[i])
				in = true
			}
		case '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
			in = true
		case '"':
			in = true
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\\\"$`\n", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, false
			}
		case ' ', '\t', '\n', '\r':
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteByte(c)
			in = true
		}
	}
	if in {
		words = append(words, cur.String())
	}
	return words, true
}

// localDropFile is the scanner's isFile outside tests.
func localDropFile(p string) bool {
	_, ok := dropFile(p)
	return ok
}

// shellEscapePath backslash-escapes p the way Terminal.app does a dropped
// path, which Claude Code reads.
func shellEscapePath(p string) string {
	var b strings.Builder
	for _, r := range p {
		if r < 0x80 && !pathSafe(r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// pathSafe is an ASCII character a shell reads as itself in a word.
func pathSafe(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+,:@%", r)
}

// bracketed wraps guest paths in a bracketed paste. tmux delivers it to
// the pane as a paste when the program there asked for one, which is
// what makes Claude Code attach an image, and as plain text otherwise.
func bracketed(paths []string, trail string) []byte {
	esc := make([]string, len(paths))
	for i, p := range paths {
		esc[i] = shellEscapePath(p)
	}
	var b []byte
	b = append(b, pasteStartSeq...)
	b = append(b, strings.Join(esc, " ")...)
	b = append(b, trail...)
	return append(b, pasteEndSeq...)
}

// Limits of one drop. A dropped file is copied whatever its type, up to
// the same 20 MB as a pasted image (pasteMaxBytes): the copy holds up the
// keys typed after it, and /tmp is not for large files; `repose cp` is.
const dropMaxFiles = 20

// dropHandler does what the scanner found: copies files and clipboard
// images to the guest and returns the bytes to type in their place.
type dropHandler struct {
	target  sshTarget
	slug    string
	repoDir string // the laptop checkout that is the machine's checkout (target.Checkout's, I-480); "" when there is none
	clip    clipboardReader
	// notify shows a message on the tmux status line or as a herdr
	// notification, without blocking.
	notify func(msg string)
	now    func() time.Time

	toolOnce sync.Once
}

func newDropHandler(t sshTarget, slug, repoDir string) *dropHandler {
	h := &dropHandler{target: t, slug: slug, repoDir: repoDir, clip: pasteClipboard, now: time.Now}
	h.notify = func(msg string) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = runSSH(ctx, t, guestMessageScript(slug, "repose: "+msg), nil)
		}()
	}
	return h
}

// uploadWait is how long a copy runs before the status line says so.
const uploadWait = 500 * time.Millisecond

// slowNotice shows msg if the returned stop is not called within
// uploadWait.
func (h *dropHandler) slowNotice(msg string) (stop func()) {
	t := time.AfterFunc(uploadWait, func() { h.notify(msg) })
	return func() { t.Stop() }
}

// files handles a drop; on any failure the drop goes through as it came
// and the status line says why.
func (h *dropHandler) files(a inputAction) []byte {
	if len(a.files) > dropMaxFiles {
		h.notify(fmt.Sprintf("%d files dropped; up to %d are copied to the machine at once. Nothing was copied.", len(a.files), dropMaxFiles))
		return a.raw
	}
	// What is read is what the paths name now, checked again: a link
	// changed since the scan reads nothing it would not have then.
	real := make([]string, len(a.files))
	sizes := make([]int64, len(a.files))
	for i, f := range a.files {
		r, ok := dropFile(f)
		if !ok {
			return a.raw
		}
		real[i] = r
		fi, err := os.Stat(r)
		if err != nil {
			return a.raw
		}
		if fi.Size() > pasteMaxBytes {
			h.notify(fmt.Sprintf("%s is %s; dropped files are copied up to %s. Use repose cp for larger ones.", filepath.Base(f), humanBytes(fi.Size()), humanBytes(pasteMaxBytes)))
			return a.raw
		}
		sizes[i] = fi.Size()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	guest := h.checkoutPaths(ctx, a.files, sizes)
	stop := h.slowNotice("copying to the machine…")
	defer stop()
	stamp := h.now().UTC()
	var copied []string
	for i, f := range a.files {
		if guest[i] != "" {
			continue
		}
		name := dropGuestName(stamp, i, filepath.Base(f))
		if err := h.upload(ctx, real[i], name); err != nil {
			h.notify(err.Error())
			return a.raw
		}
		guest[i] = name
		copied = append(copied, filepath.Base(f))
	}
	// Name what left the laptop, so a path pasted because an agent asked
	// for it is never copied unseen.
	if len(copied) > 0 {
		h.notify(copiedNotice(copied))
	}
	return bracketed(guest, a.trail)
}

// copiedNotice names the files a drop copied to the machine.
func copiedNotice(names []string) string {
	if len(names) > 3 {
		return fmt.Sprintf("copied %s and %d more files to the machine", strings.Join(names[:2], ", "), len(names)-2)
	}
	return "copied " + strings.Join(names, ", ") + " to the machine"
}

// upload copies one local regular file to guestPath.
func (h *dropHandler) upload(ctx context.Context, local, guestPath string) error {
	f, err := os.Open(local)
	if err != nil {
		return fmt.Errorf("could not read %s", filepath.Base(local))
	}
	defer func() { _ = f.Close() }()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("could not read %s", filepath.Base(local))
	}
	return h.save(ctx, f, guestPath)
}

func (h *dropHandler) save(ctx context.Context, r io.Reader, guestPath string) error {
	_, err := runSSH(ctx, h.target, pasteSaveScript(guestPath), r)
	var se *sshError
	if errors.As(err, &se) && se.ExitCode == pasteExitUnsafeDir {
		return fmt.Errorf("%s on the machine is not a directory of dev's own; remove it and try again", pasteGuestDir)
	}
	if err != nil {
		return fmt.Errorf("could not copy to the machine (%s)", oneLine(err.Error()))
	}
	return nil
}

// checkoutPaths maps the dropped files that belong to the synced
// checkout to their paths in the guest's checkout, when the guest's copy
// is there with the same size; "" for every other file, which is copied.
// One ssh round trip, and none when no file is in the checkout.
func (h *dropHandler) checkoutPaths(ctx context.Context, files []string, sizes []int64) []string {
	guest := make([]string, len(files))
	if h.repoDir == "" {
		return guest
	}
	root, err := filepath.EvalSymlinks(h.repoDir)
	if err != nil {
		return guest
	}
	rels := make([]string, len(files))
	found := false
	for i, f := range files {
		real, err := filepath.EvalSymlinks(f)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, real)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == ".git" || strings.HasPrefix(rel, ".git/") {
			continue
		}
		rels[i] = rel
		found = true
	}
	if !found {
		return guest
	}
	var b strings.Builder
	// The checkout by the rule every reader shares (I-368), not ~/<slug>,
	// which is a machine's checkout only from before I-368.
	b.WriteString(checkoutVar(h.slug, h.target.Checkout))
	// With no checkout there is nothing to find: a path that never exists.
	b.WriteString("d=$repose_co\n[ \"$d\" != \"$HOME\" ] || d=/nonexistent\nprintf '%s\\n' \"$d\"\n")
	for _, rel := range rels {
		if rel == "" {
			b.WriteString("echo -\n")
			continue
		}
		fmt.Fprintf(&b, "stat -L -c %%s -- \"$d\"/%s 2>/dev/null || echo -\n", shQuote(rel))
	}
	out, err := runSSH(ctx, h.target, b.String(), nil)
	if err != nil {
		return guest
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != len(files)+1 {
		return guest
	}
	dir := lines[0]
	for i, rel := range rels {
		if rel == "" {
			continue
		}
		if n, err := strconv.ParseInt(lines[i+1], 10, 64); err == nil && n == sizes[i] {
			guest[i] = dir + "/" + rel
		}
	}
	return guest
}

// clipboardProbe bounds the clipboard read behind Ctrl+V; past it the key
// goes through.
const clipboardProbe = 2 * time.Second

// ctrlV pastes the clipboard's image, or passes the key on.
func (h *dropHandler) ctrlV(a inputAction) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), clipboardProbe)
	img, err := h.clip.ReadPNG(ctx)
	cancel()
	var te *clipboardToolError
	if errors.As(err, &te) && (goos() == "darwin" || os.Getenv(envDisplay) != "" || os.Getenv(envWaylandDisplay) != "") {
		// A desktop without the tool: say so once, then Ctrl+V is only
		// Ctrl+V for the rest of the session.
		h.toolOnce.Do(func() { h.notify(te.msg) })
	}
	if err != nil || !bytes.HasPrefix(img, pngMagic) {
		return a.raw
	}
	if len(img) > pasteMaxBytes {
		h.notify(fmt.Sprintf("the image on the clipboard is %s; images are copied up to %s.", humanBytes(int64(len(img))), humanBytes(pasteMaxBytes)))
		return a.raw
	}
	uctx, ucancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer ucancel()
	stop := h.slowNotice("copying the image to the machine…")
	defer stop()
	now := h.now().UTC()
	guestPath := pasteGuestDir + "/" + now.Format("20060102-150405") + fmt.Sprintf("-%03d.png", now.Nanosecond()/1e6)
	if err := h.save(uctx, bytes.NewReader(img), guestPath); err != nil {
		h.notify(err.Error())
		return a.raw
	}
	return bracketed([]string{guestPath}, "")
}

// dropGuestName is where the i-th file of a drop lands: the paste
// directory, a timestamp, and the file's own name made shell-safe so the
// agent sees what it was (and its extension, which is how Claude Code
// tells an image).
func dropGuestName(t time.Time, i int, base string) string {
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name := strings.TrimLeft(b.String(), ".")
	if len(name) > 80 {
		name = name[len(name)-80:]
	}
	if name == "" {
		name = "file"
	}
	return fmt.Sprintf("%s/%s-%03d-%d-%s", pasteGuestDir, t.Format("20060102-150405"), t.Nanosecond()/1e6, i+1, name)
}
