package cli

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/heracraft/repose/internal/checkoutname"
)

// `repose code [PROJECT]` (DECISIONS I-282): open an editor on the
// project's checkout in the machine, over the same `<slug>.repose` host
// plain ssh uses (I-281). The CLI only launches the editor's own command;
// the editor then connects with the laptop's ssh and ~/.ssh/config.

type editorSpec struct {
	Name  string   // the --editor value
	Label string   // how messages name it
	Bins  []string // commands looked up on PATH, in order
	// Apps are the launchers inside the macOS app bundles, for an editor
	// installed without its shell command; relative to /Applications and
	// ~/Applications.
	Apps []string
	// Link is an app opened by a URL rather than a command: Claude
	// Desktop's claude:// link (DECISIONS I-1606). It is never picked when
	// nothing names an editor, because nothing on the laptop says it is
	// installed.
	Link bool
}

// editors in the order `repose code` tries them when nothing names one.
var editors = []editorSpec{
	{Name: "code", Label: "VS Code", Bins: []string{"code"}, Apps: []string{"Visual Studio Code.app/Contents/Resources/app/bin/code"}},
	{Name: "cursor", Label: "Cursor", Bins: []string{"cursor"}, Apps: []string{"Cursor.app/Contents/Resources/app/bin/cursor"}},
	{Name: "zed", Label: "Zed", Bins: []string{"zed", "zeditor"}, Apps: []string{"Zed.app/Contents/MacOS/cli"}},
	{Name: "claude", Label: "Claude Desktop", Link: true},
}

func editorNames() []string {
	var n []string
	for _, ed := range editors {
		n = append(n, ed.Name)
	}
	return n
}

// editorLookPath is exec.LookPath; tests point it at a scratch PATH.
var editorLookPath = exec.LookPath

// find returns the editor's launcher, or "" when it is not installed.
func (ed editorSpec) find(home string) string {
	for _, b := range ed.Bins {
		if p, err := editorLookPath(b); err == nil {
			return p
		}
	}
	if goos() != "darwin" {
		return ""
	}
	for _, dir := range []string{"/Applications", filepath.Join(home, "Applications")} {
		for _, a := range ed.Apps {
			p := filepath.Join(dir, a)
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return p
			}
		}
	}
	return ""
}

// editorArgs is what opens host:dir in ed.
func editorArgs(ed editorSpec, host, dir string) []string {
	if ed.Name == "zed" {
		return []string{"ssh://" + host + dir}
	}
	// VS Code and Cursor (a VS Code fork) take the same flags.
	return []string{"--remote", "ssh-remote+" + host, dir}
}

// pickEditor resolves --editor, then $REPOSE_EDITOR, then config.toml's
// editor (I-622), then the first editor installed. It returns the spec
// and its launcher.
func pickEditor(flag, configured, home string) (editorSpec, string, error) {
	name, from := flag, "--editor"
	if name == "" {
		name, from = strings.TrimSpace(os.Getenv(envReposeEditor)), envReposeEditor
	}
	if name == "" {
		name, from = strings.TrimSpace(configured), "config.toml's editor"
	}
	if name != "" {
		for _, ed := range editors {
			if ed.Name == name {
				if ed.Link {
					return ed, "", nil
				}
				bin := ed.find(home)
				if bin == "" {
					return ed, "", exitf(ExitGeneric, "%s is not installed, or its `%s` command is not on your PATH.%s", ed.Label, ed.Bins[0], installHint(ed))
				}
				return ed, bin, nil
			}
		}
		return editorSpec{}, "", exitf(ExitUsage, "%s must be one of %s, got %q.", from, strings.Join(editorNames(), ", "), name)
	}
	for _, ed := range editors {
		if ed.Link {
			continue
		}
		if bin := ed.find(home); bin != "" {
			return ed, bin, nil
		}
	}
	return editorSpec{}, "", exitf(ExitGeneric, "No editor found: repose code opens VS Code (`code`), Cursor (`cursor`) or Zed (`zed`), and none is on your PATH. Other editors: https://repose.run/docs/ssh-and-editors")
}

func installHint(ed editorSpec) string {
	if ed.Name == "code" || ed.Name == "cursor" {
		return fmt.Sprintf(" In %s, run \"Shell Command: Install '%s' command in PATH\" from the Command Palette.", ed.Label, ed.Bins[0])
	}
	return ""
}

// connectRunning is what `exec`, `mcp` and `browser bridge` do before
// their own process: the project, which must be running (exit 5
// otherwise: a one-off command does not take the plan's memory unasked,
// I-631), and a proved connection to it.
func connectRunning(ctx context.Context, e *Env, projectArg string) (*Project, sshTarget, error) {
	return connectProject(ctx, e, projectArg, false)
}

// connectToWork is connectRunning for `ssh` and `code`, which start a
// stopped machine as attach does: opening a shell or an editor on it is
// asking to work there (I-641).
func connectToWork(ctx context.Context, e *Env, projectArg string) (*Project, sshTarget, error) {
	return connectProject(ctx, e, projectArg, true)
}

func connectProject(ctx context.Context, e *Env, projectArg string, start bool) (*Project, sshTarget, error) {
	res, err := requireProjectRes(ctx, e, projectArg)
	if err != nil {
		return nil, sshTarget{}, err
	}
	project := res.Project
	// A running machine on a restarting server is frozen (I-652): ssh and
	// code wait for the server as attach does (I-709).
	if project.State != "running" || (start && project.HostUnreachable) {
		if !start {
			return nil, sshTarget{}, notRunningError(project)
		}
		pr := e.newProgress()
		err := startToWork(ctx, e, project, pr)
		pr.Fail()
		if err != nil {
			return nil, sshTarget{}, err
		}
	}
	if frozen(project) {
		// A resting machine is woken now, beside the certificate check and
		// the ssh handshake, rather than when the gateway's first
		// connection reaches it (DECISIONS I-1682). The gateway still
		// wakes it if this is lost; a wake that fails says so there.
		go func(id string) {
			wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			_ = e.Client.WakeProject(wctx, id)
		}(project.ID)
	}
	target, err := connect(ctx, e, project)
	if err != nil {
		return nil, sshTarget{}, err
	}
	// A folder `repose run --on` added works in its own checkout (I-480).
	target.Checkout = res.Checkout
	return project, target, nil
}

// windowsEditor is an editor command under WSL that is the Windows
// app's launcher (on a /mnt drive): the editor then connects with
// Windows' ssh, which reads neither WSL's ~/.ssh nor runs ssh-prepare
// (review C10).
func windowsEditor(bin string) bool {
	return isWSL() && strings.HasPrefix(bin, "/mnt/")
}

// CodeCmd is `repose code`.
func CodeCmd(ctx context.Context, e *Env, projectArg, editorFlag string) error {
	ed, bin, err := pickEditor(editorFlag, e.Cfg.Editor, e.HomeDir)
	if err != nil {
		return err
	}
	if ed.Link && isWSL() {
		return exitf(ExitGeneric, "%s on Windows connects with Windows' ssh and cannot reach a repose machine yet. Use repose ssh, or an editor installed inside WSL.", ed.Label)
	}
	if windowsEditor(bin) {
		return exitf(ExitGeneric, "%s here is the Windows app, which connects with Windows' ssh and cannot reach a repose machine yet. Use repose ssh, or an editor installed inside WSL.", ed.Label)
	}
	// The certificate and the Host block, proved with an ssh, before the
	// editor tries its own connection: the editor's errors are far less
	// clear than the CLI's.
	project, target, err := connectToWork(ctx, e, projectArg)
	if err != nil {
		return err
	}
	host := project.Slug + ".repose"
	name, err := guestCheckoutName(ctx, target, project.Slug)
	if err != nil {
		return err
	}
	if target.Checkout == "" {
		e.noteCheckout(ctx, project, name)
	}
	dir := guestHomePath(name)
	_, _ = fmt.Fprintf(e.Out, "Opening %s:%s in %s\n", host, dir, ed.Label)
	if ed.Link {
		link := claudeDesktopLink(project.Slug, name)
		if !browserAvailable(false, false, displayEnv(), goos()) {
			_, _ = fmt.Fprintf(e.Out, "Open this link on the computer with Claude Desktop: %s\n", link)
			return nil
		}
		if err := openLink(link); err != nil {
			_, _ = fmt.Fprintf(e.ErrOut, "Could not open Claude Desktop (%v). Open this link: %s\n", err, link)
		}
		return nil
	}
	cmd := exec.CommandContext(ctx, bin, editorArgs(ed, host, dir)...)
	// The editor's own ssh runs later, outside this command: it must run
	// `repose ssh-prepare` like any other ssh.
	cmd.Env = envWithoutSSHPrepared()
	cmd.Stdout, cmd.Stderr = e.Out, e.ErrOut
	if err := cmd.Run(); err != nil {
		return exitf(ExitGeneric, "Could not start %s (%v).", ed.Label, err)
	}
	return nil
}

func newCodeCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var editor string
	cmd := &cobra.Command{
		Use:   "code [PROJECT[:CHECKOUT]]",
		Short: "Open the checkout in VS Code, Cursor, Zed or Claude Desktop over SSH",
		Long: "Open the project's checkout in VS Code, Cursor, Zed or Claude Desktop on this\n" +
			"laptop, over SSH to PROJECT.repose. A stopped machine starts first.",
		Args:              projectArgs,
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := projectFrom(args, g)
			if err != nil {
				return err
			}
			e, err := env()
			if err != nil {
				return err
			}
			return CodeCmd(cmd.Context(), e, project, editor)
		},
	}
	cmd.Flags().StringVar(&editor, "editor", "", "code, cursor, zed or claude (default: $REPOSE_EDITOR, editor in config.toml, else the first one installed of code, cursor and zed)")
	_ = cmd.RegisterFlagCompletionFunc("editor", cobra.FixedCompletions(editorNames(), cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

// openLink opens a URL with the laptop's handler for its scheme; tests
// replace it.
var openLink = openBrowser

// displayEnv is what browserAvailable reads on Linux: an X or a Wayland
// display.
func displayEnv() string {
	if d := os.Getenv("DISPLAY"); d != "" {
		return d
	}
	return os.Getenv("WAYLAND_DISPLAY")
}

// claudeDesktopLink is Claude Desktop's documented link to a new session
// on an SSH host (code.claude.com/docs/en/desktop, "Open an SSH session
// from a link"; DECISIONS I-1606): ssh_host is the project's alias in the
// user's ssh config, which names the user and the certificate, and
// ssh_folder the checkout, or the home when the machine has none. The
// link carries no prompt, user, token or project id. The dashboard builds
// the same link (apps/web/src/lib/claudeLink.ts).
func claudeDesktopLink(slug, checkout string) string {
	folder := guestHomePath(checkout)
	if !checkoutname.Valid(checkout) {
		folder = "~"
	}
	// Both values are letters, digits, '.', '_', '-' and '/', which a
	// query value carries as they are; '/' stays unescaped, as in the
	// documented example.
	return "claude://code/new?ssh_host=" + url.QueryEscape(slug+".repose") + "&ssh_folder=" + strings.ReplaceAll(url.QueryEscape(folder), "%2F", "/")
}
