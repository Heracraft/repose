package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
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
}

// editors in the order `repose code` tries them when nothing names one.
var editors = []editorSpec{
	{Name: "code", Label: "VS Code", Bins: []string{"code"}, Apps: []string{"Visual Studio Code.app/Contents/Resources/app/bin/code"}},
	{Name: "cursor", Label: "Cursor", Bins: []string{"cursor"}, Apps: []string{"Cursor.app/Contents/Resources/app/bin/cursor"}},
	{Name: "zed", Label: "Zed", Bins: []string{"zed", "zeditor"}, Apps: []string{"Zed.app/Contents/MacOS/cli"}},
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

// pickEditor resolves --editor, then $REPOSE_EDITOR, then the first
// editor installed. It returns the spec and its launcher.
func pickEditor(flag, home string) (editorSpec, string, error) {
	name, from := flag, "--editor"
	if name == "" {
		name, from = strings.TrimSpace(os.Getenv(envReposeEditor)), envReposeEditor
	}
	if name != "" {
		for _, ed := range editors {
			if ed.Name == name {
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
		if bin := ed.find(home); bin != "" {
			return ed, bin, nil
		}
	}
	return editorSpec{}, "", exitf(ExitGeneric, "No editor found: repose code opens VS Code (`code`), Cursor (`cursor`) or Zed (`zed`), and none is on your PATH. Any editor that connects over SSH can open the project: see https://repose.herakraft.co/docs/ssh-and-editors")
}

func installHint(ed editorSpec) string {
	if ed.Name == "code" || ed.Name == "cursor" {
		return fmt.Sprintf(" In %s, run \"Shell Command: Install '%s' command in PATH\" from the Command Palette.", ed.Label, ed.Bins[0])
	}
	return ""
}

// connectRunning is what `code`, `exec` and `ssh` do before their own
// process: the project, which must be running (exit 5 otherwise; none of
// them starts a machine), and a proved connection to it.
func connectRunning(ctx context.Context, e *Env, projectArg string) (*Project, sshTarget, error) {
	res, err := requireProjectRes(ctx, e, projectArg)
	if err != nil {
		return nil, sshTarget{}, err
	}
	project := res.Project
	if project.State != "running" {
		return nil, sshTarget{}, notRunningError(project)
	}
	target, err := connect(ctx, e, project)
	if err != nil {
		return nil, sshTarget{}, err
	}
	// A folder `repose run --on` added works in its own checkout (I-480).
	target.Checkout = res.Checkout
	return project, target, nil
}

// CodeCmd is `repose code`.
func CodeCmd(ctx context.Context, e *Env, projectArg, editorFlag string) error {
	ed, bin, err := pickEditor(editorFlag, e.HomeDir)
	if err != nil {
		return err
	}
	// The certificate and the Host block, proved with an ssh, before the
	// editor tries its own connection: the editor's errors are far less
	// clear than the CLI's.
	project, target, err := connectRunning(ctx, e, projectArg)
	if err != nil {
		return err
	}
	host := project.Slug + ".repose"
	name, err := guestCheckoutName(ctx, target, project.Slug)
	if err != nil {
		return err
	}
	dir := guestHomePath(name)
	_, _ = fmt.Fprintf(e.Out, "Opening %s:%s in %s\n", host, dir, ed.Label)
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
		Use:               "code [PROJECT[:CHECKOUT]]",
		Short:             "Open the project's checkout in VS Code, Cursor or Zed over SSH",
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
	cmd.Flags().StringVar(&editor, "editor", "", "code, cursor or zed (or $REPOSE_EDITOR); default: the first one installed")
	_ = cmd.RegisterFlagCompletionFunc("editor", cobra.FixedCompletions(editorNames(), cobra.ShellCompDirectiveNoFileComp))
	return cmd
}
