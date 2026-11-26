package main

import (
	"flag"
	"reflect"
	"testing"
)

func TestSplitArgsKeepsGlobalFlagsAndHandsTheRestToTheSubcommand(t *testing.T) {
	fs := flag.NewFlagSet("hostd", flag.ContinueOnError)
	options(fs)
	global, sub := splitArgs(fs, []string{"--state", "/s", "--rebuild", "--no-wg", "--reason=manual", "--api-addr=a:1", "x"})
	if want := []string{"--state", "/s", "--no-wg", "--api-addr=a:1"}; !reflect.DeepEqual(global, want) {
		t.Fatalf("global = %v, want %v", global, want)
	}
	if want := []string{"--rebuild", "--reason=manual", "x"}; !reflect.DeepEqual(sub, want) {
		t.Fatalf("sub = %v, want %v", sub, want)
	}
}

// The two runtime flags PR 6's standalone hostd sets (DECISIONS I-1478,
// I-1484): defaults keep a NixOS host as it was plus the image cache.
func TestRuntimeFlags(t *testing.T) {
	fs := flag.NewFlagSet("hostd", flag.ContinueOnError)
	o := options(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if o.ImagesDir != "/var/lib/repose/images" || o.NixMode != "on" {
		t.Fatalf("defaults images-dir %q nix-mode %q", o.ImagesDir, o.NixMode)
	}
	fs = flag.NewFlagSet("hostd", flag.ContinueOnError)
	o = options(fs)
	if err := fs.Parse([]string{"--images-dir", "/srv/images", "--nix-mode=off"}); err != nil {
		t.Fatal(err)
	}
	if o.ImagesDir != "/srv/images" || o.NixMode != "off" {
		t.Fatalf("parsed images-dir %q nix-mode %q", o.ImagesDir, o.NixMode)
	}
}

// Guest RAM keeps 2 MiB pages on an owned host unless hostd.env says
// --owned-guest-thp=false (DECISIONS I-1650): 4 KiB pages made a build on
// the M4 appliance not finish its first step in 12.7 minutes.
func TestOwnedGuestTHPOnByDefault(t *testing.T) {
	fs := flag.NewFlagSet("hostd", flag.ContinueOnError)
	o := options(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if !o.OwnedGuestTHP {
		t.Fatal("--owned-guest-thp defaults to false")
	}
	fs = flag.NewFlagSet("hostd", flag.ContinueOnError)
	o = options(fs)
	if err := fs.Parse([]string{"--owned-guest-thp=false"}); err != nil || o.OwnedGuestTHP {
		t.Fatalf("--owned-guest-thp=false: %v %v", o.OwnedGuestTHP, err)
	}
}
