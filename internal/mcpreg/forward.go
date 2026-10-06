package mcpreg

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// The forward's files (DECISIONS I-557): forward/NAME.json registers NAME
// for every agent and holds what the shim answers while the laptop is
// away; /run/repose/mcp/NAME.sock is where `repose-mcp hold` listens.

// SocketPath is where hold listens for NAME's shims.
func (p Paths) SocketPath(name string) string {
	return filepath.Join(p.SocketDir, name+".sock")
}

// AlivePath is /run/repose/mcp/NAME.alive, which hold touches each time
// the laptop's keepalive arrives. A CLI from before the keepalive sends
// none, so hold never writes the file for it.
func (p Paths) AlivePath(name string) string {
	return filepath.Join(p.SocketDir, name+".alive")
}

// AliveStale is how old NAME.alive may be before status calls the
// laptop away: three keepalives missed.
const AliveStale = 30 * time.Second

// LaptopAway reports whether a forward's laptop is away: no socket, or a
// keepalive file older than AliveStale.
func (p Paths) LaptopAway(name string, now time.Time) bool {
	if _, err := os.Stat(p.SocketPath(name)); err != nil {
		return true
	}
	fi, err := os.Stat(p.AlivePath(name))
	return err == nil && now.Sub(fi.ModTime()) > AliveStale
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
	// The agent names only, whatever the file's version.
	var rf struct {
		Agents map[string]json.RawMessage `json:"agents"`
	}
	_ = readJSON(p.renderedFile(), &rf) // unreadable: no agent yet
	for a := range rf.Agents {
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
