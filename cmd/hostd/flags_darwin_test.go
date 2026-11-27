package main

import (
	"flag"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/hostd/app"
)

// The owner's ruling (ENG-273, DECISIONS I-1682): on a Mac an idle machine
// rests as a save, giving its memory back, unless hostd is told otherwise.
func TestRestBySaveIsTheMacDefault(t *testing.T) {
	var o app.Options
	fs := flag.NewFlagSet("hostd", flag.ContinueOnError)
	platformFlags(fs, &o)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if !o.Mac.RestBySave {
		t.Fatal("rest by save is off by default")
	}
	if err := fs.Parse([]string{"--mac-rest-by-save=false"}); err != nil || o.Mac.RestBySave {
		t.Fatalf("--mac-rest-by-save=false: %v %v", err, o.Mac.RestBySave)
	}
}

// The two-step rest (DECISIONS I-1682): half an hour of rest by pause
// before the save, unless told otherwise.
func TestRestSaveAfterDefault(t *testing.T) {
	var o app.Options
	fs := flag.NewFlagSet("hostd", flag.ContinueOnError)
	platformFlags(fs, &o)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if o.Mac.RestSaveAfter != 30*time.Minute {
		t.Fatalf("--mac-rest-save-after %s, want 30m", o.Mac.RestSaveAfter)
	}
}
