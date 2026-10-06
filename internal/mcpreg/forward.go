package mcpreg

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// The forward's files (DECISIONS I-557): forward/NAME.json registers NAME
// for every agent and holds what the shim answers while the laptop is
// away; /run/repose/mcp/NAME.sock is where `repose-mcp hold` listens.

// SocketPath is where hold listens for NAME's shims.
func (p Paths) SocketPath(name string) string {
	return filepath.Join(p.SocketDir, name+".sock")
}

// ForwardFile is forward/NAME.json.
func (p Paths) ForwardFile(name string) string {
	return filepath.Join(p.forwardDir(), name+".json")
}

// LoadForward reads forward/NAME.json; ok is false when it is absent.
func LoadForward(p Paths, name string) (f *Forward, ok bool, err error) {
	b, ok, err := readFile(p.ForwardFile(name))
	if err != nil || !ok {
		return nil, false, err
	}
	f = &Forward{}
	if err := json.Unmarshal(b, f); err != nil {
		return nil, true, err
	}
	return f, true, nil
}

// WriteForward replaces forward/NAME.json, mode 0600, by rename.
func WriteForward(p Paths, f *Forward) error {
	if err := os.MkdirAll(p.Dir(), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(p.forwardDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(p.ForwardFile(f.Name), append(b, '\n'), 0o600)
}

// RemoveForward deletes forward/NAME.json; ok is false when there was none.
func RemoveForward(p Paths, name string) (ok bool, err error) {
	err = os.Remove(p.ForwardFile(name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// RenderedAgents are the agents sync has rendered for before: the ones an
// agent-setup run has met on this machine.
func RenderedAgents(p Paths) []string {
	var out []string
	for a := range readRendered(p).Agents {
		if KnownAgent(a) {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

// SyncRendered is Sync for the agents sync has rendered for before, so a
// forward reaches their next start without writing the config of an agent
// that never ran here (agent-setup syncs that one at its first start).
func SyncRendered(p Paths, stderr io.Writer) {
	if agents := RenderedAgents(p); len(agents) > 0 {
		Sync(p, agents, stderr)
	}
}
