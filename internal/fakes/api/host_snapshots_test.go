package api

import (
	"testing"
)

// The fake mirrors the api's I-1542: a snapshot taken on the account's own
// host goes with the host when it is removed; the lists say so and a
// restore of it is refused naming the host, creating nothing.
func TestFakeRemovingAHostTakesItsSnapshots(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	hostID := f.AddOwnedHost("mac-mini")
	var p Project
	r := call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "app", "class": "small"})
	want(t, r, 201)
	r.json(t, &p)
	f.SetProjectHost(p.ID, hostID)
	want(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/snapshots", tok, nil), 202)
	want(t, call(t, f, "DELETE", "/v1/projects/"+p.ID, tok, nil), 202)

	var hosts []OwnedHost
	call(t, f, "GET", "/v1/hosts", tok, nil).json(t, &hosts)
	if len(hosts) != 1 || hosts[0].Snapshots < 1 {
		t.Fatalf("hosts %+v, want the snapshots kept on it counted", hosts)
	}
	n := hosts[0].Snapshots
	want(t, call(t, f, "DELETE", "/v1/hosts/"+hostID, tok, nil), 204)

	var gone []DestroyedProject
	call(t, f, "GET", "/v1/projects/destroyed", tok, nil).json(t, &gone)
	if len(gone) != 1 || gone[0].GoneWithHost == nil || *gone[0].GoneWithHost != "mac-mini" || gone[0].RestorableUntil != nil {
		t.Fatalf("destroyed %+v", gone)
	}
	before := len(f.projects)
	r = call(t, f, "POST", "/v1/projects/restore", tok, map[string]any{"slug": "app", "name": "app-back"})
	wantErr(t, r, 409, "conflict")
	if d := detailOf(t, r); d["reason"] != "snapshot_gone" || d["host"] != "mac-mini" {
		t.Fatalf("restore detail %v", d)
	}
	if len(f.projects) != before {
		t.Fatal("a refused restore made a project")
	}
	var snaps []Snapshot
	call(t, f, "GET", "/v1/projects/"+p.ID+"/snapshots", tok, nil).json(t, &snaps)
	if len(snaps) != n || snaps[0].GoneWithHost == nil {
		t.Fatalf("snapshots %+v", snaps)
	}
}
