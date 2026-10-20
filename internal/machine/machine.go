// Package machine holds a machine the user owns: its record, the registry
// of them on the laptop, and the ssh config the CLI generates to reach
// them (docs/workstreams/SPINE.md "Contract 2", DECISIONS I-812).
//
// There is no api and no database behind any of this. A machine is a
// table in ~/.config/repose/machines.toml and an address ssh can reach,
// so the whole of `repose machine` works with the laptop's filesystem.
//
// The login is the box's own owner, not `dev`. CLAUDE.md fixes `dev` as
// the guest user; a machine the user owns has no guest and already has an
// owner, which is the one place this path differs from the hosted one
// (I-812).
package machine

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Machine is one machine the user owns.
type Machine struct {
	Name    string // the user's name for it, [a-z0-9-], unique
	Address string // host or IP as ssh reaches it, resolved when it was added
	Port    int    // 0 means 22
	Login   string // the ssh user: the box's own owner, not "dev"
	KeyPath string // the laptop key authorised on the box
	// ProxyJump is the hops ssh reaches it through, in order, each as
	// [user@]host[:port], and ProxyCommand the command it reaches it
	// through instead. Both come from the user's own ~/.ssh/config, read
	// once with `ssh -G` when the machine is added (resolve.go).
	ProxyJump    string
	ProxyCommand string
	// Spelled is the --ssh value as the user wrote it, when that is an
	// alias their config answers for rather than the address itself. It
	// is what `repose machine ls` shows, since it is the name they know
	// the box by.
	Spelled  string
	UndoPath string // the store's mountpoint on the box
	AddedAt  time.Time
}

// DefaultPort is what Port 0 means.
const DefaultPort = 22

// Registry is the machines this laptop knows. FileRegistry is the one the
// CLI uses; the interface is here so a test stands in for the file.
type Registry interface {
	List() ([]Machine, error)
	Get(name string) (Machine, error)
	Add(m Machine) error      // refuses a duplicate name
	Update(m Machine) error   // replaces the entry called m.Name, ErrNotFound when it has none
	Remove(name string) error // absence is success
	// SSHConfig renders ~/.ssh/repose/machines, one Host block per
	// machine, in name order. Included by ~/.ssh/repose/config like the
	// gateway blocks.
	SSHConfig(prefix string) string
}

var (
	// ErrNotFound is a name no machine has.
	ErrNotFound = errors.New("no such machine")
	// ErrExists is the refusal of a name this laptop already answers for.
	ErrExists = errors.New("the name is taken")
)

// maxName is the length cap, the same one the api's slug has.
const maxName = 40

// The slug rules of internal/api/http (projects.go Slug), copied and not
// imported: a machine name never reaches the api, and that package pulls
// in Postgres. The shape is the same so a machine name and a project slug
// cannot come out spelled differently.
var (
	slugClean  = regexp.MustCompile(`[^a-z0-9-]+`)
	slugDashes = regexp.MustCompile(`-{2,}`)
)

// Slug is the machine name a user's word becomes: lowercase, [a-z0-9-],
// runs collapsed, 1 to 40 characters. A name is valid exactly when it is
// already its own slug, which is what CheckName tests.
func Slug(word string) string {
	s := strings.ToLower(word)
	s = slugClean.ReplaceAllString(s, "-")
	s = slugDashes.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > maxName {
		s = strings.TrimRight(s[:maxName], "-")
	}
	return s
}

// aliasSuffix is the ssh host suffix the generated config claims; the
// gateway's blocks and the `*.repose` Match of internal/cli/sshprepare.go
// use the same one.
const aliasSuffix = ".repose"

// Alias is a machine's ssh host name: after `repose machine add homebox`,
// `ssh homebox.repose` reaches it.
func Alias(name string) string { return name + aliasSuffix }

// CheckName reports why name cannot name a machine.
//
// taken is every alias this laptop already answers for: the machines
// already added and, from the CLI, the project slugs in
// ~/.ssh/repose/hosts. Both are spelled <name>.repose, and ssh takes the
// first value it reads for each keyword, so two blocks for one alias send
// the connection to whichever file the Include reached first.
//
// Nothing else can make the alias ambiguous: the slug rules leave no ssh
// pattern character (* ? !), no leading dash for ssh to read as an option
// and no dot, so <name>.repose has two labels and matches the *.repose
// the generated config already claims.
func CheckName(name string, taken []string) error {
	if name == "" {
		return errors.New("a machine needs a name")
	}
	if Slug(name) != name {
		return fmt.Errorf("%q cannot name a machine; a name is lowercase letters, digits and single dashes, at most %d characters", name, maxName)
	}
	for _, t := range taken {
		if t == name {
			return fmt.Errorf("%s is already a host this laptop reaches: %w", Alias(name), ErrExists)
		}
	}
	return nil
}
