package ch

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// With free page reporting the guest gets a balloon that never inflates;
// without it the argv is as before (the golden).
func TestArgsBalloon(t *testing.T) {
	s := Spec{GuestDir: "/g", StoreTag: "ro-store"}
	if slices.Contains(s.Args(), "--balloon") {
		t.Fatal("balloon without free page reporting")
	}
	s.FreePageReporting = true
	a := s.Args()
	i := slices.Index(a, "--balloon")
	if i < 0 || a[i+1] != "size=0,free_page_reporting=on" {
		t.Fatalf("argv %v", a)
	}
}

// The restore argv takes everything from the image but the API socket,
// in copy mode.
func TestRestoreArgs(t *testing.T) {
	got := strings.Join(RestoreArgs("/var/lib/repose/guests/g1", "/var/lib/repose/guests/g1/freeze/snap"), " ")
	want := "cloud-hypervisor --api-socket /var/lib/repose/guests/g1/ch.sock --restore source_url=file:///var/lib/repose/guests/g1/freeze/snap,memory_restore_mode=copy --seccomp true"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// vm.snapshot is a PUT with the destination as a file URL.
func TestSnapshotRequest(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "ch.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var path, dest, method string
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		var body map[string]string
		_ = json.Unmarshal(b, &body)
		dest = body["destination_url"]
		w.WriteHeader(http.StatusNoContent)
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()
	var f Freezer = &HTTP{}
	if err := f.Snapshot(context.Background(), sock, "/var/lib/repose/guests/g1/freeze/snap"); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPut || path != "/api/v1/vm.snapshot" || dest != "file:///var/lib/repose/guests/g1/freeze/snap" {
		t.Fatalf("%s %s %q", method, path, dest)
	}
}
