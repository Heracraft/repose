package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/machine"
)

// The wrapper-only spine (docs/workstreams/SPINE.md): a project on a
// plain Linux machine the user owns, reached by ordinary ssh, with undo
// on btrfs subvolumes. There is no api on this path and no gateway: the
// registry is a file on the laptop and the CLI talks straight to the box.
//
// This file holds what `repose machine` and `repose rewind` share: the
// two stores under ~/.config/repose, how a checkout resolves to a
// machine, and the one call into repose-agent on the box.

// reposeAgent is where `machine add` installs the on-box command
// (SPINE.md Contract 3). It prints one JSON object on stdout and nothing
// else.
const reposeAgent = "/opt/repose/bin/repose-agent"

// defaultUndoPath is where the installer mounts the undo store
// (ops/install/machine-install.sh step 3). --device changes what backs
// it, never where it is.
const defaultUndoPath = "/var/lib/repose/undo"

// registryFor is the laptop's machine registry, beside the rest of the
// CLI's configuration.
func registryFor(e *Env) *machine.FileRegistry { return machine.NewRegistry(e.Dir) }

// isNoSuchMachine is the registry's answer for a name it has not got.
func isNoSuchMachine(err error) bool { return errors.Is(err, machine.ErrNotFound) }

// writeMachineSSH writes ~/.ssh/repose/machines from the registry: one
// Host block per machine, with no certificate and no gateway in it.
func writeMachineSSH(reg *machine.FileRegistry) error {
	sd, err := sshDir()
	if err != nil {
		return err
	}
	return reg.WriteSSH(sd, sshPathPrefix())
}

// machinesConfigPath is ~/.ssh/repose/machines, the file every ssh to a
// machine is run with.
func machinesConfigPath() (string, error) {
	sd, err := sshDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(sd, machine.SSHName), nil
}

// machineTarget is the ssh args that reach one machine: its own Host
// block, named with -F, since the Include in ~/.ssh/config covers the
// gateway's blocks alone (cert.go, I-151).
func machineTarget(configPath string, m machine.Machine) sshTarget {
	return sshTarget{Args: []string{"-F", configPath, machine.Alias(m.Name)}}
}

// projectAliases is every <slug>.repose the gateway's blocks already
// claim (~/.ssh/repose/hosts). A machine may not take one of those
// names: ssh reads the first value it finds for a keyword, so two blocks
// for one alias send the connection wherever the first Include reached
// (machine.CheckName).
func projectAliases() []string {
	sd, err := sshDir()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(sd, sshHostsName))
	if err != nil {
		return nil
	}
	var names []string
	for _, l := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), " ")
		if !ok || !strings.EqualFold(k, "Host") {
			continue
		}
		if name, ok := strings.CutSuffix(strings.TrimSpace(v), ".repose"); ok && name != "" {
			names = append(names, name)
		}
	}
	return names
}

// checkoutKeys is how the working directory is recorded in
// projects.toml: its git remote, and the repository root when it is
// inside one (dirKey). It is resolveProject's order (project.go §5.3)
// minus the api, which this path has not got.
func checkoutKeys(e *Env, deps resolveDeps) (remote, dir string) {
	if deps.RemoteFor != nil {
		remote = deps.RemoteFor(e.Cwd)
	}
	return remote, dirKey(e.Cwd, deps)
}

// placeFor resolves the project a spine command acts on: the one
// projectArg names, or the working directory's. There is no fallback to
// the api: a checkout on no machine of the user's is exit 5 and one
// sentence.
func placeFor(e *Env, projectArg string, deps resolveDeps) (machine.Machine, machine.Project, error) {
	none := func(err error) (machine.Machine, machine.Project, error) {
		return machine.Machine{}, machine.Project{}, err
	}
	projects, err := machine.LoadProjects(e.Dir)
	if err != nil {
		return none(err)
	}
	var place machine.Project
	if projectArg != "" {
		found := false
		for _, byKey := range []map[string]machine.Project{projects.ByRemote, projects.ByDir} {
			for _, pr := range byKey {
				if pr.Slug == projectArg {
					place, found = pr, true
				}
			}
		}
		if !found {
			return none(exitf(ExitGuestNotRunning, "No project called %s is on a machine you added.", projectArg))
		}
	} else {
		remote, dir := checkoutKeys(e, deps)
		pr, ok := projects.Resolve(remote, dir)
		if !ok {
			return none(exitf(ExitGuestNotRunning, "This folder's project is on no machine you added."))
		}
		place = pr
	}
	m, err := registryFor(e).Get(place.Machine)
	if err != nil {
		if isNoSuchMachine(err) {
			return none(exitf(ExitGuestNotRunning, "%s is on %s, which is not one of your machines any more.", place.Slug, place.Machine))
		}
		return none(err)
	}
	return m, place, nil
}

// rememberPlace records where the working directory's project lives,
// under both keys placeFor resolves it by. `machine rm` is what forgets
// these again.
func rememberPlace(e *Env, deps resolveDeps, place machine.Project) error {
	remote, dir := checkoutKeys(e, deps)
	return machine.UpdateProjects(e.Dir, func(p *machine.Projects) error {
		p.Remember(remote, dir, place)
		return nil
	})
}

// forgetMachineProjects drops every project recorded on the machine
// called name.
func forgetMachineProjects(e *Env, name string) error {
	return machine.UpdateProjects(e.Dir, func(p *machine.Projects) error {
		p.ForgetMachine(name)
		return nil
	})
}

// runOnMachine runs one command on a machine and returns its stdout. A
// variable so a test stands in for ssh, as execSSH does for the
// foreground exec.
var runOnMachine = func(ctx context.Context, t sshTarget, remoteCmd string, stdin io.Reader) ([]byte, error) {
	return runSSH(ctx, t, remoteCmd, stdin)
}

// agentError is the object repose-agent prints instead of a result: one
// of the six codes of docs/interfaces/README.md. Its message is a clause
// that follows "Could not <step>: " (stepFailed, I-153).
type agentError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *agentError) Error() string {
	if e.Message == "" {
		return "the machine refused it (" + e.Code + ")"
	}
	return e.Message + " (" + e.Code + ")"
}

// The codes the CLI acts on (Contract 3); the rest reach the user as the
// agent's own sentence.
const (
	agentCodeNotFound = "not_found"
	agentCodeBusy     = "busy"
)

// agentPoint is one rewind point as repose-agent prints it (Contract 1's
// Point, with taken_at in RFC 3339).
type agentPoint struct {
	Name    string    `json:"name"`
	Project string    `json:"project"`
	Reason  string    `json:"reason"`
	TakenAt time.Time `json:"taken_at"`
	Bytes   uint64    `json:"bytes"`
}

type agentPoints struct {
	Points []agentPoint `json:"points"`
}

type agentRewind struct {
	Rewound     bool   `json:"rewound"`
	SafetyPoint string `json:"safety_point"`
}

type agentQuiesce struct {
	Stopped []string `json:"stopped"`
	Busy    []string `json:"busy"`
}

type agentSpace struct {
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
	Free  uint64 `json:"free"`
}

// agentRun runs one repose-agent subcommand on the machine and decodes
// the one JSON object it prints into v, which may be nil when only
// success matters. An error object is returned as *agentError whatever
// the exit status was: the agent prints it on stdout for exactly that.
func agentRun(ctx context.Context, t sshTarget, v any, args ...string) error {
	cmd := reposeAgent
	for _, a := range args {
		cmd += " " + shQuote(a)
	}
	out, err := runOnMachine(ctx, t, cmd, nil)
	var refusal struct {
		Error *agentError `json:"error"`
	}
	if json.Unmarshal(out, &refusal) == nil && refusal.Error != nil {
		return refusal.Error
	}
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("repose on the machine answered something that is not its JSON (%v)", err)
	}
	return nil
}

// pointAge is how a rewind point's age reads in the question and in the
// table: whole minutes, hours or days.
func pointAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute ago"
	case d < 2*time.Minute:
		return "1 minute ago"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d/time.Minute))
	case d < 2*time.Hour:
		return "1 hour ago"
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d/time.Hour))
	case d < 48*time.Hour:
		return "1 day ago"
	default:
		return fmt.Sprintf("%d days ago", int(d/(24*time.Hour)))
	}
}

// sizeWords is humanBytes without a trailing ".0", so a store the user
// asked for in whole gigabytes reads as "40 GB".
func sizeWords(n uint64) string {
	return strings.Replace(humanBytes(int64(n)), ".0 ", " ", 1)
}
