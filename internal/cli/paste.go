package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/heracraft/repose/internal/multiplexer"
)

// `repose paste` (DECISIONS I-252): the image on the laptop's clipboard
// goes to /tmp/repose-paste/<timestamp>.png in the guest, over the
// project's multiplexed ssh connection (the one `repose cp` uses), and its
// path is pasted into the tmux session's active pane, where Claude Code
// attaches an image whose path is pasted. One direction only: nothing in
// the guest can read the laptop's clipboard (proposal 2026-09-23 item 8).

// pasteGuestDir is where pasted images land in the guest; a variable so
// tests, whose fake guest shares this machine's /tmp, use their own.
var pasteGuestDir = "/tmp/repose-paste"

// pasteMaxBytes caps one image. A full-screen 5K screenshot as PNG is
// about 15 MB; more than this is not a screenshot.
const pasteMaxBytes = 20 << 20

// pasteKeep bounds the directory: files older than a day go, and at most
// this many of the newest stay.
const (
	pasteMaxAgeMinutes = 24 * 60
	pasteKeep          = 50
)

// pngMagic is the eight bytes every PNG starts with.
var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// clipboardReader reads the image on the laptop's clipboard as PNG bytes.
// Tests swap pasteClipboard for a fake so they need no clipboard.
type clipboardReader interface {
	ReadPNG(ctx context.Context) ([]byte, error)
}

var pasteClipboard clipboardReader = systemClipboard{}

// errNoImage is a clipboard with no image on it; the message is the one
// the user sees.
var errNoImage = errors.New("there is no image on the clipboard")

// clipboardToolError names the program the user has to install.
type clipboardToolError struct{ msg string }

func (e *clipboardToolError) Error() string { return e.msg }

// systemClipboard reads the clipboard with the platform's own tools:
// pngpaste or osascript on macOS, wl-paste on Wayland, xclip on X11.
type systemClipboard struct{}

func (systemClipboard) ReadPNG(ctx context.Context) ([]byte, error) {
	switch goos() {
	case "darwin":
		return readClipboardMac(ctx)
	case "windows":
		return nil, &clipboardToolError{"repose paste reads the clipboard on macOS and Linux only. On Windows, save the image and copy it with `repose cp FILE :/tmp/`, then type its path."}
	default:
		return readClipboardUnix(ctx)
	}
}

// runClipboardTool runs one clipboard program and returns its stdout.
func runClipboardTool(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w (%s)", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// macPNGScript writes the clipboard's PNG data to the file named by the
// first argument, and prints "noimage" when there is none. «class PNGf»
// is what a screenshot to the clipboard and a copied image both offer.
const macPNGScript = `on run argv
	try
		set img to the clipboard as «class PNGf»
	on error
		return "noimage"
	end try
	set f to open for access (POSIX file (item 1 of argv)) with write permission
	try
		set eof f to 0
		write img to f
	end try
	close access f
	return "ok"
end run`

func readClipboardMac(ctx context.Context) ([]byte, error) {
	if _, err := exec.LookPath("pngpaste"); err == nil {
		out, err := runClipboardTool(ctx, "pngpaste", "-")
		if err != nil {
			// pngpaste exits 1 with "No PNG data found on the pasteboard".
			return nil, errNoImage
		}
		return out, nil
	}
	f, err := os.CreateTemp("", "repose-paste-*.png")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	_ = f.Close()
	defer func() { _ = os.Remove(name) }()
	out, err := runClipboardTool(ctx, "osascript", "-e", macPNGScript, name)
	if err != nil {
		return nil, fmt.Errorf("could not read the clipboard with osascript: %w", err)
	}
	if strings.TrimSpace(string(out)) == "noimage" {
		return nil, errNoImage
	}
	return os.ReadFile(name)
}

// readClipboardUnix reads Wayland's clipboard when there is a Wayland
// display, else X11's. It asks for the offered types first so "no image"
// and "the tool failed" read differently.
func readClipboardUnix(ctx context.Context) ([]byte, error) {
	hint := ""
	if isWSL() {
		hint = " Under WSL this is the Linux clipboard; an image copied in Windows may not be on it."
	}
	switch {
	case os.Getenv(envWaylandDisplay) != "":
		if _, err := exec.LookPath("wl-paste"); err != nil {
			return nil, &clipboardToolError{"repose paste needs wl-paste to read the Wayland clipboard; install wl-clipboard." + hint}
		}
		types, err := runClipboardTool(ctx, "wl-paste", "--list-types")
		if err != nil || !hasLine(types, "image/png") {
			return nil, noImageHint(hint)
		}
		return runClipboardTool(ctx, "wl-paste", "--no-newline", "--type", "image/png")
	case os.Getenv(envDisplay) != "":
		if _, err := exec.LookPath("xclip"); err != nil {
			return nil, &clipboardToolError{"repose paste needs xclip to read the X11 clipboard; install xclip." + hint}
		}
		types, err := runClipboardTool(ctx, "xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
		if err != nil || !hasLine(types, "image/png") {
			return nil, noImageHint(hint)
		}
		return runClipboardTool(ctx, "xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	default:
		return nil, &clipboardToolError{"repose paste reads the clipboard of a desktop session, and neither WAYLAND_DISPLAY nor DISPLAY is set (over ssh, run it on the computer you copied the image on)." + hint}
	}
}

func noImageHint(hint string) error {
	if hint == "" {
		return errNoImage
	}
	return fmt.Errorf("%w.%s", errNoImage, strings.TrimSuffix(hint, "."))
}

func hasLine(b []byte, want string) bool {
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == want {
			return true
		}
	}
	return false
}

// isWSL reports a Linux kernel built for WSL.
func isWSL() bool {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

// PasteOptions are `repose paste`'s flags.
type PasteOptions struct {
	ProjectArg string
	Window     string // "" is the session's current window
	Print      bool   // only print the guest path; type nothing
}

func newPasteCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var opts PasteOptions
	cmd := &cobra.Command{
		Use:   "paste [PROJECT]",
		Short: "Send the image on the clipboard to the machine and paste its path into the agent's prompt",
		Long: `Copy the image on this computer's clipboard to /tmp/repose-paste/ on the
machine and paste its path into the active pane (tmux) or the focused
pane (herdr), where Claude Code attaches it. --window NAME pastes into
that window, or that herdr agent's pane, instead; --print only prints
the path.

The clipboard is read with pngpaste or osascript on macOS, wl-paste on
Wayland and xclip on X11.`,
		Args:              projectArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := projectFrom(args, g)
			if err != nil {
				return err
			}
			if opts.Print && opts.Window != "" {
				return cobraUsageError{fmt.Errorf("--window names where to paste; --print pastes nowhere")}
			}
			opts.ProjectArg = project
			e, err := env()
			if err != nil {
				return err
			}
			return PasteCmd(cmd.Context(), e, opts)
		},
	}
	cmd.Flags().StringVar(&opts.Window, "window", "", "paste into this tmux window (name or index), or this herdr agent's pane, instead of the current one")
	cmd.Flags().BoolVar(&opts.Print, "print", false, "copy the image and print its path on the machine; paste nothing")
	return cmd
}

// PasteCmd reads the clipboard first (no image means no api call and no
// ssh), then sends the image and pastes its path in one ssh command.
func PasteCmd(ctx context.Context, e *Env, opts PasteOptions) error {
	img, err := pasteClipboard.ReadPNG(ctx)
	var te *clipboardToolError
	switch {
	case errors.As(err, &te):
		return exitf(ExitGeneric, "%s", te.msg)
	case errors.Is(err, errNoImage):
		return exitf(ExitGeneric, "Nothing to paste: %s. Copy an image or take a screenshot to the clipboard first.", strings.TrimSuffix(err.Error(), "."))
	case err != nil:
		return exitf(ExitGeneric, "Could not read the clipboard: %v.", err)
	}
	if !bytes.HasPrefix(img, pngMagic) {
		return exitf(ExitGeneric, "Nothing to paste: %s. Copy an image or take a screenshot to the clipboard first.", errNoImage)
	}
	if len(img) > pasteMaxBytes {
		return exitf(ExitGeneric, "The image on the clipboard is %s; repose paste takes up to %s.", humanBytes(int64(len(img))), humanBytes(pasteMaxBytes))
	}
	project, err := requireRunningProject(ctx, e, opts.ProjectArg)
	if err != nil {
		return err
	}
	target, err := connect(ctx, e, project)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	guestPath := pasteGuestDir + "/" + now.Format("20060102-150405") + fmt.Sprintf("-%03d.png", now.Nanosecond()/1e6)
	m := muxer(tmuxMux{})
	if !opts.Print {
		m = muxFor(ctx, target)
	}
	out, err := runSSH(ctx, target, pasteScriptWith(m, project.Slug, guestPath, opts), bytes.NewReader(img))
	var se *sshError
	if errors.As(err, &se) {
		switch se.ExitCode {
		case pasteExitUnsafeDir:
			return exitf(ExitGeneric, "Could not save the image on %s: %s is not a directory of its own for dev. Remove it on the machine and try again.", project.Slug, pasteGuestDir)
		case pasteExitNoPane:
			where := project.Slug + "'s tmux session"
			if opts.Window != "" {
				where = fmt.Sprintf("window %s in %s", opts.Window, where)
			}
			if m.Name() == multiplexer.Herdr {
				where = "focused pane in herdr on " + project.Slug
				if opts.Window != "" {
					where = fmt.Sprintf("herdr agent %s on %s", opts.Window, project.Slug)
				}
			}
			return exitf(ExitGeneric, "Saved %s on %s, but could not paste the path: no %s. Paste it yourself, or pass --window.", guestPath, project.Slug, where)
		}
	}
	if err != nil {
		return stepFailed("copy the image to "+project.Slug, err, "")
	}
	if opts.Print {
		_, _ = fmt.Fprintln(e.Out, guestPath)
		return nil
	}
	window := strings.TrimSpace(string(out))
	_, _ = fmt.Fprintf(e.Out, "Pasted %s into %s:%s.\n", filepath.Base(guestPath), project.Slug, window)
	return nil
}

// pasteSaveScript saves stdin as guestPath (in pasteGuestDir) and prunes
// the directory first: files over a day old, and all but the newest
// pasteKeep. `repose paste`, and the input proxy's drops and Ctrl+V
// (I-280), share it and the directory.
func pasteSaveScript(guestPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "umask 077\nd=%s\nf=%s\n", shQuote(pasteGuestDir), shQuote(guestPath))
	fmt.Fprintf(&b, `mkdir -p "$d" 2>/dev/null
if [ -L "$d" ] || [ ! -d "$d" ] || [ ! -O "$d" ]; then cat >/dev/null; exit %d; fi
chmod 700 "$d" || exit 1
find "$d" -maxdepth 1 -type f -mmin +%d -delete 2>/dev/null
ls -1t "$d" 2>/dev/null | grep -v '\.part$' | tail -n +%d | while IFS= read -r old; do rm -f "$d/$old"; done
cat > "$f.part" || { rm -f "$f.part"; exit 1; }
mv -f "$f.part" "$f" || exit 1
`, pasteExitUnsafeDir, pasteMaxAgeMinutes, pasteKeep)
	return b.String()
}

// Exit statuses of pasteScript that mean something to PasteCmd.
const (
	pasteExitUnsafeDir = 3
	pasteExitNoPane    = 4
)

// pasteScript saves stdin as guestPath and, unless opts.Print, pastes the
// path into the target pane of the tmux session and prints that pane's
// window name; pasteScriptWith on the machine's multiplexer.
func pasteScript(slug, guestPath string, opts PasteOptions) string {
	return pasteScriptWith(tmuxMux{}, slug, guestPath, opts)
}

// pasteScriptWith saves stdin as guestPath and, unless opts.Print, has m
// paste the path into the focused terminal (or opts.Window) and print
// where. The directory is dev's own, 0700, and not a symlink (it is in
// the shared /tmp); files are 0600. Old pastes go first: over a day old,
// or past the newest pasteKeep. On tmux the path goes in with
// paste-buffer -p, a bracketed paste when the program asked for one, the
// way a terminal delivers a dropped file; send-keys would type it key by
// key. list-panes, not display-message, finds the pane: display-message
// falls back to the current pane when the target window does not exist.
// On herdr it is `herdr pane send-text` (I-509), with no Enter.
func pasteScriptWith(m muxer, slug, guestPath string, opts PasteOptions) string {
	var b strings.Builder
	b.WriteString(pasteSaveScript(guestPath))
	if opts.Print {
		return b.String()
	}
	b.WriteString(m.PasteScript(slug, opts.Window))
	return b.String()
}
