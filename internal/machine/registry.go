package machine

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/BurntSushi/toml"
)

// RegistryName is the registry file, beside config.toml in the CLI's
// config directory (internal/cli configDir: ~/.config/repose).
const RegistryName = "machines.toml"

// FileRegistry is the registry in ~/.config/repose/machines.toml. The file
// is read on every call, so one repose process never serves a list
// another has already changed, and a read-modify-write holds a lock on it
// the way saveProjectsCache does for projects.json.
type FileRegistry struct{ dir string }

var _ Registry = (*FileRegistry)(nil)

// NewRegistry opens the registry in dir, the CLI's config directory.
// Nothing is read or created until a call needs it; the directory is
// taken as a parameter so a test uses a temporary one.
func NewRegistry(dir string) *FileRegistry { return &FileRegistry{dir: dir} }

// Path is the registry file.
func (r *FileRegistry) Path() string { return filepath.Join(r.dir, RegistryName) }

// registryFile is machines.toml: one [machines.NAME] table per machine,
// so the name cannot be in the file twice.
type registryFile struct {
	Machines map[string]machineEntry `toml:"machines"`
}

// machineEntry is one [machines.NAME] table. The name is the table key.
type machineEntry struct {
	Address      string    `toml:"address"`
	Port         int       `toml:"port,omitempty"`
	Login        string    `toml:"login"`
	KeyPath      string    `toml:"key_path,omitempty"`
	ProxyJump    string    `toml:"proxy_jump,omitempty"`
	ProxyCommand string    `toml:"proxy_command,omitempty"`
	Spelled      string    `toml:"spelled,omitempty"`
	UndoPath     string    `toml:"undo_path,omitempty"`
	AddedAt      time.Time `toml:"added_at"`
}

// List is every machine, in name order.
func (r *FileRegistry) List() ([]Machine, error) { return r.load() }

// Get is the machine called name, else ErrNotFound.
func (r *FileRegistry) Get(name string) (Machine, error) {
	ms, err := r.load()
	if err != nil {
		return Machine{}, err
	}
	for _, m := range ms {
		if m.Name == name {
			return m, nil
		}
	}
	return Machine{}, fmt.Errorf("%s: %w", name, ErrNotFound)
}

// Add records m. It refuses a name the slug rules reject or one already
// taken (ErrExists), and stamps AddedAt when the caller left it zero.
func (r *FileRegistry) Add(m Machine) error {
	if err := checkEntry(m); err != nil {
		return err
	}
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer unlock()
	ms, err := r.load()
	if err != nil {
		return err
	}
	taken := make([]string, 0, len(ms))
	for _, e := range ms {
		taken = append(taken, e.Name)
	}
	if err := CheckName(m.Name, taken); err != nil {
		return err
	}
	if m.AddedAt.IsZero() {
		m.AddedAt = time.Now()
	}
	m.AddedAt = m.AddedAt.UTC().Truncate(time.Second)
	return r.save(append(ms, m))
}

// Update replaces the entry called m.Name, which is how a machine that
// moved keeps the projects recorded on it: a box on DHCP, a laptop that
// changed network, a `Host` entry of the user's resolving somewhere new.
// A name the registry has not got is ErrNotFound, since an update is not
// how a machine is added.
func (r *FileRegistry) Update(m Machine) error {
	if err := checkEntry(m); err != nil {
		return err
	}
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer unlock()
	ms, err := r.load()
	if err != nil {
		return err
	}
	found := false
	for i, have := range ms {
		if have.Name != m.Name {
			continue
		}
		// The entry's own history stays the entry's: when the machine was
		// added is not what an address change is about.
		if m.AddedAt.IsZero() {
			m.AddedAt = have.AddedAt
		}
		ms[i], found = m, true
	}
	if !found {
		return fmt.Errorf("%s: %w", m.Name, ErrNotFound)
	}
	for i := range ms {
		ms[i].AddedAt = ms[i].AddedAt.UTC().Truncate(time.Second)
	}
	return r.save(ms)
}

// checkEntry is what every entry needs whichever call writes it.
func checkEntry(m Machine) error {
	if m.Address == "" {
		return errors.New("a machine needs an address ssh can reach")
	}
	if m.Login == "" {
		return errors.New("a machine needs an ssh login")
	}
	if m.Port < 0 || m.Port > 65535 {
		return fmt.Errorf("%d is not an ssh port", m.Port)
	}
	return nil
}

// Remove drops the machine called name. Absence is success.
func (r *FileRegistry) Remove(name string) error {
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer unlock()
	ms, err := r.load()
	if err != nil {
		return err
	}
	kept := make([]Machine, 0, len(ms))
	for _, m := range ms {
		if m.Name != name {
			kept = append(kept, m)
		}
	}
	if len(kept) == len(ms) {
		return nil
	}
	return r.save(kept)
}

// load reads the registry. A missing file is an empty one. A name the
// slug rules reject (a hand edit) is an error naming the file, not a
// machine no command can reach.
func (r *FileRegistry) load() ([]Machine, error) {
	b, err := os.ReadFile(r.Path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f registryFile
	if _, err := toml.Decode(string(b), &f); err != nil {
		return nil, fmt.Errorf("%s: %w", r.Path(), err)
	}
	out := make([]Machine, 0, len(f.Machines))
	for name, e := range f.Machines {
		if err := CheckName(name, nil); err != nil {
			return nil, fmt.Errorf("%s: %w", r.Path(), err)
		}
		out = append(out, Machine{
			Name:         name,
			Address:      e.Address,
			Port:         e.Port,
			Login:        e.Login,
			KeyPath:      e.KeyPath,
			ProxyJump:    e.ProxyJump,
			ProxyCommand: e.ProxyCommand,
			Spelled:      e.Spelled,
			UndoPath:     e.UndoPath,
			AddedAt:      e.AddedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// save replaces the registry with ms. The caller holds the lock.
func (r *FileRegistry) save(ms []Machine) error {
	f := registryFile{Machines: make(map[string]machineEntry, len(ms))}
	for _, m := range ms {
		f.Machines[m.Name] = machineEntry{
			Address:      m.Address,
			Port:         m.Port,
			Login:        m.Login,
			KeyPath:      m.KeyPath,
			ProxyJump:    m.ProxyJump,
			ProxyCommand: m.ProxyCommand,
			Spelled:      m.Spelled,
			UndoPath:     m.UndoPath,
			AddedAt:      m.AddedAt,
		}
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(f); err != nil {
		return err
	}
	return writeFileAtomic(r.Path(), buf.Bytes(), 0o600)
}

// lock serialises a read-modify-write against another repose process.
func (r *FileRegistry) lock() (func(), error) {
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return nil, err
	}
	return lockFile(r.Path() + ".lock")
}

// writeFileAtomic writes to a temp file in the same directory and renames
// it into place, so a crash never leaves a partial file. The same
// function as internal/cli/paths.go's, which this package cannot import.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename below succeeds
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
