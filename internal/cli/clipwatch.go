package cli

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cmd+V on macOS (DECISIONS I-341). With only an image on the clipboard,
// Terminal, iTerm2, Ghostty and the rest send the terminal nothing for
// Cmd+V, so no key handler can see it. While the input proxy runs, a
// watcher gives an image-only clipboard a second flavour, plain text:
// the path of a PNG copy of the image in the CLI's cache. Cmd+V then
// pastes that path as a bracketed paste, which the proxy already treats
// as a drop (the file is copied to the machine and its path there pasted
// in its place). The image flavours stay as they were, so apps that take
// images still get the image; a plain-text field gets the path.
//
// The watcher is one osascript process polling the pasteboard's change
// count, never a process per poll. It writes a heartbeat line; once the
// CLI is gone the write fails and osascript exits with it. When the
// session ends the CLI takes the text flavour off again, if the clipboard
// is still the one the watcher wrote. REPOSE_NO_CLIPBOARD_PATH=1 turns the
// watcher off; REPOSE_NO_INPUT_PROXY=1 turns off the proxy and with it the
// watcher (each also as its old =0 spelling, I-621).

// clipWatchScript is JXA (osascript -l JavaScript). argv[0] is the
// directory for the PNG copies. It prints "set <changeCount> <path>" for
// each clipboard it gives a path, and "." as a heartbeat.
const clipWatchScript = `ObjC.import('AppKit');
function stamp() {
	var d = new Date(), p = function (n, w) { return ('000' + n).slice(-(w || 2)); };
	return d.getFullYear() + p(d.getMonth() + 1) + p(d.getDate()) + '-' + p(d.getHours()) +
		p(d.getMinutes()) + p(d.getSeconds()) + '-' + p(d.getMilliseconds(), 3);
}
function augment(pb, dir, count) {
	var types = ObjC.deepUnwrap(pb.types) || [];
	if (types.indexOf('public.utf8-plain-text') >= 0 || types.indexOf('public.file-url') >= 0) return '';
	var png = pb.dataForType('public.png');
	if (png.isNil()) {
		var tiff = pb.dataForType('public.tiff');
		if (tiff.isNil()) return '';
		var rep = $.NSBitmapImageRep.imageRepWithData(tiff);
		if (rep.isNil()) return '';
		png = rep.representationUsingTypeProperties(4, $.NSDictionary.dictionary);
		if (png.isNil()) return '';
	}
	if (png.length > 20971520) return '';
	var name = dir + '/clipboard-' + stamp() + '.png';
	if (!png.writeToFileAtomically(name, true)) return '';
	var item = $.NSPasteboardItem.alloc.init;
	types.forEach(function (t) {
		var d = pb.dataForType(t);
		if (!d.isNil()) item.setDataForType(d, t);
	});
	item.setStringForType(name, 'public.utf8-plain-text');
	if (pb.changeCount !== count) return '';
	pb.clearContents;
	pb.writeObjects($([item]));
	return name;
}
function run(argv) {
	var dir = argv[0], pb = $.NSPasteboard.generalPasteboard;
	var out = $.NSFileHandle.fileHandleWithStandardOutput;
	var say = function (s) { out.writeData($(s + '\n').dataUsingEncoding($.NSUTF8StringEncoding)); };
	var seen = -1, beat = 0;
	for (;;) {
		var n = pb.changeCount;
		if (n !== seen) {
			seen = n;
			var name = augment(pb, dir, n);
			if (name) {
				seen = pb.changeCount;
				say('set ' + seen + ' ' + name);
			}
		}
		if (beat++ % 8 === 0) say('.');
		delay(0.25);
	}
}`

// clipRestoreScript takes the text flavour off again: argv is the change
// count the watcher left and the path it wrote. A clipboard that changed
// since, or whose text is something else, is left alone.
const clipRestoreScript = `ObjC.import('AppKit');
function run(argv) {
	var pb = $.NSPasteboard.generalPasteboard;
	if (String(pb.changeCount) !== argv[0]) return 'changed';
	if (ObjC.unwrap(pb.stringForType('public.utf8-plain-text')) !== argv[1]) return 'changed';
	var item = $.NSPasteboardItem.alloc.init;
	(ObjC.deepUnwrap(pb.types) || []).forEach(function (t) {
		if (t === 'public.utf8-plain-text') return;
		var d = pb.dataForType(t);
		if (!d.isNil()) item.setDataForType(d, t);
	});
	pb.clearContents;
	pb.writeObjects($([item]));
	return 'restored';
}`

// clipWatchCommand and clipRestoreCommand build the two osascript runs;
// tests put a script of their own in their place.
var (
	clipWatchCommand = func(dir string) *exec.Cmd {
		return exec.Command("osascript", "-l", "JavaScript", "-e", clipWatchScript, dir)
	}
	clipRestoreCommand = func(ctx context.Context, count, path string) *exec.Cmd {
		return exec.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", clipRestoreScript, count, path)
	}
)

// clipKeep bounds the PNG copies: at most this many, none older than a
// day, pruned when a watcher starts.
const clipKeep = 20

// clipboardWatchEnabled is the watcher's switch: macOS only, on unless
// REPOSE_NO_CLIPBOARD_PATH=1 or the old REPOSE_CLIPBOARD_PATH=0.
func clipboardWatchEnabled() bool {
	return goos() == "darwin" && os.Getenv(envNoClipboardPath) != "1" && os.Getenv(envClipboardPath) != "0"
}

// clipboardDir is where the PNG copies go: the user's cache directory,
// so a path the watcher offers is never hidden (the proxy never copies a
// file under a dot directory) and never under a system folder.
func clipboardDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "repose", "clipboard")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// pruneClipboardDir deletes copies older than a day and all but the
// newest clipKeep.
func pruneClipboardDir(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type copyFile struct {
		path string
		mod  time.Time
	}
	var files []copyFile
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "clipboard-") || !strings.HasSuffix(e.Name(), ".png") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, copyFile{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for i, f := range files {
		if i >= clipKeep || now.Sub(f.mod) > 24*time.Hour {
			_ = os.Remove(f.path)
		}
	}
}

// clipWatch is a running watcher.
type clipWatch struct {
	cmd  *exec.Cmd
	done chan struct{}

	mu    sync.Mutex
	count string // the change count of the clipboard the watcher last wrote
	path  string // and the path it put on it
}

// startClipboardWatch starts the watcher and returns its stop. Any
// failure leaves Cmd+V as it was and returns a stop that does nothing.
func startClipboardWatch() (stop func()) {
	if !clipboardWatchEnabled() {
		return func() {}
	}
	dir, err := clipboardDir()
	if err != nil {
		return func() {}
	}
	pruneClipboardDir(dir, time.Now())
	return runClipWatch(clipWatchCommand(dir))
}

func runClipWatch(cmd *exec.Cmd) (stop func()) {
	out, err := cmd.StdoutPipe()
	if err != nil {
		return func() {}
	}
	if err := cmd.Start(); err != nil {
		return func() {}
	}
	w := &clipWatch{cmd: cmd, done: make(chan struct{})}
	go func() {
		defer close(w.done)
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			count, path, ok := parseClipSet(sc.Text())
			if !ok {
				continue
			}
			w.mu.Lock()
			w.count, w.path = count, path
			w.mu.Unlock()
		}
	}()
	return w.stop
}

// parseClipSet reads one "set <changeCount> <path>" line.
func parseClipSet(line string) (count, path string, ok bool) {
	rest, ok := strings.CutPrefix(line, "set ")
	if !ok {
		return "", "", false
	}
	count, path, ok = strings.Cut(rest, " ")
	if !ok || path == "" {
		return "", "", false
	}
	if _, err := strconv.ParseInt(count, 10, 64); err != nil {
		return "", "", false
	}
	return count, path, true
}

// clipRestoreWait bounds the restore at the end of a session.
const clipRestoreWait = 2 * time.Second

func (w *clipWatch) stop() {
	_ = w.cmd.Process.Kill()
	<-w.done // the pipe reads end before Wait closes it
	_ = w.cmd.Wait()
	w.mu.Lock()
	count, path := w.count, w.path
	w.mu.Unlock()
	if count == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), clipRestoreWait)
	defer cancel()
	_ = clipRestoreCommand(ctx, count, path).Run()
}
