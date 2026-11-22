package cli

import (
	"context"
	"strings"
	"testing"
)

// ENG-38: `repose host add --cpus 0` exited 0 and created a host with a
// join token instead of refusing the value, where the same CLI refuses
// `--expires 0` with "a number of days, 1 to 365". The cause is the
// flag's own zero value: applianceOptions uses 0 for "not given", so an
// explicit zero is indistinguishable from the flag left out. The same
// trap is in --memory and --disk.
//
// In the same family and a different mechanism: `--disk banana` was
// refused by cobra's int parser with "invalid argument \"banana\" for
// \"--disk\" flag: strconv.ParseInt: parsing \"banana\": invalid
// syntax", where every other validator in this CLI writes prose.

func TestApplianceFlagsRefuseInThisCLIsWords(t *testing.T) {
	for _, c := range []struct{ name, disk, memory, cpus, want string }{
		{name: "an explicit zero is not the default", cpus: "0", want: "at least 4 cores"},
		{name: "memory zero", memory: "0", want: "at least 8192"},
		{name: "disk zero", disk: "0", want: "at least 20"},
		{name: "below the floor says the floor", cpus: "1", want: "at least 4 cores"},
		{name: "disk below the floor", disk: "10", want: "at least 20"},
		{name: "not a number at all", disk: "banana", want: `"banana" is not`},
		{name: "cores that are not a number", cpus: "banana", want: `"banana" is not`},
		{name: "a negative", memory: "-1", want: "at least 8192"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := applianceOptionsFrom(c.disk, c.memory, c.cpus)
			if err == nil {
				t.Fatalf("disk=%q memory=%q cpus=%q was accepted", c.disk, c.memory, c.cpus)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%v, want it to say %q", err, c.want)
			}
			// Go's own parse error never reaches a customer.
			if strings.Contains(err.Error(), "strconv") || strings.Contains(err.Error(), "invalid syntax") {
				t.Fatalf("the refusal is Go's, not this CLI's: %v", err)
			}
		})
	}

	// The flags left out are the defaults, which is what 0 used to mean.
	o, err := applianceOptionsFrom("", "", "")
	if err != nil || o.DiskGB != 0 || o.MemoryMiB != 0 || o.CPUs != 0 {
		t.Fatalf("no flags: %+v %v", o, err)
	}
	// And a value above the floor is taken as typed.
	o, err = applianceOptionsFrom("60", "16384", "8")
	if err != nil || o.DiskGB != 60 || o.MemoryMiB != 16384 || o.CPUs != 8 {
		t.Fatalf("flags as typed: %+v %v", o, err)
	}
}

// The refusal lands before the api is asked for anything: a bad flag
// that minted a host first would leave a record whose retry collides on
// the name, which is the trap I-1422 closed for the disk guard.
func TestApplianceFlagRefusalMintsNoHost(t *testing.T) {
	root := newRootCmd("test")
	root.SetArgs([]string{"host", "add", "--cpus", "0"})
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("`repose host add --cpus 0` was accepted")
	}
	if !strings.Contains(err.Error(), "at least 4 cores") {
		t.Fatalf("%v, want the cores refusal", err)
	}
	// Main turns a cobraUsageError into exit 2 (cli.go:86), which is
	// where a usage mistake's code is decided; exitCodeFor never sees
	// one.
	if _, ok := err.(cobraUsageError); !ok {
		t.Fatalf("%T is not a usage error, so `repose host add --cpus 0` would not exit 2", err)
	}
}
