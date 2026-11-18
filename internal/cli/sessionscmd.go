package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// sessionLast is `--session`'s value when it is given without one
// (cobra's NoOptDefVal, as --temp): this checkout's newest conversation.
const sessionLast = "last"

// newSessionsCmd is `repose sessions`: the Claude Code conversations this
// laptop holds for this checkout, which `repose run --session` brings to
// the machine. It asks no machine and no server: the files are here.
func newSessionsCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var quiet bool
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List the Claude Code conversations you can continue on a machine",
		Long: "List the Claude Code conversations this laptop has in this checkout, newest\n" +
			"first. repose run --session=ID brings one to the machine, where claude --resume\n" +
			"continues it; repose attach and repose sync take the same flag.\n\n" +
			"In your home folder it exits 2: a conversation belongs to a checkout.",
		Example: "  repose sessions\n  repose run --session=907f3a2c",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cobraUsageError{fmt.Errorf("%s takes no arguments, got %s", cmd.CommandPath(), gotArgs(args))}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if quiet && g.json {
				return cobraUsageError{fmt.Errorf("-q and --json are two different outputs; pass one")}
			}
			e, err := env()
			if err != nil {
				return err
			}
			e.Quiet = quiet
			return SessionsCmd(e)
		},
	}
	cmd.Flags().BoolVar(&g.json, "json", false, "print as JSON")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "print only the conversation ids, one per line")
	return cmd
}

// errHomeSession is a conversation command in the home folder (I-601).
// The home folder is nobody's checkout, so there is no folder on the
// machine to map it onto — the checkout swap and the home swap would be
// the same path, and `~/.claude` would land inside the machine's
// checkout — and every conversation of every project sits under it.
func errHomeSession() error {
	return exitf(ExitUsage, "A Claude Code conversation belongs to a checkout; your home folder is not one. cd into your project.")
}

// SessionsCmd implements `repose sessions`.
func SessionsCmd(e *Env) error {
	root := sessionRoot(e)
	if isHomeFolder(root) {
		return errHomeSession()
	}
	sessions, err := listLaptopSessions(e.HomeDir, root)
	if err != nil {
		return fmt.Errorf("read this laptop's Claude Code conversations: %w", err)
	}
	if e.JSON {
		if sessions == nil {
			sessions = []laptopSession{}
		}
		return writeJSONOut(e.Out, sessions)
	}
	if e.Quiet {
		for _, s := range sessions {
			_, _ = fmt.Fprintln(e.Out, s.ID)
		}
		return nil
	}
	if len(sessions) == 0 {
		_, _ = fmt.Fprintf(e.Out, "No Claude Code conversations in %s.\n", laptopPath(root))
		return nil
	}
	tw := tabwriter.NewWriter(e.Out, 0, 0, 2, ' ', 0)
	// The id's end, as every id a table shows (I-619); -q and --json
	// give the whole id.
	_, _ = fmt.Fprintln(tw, "ID\tLAST USED\tFOLDER\tTITLE")
	for _, s := range sessions {
		folder := "."
		if rel, err := filepath.Rel(root, filepath.Clean(s.Cwd)); err == nil {
			folder = filepath.ToSlash(rel)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", shortID(s.ID), humanAge(s.At), folder, shortTitle(s.Title))
	}
	return tw.Flush()
}

// sessionRoot is the folder whose conversations a command works with: the
// checkout this command runs in, else the folder itself, the same rule
// the sync uses to find what to send.
func sessionRoot(e *Env) string {
	if root := gitRepoRoot(e.Cwd); root != "" {
		return root
	}
	return e.Cwd
}

// sessionArgs consumes the id after a bare `--session`. Flags and
// arguments interleave, so `repose run --session 907f3a2c` leaves the id
// among the arguments, where `repose run PROJECT` would read it as the
// project; a word is the id only when it names one of this laptop's
// conversations in this folder, so a project whose name happens to look
// like an id stays the project. `--session=ID` never needs this.
// ids is read only for a bare `--session`, so a `repose run` without the
// flag never looks at ~/.claude at all.
func sessionArgs(raw string, args []string, ids func() []string) (string, []string) {
	if raw != sessionLast || len(args) == 0 {
		return raw, args
	}
	known := ids()
	for i, a := range args {
		if len(matchID(known, a)) == 1 {
			return a, append(slices.Clone(args[:i]), args[i+1:]...)
		}
	}
	return raw, args
}

// addSessionFlag is run's, sync's and attach's --session [ID]
// (DECISIONS I-1328).
func addSessionFlag(cmd *cobra.Command, raw *string, env func() (*Env, error)) {
	cmd.Flags().StringVar(raw, "session", "", "bring this laptop's Claude Code conversation ID to the machine and continue it there (bare: this checkout's newest)")
	cmd.Flags().Lookup("session").NoOptDefVal = sessionLast
	_ = cmd.RegisterFlagCompletionFunc("session", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		e, err := env()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return laptopSessionIDs(e.HomeDir, sessionRoot(e)), cobra.ShellCompDirectiveNoFileComp
	})
}

// sessionImportStep is the phase name the run shows while a conversation
// travels.
const sessionImportStep = "Bringing the conversation"

// findSession is the conversation idArg names, and the folder it was
// looked for in. laptopRoot is the project's checkout, "" to take this
// command's folder. It reads the laptop only, so `run --session` can
// refuse a typo before it has touched the machine, and the answer is
// held for the import that follows.
func (e *Env) findSession(idArg, laptopRoot string) (laptopSession, string, error) {
	root := laptopRoot
	if root == "" {
		root = sessionRoot(e)
	}
	if isHomeFolder(root) {
		return laptopSession{}, root, errHomeSession()
	}
	if e.session != nil && e.session.ID == idArg {
		return *e.session, root, nil
	}
	sessions, err := listLaptopSessions(e.HomeDir, root)
	if err != nil {
		return laptopSession{}, root, fmt.Errorf("read this laptop's Claude Code conversations: %w", err)
	}
	s, err := resolveLaptopSession(sessions, idArg, root)
	if err != nil {
		return laptopSession{}, root, err
	}
	e.session = &s
	return s, root, nil
}

// bringSession writes the conversation idArg names into the machine's
// Claude Code projects directory, and returns its id for the `claude
// --resume` the window starts with. laptopRoot is the checkout on this
// laptop and machineCheckout the machine's checkout name (guestHomePath).
//
// Two ssh commands: what the machine already has, then the conversation
// when it is not that. A machine that has gone further with the
// conversation than the laptop keeps its own copy and the laptop's is
// never sent.
func (e *Env) bringSession(ctx context.Context, t sshTarget, idArg, laptopRoot, machineCheckout string) (*sessionBrought, error) {
	s, root, err := e.findSession(idArg, laptopRoot)
	if err != nil {
		return nil, err
	}
	sc, err := buildSessionCarry(s, e.HomeDir, root, machineCheckout)
	if err != nil {
		return nil, fmt.Errorf("read the conversation %s: %w", shortID(s.ID), err)
	}
	b := &sessionBrought{ID: s.ID, Title: s.Title, Dir: tildeGuestPath(sc.Cwd)}
	out, err := runSSH(ctx, t, sessionProbeScript(sc.Dir, sc.ID, sc.Cwd), nil)
	if err != nil {
		return nil, stepFailed("ask the machine what it has of the conversation", err, "")
	}
	probe := parseSessionProbe(string(out))
	b.Window = probe.Window
	for _, n := range sc.Notes {
		e.warn("%s", n)
	}
	switch {
	case b.Window != "":
		// A window on the machine is resumed on this conversation, so
		// its copy is the live one: two claudes appending one transcript
		// would interleave two conversations into one file. Nothing is
		// sent, and the caller uses that window.
		b.Kept = true
		return b, nil
	case probe.Mark == sc.Hash:
		// Byte for byte what the machine already took.
		return b, nil
	case probe.HasIt && probe.Have > int64(len(sc.Files[s.ID+".jsonl"])):
		// The machine's copy has more of the conversation in it: the
		// user went on with it there, and this laptop is behind.
		e.warn("Kept the machine's copy of the conversation %s: it has gone further than this laptop's.", shortID(s.ID))
		b.Kept = true
		return b, nil
	}
	p := newGuestPayload()
	if err := addSessionPart(p, sc); err != nil {
		return nil, err
	}
	res, err := p.run(ctx, t)
	if err != nil {
		return nil, stepFailed("bring the conversation to the machine", err, "")
	}
	if strings.Contains(string(res), "#failed") {
		return nil, stepFailed("bring the conversation to the machine", fmt.Errorf("the machine could not write it"), strings.TrimSpace(string(res)))
	}
	return b, nil
}

// sessionBrought is what `--session` did.
type sessionBrought struct {
	ID    string // the conversation
	Title string // what names it to a human, for the line a bare flag prints
	// Dir is the conversation's folder on the machine, as the guest's
	// shell spells it: where the window opens.
	Dir string
	// Window is a window (or herdr agent) already resumed on this
	// conversation; "" when there is none. The caller opens no second
	// one.
	Window string
	// Kept says the machine's copy stayed and the laptop's was not sent.
	Kept bool
}

// line is what the run says about the conversation: which one it is when
// the user did not name it (a bare `--session` chose for them), and that
// it is there when no window follows to show it. Nothing when the user
// named the id and a window is about to open on it.
func (b *sessionBrought) line(bare, noWindow bool) string {
	switch {
	case bare && b.Title != "":
		return fmt.Sprintf("Conversation %s: %s", shortID(b.ID), shortTitle(b.Title))
	case bare:
		return fmt.Sprintf("Conversation %s is on the machine.", shortID(b.ID))
	case noWindow && !b.Kept:
		return fmt.Sprintf("Conversation %s is on the machine.", shortID(b.ID))
	}
	return ""
}

// sessionIDsHere is this folder's conversation ids for the flag parser,
// read straight from the laptop. The parser runs before the command's
// Env exists, and the two read the same two places (os.UserHomeDir and
// os.Getwd, env.go's newEnv), so they cannot disagree outside a test;
// a test that needs another home passes `--session=ID`, which never
// consults this.
func sessionIDsHere() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	root := cwd
	if r := gitRepoRoot(cwd); r != "" {
		root = r
	}
	return laptopSessionIDs(home, root)
}

// checkSessionFlags refuses `--session` beside a flag that would send the
// conversation somewhere it cannot be resumed from.
func checkSessionFlags(opts RunOptions) error {
	if opts.Session == "" {
		return nil
	}
	switch {
	case opts.Agent != "" && opts.Agent != "claude":
		return cobraUsageError{fmt.Errorf("--session brings a Claude Code conversation, so it starts claude; --agent %s starts another agent", opts.Agent)}
	case opts.Window != "":
		return cobraUsageError{fmt.Errorf("--session opens the conversation in a new window; -w types into one that is already there")}
	case opts.Worktree:
		return cobraUsageError{fmt.Errorf("--session continues the conversation in the folder it came from; --worktree starts the agent in another one, where Claude Code would not find it")}
	}
	return nil
}
