package machine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// added is a machine with every field set, for a round trip.
func added(name string) Machine {
	return Machine{
		Name:         name,
		Address:      "192.168.1.40",
		Port:         2222,
		Login:        "me",
		KeyPath:      "~/.ssh/id_ed25519",
		ProxyJump:    "jump@bast.example",
		ProxyCommand: "cloudflared access ssh --hostname %h",
		Spelled:      "homebox",
		UndoPath:     "/var/lib/repose/undo",
		AddedAt:      time.Date(2026, 10, 19, 9, 30, 0, 0, time.UTC),
	}
}

func TestRegistryRoundTrip(t *testing.T) {
	// A directory that does not exist yet: the first add creates it.
	dir := filepath.Join(t.TempDir(), "config", "repose")
	r := NewRegistry(dir)

	ms, err := r.List()
	if err != nil || len(ms) != 0 {
		t.Fatalf("List on a missing file = %v, %v", ms, err)
	}
	if _, err := r.Get("homebox"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on a missing file = %v, want ErrNotFound", err)
	}

	if err := r.Add(added("homebox")); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(added("workshop")); err != nil {
		t.Fatal(err)
	}

	got, err := r.Get("homebox")
	if err != nil {
		t.Fatal(err)
	}
	want := added("homebox")
	if got.Address != want.Address || got.Port != want.Port || got.Login != want.Login ||
		got.KeyPath != want.KeyPath || got.ProxyJump != want.ProxyJump ||
		got.ProxyCommand != want.ProxyCommand || got.Spelled != want.Spelled ||
		got.UndoPath != want.UndoPath || !got.AddedAt.Equal(want.AddedAt) {
		t.Errorf("Get = %+v, want %+v", got, want)
	}

	// name order, not the order they were added.
	if err := r.Add(added("abacus")); err != nil {
		t.Fatal(err)
	}
	ms, err = r.List()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range ms {
		names = append(names, m.Name)
	}
	if strings.Join(names, " ") != "abacus homebox workshop" {
		t.Errorf("List order = %v", names)
	}

	if err := r.Remove("homebox"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("homebox"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Remove = %v, want ErrNotFound", err)
	}
	if err := r.Remove("homebox"); err != nil {
		t.Errorf("Remove of a machine that is not there = %v, want nil", err)
	}
}

// A machine that moved keeps its entry, and with it the projects
// recorded on it: the address is replaced, not the machine.
func TestRegistryUpdateReplacesTheAddress(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)
	if err := r.Add(added("homebox")); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(added("workshop")); err != nil {
		t.Fatal(err)
	}
	moved := added("homebox")
	moved.Address = "10.0.0.9"
	moved.Port = 0
	moved.Login = "ada"
	moved.Spelled = "homebox.tail1234.ts.net"
	moved.ProxyJump = "jump@bast.example:2222"
	moved.AddedAt = time.Time{} // the entry keeps the day it was added
	if err := r.Update(moved); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := r.Get("homebox")
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != "10.0.0.9" || got.Port != 0 || got.Login != "ada" ||
		got.Spelled != moved.Spelled || got.ProxyJump != moved.ProxyJump {
		t.Errorf("Get after Update = %+v", got)
	}
	if !got.AddedAt.Equal(added("homebox").AddedAt) {
		t.Errorf("AddedAt = %v, want the day it was added", got.AddedAt)
	}
	if ms, err := r.List(); err != nil || len(ms) != 2 {
		t.Errorf("Update changed how many machines there are: %v, %v", ms, err)
	}
	if other, err := r.Get("workshop"); err != nil || other.Address != "192.168.1.40" {
		t.Errorf("Update reached another machine: %+v, %v", other, err)
	}
}

// An update is not how a machine is added: a name the registry has not
// got is ErrNotFound and writes nothing.
func TestRegistryUpdateOfNoMachine(t *testing.T) {
	r := NewRegistry(t.TempDir())
	if err := r.Update(added("homebox")); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update of a machine that is not there = %v, want ErrNotFound", err)
	}
	if ms, err := r.List(); err != nil || len(ms) != 0 {
		t.Errorf("a refused Update wrote something: %v, %v", ms, err)
	}
}

func TestRegistryUpdateRefusesWhatIsNoEntry(t *testing.T) {
	r := NewRegistry(t.TempDir())
	if err := r.Add(added("homebox")); err != nil {
		t.Fatal(err)
	}
	noAddress := added("homebox")
	noAddress.Address = ""
	if err := r.Update(noAddress); err == nil {
		t.Error("Update with no address: want an error")
	}
	badPort := added("homebox")
	badPort.Port = 70000
	if err := r.Update(badPort); err == nil {
		t.Error("Update with a port out of range: want an error")
	}
	if got, err := r.Get("homebox"); err != nil || got.Address != "192.168.1.40" || got.Port != 2222 {
		t.Errorf("a refused Update changed the entry: %+v, %v", got, err)
	}
}

func TestRegistryRefusals(t *testing.T) {
	r := NewRegistry(t.TempDir())
	if err := r.Add(added("homebox")); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(added("homebox")); !errors.Is(err, ErrExists) {
		t.Errorf("Add of a duplicate name = %v, want ErrExists", err)
	}
	if err := r.Add(added("Home Box")); err == nil {
		t.Error("Add of a name the slug rules reject: want an error")
	}
	noAddress := added("spare")
	noAddress.Address = ""
	if err := r.Add(noAddress); err == nil {
		t.Error("Add with no address: want an error")
	}
	noLogin := added("spare")
	noLogin.Login = ""
	if err := r.Add(noLogin); err == nil {
		t.Error("Add with no login: want an error")
	}
	badPort := added("spare")
	badPort.Port = 70000
	if err := r.Add(badPort); err == nil {
		t.Error("Add with a port out of range: want an error")
	}
	ms, err := r.List()
	if err != nil || len(ms) != 1 {
		t.Fatalf("a refused Add changed the registry: %v, %v", ms, err)
	}
}

// AddedAt is stamped when the caller leaves it zero, so every machine in
// the file has one.
func TestRegistryStampsAddedAt(t *testing.T) {
	r := NewRegistry(t.TempDir())
	m := added("homebox")
	m.AddedAt = time.Time{}
	before := time.Now().Add(-time.Second)
	if err := r.Add(m); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get("homebox")
	if err != nil {
		t.Fatal(err)
	}
	if got.AddedAt.Before(before) || got.AddedAt.After(time.Now().Add(time.Second)) {
		t.Errorf("AddedAt = %v", got.AddedAt)
	}
}

// The file is the user's alone, and a crash leaves no half-written one:
// the write is a temp file in the same directory and a rename.
func TestRegistryFileIsPrivateAndWholeOnDisk(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)
	if err := r.Add(added("homebox")); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(r.Path()) != "machines.toml" {
		t.Errorf("Path = %s", r.Path())
	}
	info, err := os.Stat(r.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("%s was left behind", e.Name())
		}
	}
}

// A hand-edited file with a name no command could reach is an error
// naming the file, not a machine that answers to nothing.
func TestRegistryRefusesAnUnreachableName(t *testing.T) {
	dir := t.TempDir()
	body := "[machines.\"Home Box\"]\naddress = \"192.168.1.40\"\nlogin = \"me\"\n"
	if err := os.WriteFile(filepath.Join(dir, RegistryName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(dir).List(); err == nil {
		t.Error("List of a file with an invalid name: want an error")
	}
}

// A machine added by one process is in the file the next one reads: the
// registry is the file, not a value held in memory.
func TestRegistryReadsTheFileEachTime(t *testing.T) {
	dir := t.TempDir()
	if err := NewRegistry(dir).Add(added("homebox")); err != nil {
		t.Fatal(err)
	}
	ms, err := NewRegistry(dir).List()
	if err != nil || len(ms) != 1 || ms[0].Name != "homebox" {
		t.Fatalf("List = %v, %v", ms, err)
	}
}
