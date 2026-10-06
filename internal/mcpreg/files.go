package mcpreg

import (
	"fmt"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// writeAtomic replaces path with data through a temporary file in the same
// directory, keeping the mode of the file it replaces (mode when new).
func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".repose-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op after the rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readFile is os.ReadFile with a missing file read as empty.
func readFile(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}

var errLocked = errors.New("locked")

// Claude Code's own lock on ~/.claude.json (proper-lockfile, 2.1.283): a
// directory beside the file, held while it re-reads and writes, kept fresh
// by touching it, and taken over once older than 10 s.
var (
	claudeLockWait  = 5 * time.Second
	claudeLockStale = 10 * time.Second
)

// lockDir takes the mkdir lock at lock, waiting up to wait; a lock older
// than stale is abandoned and taken over. unlock removes it.
func lockDir(lock string, wait, stale time.Duration) (unlock func(), err error) {
	deadline := time.Now().Add(wait)
	for {
		err := os.Mkdir(lock, 0o700)
		if err == nil {
			return func() { _ = os.Remove(lock) }, nil // gone already is fine
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if st, serr := os.Stat(lock); serr == nil && time.Since(st.ModTime()) > stale {
			if os.Remove(lock) == nil {
				continue
			}
		}
		if time.Now().After(deadline) {
			return nil, errLocked
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// renderedFile is ~/.repose/mcp/rendered.json: per agent, the entries
// sync last left in that agent's config as repose's.
type renderedFile struct {
	Version int                     `json:"version"`
	Agents  map[string]*agentRecord `json:"agents"`
}

type agentRecord struct {
	User     map[string]any            `json:"user,omitempty"`
	Projects map[string]map[string]any `json:"projects,omitempty"`
	// Held is, per name, the value sync could not write because the file
	// holds that name in a form repose does not edit (Codex dotted keys or
	// an inline table). Sync warns again only when that value changes.
	Held map[string]any `json:"held,omitempty"`
}

// held is rec's Held; a nil record holds nothing.
func (rec *agentRecord) held() map[string]any {
	if rec == nil {
		return nil
	}
	return rec.Held
}

// readRendered reads rendered.json, or an empty record when it is
// missing, does not parse, or is a version this base does not know: then
// an entry equal to what sync writes now still counts as repose's, and
// nothing else does. problem is a line for that last case.
func readRendered(p Paths) (rf *renderedFile, problem string) {
	rf = &renderedFile{Version: RenderedVersion, Agents: map[string]*agentRecord{}}
	var got renderedFile
	if err := readJSON(p.renderedFile(), &got); err == nil && got.Agents != nil {
		if got.Version > RenderedVersion {
			return rf, fmt.Sprintf("~/.repose/mcp/rendered.json is version %d, newer than this base reads; read as missing", got.Version)
		}
		rf.Agents = got.Agents
	}
	return rf, ""
}

func writeJSONFile(path string, v any) error {
	b, err := marshal(v)
	if err != nil {
		return err
	}
	o, err := parseObject(b)
	if err != nil {
		return err
	}
	if old, ok, _ := readFile(path); ok && string(old) == string(o.indented()) {
		return nil
	}
	return writeAtomic(path, o.indented(), 0o600)
}

func rawMap(o *object) map[string]any {
	out := make(map[string]any, len(o.keys))
	for _, k := range o.keys {
		out[k] = json.RawMessage(o.vals[k])
	}
	return out
}

// stripJSONC turns JSONC (opencode.jsonc) into JSON: comments become
// spaces and a comma before } or ] goes. Strings are left as they are.
func stripJSONC(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(b) && b[j] != '"' {
				if b[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(b) {
				return append(out, b[i:]...)
			}
			out = append(out, b[i:j+1]...)
			i = j
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
			out = append(out, ' ')
		case c == '}' || c == ']':
			k := len(out) - 1
			for k >= 0 && (out[k] == ' ' || out[k] == '\t' || out[k] == '\n' || out[k] == '\r') {
				k--
			}
			if k >= 0 && out[k] == ',' {
				out[k] = ' '
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
