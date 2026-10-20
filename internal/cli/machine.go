package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/heracraft/repose/internal/machine"
	"github.com/heracraft/repose/ops/install"
)

// `repose machine` is the machines the user owns (docs/workstreams/SPINE.md
// Contract 4): a box they can already ssh into becomes one with
// `machine add`, which installs repose on it over that same ssh. There
// is no api on this path, so the registry is a file on the laptop
// (internal/machine).

// defaultUndoGB is how much undo space the installer provisions when
// --undo-gb is not given.
const defaultUndoGB = 40

// machineSpaceWait bounds the undo reading `machine ls` takes from each
// machine: the table is worth showing even when a box is off.
const machineSpaceWait = 10 * time.Second

// resolveTarget is what this laptop's ssh makes of a --ssh address. A
// variable so a test stands in for ssh, as streamOnMachine does for the
// install.
var resolveTarget = machine.ResolveTarget

func newMachineCmd(env func() (*Env, error)) *cobra.Command {
	root := &cobra.Command{
		Use:   "machine",
		Short: "Add, list or remove a machine you own",
		Long: "Add, list or remove a machine you own: a Linux box you can already ssh into,\n" +
			"which repose then runs projects and rewind points on. Alone, list them.",
		SuggestFor: []string{"machines"},
	}
	var opts machineAddOptions
	add := &cobra.Command{
		Use:   "add NAME --ssh [user@]HOST[:PORT]",
		Short: "Add a machine you own and install repose on it",
		Long: "Add a machine you own: check the box, provision its undo space, install\n" +
			"repose's agent and the coding agents, and record it as NAME. Running it\n" +
			"again on a machine already added installs over it, which changes nothing\n" +
			"that is already in place.",
		Example: "  repose machine add homebox --ssh me@192.168.1.40\n" +
			"  repose machine add shed --ssh me@shed:2222 --undo-gb 80",
		Args: argsN(1, 1, "one NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			opts.Name = args[0]
			return MachineAddCmd(cmd.Context(), e, opts)
		},
	}
	add.Flags().StringVar(&opts.SSH, "ssh", "", "reach it at `[user@]HOST[:PORT]`, as ssh would")
	add.Flags().StringVar(&opts.Key, "key", "", "authorise with the key at `PATH` (default: the keys ssh already offers)")
	add.Flags().IntVar(&opts.UndoGB, "undo-gb", defaultUndoGB, "provision `N` GB of undo space")
	add.Flags().StringVar(&opts.AgentURL, "agent-url", "", "install the repose-agent at this `URL` instead of the one from the signed release")
	add.Flags().StringVar(&opts.AgentSHA256, "agent-sha256", "", "the sha256 --agent-url must match")
	ls := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the machines you own",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return MachineLsCmd(cmd.Context(), e)
		},
	}
	rm := &cobra.Command{
		Use:     "rm NAME",
		Aliases: []string{"remove"},
		Short:   "Forget a machine you own",
		Long: "Forget a machine: the laptop stops reaching it and forgets which projects\n" +
			"are on it. What is installed on the box, and its rewind points, stay as\n" +
			"they are.",
		Args: argsN(1, 1, "one NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return MachineRmCmd(cmd.Context(), e, args[0])
		},
	}
	root.AddCommand(add, ls, rm)
	bareRuns(root, ls)
	return root
}

// machineAddOptions is what `repose machine add` was asked for. Now is
// the clock the ✓ lines time the installer's steps by.
type machineAddOptions struct {
	Name   string
	SSH    string
	Key    string
	UndoGB int
	// AgentURL and AgentSHA256 install that repose-agent instead of the
	// one from the signed release: what a self-hosted build needs, since
	// it has no release to be signed in (DECISIONS I-816 ships the
	// self-hostable half first). Both or neither.
	AgentURL    string
	AgentSHA256 string
	Now         func() time.Time
}

// MachineAddCmd implements `repose machine add NAME --ssh ...`: write the
// registry entry, render the ssh config it needs, then run the installer
// over that ssh and turn each step it reports into a ✓ line.
func MachineAddCmd(ctx context.Context, e *Env, opts machineAddOptions) error {
	// The name's shape first, before anything else is read; the aliases
	// it may not take are checked below, where a name already in the
	// registry is an install over it rather than a refusal.
	if err := machine.CheckName(opts.Name, nil); err != nil {
		return cobraUsageError{err}
	}
	if opts.SSH == "" {
		return cobraUsageError{errors.New("repose machine add needs --ssh [user@]HOST[:PORT]")}
	}
	if opts.UndoGB < 1 {
		return cobraUsageError{fmt.Errorf("--undo-gb takes a whole number of gigabytes, got %d", opts.UndoGB)}
	}
	if (opts.AgentURL == "") != (opts.AgentSHA256 == "") {
		return cobraUsageError{errors.New("--agent-url and --agent-sha256 go together: an agent is installed only against a hash")}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	target, err := machine.ParseSSHTarget(opts.SSH, machine.LocalUser())
	if err != nil {
		return cobraUsageError{err}
	}
	// What this laptop's own ssh makes of that address: a `Host` entry of
	// the user's, a Tailscale name and a jump host all reach a box, and
	// the blocks repose writes are read with -F, which replaces the
	// user's config rather than adding to it. So the address is resolved
	// here, once, and what it resolved to is what the registry keeps
	// (machine.ResolveTarget).
	res, err := resolveTarget(ctx, target)
	if err != nil {
		return cobraUsageError{err}
	}
	reg := registryFor(e)
	m := machine.Machine{
		Name:         opts.Name,
		Address:      res.Address,
		Port:         res.Port,
		Login:        res.Login,
		KeyPath:      res.KeyPath,
		ProxyJump:    res.ProxyJump,
		ProxyCommand: res.ProxyCommand,
		UndoPath:     defaultUndoPath,
		AddedAt:      now(),
	}
	// The name the user knows the box by, when that is not the address
	// itself: it is what `machine ls` shows them.
	if !res.Target().Same(target) {
		m.Spelled = strings.TrimSpace(opts.SSH)
	}
	if opts.Key != "" {
		m.KeyPath = opts.Key
	}
	added := false
	switch have, err := reg.Get(opts.Name); {
	case err == nil:
		// A machine of this name already: the install runs over it, and
		// the entry is rewritten with where the box is now. A box on DHCP,
		// a laptop on another network and a `Host` entry that was edited
		// all move a machine, and rm-then-add would take the projects
		// recorded on it with them.
		moved := !have.Target().Same(m.Target())
		m.AddedAt, m.UndoPath = have.AddedAt, have.UndoPath
		// A key the machine was added with stays until another --key
		// replaces it: an install over a box whose config no longer names
		// the key would otherwise be refused by it.
		if m.KeyPath == "" {
			m.KeyPath = have.KeyPath
		}
		if err := reg.Update(m); err != nil {
			return err
		}
		if moved {
			_, _ = fmt.Fprintf(e.ErrOut, "%s moved from %s to %s.\n", m.Name, have.Target(), m.Target())
		}
	case !isNoSuchMachine(err):
		return err
	default:
		if err := machine.CheckName(opts.Name, projectAliases()); err != nil {
			return cobraUsageError{err}
		}
		if err := reg.Add(m); err != nil {
			return err
		}
		added = true
	}
	if err := writeMachineSSH(reg); err != nil {
		return err
	}
	configPath, err := machinesConfigPath()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.ErrOut, "Installing on %s ...\n", m.Target())
	step := now()
	onLine := func(line string) {
		text, ok := installerStep(line)
		if !ok {
			return
		}
		at := now()
		_, _ = fmt.Fprintf(e.ErrOut, "✓ %s  %s\n", text, fmtElapsed(at.Sub(step)))
		step = at
	}
	remote := "bash -s -- --name " + shQuote(opts.Name) + " --undo-gb " + shQuote(strconv.Itoa(opts.UndoGB))
	if opts.AgentURL != "" {
		remote += " --agent-url " + shQuote(opts.AgentURL) + " --agent-sha256 " + shQuote(opts.AgentSHA256)
	}
	out, err := streamOnMachine(ctx, machineTarget(configPath, m), remote, strings.NewReader(install.Script), onLine)
	if err == nil {
		err = installerResult(out)
	}
	if err != nil {
		// A machine that did not finish installing is no machine: forget
		// the entry this run added, so the next `machine add` starts
		// where this one did.
		if added {
			if rmErr := reg.Remove(opts.Name); rmErr == nil {
				_ = writeMachineSSH(reg)
			}
		}
		return stepFailed("install repose on "+opts.Name, err, "")
	}
	_, _ = fmt.Fprintf(e.Out, "Added %s.\n", opts.Name)
	return nil
}

// installerDiePrefix is how ops/install/machine-install.sh prefixes the
// one line it fails with. That line is the failure, not a step, and the
// error the command returns carries it.
const installerDiePrefix = "repose: "

// installerStep is the ✓ line one of the installer's stderr lines
// becomes, and whether it is a step at all: ssh's own chatter and the
// installer's refusal are not.
func installerStep(line string) (string, bool) {
	text := strings.TrimSpace(line)
	switch {
	case text == "",
		strings.HasPrefix(text, installerDiePrefix),
		strings.HasPrefix(text, "usage: "),
		strings.HasPrefix(text, "Warning: Permanently added"):
		return "", false
	}
	return text, true
}

// installerResult reads the installer's one stdout object (Contract 5
// step 7). A box that printed no `ok` finished half-installed, whatever
// its exit status said.
func installerResult(out []byte) error {
	var res struct {
		OK bool `json:"ok"`
	}
	// The last line, not the whole stream: a step's command can write to
	// stdout however quiet it is asked to be (mkfs.btrfs prints its
	// layout), and the result is the installer's last word.
	lines := strings.Split(string(bytes.TrimSpace(out)), "\n")
	last := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "{") {
			last = strings.TrimSpace(lines[i])
			break
		}
	}
	if err := json.Unmarshal([]byte(last), &res); err != nil {
		return errors.New("the installer did not report that it finished")
	}
	if !res.OK {
		return errors.New("the installer did not finish")
	}
	return nil
}

// MachineLsCmd implements `repose machine ls`: the machines in the
// registry with the undo space each one reports.
func MachineLsCmd(ctx context.Context, e *Env) error {
	all, err := registryFor(e).List()
	if err != nil {
		return err
	}
	if len(all) == 0 {
		_, _ = fmt.Fprintln(e.Out, "You have added no machines.")
		return nil
	}
	configPath, err := machinesConfigPath()
	if err != nil {
		return err
	}
	undo := make([]string, len(all))
	var wg sync.WaitGroup
	for i, m := range all {
		wg.Add(1)
		go func(i int, m machine.Machine) {
			defer wg.Done()
			undo[i] = undoCell(ctx, probeTarget(machineTarget(configPath, m)))
		}(i, m)
	}
	wg.Wait()
	tw := tabwriter.NewWriter(e.Out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tADDRESS\tUNDO")
	for i, m := range all {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", m.Name, m.Where(), undo[i])
	}
	return tw.Flush()
}

// undoHighWater is the percent of an undo store its machine keeps it
// under: the timer the installer writes runs `repose-agent gc
// --high-water 85` (ops/install/machine-install.sh), which deletes the
// oldest points until the store is under the mark.
const undoHighWater = 85

// machineConnectWait is how long the reading `machine ls` takes waits
// for a box to answer the connection. The table is worth having while a
// box is off, so ssh gives up rather than holding it.
const machineConnectWait = 5 * time.Second

// probeTarget is a machine's target for that reading: ssh gives up on a
// box that is off, and asks for no password, since `machine ls` has
// nobody to type one.
func probeTarget(t sshTarget) sshTarget {
	if len(t.Args) == 0 {
		return t
	}
	last := len(t.Args) - 1
	args := make([]string, 0, len(t.Args)+4)
	args = append(args, t.Args[:last]...)
	args = append(args,
		"-o", fmt.Sprintf("ConnectTimeout=%d", int(machineConnectWait.Seconds())),
		"-o", "BatchMode=yes",
		t.Args[last])
	t.Args = args
	return t
}

// undoCell is the UNDO column for one machine: what its store holds and
// whether the oldest points are going, or why the machine said nothing.
func undoCell(ctx context.Context, t sshTarget) string {
	ctx, cancel := context.WithTimeout(ctx, machineSpaceWait)
	defer cancel()
	var space agentSpace
	if err := agentRun(ctx, t, &space, "space"); err != nil {
		// A box that took the connection and then said nothing: the wait
		// above ended it, and ssh's own failure is that it was killed.
		if ctx.Err() != nil {
			return "no answer"
		}
		return silentWords(err)
	}
	cell := fmt.Sprintf("%s used of %s", sizeWords(space.Used), sizeWords(space.Total))
	if space.Total > 0 && space.Used >= space.Total/100*undoHighWater {
		cell += ", the oldest points are going"
	}
	return cell
}

// silentWords is why a machine said nothing, in a few words. One word
// for all of it ("unreachable") covers a box that is off, a box that
// refused the laptop's key and a box whose host key changed, which are
// three different things to do about it.
func silentWords(err error) string {
	var se *sshError
	if errors.As(err, &se) {
		switch se.ExitCode {
		case 127:
			return "no repose on the box"
		case -1:
			return "could not run ssh"
		}
		switch classifySSH(se.Stderr) {
		case sshFailNoAnswer:
			return "no answer"
		case sshFailNoName:
			return "address does not resolve"
		case sshFailKeyRefused:
			return "refused the key"
		case sshFailHostKey:
			return "host key changed"
		case sshFailNoConfig:
			return "no ssh config"
		}
	}
	var ae *agentError
	if errors.As(err, &ae) {
		return "refused (" + ae.Code + ")"
	}
	return "unreachable"
}

// MachineRmCmd implements `repose machine rm NAME`: forget the machine
// and the projects recorded on it. What is installed on the box stays.
func MachineRmCmd(ctx context.Context, e *Env, name string) error {
	reg := registryFor(e)
	if _, err := reg.Get(name); err != nil {
		if isNoSuchMachine(err) {
			return exitf(ExitGuestNotRunning, "No machine of yours is called %s.", name)
		}
		return err
	}
	if err := reg.Remove(name); err != nil {
		return err
	}
	if err := writeMachineSSH(reg); err != nil {
		return err
	}
	if err := forgetMachineProjects(e, name); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.Out, "Removed %s.\n", name)
	return nil
}

// streamOnMachine runs one command on a machine, giving each line of its
// stderr to onLine as it arrives, and returns its stdout. The installer
// is the one command long enough to watch: it prints a line per step
// while it works. A variable so a test stands in for ssh.
var streamOnMachine = streamSSH

func streamSSH(ctx context.Context, t sshTarget, remoteCmd string, stdin io.Reader, onLine func(string)) ([]byte, error) {
	args := append(append([]string{}, t.Args...), remoteCmd)
	verboseSSH(t.Args, remoteCmd)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = stdin
	cmd.WaitDelay = sshWaitDelay
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, &sshError{ExitCode: -1, Err: err, Target: t}
	}
	if err := cmd.Start(); err != nil {
		return nil, &sshError{ExitCode: -1, Err: err, Target: t}
	}
	// The last lines are kept for the failure message, the way runSSH's
	// sshError carries them (sshStderrDetail).
	var tail []string
	sc := bufio.NewScanner(errPipe)
	for sc.Scan() {
		line := sc.Text()
		tail = append(tail, line)
		if len(tail) > 2 {
			tail = tail[1:]
		}
		onLine(line)
	}
	err = cmd.Wait()
	if err != nil && errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		err = nil
	}
	if err != nil {
		code := -1
		var xe *exec.ExitError
		if errors.As(err, &xe) {
			code = xe.ExitCode()
		}
		return stdout.Bytes(), &sshError{ExitCode: code, Stderr: strings.Join(tail, "\n"), Err: err, Target: t}
	}
	return stdout.Bytes(), nil
}
